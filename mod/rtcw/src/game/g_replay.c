#include "g_local.h"

#include <limits.h>
#include <stdlib.h>
#include <zlib.h>

extern vmCvar_t g_replayEnable;
extern vmCvar_t g_replayTailMsec;
extern vmCvar_t g_replayKeepMatches;

#define REPLAY_ARCHIVE_MAGIC 0x52504C59
#define REPLAY_ARCHIVE_VERSION 2
#define REPLAY_ARCHIVE_CODEC_ZLIB 1
#define REPLAY_RECORD_MSEC 100
#define REPLAY_CHUNK_MSEC 5000
#define REPLAY_SCOREBOARD_MSEC 5000
#define REPLAY_COUNTDOWN_MSEC 3000
#define REPLAY_WINDOW_MSEC 20000
#define REPLAY_PREROLL_MSEC 2000
#define REPLAY_POSTROLL_MSEC 3000
#define REPLAY_MAX_CLIP_MSEC 25000
#define REPLAY_MULTI_KILL_MSEC 3000
#define REPLAY_NEAR_GOAL_MSEC 3000
#define REPLAY_CLUTCH_CLOSE_DIST 1024.0f
#define REPLAY_CLUTCH_NEAR_DIST 2048.0f
#define REPLAY_RED_FLAG_TRIGGER 1
#define REPLAY_BLUE_FLAG_TRIGGER 2

#define REPLAY_SCORE_KILL 100
#define REPLAY_SCORE_HEADSHOT 25
#define REPLAY_SCORE_EXPLOSIVE 20
#define REPLAY_SCORE_KNIFE 35
#define REPLAY_SCORE_MULTI_SECOND 50
#define REPLAY_SCORE_MULTI_THIRD 100
#define REPLAY_SCORE_TEAMKILL -150
#define REPLAY_SCORE_SUICIDE -100
#define REPLAY_SCORE_REVIVE 80
#define REPLAY_SCORE_OBJECTIVE_STEAL 75
#define REPLAY_SCORE_OBJECTIVE_RETURN 125
#define REPLAY_SCORE_OBJECTIVE_CAPTURE 300
#define REPLAY_SCORE_CARRIER_KILL 125
#define REPLAY_SCORE_DENIAL_CLOSE 200
#define REPLAY_SCORE_DENIAL_NEAR 100

typedef enum {
	REPLAY_PHASE_NONE,
	REPLAY_PHASE_SCOREBOARD,
	REPLAY_PHASE_COUNTDOWN,
	REPLAY_PHASE_PLAYBACK,
	REPLAY_PHASE_COMPLETE
} replayPhase_t;

typedef enum {
	REPLAY_EVENT_KILL,
	REPLAY_EVENT_HEADSHOT,
	REPLAY_EVENT_EXPLOSIVE_KILL,
	REPLAY_EVENT_KNIFE_KILL,
	REPLAY_EVENT_MULTIKILL,
	REPLAY_EVENT_TEAMKILL,
	REPLAY_EVENT_SUICIDE,
	REPLAY_EVENT_REVIVE,
	REPLAY_EVENT_OBJECTIVE_STEAL,
	REPLAY_EVENT_OBJECTIVE_RETURN,
	REPLAY_EVENT_OBJECTIVE_CAPTURE,
	REPLAY_EVENT_OBJECTIVE_DENIAL,
	REPLAY_EVENT_TAPOUT
} replayEventType_t;

typedef struct {
	int magic;
	int version;
	int codec;
	int recordMsec;
	int chunkMsec;
	int gametype;
	int maxclients;
	int sampleSize;
	int eventSize;
	int frameCount;
	int eventCount;
	char mapname[MAX_QPATH];
} replayArchiveHeader_t;

typedef struct {
	int startTime;
	int endTime;
	int frameCount;
	int eventCount;
	int uncompressedBytes;
	int compressedBytes;
} replayChunkHeader_t;

typedef struct {
	int clientNum;
	int health;
	int team;
	int pm_type;
	int pm_flags;
	int weaponstate;
	int viewheight;
	int movementDir;
	entityState_t es;
	vec3_t origin;
	vec3_t velocity;
	vec3_t viewangles;
} replaySample_t;

typedef struct {
	int serverTime;
	int firstSample;
	int sampleCount;
} replayFrame_t;

typedef struct {
	int serverTime;
	int actorClientNum;
	int targetClientNum;
	int score;
	int type;
	int meansOfDeath;
	int extra;
	vec3_t origin;
} replayEvent_t;

typedef struct {
	byte *data;
	int size;
	int capacity;
} replayBuffer_t;

typedef struct {
	int targetClientNum;
	int score;
	int windowStartTime;
	int windowEndTime;
	int clipStartTime;
	int clipEndTime;
	int startFrameIndex;
	int endFrameIndex;
} replaySelection_t;

typedef struct {
	replayPhase_t phase;
	int phaseStartTime;
	int lastRecordTime;
	int playbackStartServerTime;
	int playbackClipStartTime;
	int playbackClipEndTime;
	int playbackFrameIndex;
	qboolean archiveWritten;
	qboolean hasSelection;
	replaySelection_t selection;
	replayFrame_t *frames;
	int frameCount;
	int frameCapacity;
	replaySample_t *samples;
	int sampleCount;
	int sampleCapacity;
	replayEvent_t *events;
	int eventCount;
	int eventCapacity;
	int lastKillTime[MAX_CLIENTS];
	int lastKillChain[MAX_CLIENTS];
	qboolean replayEntityActive[MAX_GENTITIES];
	char archivePath[MAX_QPATH];
	char archiveMetaPath[MAX_QPATH];
} replayState_t;

static replayState_t g_replayState;

static qboolean G_ReplayEnsureCapacity( void **buffer, int *capacity, int needed, size_t elementSize ) {
	void *newBuffer;
	int newCapacity;

	if ( needed <= *capacity ) {
		return qtrue;
	}

	newCapacity = *capacity ? *capacity : 256;
	while ( newCapacity < needed ) {
		newCapacity *= 2;
	}

	newBuffer = realloc( *buffer, newCapacity * elementSize );
	if ( !newBuffer ) {
		return qfalse;
	}

	*buffer = newBuffer;
	*capacity = newCapacity;
	return qtrue;
}

static void G_ReplayBufferReset( replayBuffer_t *buffer ) {
	if ( buffer->data ) {
		free( buffer->data );
	}
	memset( buffer, 0, sizeof( *buffer ) );
}

static qboolean G_ReplayBufferWrite( replayBuffer_t *buffer, const void *data, int size ) {
	byte *newData;
	int newCapacity;

	if ( buffer->size + size > buffer->capacity ) {
		newCapacity = buffer->capacity ? buffer->capacity : 1024;
		while ( newCapacity < buffer->size + size ) {
			newCapacity *= 2;
		}

		newData = (byte *)realloc( buffer->data, newCapacity );
		if ( !newData ) {
			return qfalse;
		}

		buffer->data = newData;
		buffer->capacity = newCapacity;
	}

	memcpy( buffer->data + buffer->size, data, size );
	buffer->size += size;
	return qtrue;
}

static qboolean G_ReplaySampleAlive( const replaySample_t *sample ) {
	return sample && sample->health > 0 && sample->pm_type != PM_DEAD && !( sample->pm_flags & PMF_LIMBO );
}

static qboolean G_ReplayShouldCaptureEntity( const gentity_t *ent ) {
	if ( !ent || !ent->inuse ) {
		return qfalse;
	}

	if ( ent->client ) {
		return ent->client->pers.connected == CON_CONNECTED &&
			   ent->client->sess.sessionTeam != TEAM_SPECTATOR;
	}

	if ( ent->s.number < g_maxclients.integer || ent->s.number >= MAX_GENTITIES ) {
		return qfalse;
	}

	switch ( ent->s.eType ) {
	case ET_ITEM:
	case ET_MISSILE:
	case ET_FLAMETHROWER_CHUNK:
	case ET_FP_PARTS:
	case ET_FIRE_COLUMN:
	case ET_FIRE_COLUMN_SMOKE:
	case ET_EXPLO_PART:
	case ET_RAMJET:
	case ET_SMOKER:
		return qtrue;
	default:
		return qfalse;
	}
}

static void G_ReplayResetState( void ) {
	free( g_replayState.frames );
	free( g_replayState.samples );
	free( g_replayState.events );
	memset( &g_replayState, 0, sizeof( g_replayState ) );
}

static const replaySample_t *G_ReplayFindSampleForClient( const replayFrame_t *frame, int clientNum ) {
	int i;

	if ( !frame ) {
		return NULL;
	}

	for ( i = 0; i < frame->sampleCount; i++ ) {
		const replaySample_t *sample = &g_replayState.samples[frame->firstSample + i];
		if ( sample->clientNum == clientNum ) {
			return sample;
		}
	}

	return NULL;
}

static qboolean G_ReplayIsExplosiveKill( int meansOfDeath ) {
	switch ( meansOfDeath ) {
	case MOD_GRENADE:
	case MOD_GRENADE_SPLASH:
	case MOD_ROCKET:
	case MOD_ROCKET_SPLASH:
	case MOD_ROCKET_LAUNCHER:
	case MOD_GRENADE_LAUNCHER:
	case MOD_GRENADE_PINEAPPLE:
	case MOD_DYNAMITE:
	case MOD_DYNAMITE_SPLASH:
	case MOD_AIRSTRIKE:
	case MOD_MORTAR:
	case MOD_MORTAR_SPLASH:
	case MOD_EXPLOSIVE:
	case MOD_PANZERFAUST:
		return qtrue;
	default:
		return qfalse;
	}
}

static qboolean G_ReplayIsKnifeKill( int meansOfDeath ) {
	switch ( meansOfDeath ) {
	case MOD_KNIFE:
	case MOD_KNIFE2:
	case MOD_KNIFE_STEALTH:
	case MOD_KNIFE_THROWN:
		return qtrue;
	default:
		return qfalse;
	}
}

static int G_ReplayCarrierPowerup( const gentity_t *ent ) {
	if ( !ent || !ent->client ) {
		return 0;
	}

	if ( ent->client->ps.powerups[PW_REDFLAG] ) {
		return PW_REDFLAG;
	}
	if ( ent->client->ps.powerups[PW_BLUEFLAG] ) {
		return PW_BLUEFLAG;
	}

	return 0;
}

static float G_ReplayNearestGoalDistance( int powerup, const vec3_t origin ) {
	int requiredSpawnflags;
	float bestDistance;
	int i;

	if ( powerup == PW_REDFLAG ) {
		requiredSpawnflags = REPLAY_RED_FLAG_TRIGGER;
	} else if ( powerup == PW_BLUEFLAG ) {
		requiredSpawnflags = REPLAY_BLUE_FLAG_TRIGGER;
	} else {
		return 999999.0f;
	}

	bestDistance = 999999.0f;
	for ( i = level.maxclients; i < level.num_entities; i++ ) {
		gentity_t *ent = &g_entities[i];
		float distance;

		if ( !ent->inuse || !ent->classname || Q_stricmp( ent->classname, "trigger_flagonly" ) ) {
			continue;
		}
		if ( !( ent->spawnflags & requiredSpawnflags ) ) {
			continue;
		}

		distance = Distance( origin, ent->r.currentOrigin );
		if ( distance < bestDistance ) {
			bestDistance = distance;
		}
	}

	return bestDistance;
}

static void G_ReplayAppendEvent( int actorClientNum, int targetClientNum, int type, int score, int meansOfDeath, int extra, const vec3_t origin ) {
	replayEvent_t *event;

	if ( !g_replayEnable.integer || g_gamestate.integer != GS_PLAYING ) {
		return;
	}

	if ( actorClientNum < 0 || actorClientNum >= MAX_CLIENTS ) {
		return;
	}

	if ( !G_ReplayEnsureCapacity( (void **)&g_replayState.events, &g_replayState.eventCapacity,
								  g_replayState.eventCount + 1, sizeof( g_replayState.events[0] ) ) ) {
		return;
	}

	event = &g_replayState.events[g_replayState.eventCount++];
	memset( event, 0, sizeof( *event ) );
	event->serverTime = level.time;
	event->actorClientNum = actorClientNum;
	event->targetClientNum = targetClientNum;
	event->score = score;
	event->type = type;
	event->meansOfDeath = meansOfDeath;
	event->extra = extra;
	if ( origin ) {
		VectorCopy( origin, event->origin );
	}
}

static void G_ReplayCaptureSample( const gentity_t *ent, replaySample_t *sample ) {
	const gclient_t *client = ent->client;

	memset( sample, 0, sizeof( *sample ) );
	sample->clientNum = ent->s.number;
	sample->es = ent->s;

	if ( client ) {
		sample->health = ent->health;
		sample->team = client->sess.sessionTeam;
		sample->pm_type = client->ps.pm_type;
		sample->pm_flags = client->ps.pm_flags;
		sample->weaponstate = client->ps.weaponstate;
		sample->viewheight = client->ps.viewheight;
		sample->movementDir = client->ps.movementDir;
		VectorCopy( client->ps.origin, sample->origin );
		VectorCopy( client->ps.velocity, sample->velocity );
		VectorCopy( client->ps.viewangles, sample->viewangles );
		return;
	}

	sample->health = ent->health;
	sample->team = TEAM_FREE;
	sample->pm_type = PM_NORMAL;
	sample->pm_flags = 0;
	sample->weaponstate = 0;
	sample->viewheight = 0;
	sample->movementDir = 0;
	VectorCopy( ent->r.currentOrigin, sample->origin );
	VectorCopy( ent->s.pos.trDelta, sample->velocity );
	VectorCopy( ent->s.angles, sample->viewangles );
}

static int G_ReplayFindFrameAtOrAfter( int serverTime ) {
	int i;

	for ( i = 0; i < g_replayState.frameCount; i++ ) {
		if ( g_replayState.frames[i].serverTime >= serverTime ) {
			return i;
		}
	}

	return -1;
}

static int G_ReplayFindFrameAtOrBefore( int serverTime ) {
	int i;

	for ( i = g_replayState.frameCount - 1; i >= 0; i-- ) {
		if ( g_replayState.frames[i].serverTime <= serverTime ) {
			return i;
		}
	}

	return -1;
}

static void G_ReplayApplySampleToEntity( gentity_t *ent, const replaySample_t *sample, int serverTime ) {
	playerState_t *ps;
	vec3_t angles;

	if ( !ent || !sample ) {
		return;
	}

	ent->inuse = qtrue;
	ent->health = sample->health;
	ent->s = sample->es;

	if ( ent->client ) {
		ps = &ent->client->ps;
		VectorCopy( sample->origin, ps->origin );
		VectorCopy( sample->velocity, ps->velocity );
		VectorCopy( sample->viewangles, ps->viewangles );
		ps->weapon = sample->es.weapon;
		ps->weaponstate = sample->weaponstate;
		ps->eFlags = sample->es.eFlags;
		ps->pm_flags = sample->pm_flags;
		ps->pm_type = sample->pm_type;
		ps->groundEntityNum = sample->es.groundEntityNum;
		ps->viewheight = sample->viewheight;
		ps->legsAnim = sample->es.legsAnim;
		ps->torsoAnim = sample->es.torsoAnim;
		ps->movementDir = sample->movementDir;
		ps->stats[STAT_HEALTH] = sample->health;
		ps->clientNum = sample->clientNum;
		ps->aiState = sample->es.aiState;
		memset( ps->powerups, 0, sizeof( ps->powerups ) );
		if ( sample->es.powerups & ( 1 << PW_REDFLAG ) ) {
			ps->powerups[PW_REDFLAG] = INT_MAX;
		}
		if ( sample->es.powerups & ( 1 << PW_BLUEFLAG ) ) {
			ps->powerups[PW_BLUEFLAG] = INT_MAX;
		}
	}

	BG_EvaluateTrajectory( &ent->s.pos, serverTime, ent->r.currentOrigin );
	VectorCopy( ent->r.currentOrigin, ent->s.origin );
	BG_EvaluateTrajectory( &ent->s.apos, serverTime, angles );
	VectorCopy( angles, ent->s.angles );
	trap_LinkEntity( ent );
}

static void G_ReplayApplyTargetView( gentity_t *viewer, const replaySample_t *targetSample ) {
	int savedFlags;

	if ( !viewer || !viewer->client || !targetSample ) {
		return;
	}

	savedFlags = viewer->client->ps.eFlags & EF_VOTED;
	VectorCopy( targetSample->origin, viewer->client->ps.origin );
	VectorCopy( targetSample->velocity, viewer->client->ps.velocity );
	VectorCopy( targetSample->viewangles, viewer->client->ps.viewangles );
	viewer->client->ps.weapon = targetSample->es.weapon;
	viewer->client->ps.weaponstate = targetSample->weaponstate;
	viewer->client->ps.eFlags = targetSample->es.eFlags;
	viewer->client->ps.pm_flags = targetSample->pm_flags | PMF_FOLLOW;
	viewer->client->ps.pm_type = targetSample->pm_type;
	viewer->client->ps.groundEntityNum = targetSample->es.groundEntityNum;
	viewer->client->ps.viewheight = targetSample->viewheight;
	viewer->client->ps.legsAnim = targetSample->es.legsAnim;
	viewer->client->ps.torsoAnim = targetSample->es.torsoAnim;
	viewer->client->ps.movementDir = targetSample->movementDir;
	viewer->client->ps.stats[STAT_HEALTH] = targetSample->health;
	viewer->client->ps.clientNum = targetSample->clientNum;
	viewer->client->ps.eFlags = ( viewer->client->ps.eFlags & ~EF_VOTED ) | savedFlags;
}

static qboolean G_ReplayBuildSelection( int targetClientNum, int score, int windowStartTime, int windowEndTime, replaySelection_t *selection ) {
	int clipStartTime;
	int clipEndTime;
	int startFrameIndex;
	int endFrameIndex;
	int i;
	int firstPlayableFrame;
	int lastPlayableFrame;

	clipStartTime = windowStartTime - REPLAY_PREROLL_MSEC;
	if ( clipStartTime < 0 ) {
		clipStartTime = 0;
	}

	clipEndTime = windowEndTime;
	for ( i = 0; i < g_replayState.eventCount; i++ ) {
		const replayEvent_t *event = &g_replayState.events[i];
		if ( event->actorClientNum != targetClientNum || event->score <= 0 ) {
			continue;
		}
		if ( event->serverTime <= windowEndTime ) {
			continue;
		}
		if ( event->serverTime > windowEndTime + REPLAY_POSTROLL_MSEC ) {
			break;
		}
		clipEndTime = event->serverTime;
	}

	if ( clipEndTime - clipStartTime > REPLAY_MAX_CLIP_MSEC ) {
		clipEndTime = clipStartTime + REPLAY_MAX_CLIP_MSEC;
	}

	startFrameIndex = G_ReplayFindFrameAtOrAfter( clipStartTime );
	endFrameIndex = G_ReplayFindFrameAtOrBefore( clipEndTime );
	if ( startFrameIndex < 0 || endFrameIndex < startFrameIndex ) {
		return qfalse;
	}

	firstPlayableFrame = -1;
	lastPlayableFrame = -1;
	for ( i = startFrameIndex; i <= endFrameIndex; i++ ) {
		const replaySample_t *sample = G_ReplayFindSampleForClient( &g_replayState.frames[i], targetClientNum );
		if ( !G_ReplaySampleAlive( sample ) ) {
			if ( firstPlayableFrame >= 0 ) {
				break;
			}
			continue;
		}
		if ( firstPlayableFrame < 0 ) {
			firstPlayableFrame = i;
		}
		lastPlayableFrame = i;
	}

	if ( firstPlayableFrame < 0 || lastPlayableFrame < firstPlayableFrame ) {
		return qfalse;
	}

	memset( selection, 0, sizeof( *selection ) );
	selection->targetClientNum = targetClientNum;
	selection->score = score;
	selection->windowStartTime = windowStartTime;
	selection->windowEndTime = windowEndTime;
	selection->clipStartTime = g_replayState.frames[firstPlayableFrame].serverTime;
	selection->clipEndTime = g_replayState.frames[lastPlayableFrame].serverTime;
	selection->startFrameIndex = firstPlayableFrame;
	selection->endFrameIndex = lastPlayableFrame;
	return qtrue;
}

static qboolean G_ReplayFindBestSelection( replaySelection_t *selection ) {
	int bestScore;
	int i;

	bestScore = 0;
	memset( selection, 0, sizeof( *selection ) );

	for ( i = 0; i < g_replayState.eventCount; i++ ) {
		replaySelection_t candidate;
		int score;
		int j;
		int windowStartTime;
		int actorClientNum;

		actorClientNum = g_replayState.events[i].actorClientNum;
		if ( actorClientNum < 0 || actorClientNum >= MAX_CLIENTS ) {
			continue;
		}

		score = 0;
		windowStartTime = g_replayState.events[i].serverTime - REPLAY_WINDOW_MSEC;
		for ( j = i; j >= 0; j-- ) {
			const replayEvent_t *event = &g_replayState.events[j];
			if ( event->serverTime < windowStartTime ) {
				break;
			}
			if ( event->actorClientNum == actorClientNum ) {
				score += event->score;
			}
		}

		if ( score <= bestScore ) {
			continue;
		}

		if ( !G_ReplayBuildSelection( actorClientNum, score, windowStartTime,
									 g_replayState.events[i].serverTime, &candidate ) ) {
			continue;
		}

		bestScore = score;
		*selection = candidate;
	}

	return bestScore > 0;
}

static qboolean G_ReplaySerializeChunk( int startFrameIndex, int endFrameIndex, int startEventIndex, int endEventIndex,
									 replayBuffer_t *payload ) {
	int frameCount;
	int eventCount;
	int i;

	G_ReplayBufferReset( payload );
	frameCount = endFrameIndex - startFrameIndex;
	eventCount = endEventIndex - startEventIndex;

	if ( !G_ReplayBufferWrite( payload, &frameCount, sizeof( frameCount ) ) ||
		 !G_ReplayBufferWrite( payload, &eventCount, sizeof( eventCount ) ) ) {
		return qfalse;
	}

	for ( i = startFrameIndex; i < endFrameIndex; i++ ) {
		const replayFrame_t *frame = &g_replayState.frames[i];
		int j;

		if ( !G_ReplayBufferWrite( payload, &frame->serverTime, sizeof( frame->serverTime ) ) ||
			 !G_ReplayBufferWrite( payload, &frame->sampleCount, sizeof( frame->sampleCount ) ) ) {
			return qfalse;
		}

		for ( j = 0; j < frame->sampleCount; j++ ) {
			const replaySample_t *sample = &g_replayState.samples[frame->firstSample + j];
			if ( !G_ReplayBufferWrite( payload, sample, sizeof( *sample ) ) ) {
				return qfalse;
			}
		}
	}

	for ( i = startEventIndex; i < endEventIndex; i++ ) {
		if ( !G_ReplayBufferWrite( payload, &g_replayState.events[i], sizeof( g_replayState.events[i] ) ) ) {
			return qfalse;
		}
	}

	return qtrue;
}

static void G_ReplayPrepareArchivePaths( void ) {
	qtime_t now;

	memset( &now, 0, sizeof( now ) );
	trap_RealTime( &now );

	Com_sprintf( g_replayState.archivePath, sizeof( g_replayState.archivePath ),
				 "replays_%04d%02d%02d_%02d%02d%02d.rpl",
				 now.tm_year + 1900, now.tm_mon + 1, now.tm_mday,
				 now.tm_hour, now.tm_min, now.tm_sec );
	Com_sprintf( g_replayState.archiveMetaPath, sizeof( g_replayState.archiveMetaPath ),
				 "replays_%04d%02d%02d_%02d%02d%02d.txt",
				 now.tm_year + 1900, now.tm_mon + 1, now.tm_mday,
				 now.tm_hour, now.tm_min, now.tm_sec );
}

static void G_ReplayWriteMetadata( void ) {
	fileHandle_t metaFile;
	char text[2048];

	if ( !g_replayState.archiveMetaPath[0] ) {
		return;
	}

	if ( trap_FS_FOpenFile( g_replayState.archiveMetaPath, &metaFile, FS_WRITE ) < 0 ) {
		return;
	}

	Com_sprintf( text, sizeof( text ),
				 "map=%s\n"
				 "gametype=%d\n"
				 "recordMsec=%d\n"
				 "chunkMsec=%d\n"
				 "frames=%d\n"
				 "samples=%d\n"
				 "events=%d\n"
				 "selectionTarget=%d\n"
				 "selectionScore=%d\n"
				 "selectionWindowStart=%d\n"
				 "selectionWindowEnd=%d\n"
				 "selectionClipStart=%d\n"
				 "selectionClipEnd=%d\n"
				 "archive=%s\n",
				 level.rawmapname,
				 g_gametype.integer,
				 REPLAY_RECORD_MSEC,
				 REPLAY_CHUNK_MSEC,
				 g_replayState.frameCount,
				 g_replayState.sampleCount,
				 g_replayState.eventCount,
				 g_replayState.hasSelection ? g_replayState.selection.targetClientNum : -1,
				 g_replayState.hasSelection ? g_replayState.selection.score : 0,
				 g_replayState.hasSelection ? g_replayState.selection.windowStartTime : 0,
				 g_replayState.hasSelection ? g_replayState.selection.windowEndTime : 0,
				 g_replayState.hasSelection ? g_replayState.selection.clipStartTime : 0,
				 g_replayState.hasSelection ? g_replayState.selection.clipEndTime : 0,
				 g_replayState.archivePath );

	trap_FS_Write( text, strlen( text ), metaFile );
	trap_FS_FCloseFile( metaFile );
}

static void G_ReplayWriteArchive( void ) {
	fileHandle_t archiveFile;
	replayArchiveHeader_t header;
	replayBuffer_t payload;
	int startFrameIndex;
	int eventIndex;

	if ( g_replayState.archiveWritten || g_replayState.frameCount <= 0 ) {
		return;
	}

	G_ReplayPrepareArchivePaths();
	if ( trap_FS_FOpenFile( g_replayState.archivePath, &archiveFile, FS_WRITE ) < 0 ) {
		return;
	}

	memset( &header, 0, sizeof( header ) );
	header.magic = REPLAY_ARCHIVE_MAGIC;
	header.version = REPLAY_ARCHIVE_VERSION;
	header.codec = REPLAY_ARCHIVE_CODEC_ZLIB;
	header.recordMsec = REPLAY_RECORD_MSEC;
	header.chunkMsec = REPLAY_CHUNK_MSEC;
	header.gametype = g_gametype.integer;
	header.maxclients = g_maxclients.integer;
	header.sampleSize = sizeof( replaySample_t );
	header.eventSize = sizeof( replayEvent_t );
	header.frameCount = g_replayState.frameCount;
	header.eventCount = g_replayState.eventCount;
	Q_strncpyz( header.mapname, level.rawmapname, sizeof( header.mapname ) );
	trap_FS_Write( &header, sizeof( header ), archiveFile );

	memset( &payload, 0, sizeof( payload ) );
	startFrameIndex = 0;
	eventIndex = 0;

	while ( startFrameIndex < g_replayState.frameCount ) {
		replayChunkHeader_t chunkHeader;
		uLongf compressedSize;
		byte *compressed;
		int endFrameIndex;
		int endEventIndex;
		int chunkEndTime;

		chunkEndTime = g_replayState.frames[startFrameIndex].serverTime + REPLAY_CHUNK_MSEC;
		endFrameIndex = startFrameIndex;
		while ( endFrameIndex < g_replayState.frameCount &&
				g_replayState.frames[endFrameIndex].serverTime < chunkEndTime ) {
			endFrameIndex++;
		}

		endEventIndex = eventIndex;
		while ( endEventIndex < g_replayState.eventCount &&
				g_replayState.events[endEventIndex].serverTime < chunkEndTime ) {
			endEventIndex++;
		}

		if ( !G_ReplaySerializeChunk( startFrameIndex, endFrameIndex, eventIndex, endEventIndex, &payload ) ) {
			break;
		}

		compressedSize = compressBound( payload.size );
		compressed = (byte *)malloc( compressedSize );
		if ( !compressed ) {
			break;
		}

		if ( compress2( compressed, &compressedSize, payload.data, payload.size, Z_BEST_SPEED ) != Z_OK ) {
			free( compressed );
			break;
		}

		memset( &chunkHeader, 0, sizeof( chunkHeader ) );
		chunkHeader.startTime = g_replayState.frames[startFrameIndex].serverTime;
		chunkHeader.endTime = g_replayState.frames[endFrameIndex - 1].serverTime;
		chunkHeader.frameCount = endFrameIndex - startFrameIndex;
		chunkHeader.eventCount = endEventIndex - eventIndex;
		chunkHeader.uncompressedBytes = payload.size;
		chunkHeader.compressedBytes = (int)compressedSize;

		trap_FS_Write( &chunkHeader, sizeof( chunkHeader ), archiveFile );
		trap_FS_Write( compressed, compressedSize, archiveFile );
		free( compressed );

		startFrameIndex = endFrameIndex;
		eventIndex = endEventIndex;
	}

	G_ReplayBufferReset( &payload );
	trap_FS_FCloseFile( archiveFile );
	g_replayState.archiveWritten = qtrue;
	G_ReplayWriteMetadata();
}

static void G_ReplaySendPhase( replayPhase_t phase, int targetClientNum, int durationMsec ) {
	switch ( phase ) {
	case REPLAY_PHASE_SCOREBOARD:
		trap_SendServerCommand( -1, "replay_phase scoreboard" );
		break;
	case REPLAY_PHASE_COUNTDOWN:
		trap_SendServerCommand( -1, va( "replay_phase countdown %d %d", durationMsec, targetClientNum ) );
		break;
	case REPLAY_PHASE_PLAYBACK:
		trap_SendServerCommand( -1, va( "replay_phase playback %d %d", targetClientNum, durationMsec ) );
		break;
	case REPLAY_PHASE_COMPLETE:
		trap_SendServerCommand( -1, "replay_phase complete" );
		break;
	default:
		break;
	}
}

static void G_ReplayStartCountdown( void ) {
	g_replayState.phase = REPLAY_PHASE_COUNTDOWN;
	g_replayState.phaseStartTime = level.time;
	G_ReplaySendPhase( REPLAY_PHASE_COUNTDOWN, g_replayState.selection.targetClientNum, REPLAY_COUNTDOWN_MSEC );
}

static void G_ReplayStopPlayback( void ) {
	int i;

	if ( g_replayState.phase != REPLAY_PHASE_PLAYBACK ) {
		return;
	}

	g_replayState.phase = REPLAY_PHASE_COMPLETE;
	g_replayState.phaseStartTime = level.time;
	for ( i = 0; i < g_maxclients.integer; i++ ) {
		if ( level.clients[i].pers.connected == CON_CONNECTED ) {
			MoveClientToIntermission( &g_entities[i] );
		}
	}
	G_ReplaySendPhase( REPLAY_PHASE_COMPLETE, -1, 0 );
}

static void G_ReplayStartPlayback( void ) {
	int durationMsec;

	if ( !g_replayState.hasSelection ) {
		return;
	}

	durationMsec = g_replayState.selection.clipEndTime - g_replayState.selection.clipStartTime;
	if ( durationMsec <= 0 ) {
		return;
	}

	g_replayState.phase = REPLAY_PHASE_PLAYBACK;
	g_replayState.phaseStartTime = level.time;
	g_replayState.playbackStartServerTime = level.time;
	g_replayState.playbackClipStartTime = g_replayState.selection.clipStartTime;
	g_replayState.playbackClipEndTime = g_replayState.selection.clipEndTime;
	g_replayState.playbackFrameIndex = g_replayState.selection.startFrameIndex;
	level.readyToExit = qfalse;
	level.exitTime = 0;
	G_ReplaySendPhase( REPLAY_PHASE_PLAYBACK, g_replayState.selection.targetClientNum, durationMsec );
}

void G_ReplayInit( void ) {
	G_ReplayResetState();
}

void G_ReplayShutdown( void ) {
	G_ReplayWriteArchive();
	G_ReplayResetState();
}

void G_ReplayRecordFrame( void ) {
	replayFrame_t frame;
	int i;
	int frameIndex;
	int firstSampleIndex;

	if ( !g_replayEnable.integer || g_gamestate.integer != GS_PLAYING || level.intermissiontime ) {
		return;
	}

	if ( g_replayState.lastRecordTime && level.time < g_replayState.lastRecordTime + REPLAY_RECORD_MSEC ) {
		return;
	}

	memset( &frame, 0, sizeof( frame ) );
	frame.serverTime = level.time;
	firstSampleIndex = g_replayState.sampleCount;

	for ( i = 0; i < level.num_entities; i++ ) {
		gentity_t *ent = &g_entities[i];

		if ( !G_ReplayShouldCaptureEntity( ent ) ) {
			continue;
		}

		if ( !G_ReplayEnsureCapacity( (void **)&g_replayState.samples, &g_replayState.sampleCapacity,
									  g_replayState.sampleCount + 1, sizeof( g_replayState.samples[0] ) ) ) {
			return;
		}

		G_ReplayCaptureSample( ent, &g_replayState.samples[g_replayState.sampleCount++] );
		frame.sampleCount++;
	}

	if ( frame.sampleCount <= 0 ) {
		g_replayState.sampleCount = firstSampleIndex;
		return;
	}

	if ( !G_ReplayEnsureCapacity( (void **)&g_replayState.frames, &g_replayState.frameCapacity,
								  g_replayState.frameCount + 1, sizeof( g_replayState.frames[0] ) ) ) {
		g_replayState.sampleCount = firstSampleIndex;
		return;
	}

	frameIndex = g_replayState.frameCount++;
	frame.firstSample = firstSampleIndex;
	g_replayState.frames[frameIndex] = frame;
	g_replayState.lastRecordTime = level.time;
}

void G_ReplayApplyFrame( void ) {
	const replayFrame_t *frame;
	const replaySample_t *targetSample;
	int targetReplayTime;
	qboolean present[MAX_GENTITIES];
	int i;

	if ( g_replayState.phase != REPLAY_PHASE_PLAYBACK ) {
		return;
	}

	if ( level.time - g_replayState.playbackStartServerTime >=
		 g_replayState.playbackClipEndTime - g_replayState.playbackClipStartTime ) {
		G_ReplayStopPlayback();
		return;
	}

	targetReplayTime = g_replayState.playbackClipStartTime + ( level.time - g_replayState.playbackStartServerTime );
	while ( g_replayState.playbackFrameIndex < g_replayState.selection.endFrameIndex &&
			g_replayState.frames[g_replayState.playbackFrameIndex + 1].serverTime <= targetReplayTime ) {
		g_replayState.playbackFrameIndex++;
	}

	frame = &g_replayState.frames[g_replayState.playbackFrameIndex];
	targetSample = G_ReplayFindSampleForClient( frame, g_replayState.selection.targetClientNum );
	if ( !G_ReplaySampleAlive( targetSample ) ) {
		G_ReplayStopPlayback();
		return;
	}

	memset( present, 0, sizeof( present ) );
	for ( i = 0; i < frame->sampleCount; i++ ) {
		const replaySample_t *sample = &g_replayState.samples[frame->firstSample + i];
		gentity_t *ent;

		if ( sample->clientNum < 0 || sample->clientNum >= MAX_GENTITIES ) {
			continue;
		}

		ent = &g_entities[sample->clientNum];
		present[sample->clientNum] = qtrue;
		if ( !ent->client ) {
			g_replayState.replayEntityActive[sample->clientNum] = qtrue;
		}

		G_ReplayApplySampleToEntity( ent, sample, frame->serverTime );
	}

	for ( i = g_maxclients.integer; i < MAX_GENTITIES; i++ ) {
		gentity_t *ent;

		if ( !g_replayState.replayEntityActive[i] || present[i] ) {
			continue;
		}

		ent = &g_entities[i];
		trap_UnlinkEntity( ent );
		ent->s.eType = ET_INVISIBLE;
		ent->s.eFlags |= EF_NODRAW;
		g_replayState.replayEntityActive[i] = qfalse;
	}

	for ( i = 0; i < g_maxclients.integer; i++ ) {
		gentity_t *viewer = &g_entities[i];

		if ( !viewer->inuse || !viewer->client || viewer->client->pers.connected != CON_CONNECTED ) {
			continue;
		}

		G_ReplayApplyTargetView( viewer, targetSample );
	}
}

void G_ReplayBeginIntermission( void ) {
	if ( !g_replayEnable.integer || g_gametype.integer < GT_WOLF ) {
		g_replayState.phase = REPLAY_PHASE_NONE;
		g_replayState.hasSelection = qfalse;
		return;
	}

	g_replayState.phase = REPLAY_PHASE_SCOREBOARD;
	g_replayState.phaseStartTime = level.time;
	g_replayState.hasSelection = G_ReplayFindBestSelection( &g_replayState.selection );
	G_ReplayWriteArchive();
	G_ReplaySendPhase( REPLAY_PHASE_SCOREBOARD, g_replayState.hasSelection ? g_replayState.selection.targetClientNum : -1, 0 );
}

qboolean G_ReplayIntermissionAdvance( void ) {
	if ( !g_replayEnable.integer || g_gametype.integer < GT_WOLF ) {
		return qfalse;
	}

	switch ( g_replayState.phase ) {
	case REPLAY_PHASE_NONE:
		return level.time < level.intermissiontime + REPLAY_SCOREBOARD_MSEC;
	case REPLAY_PHASE_SCOREBOARD:
		if ( level.time < g_replayState.phaseStartTime + REPLAY_SCOREBOARD_MSEC ) {
			return qtrue;
		}
		if ( !g_replayState.hasSelection ) {
			return qfalse;
		}
		G_ReplayStartCountdown();
		return qtrue;
	case REPLAY_PHASE_COUNTDOWN:
		if ( level.time < g_replayState.phaseStartTime + REPLAY_COUNTDOWN_MSEC ) {
			return qtrue;
		}
		G_ReplayStartPlayback();
		return g_replayState.phase == REPLAY_PHASE_PLAYBACK;
	case REPLAY_PHASE_PLAYBACK:
		return qtrue;
	case REPLAY_PHASE_COMPLETE:
		return qfalse;
	}

	return qfalse;
}

qboolean G_ReplayActive( void ) {
	return g_replayState.phase == REPLAY_PHASE_PLAYBACK;
}

void G_ReplayRegisterKill( gentity_t *victim, gentity_t *attacker, int meansOfDeath ) {
	int attackerClientNum;
	int victimClientNum;
	qboolean sameTeam;
	int carrierPowerup;

	if ( !victim || !victim->client ) {
		return;
	}

	attackerClientNum = ( attacker && attacker->client ) ? attacker->s.number : -1;
	victimClientNum = victim->s.number;
	sameTeam = attacker && attacker->client && OnSameTeam( attacker, victim );
	carrierPowerup = G_ReplayCarrierPowerup( victim );

	if ( attackerClientNum >= 0 && attackerClientNum != victimClientNum && !sameTeam ) {
		int multiBonus;

		G_ReplayAppendEvent( attackerClientNum, victimClientNum, REPLAY_EVENT_KILL,
							 REPLAY_SCORE_KILL, meansOfDeath, 0, victim->r.currentOrigin );

		if ( victim->client->ps.eFlags & EF_HEADSHOT ) {
			G_ReplayAppendEvent( attackerClientNum, victimClientNum, REPLAY_EVENT_HEADSHOT,
								 REPLAY_SCORE_HEADSHOT, meansOfDeath, 0, victim->r.currentOrigin );
		}

		if ( G_ReplayIsExplosiveKill( meansOfDeath ) ) {
			G_ReplayAppendEvent( attackerClientNum, victimClientNum, REPLAY_EVENT_EXPLOSIVE_KILL,
								 REPLAY_SCORE_EXPLOSIVE, meansOfDeath, 0, victim->r.currentOrigin );
		}

		if ( G_ReplayIsKnifeKill( meansOfDeath ) ) {
			G_ReplayAppendEvent( attackerClientNum, victimClientNum, REPLAY_EVENT_KNIFE_KILL,
								 REPLAY_SCORE_KNIFE, meansOfDeath, 0, victim->r.currentOrigin );
		}

		if ( g_replayState.lastKillTime[attackerClientNum] &&
			 level.time - g_replayState.lastKillTime[attackerClientNum] <= REPLAY_MULTI_KILL_MSEC ) {
			g_replayState.lastKillChain[attackerClientNum]++;
		} else {
			g_replayState.lastKillChain[attackerClientNum] = 1;
		}
		g_replayState.lastKillTime[attackerClientNum] = level.time;

		multiBonus = 0;
		if ( g_replayState.lastKillChain[attackerClientNum] == 2 ) {
			multiBonus = REPLAY_SCORE_MULTI_SECOND;
		} else if ( g_replayState.lastKillChain[attackerClientNum] >= 3 ) {
			multiBonus = REPLAY_SCORE_MULTI_THIRD;
		}
		if ( multiBonus > 0 ) {
			G_ReplayAppendEvent( attackerClientNum, victimClientNum, REPLAY_EVENT_MULTIKILL,
								 multiBonus, meansOfDeath, g_replayState.lastKillChain[attackerClientNum],
								 victim->r.currentOrigin );
		}

		if ( carrierPowerup ) {
			float distanceToGoal = G_ReplayNearestGoalDistance( carrierPowerup, victim->r.currentOrigin );

			G_ReplayAppendEvent( attackerClientNum, victimClientNum, REPLAY_EVENT_OBJECTIVE_RETURN,
								 REPLAY_SCORE_CARRIER_KILL, meansOfDeath, carrierPowerup, victim->r.currentOrigin );
			if ( distanceToGoal <= REPLAY_CLUTCH_CLOSE_DIST ) {
				G_ReplayAppendEvent( attackerClientNum, victimClientNum, REPLAY_EVENT_OBJECTIVE_DENIAL,
									 REPLAY_SCORE_DENIAL_CLOSE, meansOfDeath, carrierPowerup, victim->r.currentOrigin );
			} else if ( distanceToGoal <= REPLAY_CLUTCH_NEAR_DIST ) {
				G_ReplayAppendEvent( attackerClientNum, victimClientNum, REPLAY_EVENT_OBJECTIVE_DENIAL,
									 REPLAY_SCORE_DENIAL_NEAR, meansOfDeath, carrierPowerup, victim->r.currentOrigin );
			}
		}
	} else if ( attackerClientNum >= 0 && sameTeam && attackerClientNum != victimClientNum ) {
		G_ReplayAppendEvent( attackerClientNum, victimClientNum, REPLAY_EVENT_TEAMKILL,
							 REPLAY_SCORE_TEAMKILL, meansOfDeath, 0, victim->r.currentOrigin );
	} else if ( meansOfDeath == MOD_SUICIDE || attackerClientNum == victimClientNum ) {
		G_ReplayAppendEvent( victimClientNum, victimClientNum, REPLAY_EVENT_SUICIDE,
							 REPLAY_SCORE_SUICIDE, meansOfDeath, 0, victim->r.currentOrigin );
	}
}

void G_ReplayRegisterTapOut( gentity_t *player ) {
	if ( !player || !player->client ) {
		return;
	}

	G_ReplayAppendEvent( player->s.number, player->s.number, REPLAY_EVENT_TAPOUT, 0, MOD_SUICIDE, 0, player->r.currentOrigin );
}

void G_ReplayRegisterRevive( gentity_t *reviver, gentity_t *revived ) {
	if ( !reviver || !reviver->client ) {
		return;
	}

	G_ReplayAppendEvent( reviver->s.number, revived && revived->client ? revived->s.number : -1,
						 REPLAY_EVENT_REVIVE, REPLAY_SCORE_REVIVE, MOD_MEDIC, 0,
						 revived ? revived->r.currentOrigin : reviver->r.currentOrigin );
}

void G_ReplayRegisterObjectiveSteal( gentity_t *player, gentity_t *item ) {
	if ( !player || !player->client ) {
		return;
	}

	G_ReplayAppendEvent( player->s.number, -1, REPLAY_EVENT_OBJECTIVE_STEAL,
						 REPLAY_SCORE_OBJECTIVE_STEAL, MOD_UNKNOWN,
						 item && item->item ? item->item->giTag : 0,
						 player->r.currentOrigin );
}

void G_ReplayRegisterObjectiveReturn( gentity_t *player, gentity_t *item ) {
	if ( !player || !player->client ) {
		return;
	}

	G_ReplayAppendEvent( player->s.number, -1, REPLAY_EVENT_OBJECTIVE_RETURN,
						 REPLAY_SCORE_OBJECTIVE_RETURN, MOD_UNKNOWN,
						 item && item->item ? item->item->giTag : 0,
						 player->r.currentOrigin );
}

void G_ReplayRegisterObjectiveCapture( gentity_t *player, gentity_t *trigger ) {
	if ( !player || !player->client ) {
		return;
	}

	G_ReplayAppendEvent( player->s.number, -1, REPLAY_EVENT_OBJECTIVE_CAPTURE,
						 REPLAY_SCORE_OBJECTIVE_CAPTURE, MOD_UNKNOWN, 0,
						 trigger ? trigger->r.currentOrigin : player->r.currentOrigin );
}
