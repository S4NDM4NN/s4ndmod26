#include "g_local.h"

void G_StoreClientPosition( gentity_t* ent ) {
	int	top;

	if(!( ent->inuse && 
 		(ent->client->sess.sessionTeam == TEAM_RED || ent->client->sess.sessionTeam == TEAM_BLUE) && 
 		ent->r.linked &&
 		(ent->health > 0) &&
 		!(ent->client->ps.pm_flags & PMF_LIMBO) &&
		(ent->client->ps.pm_type == PM_NORMAL)
 	)) {
		return;
	}

	ent->client->topMarker++;
	if( ent->client->topMarker >= MAX_CLIENT_MARKERS ) {
		ent->client->topMarker = 0;
	}

	top = ent->client->topMarker;

	VectorCopy( ent->r.mins, ent->client->clientMarkers[top].mins );
	VectorCopy( ent->r.maxs, ent->client->clientMarkers[top].maxs );
	VectorCopy( ent->s.pos.trBase, ent->client->clientMarkers[top].origin );
	ent->client->clientMarkers[top].time = level.time;

	VectorCopy( ent->client->ps.viewangles, ent->client->clientMarkers[top].viewangles );
	ent->client->clientMarkers[top].pm_flags = ent->client->ps.pm_flags;
	ent->client->clientMarkers[top].viewheight = ent->client->ps.viewheight;
	ent->client->clientMarkers[top].waterlevel = ent->waterlevel;
}

// Rewind the state G_BuildHead, the duck flag and the swim hitbox read.  Only PMF_DUCKED is touched in pm_flags: the
// other bits can change during the trace (knockback from the hit, etc.) and must survive the restore.
static void G_ApplyMarkerState( gentity_t* ent, const vec3_t viewangles, int pm_flags, int viewheight, int waterlevel ) {
	VectorCopy( viewangles, ent->client->ps.viewangles );
	ent->client->ps.pm_flags = ( ent->client->ps.pm_flags & ~PMF_DUCKED ) | ( pm_flags & PMF_DUCKED );
	ent->client->ps.viewheight = viewheight;
	ent->waterlevel = waterlevel;
}

static void G_AdjustSingleClientPosition( gentity_t* ent, int time ) {
	int	i, j;

	if( time > level.time ) {
		time = level.time;
	} // no lerping forward....

	// never rewind further than g_maxUnlag, whatever the client claims
	if( g_maxUnlag.integer > 0 && time < level.time - g_maxUnlag.integer ) {
		time = level.time - g_maxUnlag.integer;
	}

	// find a pair of markers which bound the requested time
	i = j = ent->client->topMarker;
	do {
		if( ent->client->clientMarkers[i].time <= time ) {
			break;
		}

		j = i;
		i--;
		if( i < 0 ) {
			i = MAX_CLIENT_MARKERS - 1;
		}
	} while( i != ent->client->topMarker );

	// time at or after the newest marker (i == j == topMarker): fall through and clamp to the newest
	// marker instead of skipping the rewind

	// save current position to backup
	if( ent->client->backupMarker.time != level.time ) {
		VectorCopy( ent->r.currentOrigin, ent->client->backupMarker.origin );
		VectorCopy( ent->r.mins, ent->client->backupMarker.mins );
		VectorCopy( ent->r.maxs, ent->client->backupMarker.maxs );
		VectorCopy( ent->client->ps.viewangles, ent->client->backupMarker.viewangles );
		ent->client->backupMarker.pm_flags = ent->client->ps.pm_flags;
		ent->client->backupMarker.viewheight = ent->client->ps.viewheight;
		ent->client->backupMarker.waterlevel = ent->waterlevel;
		ent->client->backupMarker.time = level.time;
	}

	if( i != ent->client->topMarker ) {
		float frac = (float)(time - ent->client->clientMarkers[i].time) /
			(float)(ent->client->clientMarkers[j].time - ent->client->clientMarkers[i].time);

		LerpPosition( ent->client->clientMarkers[i].origin, ent->client->clientMarkers[j].origin, frac,	ent->r.currentOrigin );
		LerpPosition( ent->client->clientMarkers[i].mins, ent->client->clientMarkers[j].mins, frac, ent->r.mins );
		LerpPosition( ent->client->clientMarkers[i].maxs, ent->client->clientMarkers[j].maxs, frac, ent->r.maxs );

		{
			vec3_t		angles;
			int			k, closer;

			for( k = 0; k < 3; k++ ) {
				angles[k] = LerpAngle( ent->client->clientMarkers[i].viewangles[k], ent->client->clientMarkers[j].viewangles[k], frac );
			}
			// the flags can't be lerped: take the marker closer in time
			closer = ( ent->client->clientMarkers[j].time - time < time - ent->client->clientMarkers[i].time ) ? j : i;
			G_ApplyMarkerState( ent, angles, ent->client->clientMarkers[closer].pm_flags, ent->client->clientMarkers[closer].viewheight, ent->client->clientMarkers[closer].waterlevel );
		}
	} else {
		VectorCopy( ent->client->clientMarkers[j].origin, ent->r.currentOrigin );
		VectorCopy( ent->client->clientMarkers[j].mins,	ent->r.mins );
		VectorCopy( ent->client->clientMarkers[j].maxs,	ent->r.maxs );
		G_ApplyMarkerState( ent, ent->client->clientMarkers[j].viewangles, ent->client->clientMarkers[j].pm_flags, ent->client->clientMarkers[j].viewheight, ent->client->clientMarkers[j].waterlevel );
	}

	trap_LinkEntity( ent );
}

static void G_ReAdjustSingleClientPosition( gentity_t* ent ) {
	if( !ent || !ent->client ) {
		return;
	}

	// restore from backup
	if( ent->client->backupMarker.time == level.time ) {
		VectorCopy( ent->client->backupMarker.origin, ent->r.currentOrigin );
		VectorCopy( ent->client->backupMarker.mins, ent->r.mins );
		VectorCopy( ent->client->backupMarker.maxs, ent->r.maxs );
		G_ApplyMarkerState( ent, ent->client->backupMarker.viewangles, ent->client->backupMarker.pm_flags, ent->client->backupMarker.viewheight, ent->client->backupMarker.waterlevel );
		ent->client->backupMarker.time = 0;

		trap_LinkEntity( ent );
	}
}

void G_AdjustClientPositions( gentity_t* ent, int time, qboolean forward ) {
	int	i;
	gentity_t	*list;

	for( i = 0; i < level.numConnectedClients; i++, list++ ) {
		list = g_entities + level.sortedClients[i];
		if( !forward ) {
			// put back whoever was rewound, even if they died in the trace
			if( list->client && list != ent ) {
				G_ReAdjustSingleClientPosition( list );
			}
			continue;
		}
		// Gordon: ok lets test everything under the sun
 		if( list->client && 
 			list->inuse && 
 			(list->client->sess.sessionTeam == TEAM_RED || list->client->sess.sessionTeam == TEAM_BLUE) && 
 			(list != ent) &&
 			list->r.linked &&
 			(list->health > 0) &&
 			!(list->client->ps.pm_flags & PMF_LIMBO) &&
			(list->client->ps.pm_type == PM_NORMAL)
 		) {
			if( forward ) {
				G_AdjustSingleClientPosition( list, time );
			} else {
				G_ReAdjustSingleClientPosition( list );
			}
		}
	}
}

// g_debugPlayerHitboxes & 4: when a bullet hits a player, draw that player's box where the server
// has them now (green) and where the rewind put them for the trace (red), body and head.
// & 8 prints one line per hit.  Called from Bullet_Fire_Extended while targets are still rewound,
// so backupMarker holds the unrewound position.
void G_DebugAntilagHit( gentity_t* shooter, gentity_t* target ) {
	gentity_t	*head;
	vec3_t		rewoundOrigin, delta;
	qboolean	rewound;
	char		msg[256];

	if( !( g_debugPlayerHitboxes.integer & ( 4 | 8 ) ) ) {
		return;
	}

	// bots fire constantly; only a human shooter's hits are worth looking at
	if( !shooter->client || ( shooter->r.svFlags & SVF_BOT ) || !target->client ) {
		return;
	}

	rewound = ( target->client->backupMarker.time == level.time );
	VectorCopy( target->r.currentOrigin, rewoundOrigin );
	VectorClear( delta );

	if( g_debugPlayerHitboxes.integer & 4 ) {
		// where the trace saw them: red
		G_RailPlayerBoxes( target, tv( 1.f, 0.f, 0.f ) );

		if( rewound ) {
			// where the server has them now: green.  The box helpers work from the entity's own state, so put the
			// unrewound state back for a moment.
			clientMarker_t	*backup = &target->client->backupMarker;
			vec3_t		rewoundMins, rewoundMaxs, rewoundAngles;
			int			rewoundFlags = target->client->ps.pm_flags, rewoundHeight = target->client->ps.viewheight;
			int			rewoundWater = target->waterlevel;

			VectorCopy( target->r.mins, rewoundMins );
			VectorCopy( target->r.maxs, rewoundMaxs );
			VectorCopy( target->client->ps.viewangles, rewoundAngles );

			VectorCopy( backup->origin, target->r.currentOrigin );
			VectorCopy( backup->mins, target->r.mins );
			VectorCopy( backup->maxs, target->r.maxs );
			G_ApplyMarkerState( target, backup->viewangles, backup->pm_flags, backup->viewheight, backup->waterlevel );
			G_RailPlayerBoxes( target, tv( 0.f, 1.f, 0.f ) );

			VectorCopy( rewoundOrigin, target->r.currentOrigin );
			VectorCopy( rewoundMins, target->r.mins );
			VectorCopy( rewoundMaxs, target->r.maxs );
			G_ApplyMarkerState( target, rewoundAngles, rewoundFlags, rewoundHeight, rewoundWater );
		}
	}

	if( g_debugPlayerHitboxes.integer & 8 ) {
		int			rewindMsec = level.time - shooter->client->pers.cmd.serverTime;
		qboolean	capped = qfalse;

		if( g_maxUnlag.integer > 0 && rewindMsec > g_maxUnlag.integer ) {
			rewindMsec = g_maxUnlag.integer;
			capped = qtrue;
		}

		if( rewound ) {
			VectorSubtract( target->client->backupMarker.origin, rewoundOrigin, delta );
		}
		Com_sprintf( msg, sizeof( msg ),
			"print \"antilag hit: %s rewind %dms%s moved %d units%s duck %d swim %d\n\"",
			target->client->pers.netname,
			rewindMsec,
			capped ? " (capped)" : "",
			(int)VectorLength( delta ),
			rewound ? "" : " (not rewound)",
			( target->client->ps.pm_flags & PMF_DUCKED ) ? 1 : 0,
			target->waterlevel > 1 ? 1 : 0 );
		trap_SendServerCommand( shooter - g_entities, msg );
	}
}

void G_ResetMarkers( gentity_t* ent ) {
	int	i, time;
	char	buffer[ MAX_CVAR_VALUE_STRING ];
	float	period;

	trap_Cvar_VariableStringBuffer( "sv_fps", buffer, sizeof( buffer ) - 1 );

	period = atoi( buffer );
	if( !period ) {
		period = 50;
	} else {
		period = 1000.f / period;
	}

	ent->client->topMarker = MAX_CLIENT_MARKERS - 1;
	for( i = MAX_CLIENT_MARKERS - 1, time = level.time; i >= 0; i--, time -= period ) {
		VectorCopy( ent->r.mins, ent->client->clientMarkers[i].mins );
		VectorCopy( ent->r.maxs, ent->client->clientMarkers[i].maxs );
		VectorCopy( ent->r.currentOrigin, ent->client->clientMarkers[i].origin );
		VectorCopy( ent->client->ps.viewangles, ent->client->clientMarkers[i].viewangles );
		ent->client->clientMarkers[i].pm_flags = ent->client->ps.pm_flags;
		ent->client->clientMarkers[i].viewheight = ent->client->ps.viewheight;
		ent->client->clientMarkers[i].waterlevel = ent->waterlevel;
		ent->client->clientMarkers[i].time = time;
	}
}

// A player swimming under water (waterlevel 3) plays the swim animation: the body lies flat along the way they
// face instead of standing in the upright box the player is linked with.  Measured from the swim_1h / swim_2h
// frames of the player model (body.mds), relative to the player's origin, "fwd" being their yaw direction:
// the body spans about -32..+30 forward, +-15 to the sides and 9..37 below the origin, and the head sits at the
// front.  Boxes are axis aligned, so the body is a short chain of squarish boxes rather than one long one.
qboolean G_IsSwimming( gentity_t *ent ) {
	return g_swimHitbox.integer && ent->client && ent->waterlevel > 2 &&
		   ent->health > 0 && ent->client->ps.pm_type == PM_NORMAL;
}

void G_SwimBoxes( const vec3_t origin, float yaw, vec3_t centers[SWIM_PARTS], vec3_t mins[SWIM_PARTS], vec3_t maxs[SWIM_PARTS] ) {
	static const float fwdOfs[SWIM_PARTS] = { -18, 0, 18, 30 };
	static const float zOfs[SWIM_PARTS]   = { -23, -23, -23, -18 };
	static const float half[SWIM_PARTS]   = { 14, 14, 14, 6 };     // half the box width
	static const float halfZ[SWIM_PARTS]  = { 15, 15, 15, 6 };     // half the box height
	float rad = DEG2RAD( yaw );
	vec3_t fwd;
	int i;

	fwd[0] = cos( rad );
	fwd[1] = sin( rad );
	fwd[2] = 0;

	for( i = 0; i < SWIM_PARTS; i++ ) {
		VectorMA( origin, fwdOfs[i], fwd, centers[i] );
		centers[i][2] += zOfs[i];
		VectorSet( mins[i], -half[i], -half[i], -halfZ[i] );
		VectorSet( maxs[i], half[i], half[i], halfZ[i] );
	}
}

// Draw a player's hitbox the way a trace sees it: upright box and head, or the swim boxes.
void G_RailPlayerBoxes( gentity_t *ent, vec3_t color ) {
	int number = ent - g_entities;

	if( G_IsSwimming( ent ) ) {
		vec3_t centers[SWIM_PARTS], mins[SWIM_PARTS], maxs[SWIM_PARTS];
		int i;

		G_SwimBoxes( ent->r.currentOrigin, ent->client->ps.viewangles[YAW], centers, mins, maxs );
		for( i = 0; i < SWIM_PARTS; i++ ) {
			G_RailBox( centers[i], mins[i], maxs[i], color, i == SWIM_PARTS - 1 ? ( number | HITBOXBIT_HEAD ) : ( number | ( ( i + 1 ) << 13 ) ) );
		}
	} else {
		gentity_t *head;

		G_RailBox( ent->r.currentOrigin, ent->r.mins, ent->r.maxs, color, number );
		head = G_BuildHead( ent );
		G_RailBox( head->r.currentOrigin, head->r.mins, head->r.maxs, color, number | HITBOXBIT_HEAD );
		G_FreeEntity( head );
	}
}

// Swap a swimming player's upright box for the swim boxes while a trace runs (G_DetachSwimBody puts it back).
static void G_AttachSwimBody( gentity_t *ent ) {
	vec3_t centers[SWIM_PARTS], mins[SWIM_PARTS], maxs[SWIM_PARTS];
	int i;

	G_SwimBoxes( ent->r.currentOrigin, ent->client->ps.viewangles[YAW], centers, mins, maxs );

	for( i = 0; i < SWIM_PARTS; i++ ) {
		gentity_t *part = G_Spawn();

		G_SetOrigin( part, centers[i] );
		VectorCopy( centers[i], part->r.currentOrigin );
		VectorCopy( mins[i], part->r.mins );
		VectorCopy( maxs[i], part->r.maxs );
		part->clipmask = CONTENTS_SOLID;
		part->r.contents = CONTENTS_SOLID;
		part->parent = ent;
		part->s.eType = ET_TEMPHEAD;    // G_SwitchBodyPartEntity turns a hit on it into a hit on the player
		trap_LinkEntity( part );
		ent->client->swimParts[i] = part;
	}

	ent->client->swimSavedContents = ent->r.contents;
	ent->r.contents = 0;
	trap_LinkEntity( ent );
}

static void G_DetachSwimBody( gentity_t *ent ) {
	int i;

	if( !ent->client->swimParts[0] ) {
		return;
	}

	for( i = 0; i < SWIM_PARTS; i++ ) {
		G_FreeEntity( ent->client->swimParts[i] );
		ent->client->swimParts[i] = NULL;
	}

	ent->r.contents = ent->client->swimSavedContents;
	trap_LinkEntity( ent );
}

void G_AttachBodyParts(gentity_t* ent) {
	int	i;
	gentity_t	*list;

	for( i = 0; i < level.numConnectedClients; i++, list++ ) {
		list = g_entities + level.sortedClients[i];
		// Gordon: ok lets test everything under the sun
	 	if( list->inuse && 
 			(list->client->sess.sessionTeam == TEAM_RED || list->client->sess.sessionTeam == TEAM_BLUE) && 
 			(list != ent) &&
 			list->r.linked &&
 			(list->health > 0) &&
 			!(list->client->ps.pm_flags & PMF_LIMBO) &&
			(list->client->ps.pm_type == PM_NORMAL)
 		) {
			if( G_IsSwimming( list ) ) {
				// the swim boxes include the head
				list->client->tempHead = NULL;
				G_AttachSwimBody( list );
			} else {
				list->client->tempHead = G_BuildHead( list );
			}
		} else {
			list->client->tempHead = NULL;
		}
	}
}

void G_DettachBodyParts() {
	int			i;
	gentity_t	*list;

	for( i = 0; i < level.numConnectedClients; i++, list++ ) {
		list = g_entities + level.sortedClients[i];
		if( list->client->tempHead ) {
			G_FreeEntity( list->client->tempHead );
		}
		G_DetachSwimBody( list );
	}
}

int G_SwitchBodyPartEntity(gentity_t* ent) {
	if( ent->s.eType == ET_TEMPHEAD ) {
		return ent->parent-g_entities;
	}
	return ent-g_entities;
}

#define POSITION_READJUST						\
	if( res != results->entityNum ) {				\
		VectorSubtract( end, start, dir );			\
		VectorNormalizeFast( dir );				\
									\
		VectorMA( results->endpos, -1, dir, results->endpos );	\
		results->entityNum = res;				\
	}

// Run a trace with players in historical positions.
void G_HistoricalTrace( gentity_t* ent, trace_t *results, const vec3_t start, const vec3_t mins, const vec3_t maxs, const vec3_t end, int passEntityNum, int contentmask ) {
	int res;
	vec3_t dir;

	if( !g_antilag.integer || !ent->client ) {
		G_AttachBodyParts( ent );

		trap_Trace( results, start, mins, maxs, end, passEntityNum, contentmask );

		res = G_SwitchBodyPartEntity( &g_entities[ results->entityNum ] );
		POSITION_READJUST

		G_DettachBodyParts();
		return;
	}

	G_AdjustClientPositions( ent, ent->client->pers.cmd.serverTime, qtrue );

	G_AttachBodyParts( ent ) ;

	trap_Trace( results, start, mins, maxs, end, passEntityNum, contentmask );

	res = G_SwitchBodyPartEntity( &g_entities[ results->entityNum ] );
	POSITION_READJUST

	G_DettachBodyParts();

	G_AdjustClientPositions( ent, 0, qfalse );
}

void G_HistoricalTraceBegin( gentity_t *ent ) {
	G_AdjustClientPositions( ent, ent->client->pers.cmd.serverTime, qtrue );
}

void G_HistoricalTraceEnd( gentity_t *ent ) {
	G_AdjustClientPositions( ent, 0, qfalse );
}

//bani - Run a trace without fixups (historical fixups will be done externally)
void G_Trace( gentity_t* ent, trace_t *results, const vec3_t start, const vec3_t mins, const vec3_t maxs, const vec3_t end, int passEntityNum, int contentmask ) {
	int res;
	vec3_t dir;

	G_AttachBodyParts( ent );

	trap_Trace( results, start, mins, maxs, end, passEntityNum, contentmask );

	res = G_SwitchBodyPartEntity( &g_entities[ results->entityNum ] );
	POSITION_READJUST

	G_DettachBodyParts();
}
