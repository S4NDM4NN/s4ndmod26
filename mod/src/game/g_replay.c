#include "g_local.h"

#include <limits.h>
#include <stdlib.h>
#include <zlib.h>

extern vmCvar_t g_replayEnable;
extern vmCvar_t g_replayPath;
extern vmCvar_t g_replayTailMsec;
extern vmCvar_t g_replayKeepMatches;
extern vmCvar_t g_replayDebug;

#define REPLAY_DPRINT( ... ) do { if ( g_replayDebug.integer ) { G_Printf( "[replay] " __VA_ARGS__ ); } } while(0)

#define REPLAY_ARCHIVE_MAGIC 0x52504C59
#define REPLAY_ARCHIVE_VERSION 7
#define REPLAY_ARCHIVE_CODEC_ZLIB 1
#define REPLAY_RECORD_MSEC 50
#define REPLAY_CHUNK_MSEC 5000
#define REPLAY_SCOREBOARD_MSEC 5000
#define REPLAY_COUNTDOWN_MSEC 3000
#define REPLAY_WINDOW_MSEC 10000
#define REPLAY_CLIP_PREROLL_MSEC 5000
#define REPLAY_CLIP_POSTROLL_MSEC 5000
#define REPLAY_ACTION_PREROLL_MSEC 2000  /* context before first scored event in POTG window */
#define REPLAY_ACTION_POSTROLL_MSEC 1500 /* buffer after last scored event in POTG window */
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
#define REPLAY_SCORE_AMMO_GIVE 30
#define REPLAY_SCORE_OBJECTIVE_PLANT 50
#define REPLAY_SCORE_OBJECTIVE_DEFUSE 75

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
	REPLAY_EVENT_TAPOUT,
	REPLAY_EVENT_MEDPACK_PICKUP,
	REPLAY_EVENT_DAMAGE,
	REPLAY_EVENT_AMMO_GIVE,
	REPLAY_EVENT_OBJECTIVE_PLANT,
	REPLAY_EVENT_OBJECTIVE_DEFUSE,
	REPLAY_EVENT_SPAWN_CAPTURE,    /* extra = spawn-point index (into header->spawnPointNames) */
	REPLAY_EVENT_MATCH_END         /* fired once at BeginIntermission */
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
	char mapname[MAX_QPATH];                     /* offset 44, 64 bytes — total 108 bytes */
	char playerNames[MAX_CLIENTS][MAX_NETNAME];  /* offset 108, 64*36 = 2304 bytes — total 2412 */
	char spawnPointNames[8][32];                 /* offset 2412, 8*32  =  256 bytes — total 2668 */
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
	int pm_time;
	int weaponstate;
	int viewheight;
	int movementDir;
	int viewlocked;
	int viewlocked_entNum;
	int persistant_hweapon_use;
	int persistant_hits;
	int persistant_bleh2;
	int weaponTime;
	int weapAnim;
	int playerClass;
	float leanf;
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
	int    serverTime;
	vec3_t origin;
	int    fleshEntityNum;
	int    attackerEntityNum;
} replayBulletHit_t;

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
	int playbackLastEventTime;
	int playbackLastBulletHitTime;
	qboolean archiveWritten;
	qboolean hasSelection;
	replaySelection_t selection;
	/* Live best candidate — updated on every scoring event. Frame indices are into candFrames. */
	replayFrame_t  *candFrames;
	int             candFrameCount;
	int             candFrameCapacity;
	replaySample_t *candSamples;
	int             candSampleCount;
	int             candSampleCapacity;
	replaySelection_t liveSelection;
	qboolean          hasLiveSelection;
	int               liveBestScore;
	replayFrame_t *frames;
	int frameCount;
	int frameCapacity;
	replaySample_t *samples;
	int sampleCount;
	int sampleCapacity;
	replayEvent_t *events;
	int eventCount;
	int eventCapacity;
	replayBulletHit_t *bulletHits;
	int bulletHitCount;
	int bulletHitCapacity;
	int lastKillTime[MAX_CLIENTS];
	int lastKillChain[MAX_CLIENTS];
	qboolean replayEntityActive[MAX_GENTITIES];
	int entityRecEventSeq[MAX_GENTITIES];
	int entityPlayEventSeq[MAX_GENTITIES];
	char archivePath[MAX_QPATH];
	char archiveMetaPath[MAX_QPATH];
	FILE *streamFile;
	int streamTotalFrameCount;
	int streamTotalSampleCount;
	int chunkStartFrameIdx;
	int chunkStartEventIdx;
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
	case ET_MG42_BARREL:
		return qtrue;
	default:
		return qfalse;
	}
}

static void G_ReplayResetState( void ) {
	if ( g_replayState.streamFile ) {
		fclose( g_replayState.streamFile );
	}
	free( g_replayState.frames );
	free( g_replayState.samples );
	free( g_replayState.events );
	free( g_replayState.bulletHits );
	free( g_replayState.candFrames );
	free( g_replayState.candSamples );
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

static qboolean G_ReplayBuildSelection( int targetClientNum, int score, int windowStartTime, int windowEndTime, replaySelection_t *selection );

static void G_ReplayUpdateLiveCandidate( int anchorEventIdx ) {
	int actorClientNum;
	int windowStartTime;
	int score;
	int j;
	replaySelection_t sel;
	int frameCount;
	int firstSampleSrc;
	int sampleCount;
	int i;

	actorClientNum = g_replayState.events[anchorEventIdx].actorClientNum;
	windowStartTime = g_replayState.events[anchorEventIdx].serverTime - REPLAY_WINDOW_MSEC;

	score = 0;
	for ( j = anchorEventIdx; j >= 0; j-- ) {
		const replayEvent_t *ev = &g_replayState.events[j];
		if ( ev->serverTime < windowStartTime ) {
			break;
		}
		if ( ev->actorClientNum == actorClientNum ) {
			score += ev->score;
		}
	}

	if ( score <= g_replayState.liveBestScore ) {
		return;
	}

	if ( !G_ReplayBuildSelection( actorClientNum, score, windowStartTime,
								  g_replayState.events[anchorEventIdx].serverTime, &sel ) ) {
		return;
	}

	/* Copy frames and their samples into the candidate buffers. */
	frameCount = sel.endFrameIndex - sel.startFrameIndex + 1;
	if ( !G_ReplayEnsureCapacity( (void **)&g_replayState.candFrames,
								  &g_replayState.candFrameCapacity,
								  frameCount, sizeof( g_replayState.candFrames[0] ) ) ) {
		return;
	}

	firstSampleSrc = g_replayState.frames[sel.startFrameIndex].firstSample;
	sampleCount = ( sel.endFrameIndex + 1 < g_replayState.frameCount )
		? g_replayState.frames[sel.endFrameIndex + 1].firstSample
		: g_replayState.sampleCount;
	sampleCount -= firstSampleSrc;

	if ( !G_ReplayEnsureCapacity( (void **)&g_replayState.candSamples,
								  &g_replayState.candSampleCapacity,
								  sampleCount, sizeof( g_replayState.candSamples[0] ) ) ) {
		return;
	}

	memcpy( g_replayState.candFrames, &g_replayState.frames[sel.startFrameIndex],
			frameCount * sizeof( g_replayState.candFrames[0] ) );
	for ( i = 0; i < frameCount; i++ ) {
		g_replayState.candFrames[i].firstSample -= firstSampleSrc;
	}
	memcpy( g_replayState.candSamples, &g_replayState.samples[firstSampleSrc],
			sampleCount * sizeof( g_replayState.candSamples[0] ) );

	g_replayState.candFrameCount  = frameCount;
	g_replayState.candSampleCount = sampleCount;

	/* Rebase frame indices to be relative to candFrames. */
	sel.startFrameIndex = 0;
	sel.endFrameIndex   = frameCount - 1;

	g_replayState.liveSelection    = sel;
	g_replayState.hasLiveSelection = qtrue;
	g_replayState.liveBestScore    = score;
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

	G_ReplayUpdateLiveCandidate( g_replayState.eventCount - 1 );
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
		sample->pm_time = client->ps.pm_time;
		sample->weaponstate = client->ps.weaponstate;
		sample->viewheight = client->ps.viewheight;
		sample->movementDir = client->ps.movementDir;
		sample->viewlocked = client->ps.viewlocked;
		sample->viewlocked_entNum = client->ps.viewlocked_entNum;
		sample->persistant_hweapon_use = client->ps.persistant[PERS_HWEAPON_USE];
		sample->persistant_hits        = client->ps.persistant[PERS_HITS];
		sample->persistant_bleh2       = client->ps.persistant[PERS_BLEH_2];
		sample->weaponTime = client->ps.weaponTime;
		sample->weapAnim   = client->ps.weapAnim;
		sample->playerClass = client->ps.stats[STAT_PLAYER_CLASS];
		sample->leanf = client->ps.leanf;
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

	/* Remap event sequences so the cgame always sees a monotonically increasing sequence.
	   Recording event sequences are from mid-match (lower than end-of-match values the
	   cgame already processed), so raw sequences would cause CG_CheckEvents to skip all
	   events or fire garbage. We track recording deltas and apply them to a playback
	   counter starting at the entity's current (end-of-match) sequence. */
	{
		int num = sample->clientNum;
		int recSeq = sample->es.eventSequence;
		int delta, i;

		if ( g_replayState.entityRecEventSeq[num] < 0 ) {
			g_replayState.entityRecEventSeq[num] = recSeq;
		}

		delta = recSeq - g_replayState.entityRecEventSeq[num];
		if ( delta > 0 ) {
			if ( delta > MAX_EVENTS ) { delta = MAX_EVENTS; }
			for ( i = 0; i < delta; i++ ) {
				int srcSlot = ( g_replayState.entityRecEventSeq[num] + i ) & ( MAX_EVENTS - 1 );
				int dstSlot = ( g_replayState.entityPlayEventSeq[num] + i ) & ( MAX_EVENTS - 1 );
				ent->s.events[dstSlot]     = sample->es.events[srcSlot];
				ent->s.eventParms[dstSlot] = sample->es.eventParms[srcSlot];
			}
			g_replayState.entityPlayEventSeq[num] += delta;
			g_replayState.entityRecEventSeq[num]   = recSeq;
		}
		ent->s.eventSequence = g_replayState.entityPlayEventSeq[num];
		/* Suppress old-style event field only for client entities — their events come
		   through the pmove events[] system above.  Non-client entities (MG42 barrel,
		   projectiles, etc.) use G_AddEvent which writes directly to s.event/s.eventParm,
		   so those must be preserved for animations and effects to play. */
		if ( ent->client ) {
			ent->s.event     = 0;
			ent->s.eventParm = 0;
		}
	}

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
		ps->pm_time = sample->pm_time;
		ps->groundEntityNum = sample->es.groundEntityNum;
		ps->viewheight = sample->viewheight;
		ps->legsAnim = sample->es.legsAnim;
		ps->torsoAnim = sample->es.torsoAnim;
		ps->movementDir = sample->movementDir;
		ps->leanf = sample->leanf;
		ps->viewlocked = sample->viewlocked;
		ps->viewlocked_entNum = sample->viewlocked_entNum;
		ps->persistant[PERS_HWEAPON_USE] = sample->persistant_hweapon_use;
		ps->weaponTime = sample->weaponTime;
		ps->weapAnim   = sample->weapAnim;
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

	if ( !ent->client ) {
		ent->think = NULL;
		ent->nextthink = 0;
	}

	BG_EvaluateTrajectory( &ent->s.pos, serverTime, ent->r.currentOrigin );
	VectorCopy( ent->r.currentOrigin, ent->s.origin );
	BG_EvaluateTrajectory( &ent->s.apos, serverTime, angles );
	VectorCopy( angles, ent->s.angles );

	/* Re-anchor pos trajectory to the current server time so the cgame can extrapolate
	   using velocity between consecutive snapshots.  Without this, pos.trTime from the
	   recording is far in the past and TR_LINEAR_STOP clamps every snapshot evaluation
	   to the same position, making entities appear frozen between keyframes. */
	VectorCopy( ent->r.currentOrigin, ent->s.pos.trBase );
	ent->s.pos.trTime     = level.time;
	ent->s.pos.trDuration = REPLAY_RECORD_MSEC;

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
	viewer->client->ps.pm_time = targetSample->pm_time;
	viewer->client->ps.weaponTime = targetSample->weaponTime;
	viewer->client->ps.groundEntityNum = targetSample->es.groundEntityNum;
	viewer->client->ps.viewheight = targetSample->viewheight;
	viewer->client->ps.legsAnim = targetSample->es.legsAnim;
	viewer->client->ps.torsoAnim = targetSample->es.torsoAnim;
	viewer->client->ps.movementDir = targetSample->movementDir;
	viewer->client->ps.leanf = targetSample->leanf;
	viewer->client->ps.viewlocked = targetSample->viewlocked;
	viewer->client->ps.viewlocked_entNum = targetSample->viewlocked_entNum;
	viewer->client->ps.stats[STAT_HEALTH] = targetSample->health;
	viewer->client->ps.clientNum = targetSample->clientNum;
	/* PERS_TEAM must match the target's team so CG_AddViewWeapon does not treat the viewer as a spectator */
	viewer->client->ps.persistant[PERS_TEAM]         = targetSample->team;
	viewer->client->ps.persistant[PERS_HWEAPON_USE]  = targetSample->persistant_hweapon_use;
	viewer->client->ps.persistant[PERS_HITS]          = targetSample->persistant_hits;
	viewer->client->ps.persistant[PERS_BLEH_2]        = targetSample->persistant_bleh2;
	viewer->client->ps.weapAnim                       = targetSample->weapAnim;
	viewer->client->ps.eFlags = ( viewer->client->ps.eFlags & ~EF_VOTED ) | savedFlags;

	/* Copy the target entity's remapped event ring to the viewer's playerState so
	   CG_TransitionPlayerState fires weapon sounds and weapon-fire animations.
	   G_ReplayApplySampleToEntity has already run for the target this frame, so
	   g_entities[clientNum].s.events[] holds the correctly remapped sequences. */
	{
		const gentity_t *targetEnt = &g_entities[targetSample->clientNum];
		viewer->client->ps.eventSequence = targetEnt->s.eventSequence;
		memcpy( viewer->client->ps.events,
		        targetEnt->s.events,
		        sizeof( viewer->client->ps.events ) );
		memcpy( viewer->client->ps.eventParms,
		        targetEnt->s.eventParms,
		        sizeof( viewer->client->ps.eventParms ) );
	}
}

static qboolean G_ReplayBuildSelection( int targetClientNum, int score, int windowStartTime, int windowEndTime, replaySelection_t *selection ) {
	int clipStartTime;
	int clipEndTime;
	int startFrameIndex;
	int endFrameIndex;
	int anchorFrameIndex;
	int i;
	int firstPlayableFrame;
	int lastPlayableFrame;
	const replaySample_t *sample;

	/* 5 sec before the best window, the full 10-sec window, 5 sec after: 20 sec total. */
	clipStartTime = windowStartTime - REPLAY_CLIP_PREROLL_MSEC;
	if ( clipStartTime < 0 ) {
		clipStartTime = 0;
	}
	clipEndTime = windowEndTime + REPLAY_CLIP_POSTROLL_MSEC;

	startFrameIndex = G_ReplayFindFrameAtOrAfter( clipStartTime );
	endFrameIndex = G_ReplayFindFrameAtOrBefore( clipEndTime );
	if ( startFrameIndex < 0 || endFrameIndex < startFrameIndex ) {
		return qfalse;
	}

	/* Find the alive run containing the anchor event (windowEndTime).
	 * The anchor event is where the player scored — they were alive there.
	 * The old forward-scan with break-on-death picked the WRONG life when a
	 * player died and respawned within the clip: it clipped at the first death
	 * and missed all events that happened in the later life. */
	anchorFrameIndex = G_ReplayFindFrameAtOrBefore( windowEndTime );
	if ( anchorFrameIndex < startFrameIndex ) {
		anchorFrameIndex = startFrameIndex;
	} else if ( anchorFrameIndex > endFrameIndex ) {
		anchorFrameIndex = endFrameIndex;
	}

	/* Walk backward from anchor to find the start of this alive run. */
	firstPlayableFrame = -1;
	for ( i = anchorFrameIndex; i >= startFrameIndex; i-- ) {
		sample = G_ReplayFindSampleForClient( &g_replayState.frames[i], targetClientNum );
		if ( !G_ReplaySampleAlive( sample ) ) {
			break;
		}
		firstPlayableFrame = i;
	}

	/* Walk forward from anchor to find the end of this alive run. */
	lastPlayableFrame = -1;
	for ( i = anchorFrameIndex; i <= endFrameIndex; i++ ) {
		sample = G_ReplayFindSampleForClient( &g_replayState.frames[i], targetClientNum );
		if ( !G_ReplaySampleAlive( sample ) ) {
			break;
		}
		lastPlayableFrame = i;
	}

	/* If the anchor frame itself is dead (e.g. an objective event fired one
	 * frame after the player died), both walks break immediately.  Fall back
	 * to the old forward scan so we at least get some clip. */
	if ( firstPlayableFrame < 0 || lastPlayableFrame < 0 ) {
		firstPlayableFrame = -1;
		lastPlayableFrame = -1;
		for ( i = startFrameIndex; i <= endFrameIndex; i++ ) {
			sample = G_ReplayFindSampleForClient( &g_replayState.frames[i], targetClientNum );
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

static const char *G_ReplayEventTypeName( int type ) {
	switch ( type ) {
	case REPLAY_EVENT_KILL:              return "KILL";
	case REPLAY_EVENT_HEADSHOT:          return "HEADSHOT";
	case REPLAY_EVENT_EXPLOSIVE_KILL:    return "EXPLOSIVE";
	case REPLAY_EVENT_KNIFE_KILL:        return "KNIFE";
	case REPLAY_EVENT_MULTIKILL:         return "MULTIKILL";
	case REPLAY_EVENT_TEAMKILL:          return "TEAMKILL";
	case REPLAY_EVENT_SUICIDE:           return "SUICIDE";
	case REPLAY_EVENT_REVIVE:            return "REVIVE";
	case REPLAY_EVENT_OBJECTIVE_STEAL:   return "OBJ_STEAL";
	case REPLAY_EVENT_OBJECTIVE_RETURN:  return "OBJ_RETURN";
	case REPLAY_EVENT_OBJECTIVE_CAPTURE: return "OBJ_CAPTURE";
	case REPLAY_EVENT_OBJECTIVE_DENIAL:  return "OBJ_DENIAL";
	case REPLAY_EVENT_TAPOUT:            return "TAPOUT";
	case REPLAY_EVENT_MEDPACK_PICKUP:    return "MEDPACK_PICKUP";
	case REPLAY_EVENT_AMMO_GIVE:         return "AMMO_GIVE";
	case REPLAY_EVENT_OBJECTIVE_PLANT:   return "OBJECTIVE_PLANT";
	case REPLAY_EVENT_OBJECTIVE_DEFUSE:  return "OBJECTIVE_DEFUSE";
	case REPLAY_EVENT_SPAWN_CAPTURE:     return "SPAWN_CAPTURE";
	case REPLAY_EVENT_MATCH_END:         return "MATCH_END";
	default:                             return "UNKNOWN";
	}
}

#define REPLAY_DEBUG_TOP_N 5

typedef struct {
	int actorClientNum;
	int score;
	int windowStartTime;
	int windowEndTime;
	qboolean hasClip;
	int clipStartTime;
	int clipEndTime;
} replayCandidateInfo_t;

static void G_ReplayDebugLogCandidates( void ) {
	replayCandidateInfo_t top[REPLAY_DEBUG_TOP_N];
	int topCount = 0;
	int i, j, k;

	if ( !g_replayDebug.integer ) {
		return;
	}

	G_Printf( "[replay] === SELECTION BREAKDOWN (%d events, %d frames, %d samples) ===\n",
			  g_replayState.eventCount, g_replayState.frameCount, g_replayState.sampleCount );

	/* Find top N candidates by replaying the scoring pass. */
	for ( i = 0; i < g_replayState.eventCount; i++ ) {
		replayCandidateInfo_t cand;
		replaySelection_t sel;
		int score;
		int windowStartTime;
		int actorClientNum;

		actorClientNum = g_replayState.events[i].actorClientNum;
		if ( actorClientNum < 0 || actorClientNum >= MAX_CLIENTS ) {
			continue;
		}

		score = 0;
		windowStartTime = g_replayState.events[i].serverTime - REPLAY_WINDOW_MSEC;
		for ( j = i; j >= 0; j-- ) {
			const replayEvent_t *ev = &g_replayState.events[j];
			if ( ev->serverTime < windowStartTime ) {
				break;
			}
			if ( ev->actorClientNum == actorClientNum ) {
				score += ev->score;
			}
		}

		if ( score <= 0 ) {
			continue;
		}

		/* Check if this candidate is worth inserting into top N. */
		if ( topCount == REPLAY_DEBUG_TOP_N && score <= top[topCount - 1].score ) {
			continue;
		}

		cand.actorClientNum = actorClientNum;
		cand.score = score;
		cand.windowStartTime = windowStartTime;
		cand.windowEndTime = g_replayState.events[i].serverTime;
		cand.hasClip = G_ReplayBuildSelection( actorClientNum, score, windowStartTime,
											   g_replayState.events[i].serverTime, &sel );
		cand.clipStartTime = cand.hasClip ? sel.clipStartTime : 0;
		cand.clipEndTime   = cand.hasClip ? sel.clipEndTime   : 0;

		/* Insertion-sort into top array (descending score). */
		if ( topCount < REPLAY_DEBUG_TOP_N ) {
			topCount++;
		}
		for ( k = topCount - 1; k > 0 && top[k - 1].score < cand.score; k-- ) {
			top[k] = top[k - 1];
		}
		top[k] = cand;
	}

	if ( topCount == 0 ) {
		G_Printf( "[replay] No scoreable candidates found.\n" );
		return;
	}

	G_Printf( "[replay] Top %d candidates:\n", topCount );

	for ( i = 0; i < topCount; i++ ) {
		const replayCandidateInfo_t *c = &top[i];
		int runningScore = 0;

		G_Printf( "[replay]  #%d  cl %-2d  score %-5d  window [%d, %d]  %s\n",
				  i + 1, c->actorClientNum, c->score,
				  c->windowStartTime, c->windowEndTime,
				  c->hasClip
				  ? va( "clip [%d, %d] (%dms)", c->clipStartTime, c->clipEndTime,
						c->clipEndTime - c->clipStartTime )
				  : "NO PLAYABLE CLIP" );

		/* List every event in this candidate's window that belongs to this actor. */
		for ( j = 0; j < g_replayState.eventCount; j++ ) {
			const replayEvent_t *ev = &g_replayState.events[j];
			int relMs;

			if ( ev->actorClientNum != c->actorClientNum ) {
				continue;
			}
			if ( ev->serverTime < c->windowStartTime || ev->serverTime > c->windowEndTime ) {
				continue;
			}

			relMs = ev->serverTime - c->windowStartTime;
			runningScore += ev->score;

			if ( ev->score > 0 ) {
				G_Printf( "[replay]       t+%-5d  %+4d  (running %-5d)  %-12s",
						  relMs, ev->score, runningScore,
						  G_ReplayEventTypeName( ev->type ) );
			} else {
				G_Printf( "[replay]       t+%-5d  %+4d  (running %-5d)  %-12s",
						  relMs, ev->score, runningScore,
						  G_ReplayEventTypeName( ev->type ) );
			}

			/* Extra detail per event type. */
			if ( ev->targetClientNum >= 0 && ev->targetClientNum < MAX_CLIENTS &&
				 ( ev->type == REPLAY_EVENT_KILL || ev->type == REPLAY_EVENT_HEADSHOT ||
				   ev->type == REPLAY_EVENT_EXPLOSIVE_KILL || ev->type == REPLAY_EVENT_KNIFE_KILL ||
				   ev->type == REPLAY_EVENT_TEAMKILL || ev->type == REPLAY_EVENT_REVIVE ) ) {
				G_Printf( "  victim/target cl %d", ev->targetClientNum );
			}
			if ( ev->type == REPLAY_EVENT_MULTIKILL ) {
				G_Printf( "  chain x%d", ev->extra );
			}
			if ( ev->type == REPLAY_EVENT_OBJECTIVE_DENIAL ) {
				G_Printf( "  flag %s", ev->extra == PW_REDFLAG ? "RED" : "BLUE" );
			}
			G_Printf( "\n" );
		}
	}

	G_Printf( "[replay] === END SELECTION BREAKDOWN ===\n" );
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

	if ( bestScore > 0 ) {
		/* Tighten the window around the actual first/last scored events so the
		 * clip doesn't start with several seconds of dead air before the action.
		 * The scoring pass above stays unchanged (comparison was fair); we only
		 * adjust the window of the winner once it's been selected. */
		int firstEventTime = selection->windowEndTime;   /* sentinel – walk down */
		int lastEventTime  = selection->windowStartTime; /* sentinel – walk up   */
		int actor = selection->targetClientNum;
		replaySelection_t tighter;

		for ( i = 0; i < g_replayState.eventCount; i++ ) {
			const replayEvent_t *ev = &g_replayState.events[i];
			if ( ev->actorClientNum != actor ) continue;
			if ( ev->serverTime < selection->windowStartTime ||
				 ev->serverTime > selection->windowEndTime ) continue;
			if ( ev->score <= 0 ) continue;
			if ( ev->serverTime < firstEventTime ) firstEventTime = ev->serverTime;
			if ( ev->serverTime > lastEventTime  ) lastEventTime  = ev->serverTime;
		}

		if ( firstEventTime <= lastEventTime ) {
			int newStart = firstEventTime - REPLAY_ACTION_PREROLL_MSEC;
			int newEnd   = lastEventTime  + REPLAY_ACTION_POSTROLL_MSEC;
			if ( newStart < 0 ) newStart = 0;
			if ( G_ReplayBuildSelection( actor, bestScore, newStart, newEnd, &tighter ) ) {
				*selection = tighter;
			}
		}
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

static qboolean G_ReplayBuildAbsolutePath( const char *relPath, char *out, int outSize ) {
	char fsHomePath[MAX_OSPATH];
	char fsGame[MAX_QPATH];

	trap_Cvar_VariableStringBuffer( "fs_homepath", fsHomePath, sizeof( fsHomePath ) );
	trap_Cvar_VariableStringBuffer( "fs_game", fsGame, sizeof( fsGame ) );
	if ( !fsHomePath[0] || !fsGame[0] ) {
		return qfalse;
	}
	Com_sprintf( out, outSize, "%s/%s/%s", fsHomePath, fsGame, relPath );
	return qtrue;
}

/* Fill header->playerNames from currently connected clients, stripping ^N color codes. */
static void G_ReplayFillHeaderNames( replayArchiveHeader_t *header ) {
	int i;
	memset( header->playerNames, 0, sizeof( header->playerNames ) );
	for ( i = 0; i < g_maxclients.integer && i < MAX_CLIENTS; i++ ) {
		const gclient_t *cl = &level.clients[i];
		const char *src;
		char *dst;
		if ( cl->pers.connected != CON_CONNECTED ) {
			continue;
		}
		src = cl->pers.netname;
		dst = header->playerNames[i];
		while ( *src && dst < header->playerNames[i] + MAX_NETNAME - 1 ) {
			if ( src[0] == '^' && src[1] >= '0' && src[1] <= '9' ) {
				src += 2;
				continue;
			}
			*dst++ = *src++;
		}
	}
}

/* Global spawn-point tracking (populated once per map load). */
#define REPLAY_MAX_SPAWN_POINTS 8
static int g_replaySpawnPointEntityNums[REPLAY_MAX_SPAWN_POINTS];
static int g_replaySpawnPointCount = 0;

/* Scan team_WOLF_checkpoint entities with SPAWNPOINT flag; fill header names
   and global entity-num table used by G_ReplayRegisterSpawnCapture. */
static void G_ReplayFillHeaderSpawnPoints( replayArchiveHeader_t *header ) {
	int i;
	g_replaySpawnPointCount = 0;
	memset( header->spawnPointNames, 0, sizeof( header->spawnPointNames ) );
	for ( i = 0; i < level.num_entities && g_replaySpawnPointCount < REPLAY_MAX_SPAWN_POINTS; i++ ) {
		const gentity_t *ent = &g_entities[i];
		if ( !ent->inuse ) continue;
		if ( !ent->classname || Q_stricmp( ent->classname, "team_WOLF_checkpoint" ) != 0 ) continue;
		if ( !( ent->spawnflags & 1 ) ) continue; /* SPAWNPOINT flag = 1 */
		g_replaySpawnPointEntityNums[g_replaySpawnPointCount] = i;
		if ( ent->scriptName ) {
			Q_strncpyz( header->spawnPointNames[g_replaySpawnPointCount],
			            ent->scriptName, 32 );
		}
		g_replaySpawnPointCount++;
	}
}

/*
 * Seek to the playerNames section of the stream header and overwrite it with
 * current names, then seek back to EOF.  Called every chunk flush so the live
 * web viewer always sees up-to-date names (handles connects, disconnects, and
 * in-game name changes).
 */
static void G_ReplayUpdateHeaderNames( void ) {
	char names[MAX_CLIENTS][MAX_NETNAME];
	replayArchiveHeader_t tmp; /* used only to call the helper via a header-shaped buffer */

	if ( !g_replayState.streamFile ) {
		return;
	}

	memset( &tmp, 0, sizeof( tmp ) );
	G_ReplayFillHeaderNames( &tmp );
	memcpy( names, tmp.playerNames, sizeof( names ) );

	/* 108 == offsetof(replayArchiveHeader_t, playerNames) */
	if ( fseek( g_replayState.streamFile, 108, SEEK_SET ) != 0 ) {
		return;
	}
	fwrite( names, sizeof( names ), 1, g_replayState.streamFile );
	fflush( g_replayState.streamFile );
	fseek( g_replayState.streamFile, 0, SEEK_END );
}

static void G_ReplayFlushCurrentChunk( qboolean pruneAfter ) {
	replayBuffer_t payload;
	int startFrameIdx;
	int chunkFrameCount;
	int chunkSampleCount;
	uLongf compressedSize;
	byte *compressed;
	replayChunkHeader_t chunkHeader;

	if ( !g_replayState.streamFile ) {
		return;
	}

	startFrameIdx = g_replayState.chunkStartFrameIdx;
	chunkFrameCount = g_replayState.frameCount - startFrameIdx;
	if ( chunkFrameCount <= 0 ) {
		return;
	}

	chunkSampleCount = g_replayState.sampleCount
		- ( startFrameIdx < g_replayState.frameCount
			? g_replayState.frames[startFrameIdx].firstSample : g_replayState.sampleCount );

	memset( &payload, 0, sizeof( payload ) );
	if ( !G_ReplaySerializeChunk( startFrameIdx, g_replayState.frameCount,
								  g_replayState.chunkStartEventIdx, g_replayState.eventCount,
								  &payload ) ) {
		G_ReplayBufferReset( &payload );
		return;
	}

	compressedSize = compressBound( payload.size );
	compressed = (byte *)malloc( compressedSize );
	if ( !compressed ) {
		G_ReplayBufferReset( &payload );
		return;
	}

	if ( compress2( compressed, &compressedSize, payload.data, payload.size, Z_BEST_SPEED ) != Z_OK ) {
		free( compressed );
		G_ReplayBufferReset( &payload );
		return;
	}

	memset( &chunkHeader, 0, sizeof( chunkHeader ) );
	chunkHeader.startTime        = g_replayState.frames[startFrameIdx].serverTime;
	chunkHeader.endTime          = g_replayState.frames[g_replayState.frameCount - 1].serverTime;
	chunkHeader.frameCount       = chunkFrameCount;
	chunkHeader.eventCount       = g_replayState.eventCount - g_replayState.chunkStartEventIdx;
	chunkHeader.uncompressedBytes = payload.size;
	chunkHeader.compressedBytes  = (int)compressedSize;

	fwrite( &chunkHeader, sizeof( chunkHeader ), 1, g_replayState.streamFile );
	fwrite( compressed, compressedSize, 1, g_replayState.streamFile );
	fflush( g_replayState.streamFile );

	free( compressed );
	G_ReplayBufferReset( &payload );

	g_replayState.streamTotalFrameCount  += chunkFrameCount;
	g_replayState.streamTotalSampleCount += chunkSampleCount;
	g_replayState.chunkStartEventIdx      = g_replayState.eventCount;

	if ( !pruneAfter ) {
		g_replayState.chunkStartFrameIdx = g_replayState.frameCount;
		return;
	}

	/* Prune frames/samples older than the tail window so memory stays bounded. */
	{
		int tailMsec = g_replayTailMsec.integer > 0 ? g_replayTailMsec.integer : 30000;
		int tailStartTime = g_replayState.frames[g_replayState.frameCount - 1].serverTime - tailMsec;
		int firstKeptFrame = g_replayState.frameCount;
		int firstKeptSample;
		int keptFrameCount;
		int keptSampleCount;
		int i;

		for ( i = 0; i < g_replayState.frameCount; i++ ) {
			if ( g_replayState.frames[i].serverTime >= tailStartTime ) {
				firstKeptFrame = i;
				break;
			}
		}

		if ( firstKeptFrame == 0 ) {
			g_replayState.chunkStartFrameIdx = g_replayState.frameCount;
			return;
		}

		firstKeptSample = ( firstKeptFrame < g_replayState.frameCount )
			? g_replayState.frames[firstKeptFrame].firstSample
			: g_replayState.sampleCount;

		keptFrameCount  = g_replayState.frameCount - firstKeptFrame;
		keptSampleCount = g_replayState.sampleCount - firstKeptSample;

		if ( keptFrameCount > 0 ) {
			memmove( g_replayState.frames, &g_replayState.frames[firstKeptFrame],
					 keptFrameCount * sizeof( g_replayState.frames[0] ) );
			for ( i = 0; i < keptFrameCount; i++ ) {
				g_replayState.frames[i].firstSample -= firstKeptSample;
			}
		}

		if ( keptSampleCount > 0 ) {
			memmove( g_replayState.samples, &g_replayState.samples[firstKeptSample],
					 keptSampleCount * sizeof( g_replayState.samples[0] ) );
		}

		g_replayState.frameCount  = keptFrameCount;
		g_replayState.sampleCount = keptSampleCount;
		g_replayState.chunkStartFrameIdx = keptFrameCount;
	}
}

static void G_ReplayPrepareArchivePaths( void ) {
	qtime_t now;
	const char *dir = g_replayPath.string[0] ? g_replayPath.string : "replays";

	memset( &now, 0, sizeof( now ) );
	trap_RealTime( &now );

	Com_sprintf( g_replayState.archivePath, sizeof( g_replayState.archivePath ),
				 "%s/replays_%04d%02d%02d_%02d%02d%02d.rpl",
				 dir, now.tm_year + 1900, now.tm_mon + 1, now.tm_mday,
				 now.tm_hour, now.tm_min, now.tm_sec );
	Com_sprintf( g_replayState.archiveMetaPath, sizeof( g_replayState.archiveMetaPath ),
				 "%s/replays_%04d%02d%02d_%02d%02d%02d.txt",
				 dir, now.tm_year + 1900, now.tm_mon + 1, now.tm_mday,
				 now.tm_hour, now.tm_min, now.tm_sec );
}

static void G_ReplayWriteMetadata( void ) {
	fileHandle_t metaFile;
	char text[2048];
	int i;

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
				 g_replayState.streamTotalFrameCount > 0 ? g_replayState.streamTotalFrameCount : g_replayState.frameCount,
				 g_replayState.streamTotalSampleCount > 0 ? g_replayState.streamTotalSampleCount : g_replayState.sampleCount,
				 g_replayState.eventCount,
				 g_replayState.hasSelection ? g_replayState.selection.targetClientNum : -1,
				 g_replayState.hasSelection ? g_replayState.selection.score : 0,
				 g_replayState.hasSelection ? g_replayState.selection.windowStartTime : 0,
				 g_replayState.hasSelection ? g_replayState.selection.windowEndTime : 0,
				 g_replayState.hasSelection ? g_replayState.selection.clipStartTime : 0,
				 g_replayState.hasSelection ? g_replayState.selection.clipEndTime : 0,
				 g_replayState.archivePath );

	trap_FS_Write( text, strlen( text ), metaFile );

	/* Write per-player names so the web layer can show real names. */
	for ( i = 0; i < g_maxclients.integer; i++ ) {
		const gclient_t *cl = &level.clients[i];
		char line[MAX_NETNAME + 32];
		if ( cl->pers.connected != CON_CONNECTED ) {
			continue;
		}
		Com_sprintf( line, sizeof( line ), "player_%d=%s\n", i, cl->pers.netname );
		trap_FS_Write( line, strlen( line ), metaFile );
	}

	trap_FS_FCloseFile( metaFile );
}

static void G_ReplayWriteArchive( void ) {
	if ( g_replayState.archiveWritten ) {
		return;
	}

	/* Streaming path: finalize the file that was written chunk-by-chunk during the match. */
	if ( g_replayState.streamFile ) {
		replayArchiveHeader_t header;

		/* Flush the final partial chunk without pruning so frames survive for POTG playback. */
		if ( g_replayState.frameCount > g_replayState.chunkStartFrameIdx ) {
			G_ReplayFlushCurrentChunk( qfalse );
		}

		if ( fseek( g_replayState.streamFile, 0, SEEK_SET ) == 0 ) {
			memset( &header, 0, sizeof( header ) );
			header.magic         = REPLAY_ARCHIVE_MAGIC;
			header.version       = REPLAY_ARCHIVE_VERSION;
			header.codec         = REPLAY_ARCHIVE_CODEC_ZLIB;
			header.recordMsec    = REPLAY_RECORD_MSEC;
			header.chunkMsec     = REPLAY_CHUNK_MSEC;
			header.gametype      = g_gametype.integer;
			header.maxclients    = g_maxclients.integer;
			header.sampleSize    = sizeof( replaySample_t );
			header.eventSize     = sizeof( replayEvent_t );
			header.frameCount    = g_replayState.streamTotalFrameCount;
			header.eventCount    = g_replayState.eventCount;
			Q_strncpyz( header.mapname, level.rawmapname, sizeof( header.mapname ) );
			G_ReplayFillHeaderNames( &header );
			G_ReplayFillHeaderSpawnPoints( &header );
			fwrite( &header, sizeof( header ), 1, g_replayState.streamFile );
		}

		fclose( g_replayState.streamFile );
		g_replayState.streamFile = NULL;
		g_replayState.archiveWritten = qtrue;
		G_ReplayWriteMetadata();
		return;
	}

	/* Fallback: streaming not active (g_replayEnable off at match start or fopen failed). */
	if ( g_replayState.frameCount <= 0 ) {
		return;
	}

	{
		fileHandle_t archiveFile;
		replayArchiveHeader_t header;
		replayBuffer_t payload;
		int startFrameIndex;
		int eventIndex;

		G_ReplayPrepareArchivePaths();
		if ( trap_FS_FOpenFile( g_replayState.archivePath, &archiveFile, FS_WRITE ) < 0 ) {
			return;
		}

		memset( &header, 0, sizeof( header ) );
		header.magic      = REPLAY_ARCHIVE_MAGIC;
		header.version    = REPLAY_ARCHIVE_VERSION;
		header.codec      = REPLAY_ARCHIVE_CODEC_ZLIB;
		header.recordMsec = REPLAY_RECORD_MSEC;
		header.chunkMsec  = REPLAY_CHUNK_MSEC;
		header.gametype   = g_gametype.integer;
		header.maxclients = g_maxclients.integer;
		header.sampleSize = sizeof( replaySample_t );
		header.eventSize  = sizeof( replayEvent_t );
		header.frameCount = g_replayState.frameCount;
		header.eventCount = g_replayState.eventCount;
		Q_strncpyz( header.mapname, level.rawmapname, sizeof( header.mapname ) );
		G_ReplayFillHeaderNames( &header );
		G_ReplayFillHeaderSpawnPoints( &header );
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
			chunkHeader.startTime         = g_replayState.frames[startFrameIndex].serverTime;
			chunkHeader.endTime           = g_replayState.frames[endFrameIndex - 1].serverTime;
			chunkHeader.frameCount        = endFrameIndex - startFrameIndex;
			chunkHeader.eventCount        = endEventIndex - eventIndex;
			chunkHeader.uncompressedBytes = payload.size;
			chunkHeader.compressedBytes   = (int)compressedSize;

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

	REPLAY_DPRINT( "playback stop at frame %d serverTime %d\n",
				   g_replayState.playbackFrameIndex,
				   g_replayState.playbackFrameIndex < g_replayState.frameCount
				   ? g_replayState.frames[g_replayState.playbackFrameIndex].serverTime : -1 );

	g_replayState.phase = REPLAY_PHASE_COMPLETE;
	g_replayState.phaseStartTime = level.time;

	for ( i = 0; i < g_maxclients.integer; i++ ) {
		gentity_t *viewer = &g_entities[i];
		playerState_t *ps;

		if ( level.clients[i].pers.connected != CON_CONNECTED ) {
			continue;
		}

		/* Restore viewer-side state that G_ReplayApplyTargetView overrode. */
		ps = &viewer->client->ps;
		ps->clientNum                    = i;
		ps->pm_flags                    &= ~PMF_FOLLOW;
		ps->persistant[PERS_TEAM]        = viewer->client->sess.sessionTeam;
		ps->persistant[PERS_HWEAPON_USE] = 0;
		ps->viewlocked                   = 0;
		ps->viewlocked_entNum            = 0;
		ps->leanf                        = 0.0f;
		ps->eFlags                      &= ~EF_MG42_ACTIVE;
		/* Clear the replayed event ring so stale events don't fire in intermission. */
		memset( ps->events,     0, sizeof( ps->events ) );
		memset( ps->eventParms, 0, sizeof( ps->eventParms ) );
		ps->eventSequence = 0;

		MoveClientToIntermission( viewer );
	}

	/* Deactivate any non-client entities that were activated for replay. */
	for ( i = g_maxclients.integer; i < MAX_GENTITIES; i++ ) {
		if ( g_replayState.replayEntityActive[i] ) {
			gentity_t *ent = &g_entities[i];
			trap_UnlinkEntity( ent );
			ent->s.eType  = ET_INVISIBLE;
			ent->s.eFlags |= EF_NODRAW;
			g_replayState.replayEntityActive[i] = qfalse;
		}
	}

	G_ReplaySendPhase( REPLAY_PHASE_COMPLETE, -1, 0 );
}

static void G_ReplayStartPlayback( void ) {
	int durationMsec;
	int i;

	if ( !g_replayState.hasSelection ) {
		return;
	}

	durationMsec = g_replayState.selection.clipEndTime - g_replayState.selection.clipStartTime;
	if ( durationMsec <= 0 ) {
		return;
	}

	/* Suppress think functions for all non-client entities before playback begins.
	   This prevents entities such as the MG42 barrel from running their think
	   callbacks while we are applying recorded state each frame. */
	for ( i = g_maxclients.integer; i < MAX_GENTITIES; i++ ) {
		gentity_t *ent = &g_entities[i];
		if ( ent->inuse && ent->think ) {
			REPLAY_DPRINT( "suppressing think on ent %d eType %d at playback start\n", i, ent->s.eType );
			ent->think = NULL;
			ent->nextthink = 0;
		}
	}

	/* Seed per-entity event tracking.  entityPlayEventSeq starts at the entity's
	   current (end-of-match) sequence so the cgame always sees a forward advance;
	   entityRecEventSeq is set to -1 as a sentinel meaning "not yet observed". */
	for ( i = 0; i < MAX_GENTITIES; i++ ) {
		g_replayState.entityRecEventSeq[i]  = -1;
		g_replayState.entityPlayEventSeq[i] = g_entities[i].s.eventSequence;
	}

	g_replayState.phase = REPLAY_PHASE_PLAYBACK;
	g_replayState.phaseStartTime = level.time;
	g_replayState.playbackStartServerTime = level.time;
	g_replayState.playbackClipStartTime = g_replayState.selection.clipStartTime;
	g_replayState.playbackClipEndTime = g_replayState.selection.clipEndTime;
	g_replayState.playbackFrameIndex = g_replayState.selection.startFrameIndex;
	g_replayState.playbackLastEventTime     = g_replayState.selection.clipStartTime - 1;
	g_replayState.playbackLastBulletHitTime = g_replayState.selection.clipStartTime - 1;
	level.readyToExit = qfalse;
	level.exitTime = 0;

	REPLAY_DPRINT( "playback start: target cl %d frames [%d,%d] clipTime [%d,%d] duration %dms\n",
				   g_replayState.selection.targetClientNum,
				   g_replayState.selection.startFrameIndex,
				   g_replayState.selection.endFrameIndex,
				   g_replayState.selection.clipStartTime,
				   g_replayState.selection.clipEndTime, durationMsec );

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

	/* Open the streaming file on the first recorded frame. */
	if ( !g_replayState.streamFile && !g_replayState.archiveWritten ) {
		char absPath[MAX_OSPATH];
		replayArchiveHeader_t placeholderHdr;
		fileHandle_t dirHandle;

		G_ReplayPrepareArchivePaths();

		/* Touch via trap filesystem to create parent directories. */
		trap_FS_FOpenFile( g_replayState.archivePath, &dirHandle, FS_WRITE );
		trap_FS_FCloseFile( dirHandle );

		if ( G_ReplayBuildAbsolutePath( g_replayState.archivePath, absPath, sizeof( absPath ) ) ) {
			g_replayState.streamFile = fopen( absPath, "wb" );
		}

		if ( g_replayState.streamFile ) {
			memset( &placeholderHdr, 0, sizeof( placeholderHdr ) );
			placeholderHdr.magic      = REPLAY_ARCHIVE_MAGIC;
			placeholderHdr.version    = REPLAY_ARCHIVE_VERSION;
			placeholderHdr.codec      = REPLAY_ARCHIVE_CODEC_ZLIB;
			placeholderHdr.recordMsec = REPLAY_RECORD_MSEC;
			placeholderHdr.chunkMsec  = REPLAY_CHUNK_MSEC;
			placeholderHdr.gametype   = g_gametype.integer;
			placeholderHdr.maxclients = g_maxclients.integer;
			placeholderHdr.sampleSize = sizeof( replaySample_t );
			placeholderHdr.eventSize  = sizeof( replayEvent_t );
			Q_strncpyz( placeholderHdr.mapname, level.rawmapname, sizeof( placeholderHdr.mapname ) );
			G_ReplayFillHeaderSpawnPoints( &placeholderHdr );
			fwrite( &placeholderHdr, sizeof( placeholderHdr ), 1, g_replayState.streamFile );
			G_ReplayUpdateHeaderNames();
		}

		g_replayState.streamTotalFrameCount  = 0;
		g_replayState.streamTotalSampleCount = 0;
		g_replayState.chunkStartFrameIdx     = 0;
		g_replayState.chunkStartEventIdx     = 0;
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

	/* Flush completed 5-second chunk to disk and prune old frames from memory. */
	if ( g_replayState.streamFile &&
		 g_replayState.chunkStartFrameIdx < g_replayState.frameCount ) {
		int chunkAge = frame.serverTime - g_replayState.frames[g_replayState.chunkStartFrameIdx].serverTime;
		if ( chunkAge >= REPLAY_CHUNK_MSEC ) {
			G_ReplayFlushCurrentChunk( qtrue );
			G_ReplayUpdateHeaderNames();
		}
	}
}

void G_ReplayRecordBulletHit( vec3_t origin, int fleshEntityNum, int attackerEntityNum ) {
	replayBulletHit_t *hit;

	if ( !g_replayEnable.integer || g_gamestate.integer != GS_PLAYING || level.intermissiontime ) {
		return;
	}

	if ( !G_ReplayEnsureCapacity( (void **)&g_replayState.bulletHits,
	                               &g_replayState.bulletHitCapacity,
	                               g_replayState.bulletHitCount + 1,
	                               sizeof( g_replayState.bulletHits[0] ) ) ) {
		return;
	}

	hit = &g_replayState.bulletHits[g_replayState.bulletHitCount++];
	hit->serverTime        = level.time;
	VectorCopy( origin, hit->origin );
	hit->fleshEntityNum    = fleshEntityNum;
	hit->attackerEntityNum = attackerEntityNum;
}

static void G_ReplayDispatchBulletHits( int upToTime ) {
	int i;

	for ( i = 0; i < g_replayState.bulletHitCount; i++ ) {
		const replayBulletHit_t *hit = &g_replayState.bulletHits[i];
		gentity_t *tent;

		if ( hit->serverTime <= g_replayState.playbackLastBulletHitTime ) {
			continue;
		}
		if ( hit->serverTime > upToTime ) {
			break;
		}

		tent = G_TempEntity( hit->origin, EV_BULLET_HIT_FLESH );
		tent->s.eventParm       = hit->fleshEntityNum;
		tent->s.otherEntityNum2 = hit->attackerEntityNum;
		tent->r.svFlags         = SVF_BROADCAST;
	}

	g_replayState.playbackLastBulletHitTime = upToTime;
}

static void G_ReplayDispatchKillMessages( int upToTime ) {
	int i;

	for ( i = 0; i < g_replayState.eventCount; i++ ) {
		replayEvent_t *ev = &g_replayState.events[i];
		gentity_t *obituaryEnt;

		if ( ev->serverTime <= g_replayState.playbackLastEventTime ) {
			continue;
		}
		if ( ev->serverTime > upToTime ) {
			continue;
		}

		/* Only fire one obituary per actual death — derivative scoring events
		   (HEADSHOT, EXPLOSIVE_KILL, KNIFE_KILL, MULTIKILL) share the same
		   serverTime as the base KILL and would produce duplicate feed entries. */
		switch ( ev->type ) {
		case REPLAY_EVENT_KILL:
		case REPLAY_EVENT_TEAMKILL:
		case REPLAY_EVENT_SUICIDE:
			break;
		default:
			continue;
		}

		/* Fire an EV_OBITUARY temp entity so cgame shows the kill in the HUD
		   kill feed, exactly as player_die does during a live match. */
		obituaryEnt = G_TempEntity( ev->origin, EV_OBITUARY );
		obituaryEnt->s.eventParm       = ev->meansOfDeath;
		obituaryEnt->s.otherEntityNum  = ev->targetClientNum;   /* victim */
		obituaryEnt->s.otherEntityNum2 = ev->actorClientNum;    /* killer */
		obituaryEnt->r.svFlags         = SVF_BROADCAST;

		/* cgame only fires "You killed" when attacker == cg.snap->ps.clientNum,
		   which never matches for replay viewers.  Send a cp directly to all
		   viewers when the replay target is the one who got the kill. */
		if ( ev->type == REPLAY_EVENT_KILL &&
		     ev->actorClientNum == g_replayState.selection.targetClientNum ) {
			const char *victim = level.clients[ev->targetClientNum].pers.netname;
			trap_SendServerCommand( -1, va( "cp \"You killed %s\" 3", victim ) );
		}
	}

	g_replayState.playbackLastEventTime = upToTime;
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
		G_Printf( "[replay] clip finished (elapsed %d ms, duration %d ms)\n",
				  level.time - g_replayState.playbackStartServerTime,
				  g_replayState.playbackClipEndTime - g_replayState.playbackClipStartTime );
		G_ReplayStopPlayback();
		return;
	}

	targetReplayTime = g_replayState.playbackClipStartTime + ( level.time - g_replayState.playbackStartServerTime );
	G_ReplayDispatchKillMessages( targetReplayTime );
	G_ReplayDispatchBulletHits( targetReplayTime );
	while ( g_replayState.playbackFrameIndex < g_replayState.selection.endFrameIndex &&
			g_replayState.frames[g_replayState.playbackFrameIndex + 1].serverTime <= targetReplayTime ) {
		g_replayState.playbackFrameIndex++;
	}

	frame = &g_replayState.frames[g_replayState.playbackFrameIndex];

	if ( frame->firstSample + frame->sampleCount > g_replayState.sampleCount ) {
		/* Non-fatal: log the anomaly but skip the frame rather than stopping playback. */
		G_Printf( "[replay] WARNING: frame %d sample range [%d,%d) exceeds total samples %d — skipping frame\n",
				  g_replayState.playbackFrameIndex, frame->firstSample,
				  frame->firstSample + frame->sampleCount, g_replayState.sampleCount );
		g_replayState.playbackFrameIndex++;
		if ( g_replayState.playbackFrameIndex > g_replayState.selection.endFrameIndex ) {
			G_ReplayStopPlayback();
		}
		return;
	}

	targetSample = G_ReplayFindSampleForClient( frame, g_replayState.selection.targetClientNum );
	if ( !G_ReplaySampleAlive( targetSample ) ) {
		G_Printf( "[replay] target client %d not alive at frame %d serverTime %d — stopping\n",
				  g_replayState.selection.targetClientNum,
				  g_replayState.playbackFrameIndex, frame->serverTime );
		G_ReplayStopPlayback();
		return;
	}

	REPLAY_DPRINT( "frame %d serverTime %d target cl %d weapon %d eFlags %08x viewlocked %d\n",
				   g_replayState.playbackFrameIndex, frame->serverTime,
				   g_replayState.selection.targetClientNum,
				   targetSample->es.weapon, targetSample->es.eFlags,
				   targetSample->viewlocked );

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

	G_ReplayAppendEvent( -1, -1, REPLAY_EVENT_MATCH_END, 0, MOD_UNKNOWN, 0, vec3_origin );

	/* Flush any unflushed frames/events now, before the candidate swap below
	   may set chunkStartFrameIdx = frameCount and cause G_ReplayWriteArchive
	   to skip the final chunk (dropping OBJECTIVE_CAPTURE / MATCH_END). */
	G_ReplayFlushCurrentChunk( qfalse );

	g_replayState.phase = REPLAY_PHASE_SCOREBOARD;
	g_replayState.phaseStartTime = level.time;
	g_replayState.hasSelection = G_ReplayFindBestSelection( &g_replayState.selection );

	/* If the live candidate outscores whatever was found in the tail, use it instead. */
	if ( g_replayState.hasLiveSelection &&
		 g_replayState.liveSelection.score > g_replayState.selection.score ) {
		free( g_replayState.frames );
		free( g_replayState.samples );
		g_replayState.frames         = g_replayState.candFrames;
		g_replayState.frameCount     = g_replayState.candFrameCount;
		g_replayState.frameCapacity  = g_replayState.candFrameCapacity;
		g_replayState.samples        = g_replayState.candSamples;
		g_replayState.sampleCount    = g_replayState.candSampleCount;
		g_replayState.sampleCapacity = g_replayState.candSampleCapacity;
		g_replayState.candFrames     = NULL;
		g_replayState.candSamples    = NULL;
		g_replayState.candFrameCount = g_replayState.candFrameCapacity  = 0;
		g_replayState.candSampleCount = g_replayState.candSampleCapacity = 0;
		g_replayState.chunkStartFrameIdx = g_replayState.frameCount;
		g_replayState.selection   = g_replayState.liveSelection;
		g_replayState.hasSelection = qtrue;
		G_Printf( "[replay] live candidate wins over tail (score %d)\n", g_replayState.selection.score );

		/* Tighten the live candidate's window around actual events (same as the
		 * tail-scan path does inside G_ReplayFindBestSelection). */
		{
			int firstEventTime = g_replayState.selection.windowEndTime;
			int lastEventTime  = g_replayState.selection.windowStartTime;
			int actor = g_replayState.selection.targetClientNum;
			int k;
			replaySelection_t tighter;

			for ( k = 0; k < g_replayState.eventCount; k++ ) {
				const replayEvent_t *ev = &g_replayState.events[k];
				if ( ev->actorClientNum != actor ) continue;
				if ( ev->serverTime < g_replayState.selection.windowStartTime ||
					 ev->serverTime > g_replayState.selection.windowEndTime ) continue;
				if ( ev->score <= 0 ) continue;
				if ( ev->serverTime < firstEventTime ) firstEventTime = ev->serverTime;
				if ( ev->serverTime > lastEventTime  ) lastEventTime  = ev->serverTime;
			}

			if ( firstEventTime <= lastEventTime ) {
				int newStart = firstEventTime - REPLAY_ACTION_PREROLL_MSEC;
				int newEnd   = lastEventTime  + REPLAY_ACTION_POSTROLL_MSEC;
				if ( newStart < 0 ) newStart = 0;
				if ( G_ReplayBuildSelection( actor, g_replayState.selection.score,
											 newStart, newEnd, &tighter ) ) {
					g_replayState.selection = tighter;
				}
			}
		}
	}

	G_ReplayDebugLogCandidates();

	if ( g_replayState.hasSelection ) {
		G_Printf( "[replay] WINNER: cl %d score %d clip [%d,%d] (%dms) frames [%d,%d] tail-frames %d match-frames %d\n",
				  g_replayState.selection.targetClientNum,
				  g_replayState.selection.score,
				  g_replayState.selection.clipStartTime,
				  g_replayState.selection.clipEndTime,
				  g_replayState.selection.clipEndTime - g_replayState.selection.clipStartTime,
				  g_replayState.selection.startFrameIndex,
				  g_replayState.selection.endFrameIndex,
				  g_replayState.frameCount,
				  g_replayState.streamTotalFrameCount + g_replayState.frameCount );
	} else {
		G_Printf( "[replay] no selection found (tail-frames %d tail-samples %d total-frames %d events %d)\n",
				  g_replayState.frameCount, g_replayState.sampleCount,
				  g_replayState.streamTotalFrameCount + g_replayState.frameCount,
				  g_replayState.eventCount );
	}

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
	int i;

	if ( !player || !player->client ) {
		return;
	}

	/* Debounce: the engine can fire multiple return callbacks within a single
	   game frame or back-to-back frames for the same pickup.  Ignore a second
	   OBJ_RETURN from the same actor if one was recorded within 500 ms. */
	for ( i = g_replayState.eventCount - 1; i >= 0; i-- ) {
		const replayEvent_t *ev = &g_replayState.events[i];
		if ( level.time - ev->serverTime > 500 ) {
			break;
		}
		if ( ev->type == REPLAY_EVENT_OBJECTIVE_RETURN &&
		     ev->actorClientNum == player->s.number ) {
			return;
		}
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

void G_ReplayRegisterSpawnCapture( gentity_t *player, gentity_t *checkpoint ) {
	int i;
	if ( !player || !player->client || !checkpoint ) {
		return;
	}
	for ( i = 0; i < g_replaySpawnPointCount; i++ ) {
		if ( g_replaySpawnPointEntityNums[i] == checkpoint->s.number ) {
			G_ReplayAppendEvent( player->s.number, -1, REPLAY_EVENT_SPAWN_CAPTURE,
								 REPLAY_SCORE_OBJECTIVE_CAPTURE, MOD_UNKNOWN, i,
								 checkpoint->r.currentOrigin );
			return;
		}
	}
}

void G_ReplayRegisterMedpackPickup( gentity_t *medic, gentity_t *patient ) {
	if ( !medic || !medic->client || !patient || !patient->client ) {
		return;
	}
	G_ReplayAppendEvent( medic->s.number, patient->s.number,
						 REPLAY_EVENT_MEDPACK_PICKUP, 0, MOD_UNKNOWN, 0,
						 patient->r.currentOrigin );
}

void G_ReplayRegisterAmmoGive( gentity_t *lt, gentity_t *recipient ) {
	if ( !lt || !lt->client || !recipient || !recipient->client ) {
		return;
	}
	G_ReplayAppendEvent( lt->s.number, recipient->s.number,
						 REPLAY_EVENT_AMMO_GIVE, REPLAY_SCORE_AMMO_GIVE, MOD_UNKNOWN, 0,
						 recipient->r.currentOrigin );
}

void G_ReplayRegisterDynamitePlant( gentity_t *planter, gentity_t *objective ) {
	if ( !planter || !planter->client ) {
		return;
	}
	G_ReplayAppendEvent( planter->s.number, -1,
						 REPLAY_EVENT_OBJECTIVE_PLANT, REPLAY_SCORE_OBJECTIVE_PLANT, MOD_UNKNOWN, 0,
						 objective ? objective->r.currentOrigin : planter->r.currentOrigin );
}

void G_ReplayRegisterDynamiteDefuse( gentity_t *defuser, gentity_t *objective ) {
	if ( !defuser || !defuser->client ) {
		return;
	}
	G_ReplayAppendEvent( defuser->s.number, -1,
						 REPLAY_EVENT_OBJECTIVE_DEFUSE, REPLAY_SCORE_OBJECTIVE_DEFUSE, MOD_UNKNOWN, 0,
						 objective ? objective->r.currentOrigin : defuser->r.currentOrigin );
}

void G_ReplayRecordDamage( gentity_t *attacker, gentity_t *victim, int damage, int mod ) {
	if ( !attacker || !attacker->client || !victim || !victim->client ) {
		return;
	}
	if ( attacker->s.number == victim->s.number ) {
		return;
	}
	G_ReplayAppendEvent( attacker->s.number, victim->s.number,
						 REPLAY_EVENT_DAMAGE, damage, mod, 0,
						 victim->r.currentOrigin );
}
