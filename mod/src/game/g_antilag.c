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
}

// Rewind the state G_BuildHead and the duck flag read.  Only PMF_DUCKED is touched in pm_flags: the
// other bits can change during the trace (knockback from the hit, etc.) and must survive the restore.
static void G_ApplyMarkerState( gentity_t* ent, const vec3_t viewangles, int pm_flags, int viewheight ) {
	VectorCopy( viewangles, ent->client->ps.viewangles );
	ent->client->ps.pm_flags = ( ent->client->ps.pm_flags & ~PMF_DUCKED ) | ( pm_flags & PMF_DUCKED );
	ent->client->ps.viewheight = viewheight;
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
			G_ApplyMarkerState( ent, angles, ent->client->clientMarkers[closer].pm_flags, ent->client->clientMarkers[closer].viewheight );
		}
	} else {
		VectorCopy( ent->client->clientMarkers[j].origin, ent->r.currentOrigin );
		VectorCopy( ent->client->clientMarkers[j].mins,	ent->r.mins );
		VectorCopy( ent->client->clientMarkers[j].maxs,	ent->r.maxs );
		G_ApplyMarkerState( ent, ent->client->clientMarkers[j].viewangles, ent->client->clientMarkers[j].pm_flags, ent->client->clientMarkers[j].viewheight );
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
		G_ApplyMarkerState( ent, ent->client->backupMarker.viewangles, ent->client->backupMarker.pm_flags, ent->client->backupMarker.viewheight );
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
	orientation_t	or;
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
		G_RailBox( target->r.currentOrigin, target->r.mins, target->r.maxs, tv( 1.f, 0.f, 0.f ), target->s.number );
		head = G_BuildHead( target );
		G_RailBox( head->r.currentOrigin, head->r.mins, head->r.maxs, tv( 1.f, 0.f, 0.f ), target->s.number | HITBOXBIT_HEAD );
		G_FreeEntity( head );

		if( rewound ) {
			// where the server has them now: green
			G_RailBox( target->client->backupMarker.origin, target->client->backupMarker.mins, target->client->backupMarker.maxs,
				tv( 0.f, 1.f, 0.f ), target->s.number );

			// G_BuildHead works from r.currentOrigin, so put the current position back for a moment
			VectorCopy( target->client->backupMarker.origin, target->r.currentOrigin );
			head = G_BuildHead( target );
			G_RailBox( head->r.currentOrigin, head->r.mins, head->r.maxs, tv( 0.f, 1.f, 0.f ), target->s.number | HITBOXBIT_HEAD );
			G_FreeEntity( head );
			VectorCopy( rewoundOrigin, target->r.currentOrigin );
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
			"print \"antilag hit: %s rewind %dms%s moved %d units%s tag_head %s duck %d swim %d\n\"",
			target->client->pers.netname,
			rewindMsec,
			capped ? " (capped)" : "",
			(int)VectorLength( delta ),
			rewound ? "" : " (not rewound)",
			trap_GetTag( target->s.number, "tag_head", &or ) ? "ok" : "MISSING",
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
		ent->client->clientMarkers[i].time = time;
	}
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
			list->client->tempHead = G_BuildHead( list );
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
