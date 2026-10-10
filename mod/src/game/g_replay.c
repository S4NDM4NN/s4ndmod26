#include "g_local.h"

#include <limits.h>
#include <stdlib.h>
#include <zlib.h>

extern vmCvar_t g_replayEnable;
extern vmCvar_t g_replayPath;
extern vmCvar_t g_replayLoadFile;
extern vmCvar_t g_replayTailMsec;
extern vmCvar_t g_replayKeepMatches;
extern vmCvar_t g_replayDebug;

#define REPLAY_DPRINT( ... ) do { if ( g_replayDebug.integer ) { G_Printf( "[replay] " __VA_ARGS__ ); } } while(0)

#define REPLAY_ARCHIVE_MAGIC 0x52504C59
#define REPLAY_ARCHIVE_VERSION 8
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
/* Live candidate keeps absorbing new frames this long after its anchor event, so the
 * tightened clip (last event + action postroll + clip postroll) isn't cut short. */
#define REPLAY_LIVE_EXTEND_MSEC ( REPLAY_ACTION_POSTROLL_MSEC + REPLAY_CLIP_POSTROLL_MSEC )
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
	REPLAY_EVENT_MATCH_END,        /* fired once at BeginIntermission */
	REPLAY_EVENT_PLAYER_JOIN,      /* actor = clientNum, name = netname (slot went from empty to named) */
	REPLAY_EVENT_PLAYER_RENAME,    /* actor = clientNum, name = new netname */
	REPLAY_EVENT_PLAYER_LEAVE,     /* actor = clientNum, slot is now empty */
	REPLAY_EVENT_STRIKE_LAUNCH     /* actor = caller, extra = replayStrikeType_t, origin = target point */
} replayEventType_t;

typedef enum {
	REPLAY_STRIKE_NONE,
	REPLAY_STRIKE_GRENADE,
	REPLAY_STRIKE_PANZER,
	REPLAY_STRIKE_AIRSTRIKE,
	REPLAY_STRIKE_ARTILLERY,
	REPLAY_STRIKE_OTHER
} replayStrikeType_t;

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
	/* v8 fields (appended, so v4-v7 offsets are unchanged) */
	int inflictorEntNum;          /* projectile/bomb entity number, -1 if none */
	int inflictorWeapon;          /* inflictor->s.weapon */
	int strikeType;               /* replayStrikeType_t */
	vec3_t attackerOrigin;        /* attacker position at event time */
	vec3_t inflictorOrigin;       /* blast/projectile position at event time */
	char name[MAX_NETNAME];       /* PLAYER_JOIN / PLAYER_RENAME: cleaned netname */
	int launchEntNum;             /* entity to follow for the strike camera (grenade/rocket itself, or the airstrike smoke can), -1 if none */
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

#define REPLAY_MAX_SHOTS 4
#define REPLAY_SHOT_MAX_POINTS 16

/* A camera shot that follows a strike projectile (grenade / panzer rocket / airstrike
 * smoke can) from the moment it appears until it detonates, then holds on the blast. */
typedef struct {
	int entNum;
	int weapon;
	int strikeType;
	int startTime;      /* first frame the projectile exists */
	int projEndTime;    /* last frame the projectile exists */
	int killTime;       /* last kill caused by this projectile */
	int endTime;        /* shot ends (cut back to the player) */
	/* playback-time state */
	vec3_t lastPos;
	vec3_t lastDir;
	vec3_t camPos;
	qboolean haveLast;
	qboolean haveCam;
	qboolean blastCamPlaced;
	/* victim positions of this strike's kills, used to frame the fixed camera */
	vec3_t points[REPLAY_SHOT_MAX_POINTS];
	int pointCount;
	int launchTime;     /* artillery: when the binoculars fired (0 otherwise) */
	vec3_t aimPos;      /* where a fixed (blast-area) camera looks */
} replayShot_t;

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
	char slotName[MAX_CLIENTS][MAX_NETNAME];   /* last name recorded per slot, "" = empty */
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
	replayShot_t shots[REPLAY_MAX_SHOTS];
	int shotCount;
	/* Replay-server mode: the clip was loaded from a .rpl by G_ReplayLoad instead of recorded. */
	qboolean serverMode;
	qboolean serverStarted;                         /* playback has been kicked off for the viewer */
	int      recordedMaxClients;                    /* g_maxclients of the recorded match */
	char     playerCS[MAX_CLIENTS][MAX_INFO_STRING / 4];   /* CS_PLAYERS strings for recorded players */
	char     playerName[MAX_CLIENTS][MAX_NETNAME];         /* recorded names (the players are not connected clients here) */
} replayState_t;

static replayState_t g_replayState;

/* Set around G_RadiusDamage calls that pass a NULL inflictor (see g_missile.c). */
gentity_t *g_replayInflictorHint = NULL;

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

	/* Airstrike/artillery shells are SVF_NOCLIENT until they are about to land. */
	if ( ent->r.svFlags & SVF_NOCLIENT ) {
		return qfalse;
	}

	switch ( ent->s.eType ) {
	case ET_GENERAL:
		/* A missile that just exploded: G_ExplodeMissile turns it into an ET_GENERAL carrying
		 * the explosion event until it is freed (~300 ms).  Without these the replay has no
		 * grenade/panzer/airstrike/artillery blast effects or sounds. */
		return ent->s.event != 0 && ent->freeAfterEvent;
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

	/* Ties go to the later window so the clip contains the final kill. */
	if ( score <= 0 || score < g_replayState.liveBestScore ) {
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

/* The candidate is snapshotted mid-frame when the scoring event fires, so it has no
 * postroll.  Keep appending each newly recorded frame until the postroll is covered. */
static void G_ReplayExtendLiveCandidate( const replayFrame_t *frame ) {
	int newFrameIdx;
	int newSampleIdx;

	if ( !g_replayState.hasLiveSelection || g_replayState.candFrameCount <= 0 ) {
		return;
	}
	if ( frame->serverTime <= g_replayState.candFrames[g_replayState.candFrameCount - 1].serverTime ) {
		return;
	}
	if ( frame->serverTime > g_replayState.liveSelection.windowEndTime + REPLAY_LIVE_EXTEND_MSEC ) {
		return;
	}

	newFrameIdx  = g_replayState.candFrameCount;
	newSampleIdx = g_replayState.candSampleCount;

	if ( !G_ReplayEnsureCapacity( (void **)&g_replayState.candFrames, &g_replayState.candFrameCapacity,
								  newFrameIdx + 1, sizeof( g_replayState.candFrames[0] ) ) ) {
		return;
	}
	if ( !G_ReplayEnsureCapacity( (void **)&g_replayState.candSamples, &g_replayState.candSampleCapacity,
								  newSampleIdx + frame->sampleCount, sizeof( g_replayState.candSamples[0] ) ) ) {
		return;
	}

	memcpy( &g_replayState.candSamples[newSampleIdx], &g_replayState.samples[frame->firstSample],
			frame->sampleCount * sizeof( g_replayState.candSamples[0] ) );
	g_replayState.candFrames[newFrameIdx] = *frame;
	g_replayState.candFrames[newFrameIdx].firstSample = newSampleIdx;

	g_replayState.candFrameCount  = newFrameIdx + 1;
	g_replayState.candSampleCount = newSampleIdx + frame->sampleCount;
	g_replayState.liveSelection.endFrameIndex = g_replayState.candFrameCount - 1;
	g_replayState.liveSelection.clipEndTime   = frame->serverTime;
}

static replayEvent_t *G_ReplayAppendEvent( int actorClientNum, int targetClientNum, int type, int score, int meansOfDeath, int extra, const vec3_t origin ) {
	replayEvent_t *event;

	if ( !g_replayEnable.integer || g_gamestate.integer != GS_PLAYING || g_replayState.serverMode ) {
		return NULL;
	}

	if ( actorClientNum < 0 || actorClientNum >= MAX_CLIENTS ) {
		return NULL;
	}

	if ( !G_ReplayEnsureCapacity( (void **)&g_replayState.events, &g_replayState.eventCapacity,
								  g_replayState.eventCount + 1, sizeof( g_replayState.events[0] ) ) ) {
		return NULL;
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
	event->inflictorEntNum = -1;
	event->launchEntNum = -1;
	if ( origin ) {
		VectorCopy( origin, event->origin );
	}

	G_ReplayUpdateLiveCandidate( g_replayState.eventCount - 1 );
	return event;
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
	/* Current velocity, not trDelta: for TR_GRAVITY (grenades, flare bits) trDelta is the
	 * launch velocity, which would make playback extrapolate from the wrong direction. */
	BG_EvaluateTrajectoryDelta( &ent->s.pos, level.time, sample->velocity );
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
	if ( !ent->client && ent->s.pos.trType == TR_GRAVITY ) {
		VectorCopy( sample->velocity, ent->s.pos.trDelta );
	}

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

/* ---- Strike camera shots ------------------------------------------------ */

#define REPLAY_SHOT_BACK_DIST        110.0f   /* chase distance behind the projectile */
#define REPLAY_SHOT_UP_DIST          28.0f
#define REPLAY_SHOT_HOLD_MSEC        1000     /* linger on the blast after the projectile is gone */
#define REPLAY_SHOT_KILL_HOLD_MSEC   1500     /* ...and after the last kill it caused */
#define REPLAY_SHOT_BLAST_BACK       700.0f   /* airstrike: blast-area camera offset */
#define REPLAY_SHOT_BLAST_UP         500.0f
#define REPLAY_ARTY_CAM_DELAY_MSEC  3000     /* binocs view first, then cut to the fixed camera */

static const replaySample_t *G_ReplayFindProjectileSample( const replayFrame_t *frame, int entNum, int weapon ) {
	const replaySample_t *sample = G_ReplayFindSampleForClient( frame, entNum );

	if ( sample && sample->es.eType == ET_MISSILE && sample->es.weapon == weapon ) {
		return sample;
	}
	return NULL;
}

/* Find the contiguous run of frames in which projectile entNum exists, searching back
 * from atTime (the detonation/kill moment, when it may already be gone). */
static qboolean G_ReplayLocateProjectile( int entNum, int weapon, int atTime, int maxBackMsec,
										  int *firstFrame, int *lastFrame ) {
	int i = G_ReplayFindFrameAtOrBefore( atTime );
	int limit = atTime - maxBackMsec;

	while ( i >= 0 && g_replayState.frames[i].serverTime >= limit &&
			!G_ReplayFindProjectileSample( &g_replayState.frames[i], entNum, weapon ) ) {
		i--;
	}
	if ( i < 0 || g_replayState.frames[i].serverTime < limit ) {
		return qfalse;
	}
	*lastFrame = i;
	while ( i > 0 && G_ReplayFindProjectileSample( &g_replayState.frames[i - 1], entNum, weapon ) ) {
		i--;
	}
	*firstFrame = i;
	return qtrue;
}

/* Find the artillery launch event that this kill belongs to; returns its index or -1. */
static int G_ReplayFindArtilleryLaunch( int killIdx ) {
	const replayEvent_t *kill = &g_replayState.events[killIdx];
	int j;

	for ( j = killIdx; j >= 0; j-- ) {
		const replayEvent_t *ev = &g_replayState.events[j];

		if ( kill->serverTime - ev->serverTime > 30000 ) {
			break;
		}
		if ( ev->type == REPLAY_EVENT_STRIKE_LAUNCH && ev->actorClientNum == kill->actorClientNum &&
			 ev->extra == REPLAY_STRIKE_ARTILLERY ) {
			return j;
		}
	}
	return -1;
}

/* Build the camera shots for actor's strike kills in [fromTime, toTime]: one per
 * projectile (or artillery barrage), sorted by start time, non-overlapping. */
static int G_ReplayCollectShots( int actor, int fromTime, int toTime, replayShot_t *out, int maxOut ) {
	int n = 0;
	int i, j;

	for ( i = 0; i < g_replayState.eventCount; i++ ) {
		const replayEvent_t *ev = &g_replayState.events[i];
		int key, launchIdx = -1;
		qboolean arty;

		if ( ev->type != REPLAY_EVENT_KILL || ev->actorClientNum != actor ) continue;
		if ( ev->serverTime < fromTime || ev->serverTime > toTime ) continue;

		arty = ev->strikeType == REPLAY_STRIKE_ARTILLERY;
		if ( arty ) {
			launchIdx = G_ReplayFindArtilleryLaunch( i );
			if ( launchIdx < 0 ) continue;
			key = -1000 - launchIdx;
		} else if ( ev->launchEntNum >= 0 &&
					( ev->strikeType == REPLAY_STRIKE_GRENADE || ev->strikeType == REPLAY_STRIKE_PANZER ||
					  ev->strikeType == REPLAY_STRIKE_AIRSTRIKE ) ) {
			key = ev->launchEntNum;
		} else {
			continue;
		}

		for ( j = 0; j < n; j++ ) {
			if ( out[j].entNum == key ) break;
		}
		if ( j < n ) {
			if ( ev->serverTime > out[j].killTime ) out[j].killTime = ev->serverTime;
			if ( out[j].pointCount < REPLAY_SHOT_MAX_POINTS ) {
				VectorCopy( ev->origin, out[j].points[out[j].pointCount++] );
			}
			continue;
		}
		if ( n >= maxOut ) continue;

		memset( &out[n], 0, sizeof( out[n] ) );
		out[n].entNum     = key;
		out[n].strikeType = ev->strikeType;
		out[n].killTime   = ev->serverTime;
		VectorCopy( ev->origin, out[n].points[out[n].pointCount++] );

		if ( arty ) {
			const replayEvent_t *launch = &g_replayState.events[launchIdx];
			int start = launch->serverTime + REPLAY_ARTY_CAM_DELAY_MSEC;

			if ( start > ev->serverTime - 1000 ) start = ev->serverTime - 1000;
			out[n].launchTime  = launch->serverTime;
			out[n].startTime   = start;
			out[n].projEndTime = start;
			/* aim at the fire-mission point until the kills tell us better */
			VectorCopy( launch->origin, out[n].lastPos );
			out[n].haveLast = qtrue;
			if ( out[n].pointCount < REPLAY_SHOT_MAX_POINTS ) {
				VectorCopy( launch->origin, out[n].points[out[n].pointCount++] );
			}
		} else {
			int first, last;
			int weapon  = ev->strikeType == REPLAY_STRIKE_AIRSTRIKE ? WP_SMOKE_GRENADE : ev->inflictorWeapon;
			int maxBack = ev->strikeType == REPLAY_STRIKE_AIRSTRIKE ? 9000 : 6000;

			if ( !G_ReplayLocateProjectile( key, weapon, ev->serverTime, maxBack, &first, &last ) ) {
				REPLAY_DPRINT( "shot: projectile ent %d weapon %d not found before t=%d\n",
							   key, weapon, ev->serverTime );
				continue;
			}
			out[n].weapon      = weapon;
			out[n].startTime   = g_replayState.frames[first].serverTime;
			out[n].projEndTime = g_replayState.frames[last].serverTime;
		}
		n++;
	}

	for ( i = 0; i < n; i++ ) {
		int e2 = out[i].killTime + REPLAY_SHOT_KILL_HOLD_MSEC;
		if ( out[i].strikeType == REPLAY_STRIKE_ARTILLERY ) {
			out[i].endTime = e2;
		} else {
			int e1 = out[i].projEndTime + REPLAY_SHOT_HOLD_MSEC;
			out[i].endTime = e1 > e2 ? e1 : e2;
		}
	}

	/* sort by start time, then drop shots that overlap an earlier one */
	for ( i = 1; i < n; i++ ) {
		replayShot_t tmp = out[i];
		for ( j = i - 1; j >= 0 && out[j].startTime > tmp.startTime; j-- ) {
			out[j + 1] = out[j];
		}
		out[j + 1] = tmp;
	}
	for ( i = 1; i < n; ) {
		if ( out[i].startTime < out[i - 1].endTime ) {
			memmove( &out[i], &out[i + 1], ( n - i - 1 ) * sizeof( out[0] ) );
			n--;
		} else {
			i++;
		}
	}
	return n;
}

static replayShot_t *G_ReplayActiveShot( int replayTime ) {
	int i;

	for ( i = 0; i < g_replayState.shotCount; i++ ) {
		replayShot_t *shot = &g_replayState.shots[i];
		if ( replayTime >= shot->startTime && replayTime <= shot->endTime ) {
			return shot;
		}
	}
	return NULL;
}

static void G_ReplayClipCameraPos( const vec3_t from, vec3_t camPos ) {
	trace_t tr;

	trap_Trace( &tr, from, NULL, NULL, camPos, ENTITYNUM_NONE, MASK_SOLID );
	if ( tr.fraction < 1.0f ) {
		VectorMA( tr.endpos, 6, tr.plane.normal, camPos );
	}
}


/* Pick a fixed camera that sees as much of the strike as possible: sample positions
 * around the centroid of the kill points at two heights, keep the ones with line of
 * sight to the most points, and prefer cameras that weren't squeezed by geometry. */
static void G_ReplayChooseStrikeCamera( replayShot_t *shot, const vec3_t lastDir ) {
	vec3_t centroid, bestPos, cand, from;
	float radius = 0, bestScore = -1e9f;
	int i, az, el;
	static const float elevations[2] = { 380.0f, 760.0f };

	VectorClear( centroid );
	for ( i = 0; i < shot->pointCount; i++ ) {
		VectorAdd( centroid, shot->points[i], centroid );
	}
	if ( shot->pointCount > 0 ) {
		VectorScale( centroid, 1.0f / shot->pointCount, centroid );
	} else {
		VectorCopy( shot->lastPos, centroid );
	}
	for ( i = 0; i < shot->pointCount; i++ ) {
		float d = Distance( centroid, shot->points[i] );
		if ( d > radius ) radius = d;
	}
	if ( radius < 200.0f ) radius = 200.0f;
	if ( radius > 1500.0f ) radius = 1500.0f;

	VectorCopy( centroid, shot->aimPos );
	shot->aimPos[2] += 40;
	VectorCopy( shot->aimPos, from );
	VectorCopy( shot->aimPos, bestPos );
	bestPos[2] += 500;

	for ( az = 0; az < 8; az++ ) {
		float yaw = az * ( M_PI / 4.0f );
		float dist = radius * 1.4f + 450.0f;

		for ( el = 0; el < 2; el++ ) {
			float score;
			float wanted, got;
			trace_t tr;
			int visible = 0;

			cand[0] = centroid[0] + cos( yaw ) * dist;
			cand[1] = centroid[1] + sin( yaw ) * dist;
			cand[2] = centroid[2] + elevations[el];

			/* pull the camera in if geometry is in the way between it and the action */
			trap_Trace( &tr, from, NULL, NULL, cand, ENTITYNUM_NONE, MASK_SOLID );
			wanted = Distance( from, cand );
			if ( tr.fraction < 1.0f ) {
				VectorMA( tr.endpos, 6, tr.plane.normal, cand );
			}
			got = Distance( from, cand );

			for ( i = 0; i < shot->pointCount; i++ ) {
				vec3_t p;
				VectorCopy( shot->points[i], p );
				p[2] += 32;
				trap_Trace( &tr, cand, NULL, NULL, p, ENTITYNUM_NONE, MASK_SOLID );
				if ( tr.fraction >= 0.99f ) visible++;
			}

			score = visible * 100.0f + 100.0f * ( got / wanted );
			/* mild preference for looking along the approach direction of the strike */
			if ( lastDir ) {
				float dx = centroid[0] - cand[0], dy = centroid[1] - cand[1];
				float len = sqrt( dx * dx + dy * dy );
				if ( len > 1 ) {
					score += 15.0f * ( ( dx * lastDir[0] + dy * lastDir[1] ) / len );
				}
			}
			if ( score > bestScore ) {
				bestScore = score;
				VectorCopy( cand, bestPos );
			}
		}
	}

	VectorCopy( bestPos, shot->camPos );
	shot->haveCam = qtrue;
	shot->blastCamPlaced = qtrue;
	REPLAY_DPRINT( "strike camera for shot ent %d: %d points, radius %.0f, score %.0f\n",
				   shot->entNum, shot->pointCount, radius, bestScore );
}

/* Work out where the camera should be for this shot in the given recorded frame. */
static qboolean G_ReplayComputeShotCamera( replayShot_t *shot, const replayFrame_t *frame,
										   vec3_t outOrigin, vec3_t outAngles ) {
	const replaySample_t *proj = G_ReplayFindProjectileSample( frame, shot->entNum, shot->weapon );
	vec3_t aim, toAim;

	if ( proj ) {
		vec3_t dir, camPos;
		float speed = VectorNormalize2( proj->velocity, dir );

		if ( speed < 30.0f ) {
			if ( shot->haveLast ) {
				VectorCopy( shot->lastDir, dir );
			} else {
				VectorSet( dir, 1, 0, 0 );
			}
		}
		VectorCopy( proj->origin, shot->lastPos );
		VectorCopy( dir, shot->lastDir );
		shot->haveLast = qtrue;

		VectorMA( proj->origin, -REPLAY_SHOT_BACK_DIST, dir, camPos );
		camPos[2] += REPLAY_SHOT_UP_DIST;
		G_ReplayClipCameraPos( proj->origin, camPos );
		VectorCopy( camPos, shot->camPos );
		shot->haveCam = qtrue;
		VectorCopy( proj->origin, aim );
	} else if ( shot->haveLast ) {
		if ( ( shot->strikeType == REPLAY_STRIKE_AIRSTRIKE || shot->strikeType == REPLAY_STRIKE_ARTILLERY ) &&
			 !shot->blastCamPlaced ) {
			/* Airstrike: the can has popped.  Artillery: the fire mission is underway.
			 * Either way, watch the impact area from the best fixed viewpoint. */
			if ( shot->strikeType == REPLAY_STRIKE_AIRSTRIKE && shot->pointCount < REPLAY_SHOT_MAX_POINTS ) {
				VectorCopy( shot->lastPos, shot->points[shot->pointCount++] );
			}
			G_ReplayChooseStrikeCamera( shot, shot->strikeType == REPLAY_STRIKE_AIRSTRIKE ? shot->lastDir : NULL );
		}
		if ( !shot->haveCam ) {
			return qfalse;
		}
		VectorCopy( shot->blastCamPlaced ? shot->aimPos : shot->lastPos, aim );
	} else {
		return qfalse;
	}

	VectorCopy( shot->camPos, outOrigin );
	VectorSubtract( aim, outOrigin, toAim );
	vectoangles( toAim, outAngles );
	return qtrue;
}

/* Override the follow-view set by G_ReplayApplyTargetView with the shot camera. */
static void G_ReplayApplyShotView( gentity_t *viewer, const vec3_t origin, const vec3_t angles ) {
	playerState_t *ps = &viewer->client->ps;

	VectorCopy( origin, ps->origin );
	VectorClear( ps->velocity );
	VectorCopy( angles, ps->viewangles );
	ps->viewheight = 0;
	ps->weapon = WP_NONE;
	ps->weaponstate = WEAPON_READY;
	ps->pm_type = PM_NORMAL;
	ps->pm_flags = PMF_FOLLOW;
	ps->eFlags &= ~( EF_DEAD | EF_ZOOMING );
	ps->groundEntityNum = ENTITYNUM_NONE;
	ps->leanf = 0;
	ps->viewlocked = 0;
	if ( ps->stats[STAT_HEALTH] <= 0 ) {
		ps->stats[STAT_HEALTH] = 100;
	}
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

/* Tighten the window around the actual first/last scored events so the clip doesn't
 * start with several seconds of dead air before the action.  Scored events by the
 * same actor shortly after the window end (the rest of a bomb run, a trailing kill)
 * extend the end so they aren't cut off.  Only the winner is tightened. */
static void G_ReplayTightenSelection( replaySelection_t *selection ) {
	int firstEventTime = selection->windowEndTime;   /* sentinel - walk down */
	int lastEventTime  = selection->windowStartTime; /* sentinel - walk up   */
	int actor = selection->targetClientNum;
	int i;
	replaySelection_t tighter;

	for ( i = 0; i < g_replayState.eventCount; i++ ) {
		const replayEvent_t *ev = &g_replayState.events[i];
		if ( ev->actorClientNum != actor ) continue;
		if ( ev->serverTime < selection->windowStartTime ||
			 ev->serverTime > selection->windowEndTime + REPLAY_ACTION_POSTROLL_MSEC ) continue;
		if ( ev->score <= 0 ) continue;
		if ( ev->serverTime < firstEventTime ) firstEventTime = ev->serverTime;
		if ( ev->serverTime > lastEventTime  ) lastEventTime  = ev->serverTime;
	}

	if ( firstEventTime <= lastEventTime ) {
		int newStart = firstEventTime - REPLAY_ACTION_PREROLL_MSEC;
		int newEnd   = lastEventTime  + REPLAY_ACTION_POSTROLL_MSEC;
		if ( newStart < 0 ) newStart = 0;
		if ( G_ReplayBuildSelection( actor, selection->score, newStart, newEnd, &tighter ) ) {
			*selection = tighter;
		}
	}

	/* Strike camera shots keep playing after the thrower dies, so the alive-run cut
	 * must not truncate them. */
	{
		replayShot_t shots[REPLAY_MAX_SHOTS];
		int n = G_ReplayCollectShots( actor, selection->windowStartTime,
									  selection->windowEndTime + REPLAY_ACTION_POSTROLL_MSEC,
									  shots, REPLAY_MAX_SHOTS );
		for ( i = 0; i < n; i++ ) {
			/* Artillery: start the clip at the binocular use, which precedes the kills by 9+ s. */
			if ( shots[i].strikeType == REPLAY_STRIKE_ARTILLERY ) {
				int idx = G_ReplayFindFrameAtOrAfter( shots[i].launchTime - 1000 );
				if ( idx >= 0 && idx < selection->startFrameIndex ) {
					selection->startFrameIndex = idx;
					selection->clipStartTime = g_replayState.frames[idx].serverTime;
				}
			}
			if ( shots[i].endTime > selection->clipEndTime ) {
				int idx = G_ReplayFindFrameAtOrBefore( shots[i].endTime );
				if ( idx > selection->endFrameIndex ) {
					selection->endFrameIndex = idx;
					selection->clipEndTime = g_replayState.frames[idx].serverTime;
				}
			}
		}
	}
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

		/* Ties go to the later window so the clip contains the final kill. */
		if ( score <= 0 || score < bestScore ) {
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
		G_ReplayTightenSelection( selection );
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
	if ( g_replayState.archiveWritten || g_replayState.serverMode ) {
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

/* First entity number that can only be a non-client entity.  In replay-server mode the recorded
 * players occupy client slots that have no connected client, so they count as replay entities too. */
static int G_ReplayFirstReplayEntity( void ) {
	return g_replayState.serverMode ? 0 : g_maxclients.integer;
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

		MoveClientToIntermission( viewer );
	}

	/* Deactivate any non-client entities that were activated for replay. */
	for ( i = G_ReplayFirstReplayEntity(); i < MAX_GENTITIES; i++ ) {
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
	g_replayState.shotCount = G_ReplayCollectShots( g_replayState.selection.targetClientNum,
													g_replayState.selection.clipStartTime,
													g_replayState.selection.clipEndTime,
													g_replayState.shots, REPLAY_MAX_SHOTS );
	for ( i = 0; i < g_replayState.shotCount; i++ ) {
		REPLAY_DPRINT( "strike shot %d: ent %d type %d [%d,%d] end %d\n", i,
				  g_replayState.shots[i].entNum, g_replayState.shots[i].strikeType,
				  g_replayState.shots[i].startTime, g_replayState.shots[i].projEndTime,
				  g_replayState.shots[i].endTime );
	}
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


/* ---- Replay-server mode: load a clip from a .rpl ------------------------- */

#define REPLAY_LOAD_MARGIN_MSEC 1000
#define REPLAY_LOAD_MAX_CHUNK_BYTES ( 256 * 1024 * 1024 )

static int G_ReplayArchiveHeaderBytes( int version ) {
	if ( version >= 6 ) {
		return (int)sizeof( replayArchiveHeader_t );
	}
	if ( version == 5 ) {
		return 2412;
	}
	return 108;
}

/* Returns the text after "key=" at the start of a line, or NULL. */
static const char *G_ReplayMetaValue( const char *text, const char *key ) {
	size_t kl = strlen( key );
	const char *p = text;

	while ( *p ) {
		if ( !strncmp( p, key, kl ) && p[kl] == '=' ) {
			return p + kl + 1;
		}
		while ( *p && *p != '\n' ) {
			p++;
		}
		if ( *p == '\n' ) {
			p++;
		}
	}
	return NULL;
}

static int G_ReplayMetaInt( const char *text, const char *key, int def ) {
	const char *v = G_ReplayMetaValue( text, key );
	return v ? atoi( v ) : def;
}

static void G_ReplayCopyLine( const char *src, char *dst, int dstSize ) {
	int n = 0;

	while ( src && *src && *src != '\n' && *src != '\r' && n < dstSize - 1 ) {
		dst[n++] = *src++;
	}
	dst[n] = '\0';
}

/* Append the frames/events of one decompressed chunk, keeping only frames in [keepStart, keepEnd]. */
static qboolean G_ReplayLoadChunkPayload( const byte *buf, int size, const replayArchiveHeader_t *hdr,
										  int keepStart, int keepEnd ) {
	int pos = 0;
	int frameCount, eventCount, i;
	int sampleSize = hdr->sampleSize;
	int eventSize = hdr->eventSize;

	if ( size < 8 ) {
		return qfalse;
	}
	memcpy( &frameCount, buf, 4 );
	memcpy( &eventCount, buf + 4, 4 );
	pos = 8;

	for ( i = 0; i < frameCount; i++ ) {
		int serverTime, sampleCount;
		int bytes;

		if ( pos + 8 > size ) {
			return qfalse;
		}
		memcpy( &serverTime, buf + pos, 4 );
		memcpy( &sampleCount, buf + pos + 4, 4 );
		pos += 8;
		if ( sampleCount < 0 || sampleCount > MAX_GENTITIES ) {
			return qfalse;
		}
		bytes = sampleCount * sampleSize;
		if ( pos + bytes > size ) {
			return qfalse;
		}

		if ( serverTime >= keepStart && serverTime <= keepEnd ) {
			replayFrame_t *frame;

			if ( !G_ReplayEnsureCapacity( (void **)&g_replayState.frames, &g_replayState.frameCapacity,
										  g_replayState.frameCount + 1, sizeof( g_replayState.frames[0] ) ) ||
				 !G_ReplayEnsureCapacity( (void **)&g_replayState.samples, &g_replayState.sampleCapacity,
										  g_replayState.sampleCount + sampleCount, sizeof( g_replayState.samples[0] ) ) ) {
				return qfalse;
			}
			frame = &g_replayState.frames[g_replayState.frameCount++];
			frame->serverTime  = serverTime;
			frame->firstSample = g_replayState.sampleCount;
			frame->sampleCount = sampleCount;
			memcpy( &g_replayState.samples[g_replayState.sampleCount], buf + pos, bytes );
			g_replayState.sampleCount += sampleCount;
		}
		pos += bytes;
	}

	for ( i = 0; i < eventCount; i++ ) {
		replayEvent_t *ev;

		if ( pos + eventSize > size ) {
			return qfalse;
		}
		if ( !G_ReplayEnsureCapacity( (void **)&g_replayState.events, &g_replayState.eventCapacity,
									  g_replayState.eventCount + 1, sizeof( g_replayState.events[0] ) ) ) {
			return qfalse;
		}
		ev = &g_replayState.events[g_replayState.eventCount++];
		memset( ev, 0, sizeof( *ev ) );
		ev->inflictorEntNum = -1;       /* defaults for archives older than v8 */
		ev->launchEntNum    = -1;
		memcpy( ev, buf + pos, eventSize < (int)sizeof( *ev ) ? eventSize : (int)sizeof( *ev ) );
		pos += eventSize;
	}
	return qtrue;
}

/* Build the CS_PLAYERS strings for every client that appears in the clip. */
static void G_ReplayBuildPlayerConfigstrings( const replayArchiveHeader_t *hdr, const char *metaText ) {
	int i, f;
	qboolean seen[MAX_CLIENTS];
	int team[MAX_CLIENTS];
	int pclass[MAX_CLIENTS];
	char name[MAX_CLIENTS][MAX_NETNAME];

	memset( seen, 0, sizeof( seen ) );
	memset( team, 0, sizeof( team ) );
	memset( pclass, 0, sizeof( pclass ) );

	for ( f = g_replayState.selection.startFrameIndex; f <= g_replayState.selection.endFrameIndex; f++ ) {
		const replayFrame_t *frame = &g_replayState.frames[f];

		for ( i = 0; i < frame->sampleCount; i++ ) {
			const replaySample_t *sm = &g_replayState.samples[frame->firstSample + i];
			int c = sm->clientNum;

			if ( c < 0 || c >= MAX_CLIENTS || c >= hdr->maxclients || sm->es.eType != ET_PLAYER ) {
				continue;
			}
			seen[c] = qtrue;
			team[c] = sm->team;
			pclass[c] = sm->playerClass;
		}
	}

	for ( i = 0; i < MAX_CLIENTS; i++ ) {
		const char *v;
		char key[32];
		int e;

		Com_sprintf( name[i], sizeof( name[i] ), "Player %d", i );
		if ( hdr->version >= 5 && hdr->playerNames[i][0] ) {
			Q_strncpyz( name[i], hdr->playerNames[i], sizeof( name[i] ) );
		}
		Com_sprintf( key, sizeof( key ), "player_%d", i );
		v = G_ReplayMetaValue( metaText, key );
		if ( v ) {
			char line[MAX_NETNAME];
			G_ReplayCopyLine( v, line, sizeof( line ) );
			if ( line[0] ) {
				Q_strncpyz( name[i], line, sizeof( name[i] ) );
			}
		}
		/* the latest join/rename seen before the clip started wins */
		for ( e = 0; e < g_replayState.eventCount; e++ ) {
			const replayEvent_t *ev = &g_replayState.events[e];
			if ( ev->serverTime > g_replayState.selection.clipStartTime ) break;
			if ( ev->actorClientNum == i && ev->name[0] &&
				 ( ev->type == REPLAY_EVENT_PLAYER_JOIN || ev->type == REPLAY_EVENT_PLAYER_RENAME ) ) {
				Q_strncpyz( name[i], ev->name, sizeof( name[i] ) );
			}
		}
	}

	for ( i = 0; i < MAX_CLIENTS; i++ ) {
		Q_strncpyz( g_replayState.playerName[i], name[i], sizeof( g_replayState.playerName[i] ) );
	}

	for ( i = 0; i < MAX_CLIENTS; i++ ) {
		const char *modelDir = team[i] == TEAM_BLUE ? "multi" : "multi_axis";
		const char *teamName = team[i] == TEAM_BLUE ? "blue" : "red";
		const char *cls;

		if ( !seen[i] ) {
			g_replayState.playerCS[i][0] = '\0';
			continue;
		}
		switch ( pclass[i] ) {
		case PC_MEDIC:    cls = "medic"; break;
		case PC_ENGINEER: cls = "engineer"; break;
		case PC_LT:       cls = "lieutenant"; break;
		default:          cls = "soldier"; break;
		}
		/* Same shape as ClientUserinfoChanged in g_client.c: in Wolf MP the model is "<dir>/<skin>" and
		 * the head is just the skin name (e.g. model multi_axis/redsoldier1, head redsoldier1); an empty
		 * head makes cgame fail to register the player.  Skin 1: the recorded skin number is not archived. */
		Com_sprintf( g_replayState.playerCS[i], sizeof( g_replayState.playerCS[i] ),
					 "n\\%s\\t\\%i\\model\\%s/%s%s1\\head\\%s%s1\\c1\\0\\hc\\100\\w\\0\\l\\0",
					 name[i], team[i], modelDir, teamName, cls, teamName, cls );
		trap_SetConfigstring( CS_PLAYERS + i, g_replayState.playerCS[i] );
	}
}

static qboolean G_ReplayLoadFromFile( const char *base ) {
	const char *dir = g_replayPath.string[0] ? g_replayPath.string : "replays";
	char path[MAX_QPATH];
	char metaText[4096];
	fileHandle_t f;
	int len, remaining;
	replayArchiveHeader_t hdr;
	int keepStart, keepEnd;
	int versionAndMagic[2];
	int headerBytes;
	int chunks = 0, chunksLoaded = 0;

	if ( !base[0] || strstr( base, ".." ) || strchr( base, '/' ) || strchr( base, '\\' ) ) {
		G_Printf( "[replay] load: bad file name '%s'\n", base );
		return qfalse;
	}

	/* sidecar: selection + names */
	Com_sprintf( path, sizeof( path ), "%s/%s.txt", dir, base );
	len = trap_FS_FOpenFile( path, &f, FS_READ );
	if ( len <= 0 || len >= (int)sizeof( metaText ) ) {
		G_Printf( "[replay] load: cannot read %s (len %d)\n", path, len );
		if ( len >= 0 ) {
			trap_FS_FCloseFile( f );
		}
		return qfalse;
	}
	trap_FS_Read( metaText, len, f );
	metaText[len] = '\0';
	trap_FS_FCloseFile( f );

	memset( &g_replayState.selection, 0, sizeof( g_replayState.selection ) );
	g_replayState.selection.targetClientNum = G_ReplayMetaInt( metaText, "selectionTarget", -1 );
	g_replayState.selection.score           = G_ReplayMetaInt( metaText, "selectionScore", 0 );
	g_replayState.selection.windowStartTime = G_ReplayMetaInt( metaText, "selectionWindowStart", 0 );
	g_replayState.selection.windowEndTime   = G_ReplayMetaInt( metaText, "selectionWindowEnd", 0 );
	g_replayState.selection.clipStartTime   = G_ReplayMetaInt( metaText, "selectionClipStart", 0 );
	g_replayState.selection.clipEndTime     = G_ReplayMetaInt( metaText, "selectionClipEnd", 0 );
	if ( g_replayState.selection.targetClientNum < 0 ||
		 g_replayState.selection.clipEndTime <= g_replayState.selection.clipStartTime ) {
		G_Printf( "[replay] load: %s has no play of the game selection\n", path );
		return qfalse;
	}
	keepStart = g_replayState.selection.clipStartTime - REPLAY_LOAD_MARGIN_MSEC;
	keepEnd   = g_replayState.selection.clipEndTime + REPLAY_LOAD_MARGIN_MSEC;

	/* archive */
	Com_sprintf( path, sizeof( path ), "%s/%s.rpl", dir, base );
	len = trap_FS_FOpenFile( path, &f, FS_READ );
	if ( len < 8 ) {
		G_Printf( "[replay] load: cannot read %s (len %d)\n", path, len );
		if ( len >= 0 ) {
			trap_FS_FCloseFile( f );
		}
		return qfalse;
	}
	remaining = len;

	trap_FS_Read( versionAndMagic, 8, f );
	remaining -= 8;
	if ( versionAndMagic[0] != REPLAY_ARCHIVE_MAGIC || versionAndMagic[1] < 4 ||
		 versionAndMagic[1] > REPLAY_ARCHIVE_VERSION ) {
		G_Printf( "[replay] load: %s has unsupported magic/version (%x/%d)\n", path,
				  versionAndMagic[0], versionAndMagic[1] );
		trap_FS_FCloseFile( f );
		return qfalse;
	}
	headerBytes = G_ReplayArchiveHeaderBytes( versionAndMagic[1] );
	if ( headerBytes - 8 > remaining ) {
		trap_FS_FCloseFile( f );
		return qfalse;
	}
	memset( &hdr, 0, sizeof( hdr ) );
	hdr.magic   = versionAndMagic[0];
	hdr.version = versionAndMagic[1];
	trap_FS_Read( ( (byte *)&hdr ) + 8, headerBytes - 8, f );
	remaining -= headerBytes - 8;

	if ( hdr.sampleSize != (int)sizeof( replaySample_t ) || hdr.eventSize < 40 ||
		 hdr.maxclients <= 0 || hdr.maxclients > MAX_CLIENTS ) {
		G_Printf( "[replay] load: %s was written by an incompatible build (sampleSize %d vs %d, eventSize %d, maxclients %d)\n",
				  path, hdr.sampleSize, (int)sizeof( replaySample_t ), hdr.eventSize, hdr.maxclients );
		trap_FS_FCloseFile( f );
		return qfalse;
	}
	g_replayState.recordedMaxClients = hdr.maxclients;

	while ( remaining >= (int)sizeof( replayChunkHeader_t ) ) {
		replayChunkHeader_t ch;
		qboolean wanted;

		trap_FS_Read( &ch, sizeof( ch ), f );
		remaining -= sizeof( ch );
		chunks++;
		if ( ch.compressedBytes <= 0 || ch.compressedBytes > remaining ||
			 ch.uncompressedBytes <= 0 || ch.uncompressedBytes > REPLAY_LOAD_MAX_CHUNK_BYTES ) {
			break;
		}

		wanted = !( ch.endTime < keepStart || ch.startTime > keepEnd );
		if ( wanted ) {
			byte *cbuf = (byte *)malloc( ch.compressedBytes );
			byte *ubuf = (byte *)malloc( ch.uncompressedBytes );
			uLongf ulen = ch.uncompressedBytes;
			qboolean ok = qfalse;

			if ( cbuf && ubuf ) {
				trap_FS_Read( cbuf, ch.compressedBytes, f );
				if ( uncompress( ubuf, &ulen, cbuf, ch.compressedBytes ) == Z_OK ) {
					ok = G_ReplayLoadChunkPayload( ubuf, (int)ulen, &hdr, keepStart, keepEnd );
				}
			}
			free( cbuf );
			free( ubuf );
			if ( !ok ) {
				G_Printf( "[replay] load: chunk %d [%d,%d] failed to load\n", chunks, ch.startTime, ch.endTime );
				trap_FS_FCloseFile( f );
				return qfalse;
			}
			chunksLoaded++;
		} else {
			/* no seek in the game syscall API: read and discard */
			byte scratch[16384];
			int left = ch.compressedBytes;

			while ( left > 0 ) {
				int n = left < (int)sizeof( scratch ) ? left : (int)sizeof( scratch );
				trap_FS_Read( scratch, n, f );
				left -= n;
			}
		}
		remaining -= ch.compressedBytes;
	}
	trap_FS_FCloseFile( f );

	g_replayState.selection.startFrameIndex = G_ReplayFindFrameAtOrAfter( g_replayState.selection.clipStartTime );
	g_replayState.selection.endFrameIndex   = G_ReplayFindFrameAtOrBefore( g_replayState.selection.clipEndTime );
	if ( g_replayState.selection.startFrameIndex < 0 ||
		 g_replayState.selection.endFrameIndex < g_replayState.selection.startFrameIndex ) {
		G_Printf( "[replay] load: no frames in clip [%d,%d] (%d chunks, %d loaded, %d frames kept)\n",
				  g_replayState.selection.clipStartTime, g_replayState.selection.clipEndTime,
				  chunks, chunksLoaded, g_replayState.frameCount );
		return qfalse;
	}

	G_ReplayBuildPlayerConfigstrings( &hdr, metaText );

	G_Printf( "[replay] loaded %s: v%d, target cl %d, clip [%d,%d], %d/%d chunks, %d frames, %d samples, %d events\n",
			  base, hdr.version, g_replayState.selection.targetClientNum,
			  g_replayState.selection.clipStartTime, g_replayState.selection.clipEndTime,
			  chunksLoaded, chunks, g_replayState.frameCount, g_replayState.sampleCount,
			  g_replayState.eventCount );
	return qtrue;
}

/* In replay-server mode, the viewer's own slot must keep the recorded player's CS_PLAYERS entry. */
qboolean G_ReplayOverrideConfigstring( int clientNum ) {
	if ( !g_replayState.serverMode || clientNum < 0 || clientNum >= MAX_CLIENTS ||
		 !g_replayState.playerCS[clientNum][0] ) {
		return qfalse;
	}
	trap_SetConfigstring( CS_PLAYERS + clientNum, g_replayState.playerCS[clientNum] );
	return qtrue;
}

qboolean G_ReplayServerMode( void ) {
	return g_replayState.serverMode;
}

/* Called every frame; once a viewer has joined, run the normal end-of-round path so the
 * clip plays through the same countdown/playback machinery as the live POTG. */
void G_ReplayServerFrame( void ) {
	int i;

	if ( !g_replayState.serverMode || g_replayState.serverStarted || level.intermissiontime ) {
		return;
	}
	for ( i = 0; i < g_maxclients.integer; i++ ) {
		if ( level.clients[i].pers.connected == CON_CONNECTED ) {
			g_replayState.serverStarted = qtrue;
			REPLAY_DPRINT( "viewer %d joined, starting playback\n", i );
			BeginIntermission();
			return;
		}
	}
}

void G_ReplayInit( void ) {
	G_ReplayResetState();
	if ( g_replayLoadFile.string[0] ) {
		if ( G_ReplayLoadFromFile( g_replayLoadFile.string ) ) {
			g_replayState.serverMode = qtrue;
			g_replayState.hasSelection = qtrue;
		} else {
			G_Printf( "[replay] load of '%s' failed; running as a normal game\n", g_replayLoadFile.string );
			G_ReplayResetState();
		}
	}
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

	if ( !g_replayEnable.integer || g_gamestate.integer != GS_PLAYING || level.intermissiontime ||
		 g_replayState.serverMode ) {
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

	G_ReplayExtendLiveCandidate( &g_replayState.frames[frameIndex] );

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
			const char *victim = ( g_replayState.serverMode && g_replayState.playerName[ev->targetClientNum][0] )
				? g_replayState.playerName[ev->targetClientNum]
				: level.clients[ev->targetClientNum].pers.netname;
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
	replayShot_t *shot;
	vec3_t shotOrigin, shotAngles;
	qboolean shotView;

	if ( g_replayState.phase != REPLAY_PHASE_PLAYBACK ) {
		return;
	}

	if ( level.time - g_replayState.playbackStartServerTime >=
		 g_replayState.playbackClipEndTime - g_replayState.playbackClipStartTime ) {
		/* Flush any obituary/hit still pending in the last tick before stopping. */
		G_ReplayDispatchKillMessages( g_replayState.playbackClipEndTime );
		REPLAY_DPRINT( "clip finished (elapsed %d ms, duration %d ms)\n",
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
	shot = G_ReplayActiveShot( targetReplayTime );
	if ( !G_ReplaySampleAlive( targetSample ) && !shot ) {
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

	for ( i = G_ReplayFirstReplayEntity(); i < MAX_GENTITIES; i++ ) {
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

	shotView = shot && G_ReplayComputeShotCamera( shot, frame, shotOrigin, shotAngles );

	for ( i = 0; i < g_maxclients.integer; i++ ) {
		gentity_t *viewer = &g_entities[i];

		if ( !viewer->inuse || !viewer->client || viewer->client->pers.connected != CON_CONNECTED ) {
			continue;
		}

		G_ReplayApplyTargetView( viewer, targetSample );
		if ( shotView ) {
			G_ReplayApplyShotView( viewer, shotOrigin, shotAngles );
		}
	}
}

void G_ReplayBeginIntermission( void ) {
	if ( !g_replayEnable.integer || g_gametype.integer < GT_WOLF ) {
		g_replayState.phase = REPLAY_PHASE_NONE;
		g_replayState.hasSelection = qfalse;
		return;
	}

	if ( g_replayState.serverMode ) {
		/* The clip was loaded from a .rpl: skip selection and archiving, go straight to the
		 * countdown (there is no scoreboard worth showing). */
		G_ReplayStartCountdown();
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
		G_ReplayTightenSelection( &g_replayState.selection );
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
		/* a replay server stays on the intermission view instead of changing map */
		return g_replayState.serverMode;
	}

	return qfalse;
}

qboolean G_ReplayActive( void ) {
	return g_replayState.phase == REPLAY_PHASE_PLAYBACK;
}

static void G_ReplayRegisterKillEvents( gentity_t *victim, gentity_t *attacker, int meansOfDeath ) {
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

/* G_Damage substitutes the world entity when it is given no inflictor, so rocket splash arrives
 * here as "world".  Prefer the projectile that g_missile.c announced through the hint. */
static gentity_t *G_ReplayResolveInflictor( gentity_t *inflictor ) {
	if ( ( !inflictor || inflictor->s.number >= ENTITYNUM_WORLD ) && g_replayInflictorHint ) {
		return g_replayInflictorHint;
	}
	return inflictor;
}

static int G_ReplayClassifyStrike( const gentity_t *inflictor ) {
	if ( !inflictor || !inflictor->classname ) {
		return REPLAY_STRIKE_NONE;
	}
	if ( !Q_stricmp( inflictor->classname, "air strike" ) ) {
		/* Weapon_Artillery tags its shells aiName = "artillery"; weapon_callAirStrike uses "air strike". */
		if ( inflictor->aiName && !Q_stricmp( inflictor->aiName, "artillery" ) ) {
			return REPLAY_STRIKE_ARTILLERY;
		}
		return REPLAY_STRIKE_AIRSTRIKE;
	}
	if ( !Q_stricmp( inflictor->classname, "grenade" ) ) {
		return REPLAY_STRIKE_GRENADE;
	}
	if ( !Q_stricmp( inflictor->classname, "rocket" ) ) {
		return REPLAY_STRIKE_PANZER;
	}
	return inflictor->s.eType == ET_MISSILE || inflictor->s.eType == ET_GENERAL ? REPLAY_STRIKE_OTHER : REPLAY_STRIKE_NONE;
}

/* Stamp inflictor/attacker info onto every event appended since firstEventIdx. */
static void G_ReplayStampCombatInfo( int firstEventIdx, const gentity_t *inflictor, const gentity_t *attacker ) {
	int i;

	for ( i = firstEventIdx; i < g_replayState.eventCount; i++ ) {
		replayEvent_t *ev = &g_replayState.events[i];

		if ( inflictor ) {
			ev->inflictorEntNum = inflictor->s.number;
			ev->inflictorWeapon = inflictor->s.weapon;
			ev->strikeType = G_ReplayClassifyStrike( inflictor );
			VectorCopy( inflictor->r.currentOrigin, ev->inflictorOrigin );
			if ( ev->strikeType == REPLAY_STRIKE_GRENADE || ev->strikeType == REPLAY_STRIKE_PANZER ) {
				ev->launchEntNum = inflictor->s.number;
			} else if ( ev->strikeType == REPLAY_STRIKE_AIRSTRIKE ) {
				/* bombs are owned by the smoke can that called them */
				ev->launchEntNum = inflictor->r.ownerNum;
			}
		}
		if ( attacker && attacker->client ) {
			VectorCopy( attacker->r.currentOrigin, ev->attackerOrigin );
		}
	}
}

void G_ReplayRegisterKill( gentity_t *victim, gentity_t *attacker, gentity_t *inflictor, int meansOfDeath ) {
	int firstEventIdx = g_replayState.eventCount;

	inflictor = G_ReplayResolveInflictor( inflictor );
	G_ReplayRegisterKillEvents( victim, attacker, meansOfDeath );
	G_ReplayStampCombatInfo( firstEventIdx, inflictor, attacker );
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

void G_ReplayRecordDamage( gentity_t *attacker, gentity_t *victim, gentity_t *inflictor, int damage, int mod ) {
	int firstEventIdx = g_replayState.eventCount;

	inflictor = G_ReplayResolveInflictor( inflictor );
	if ( !attacker || !attacker->client || !victim || !victim->client ) {
		return;
	}
	if ( attacker->s.number == victim->s.number ) {
		return;
	}
	G_ReplayAppendEvent( attacker->s.number, victim->s.number,
						 REPLAY_EVENT_DAMAGE, damage, mod, 0,
						 victim->r.currentOrigin );
	G_ReplayStampCombatInfo( firstEventIdx, inflictor, attacker );
}

/* Track who occupies each client slot so the timeline can name mid-match joiners
 * and slots reused by a different player.  Called from ClientBegin and on userinfo changes. */
void G_ReplayRecordPlayerName( int clientNum ) {
	const gentity_t *ent;
	replayEvent_t *ev;
	char clean[MAX_NETNAME];
	const char *src;
	char *dst;
	qboolean wasEmpty;

	if ( clientNum < 0 || clientNum >= MAX_CLIENTS ) {
		return;
	}
	ent = &g_entities[clientNum];
	if ( !ent->client || ent->client->pers.connected == CON_DISCONNECTED ) {
		return;
	}

	/* strip ^N color codes, same as the header names */
	dst = clean;
	for ( src = ent->client->pers.netname; *src && dst < clean + sizeof( clean ) - 1; src++ ) {
		if ( *src == Q_COLOR_ESCAPE && src[1] ) {
			src++;
			continue;
		}
		*dst++ = *src;
	}
	*dst = '\0';

	if ( !clean[0] || !strcmp( clean, g_replayState.slotName[clientNum] ) ) {
		return;
	}

	wasEmpty = g_replayState.slotName[clientNum][0] == '\0';
	ev = G_ReplayAppendEvent( clientNum, -1, wasEmpty ? REPLAY_EVENT_PLAYER_JOIN : REPLAY_EVENT_PLAYER_RENAME,
							  0, MOD_UNKNOWN, 0, vec3_origin );
	if ( !ev ) {
		/* not recording (warmup/intermission): the header snapshot covers those names */
		Q_strncpyz( g_replayState.slotName[clientNum], clean, sizeof( g_replayState.slotName[0] ) );
		return;
	}
	Q_strncpyz( ev->name, clean, sizeof( ev->name ) );
	Q_strncpyz( g_replayState.slotName[clientNum], clean, sizeof( g_replayState.slotName[0] ) );
}

void G_ReplayRegisterArtilleryLaunch( gentity_t *lt, vec3_t target ) {
	if ( !lt || !lt->client ) {
		return;
	}
	G_ReplayAppendEvent( lt->s.number, -1, REPLAY_EVENT_STRIKE_LAUNCH, 0, MOD_UNKNOWN,
						 REPLAY_STRIKE_ARTILLERY, target );
}

void G_ReplayRecordPlayerLeave( int clientNum ) {
	if ( clientNum < 0 || clientNum >= MAX_CLIENTS || !g_replayState.slotName[clientNum][0] ) {
		return;
	}
	G_ReplayAppendEvent( clientNum, -1, REPLAY_EVENT_PLAYER_LEAVE, 0, MOD_UNKNOWN, 0, vec3_origin );
	g_replayState.slotName[clientNum][0] = '\0';
}
