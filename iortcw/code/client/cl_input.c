/*
===========================================================================

Return to Castle Wolfenstein multiplayer GPL Source Code
Copyright (C) 1999-2010 id Software LLC, a ZeniMax Media company. 

This file is part of the Return to Castle Wolfenstein multiplayer GPL Source Code (RTCW MP Source Code).  

RTCW MP Source Code is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

RTCW MP Source Code is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with RTCW MP Source Code.  If not, see <http://www.gnu.org/licenses/>.

In addition, the RTCW MP Source Code is also subject to certain additional terms. You should have received a copy of these additional terms immediately following the terms and conditions of the GNU General Public License which accompanied the RTCW MP Source Code.  If not, please request a copy in writing from id Software at the address below.

If you have questions concerning this license or the applicable additional terms, you may contact in writing id Software LLC, c/o ZeniMax Media Inc., Suite 120, Rockville, Maryland 20850 USA.

===========================================================================
*/

// cl.input.c  -- builds an intended movement command to send to the server

#include "client.h"

unsigned frame_msec;
int old_com_frameTime;

/*
===============================================================================

KEY BUTTONS

Continuous button event tracking is complicated by the fact that two different
input sources (say, mouse button 1 and the control key) can both press the
same button, but the button should only be released when both of the
pressing key have been released.

When a key event issues a button command (+forward, +attack, etc), it appends
its key number as argv(1) so it can be matched up with the release.

argv(2) will be set to the time the event happened, which allows exact
control even at low framerates when the down and up events may both get qued
at the same time.

===============================================================================
*/

static kbutton_t kb[NUM_BUTTONS];

static cvar_t *cl_controllerAimAssist = NULL;
static cvar_t *cl_controllerAimAssistSlowdown = NULL;
static cvar_t *cl_controllerAimAssistRadius = NULL;
static cvar_t *cl_controllerAimAssistMaxAngle = NULL;
static cvar_t *cl_controllerAimAssistMinAngle = NULL;
static cvar_t *cl_controllerAimAssistPull = NULL;
static cvar_t *cl_controllerAimAssistDebug = NULL;
static cvar_t *cl_controllerLookRamp = NULL;
static cvar_t *cl_controllerZoomScale = NULL;
static cvar_t *cl_controllerAimAssistTrack = NULL;
static cvar_t *cl_touchLookSens = NULL;
static cvar_t *cl_touchLookPitch = NULL;
static cvar_t *cl_touchLookAccel = NULL;

static int cl_lastMouseMoveTime = 0;
static int cl_lastControllerLookTime = 0;
static int cl_lastPadTime = 0;          // any intentional controller/touch input (move stick too)
static float cl_touchDx, cl_touchDy;    // touch swipe since the last command, in CSS pixels

/* target lock: the current target is preferred until it clearly leaves its bubble */
static int cl_aaLockedTarget = -1;
static int cl_aaEntDebugTime;   // cl_controllerAimAssistDebug 2: per-enemy lines twice a second

static aimAssistVis_t cl_aimAssistVis;

const aimAssistVis_t *CL_AimAssistVis( void ) {
	return &cl_aimAssistVis;
}

qboolean isEntVisible( entityState_t *ent );

#ifdef USE_VOIP
kbutton_t	in_voiprecord;
#endif

void IN_MLookDown( void ) {
	kb[KB_MLOOK].active = qtrue;
}

void IN_MLookUp( void ) {
	kb[KB_MLOOK].active = qfalse;
	if ( !cl_freelook->integer ) {
		IN_CenterView();
	}
}

void IN_KeyDown( kbutton_t *b ) {
	int k;
	char    *c;

	c = Cmd_Argv( 1 );
	if ( c[0] ) {
		k = atoi( c );
	} else {
		k = -1;     // typed manually at the console for continuous down
	}

	if ( k == b->down[0] || k == b->down[1] ) {
		return;     // repeating key
	}

	if ( !b->down[0] ) {
		b->down[0] = k;
	} else if ( !b->down[1] ) {
		b->down[1] = k;
	} else {
		Com_Printf( "Three keys down for a button!\n" );
		return;
	}

	if ( b->active ) {
		return;     // still down
	}

	// save timestamp for partial frame summing
	c = Cmd_Argv( 2 );
	b->downtime = atoi( c );

	b->active = qtrue;
	b->wasPressed = qtrue;
}

void IN_KeyUp( kbutton_t *b ) {
	int k;
	char    *c;
	unsigned uptime;

	c = Cmd_Argv( 1 );
	if ( c[0] ) {
		k = atoi( c );
	} else {
		// typed manually at the console, assume for unsticking, so clear all
		b->down[0] = b->down[1] = 0;
		b->active = qfalse;
		return;
	}

	if ( b->down[0] == k ) {
		b->down[0] = 0;
	} else if ( b->down[1] == k ) {
		b->down[1] = 0;
	} else {
		return;     // key up without coresponding down (menu pass through)
	}
	if ( b->down[0] || b->down[1] ) {
		return;     // some other key is still holding it down
	}

	b->active = qfalse;

	// save timestamp for partial frame summing
	c = Cmd_Argv( 2 );
	uptime = atoi( c );
	if ( uptime ) {
		b->msec += uptime - b->downtime;
	} else {
		b->msec += frame_msec / 2;
	}

	b->active = qfalse;
}



/*
===============
CL_KeyState

Returns the fraction of the frame that the key was down
===============
*/
float CL_KeyState( kbutton_t *key ) {
	float val;
	int msec;

	msec = key->msec;
	key->msec = 0;

	if ( key->active ) {
		// still down
		if ( !key->downtime ) {
			msec = com_frameTime;
		} else {
			msec += com_frameTime - key->downtime;
		}
		key->downtime = com_frameTime;
	}

#if 0
	if ( msec ) {
		Com_Printf( "%i ", msec );
	}
#endif

	val = (float)msec / frame_msec;
	if ( val < 0 ) {
		val = 0;
	}
	if ( val > 1 ) {
		val = 1;
	}

	return val;
}



void IN_UpDown( void ) {IN_KeyDown( &kb[KB_UP] );}
void IN_UpUp( void ) {IN_KeyUp( &kb[KB_UP] );}
void IN_DownDown( void ) {IN_KeyDown( &kb[KB_DOWN] );}
void IN_DownUp( void ) {IN_KeyUp( &kb[KB_DOWN] );}
void IN_LeftDown( void ) {IN_KeyDown( &kb[KB_LEFT] );}
void IN_LeftUp( void ) {IN_KeyUp( &kb[KB_LEFT] );}
void IN_RightDown( void ) {IN_KeyDown( &kb[KB_RIGHT] );}
void IN_RightUp( void ) {IN_KeyUp( &kb[KB_RIGHT] );}
void IN_ForwardDown( void ) {IN_KeyDown( &kb[KB_FORWARD] );}
void IN_ForwardUp( void ) {IN_KeyUp( &kb[KB_FORWARD] );}
void IN_BackDown( void ) {IN_KeyDown( &kb[KB_BACK] );}
void IN_BackUp( void ) {IN_KeyUp( &kb[KB_BACK] );}
void IN_LookupDown( void ) {IN_KeyDown( &kb[KB_LOOKUP] );}
void IN_LookupUp( void ) {IN_KeyUp( &kb[KB_LOOKUP] );}
void IN_LookdownDown( void ) {IN_KeyDown( &kb[KB_LOOKDOWN] );}
void IN_LookdownUp( void ) {IN_KeyUp( &kb[KB_LOOKDOWN] );}
void IN_MoveleftDown( void ) {IN_KeyDown( &kb[KB_MOVELEFT] );}
void IN_MoveleftUp( void ) {IN_KeyUp( &kb[KB_MOVELEFT] );}
void IN_MoverightDown( void ) {IN_KeyDown( &kb[KB_MOVERIGHT] );}
void IN_MoverightUp( void ) {IN_KeyUp( &kb[KB_MOVERIGHT] );}

void IN_SpeedDown( void ) {IN_KeyDown( &kb[KB_SPEED] );}
void IN_SpeedUp( void ) {IN_KeyUp( &kb[KB_SPEED] );}
void IN_StrafeDown( void ) {IN_KeyDown( &kb[KB_STRAFE] );}
void IN_StrafeUp( void ) {IN_KeyUp( &kb[KB_STRAFE] );}

#ifdef USE_VOIP
void IN_VoipRecordDown(void)
{
	IN_KeyDown(&in_voiprecord);
	Cvar_Set("cl_voipSend", "1");
}

void IN_VoipRecordUp(void)
{
	IN_KeyUp(&in_voiprecord);
	Cvar_Set("cl_voipSend", "0");
}
#endif

void IN_Button0Down( void ) {IN_KeyDown( &kb[KB_BUTTONS0] );}
void IN_Button0Up( void ) {IN_KeyUp( &kb[KB_BUTTONS0] );}
void IN_Button1Down( void ) {IN_KeyDown( &kb[KB_BUTTONS1] );}
void IN_Button1Up( void ) {IN_KeyUp( &kb[KB_BUTTONS1] );}
void IN_UseItemDown( void ) {IN_KeyDown( &kb[KB_BUTTONS2] );}
void IN_UseItemUp( void ) {IN_KeyUp( &kb[KB_BUTTONS2] );}
void IN_Button3Down( void ) {IN_KeyDown( &kb[KB_BUTTONS3] );}
void IN_Button3Up( void ) {IN_KeyUp( &kb[KB_BUTTONS3] );}
void IN_Button4Down( void ) {IN_KeyDown( &kb[KB_BUTTONS4] );}
void IN_Button4Up( void ) {IN_KeyUp( &kb[KB_BUTTONS4] );}
// void IN_Button5Down(void) {IN_KeyDown(&kb[KB_BUTTONS5]);}
// void IN_Button5Up(void) {IN_KeyUp(&kb[KB_BUTTONS5]);}

// void IN_Button6Down(void) {IN_KeyDown(&kb[KB_BUTTONS6]);}
// void IN_Button6Up(void) {IN_KeyUp(&kb[KB_BUTTONS6]);}

// Rafael activate
void IN_ActivateDown( void ) {IN_KeyDown( &kb[KB_BUTTONS6] );}
void IN_ActivateUp( void ) {IN_KeyUp( &kb[KB_BUTTONS6] );}
// done.

// Rafael Kick
void IN_KickDown( void ) {IN_KeyDown( &kb[KB_KICK] );}
void IN_KickUp( void ) {IN_KeyUp( &kb[KB_KICK] );}
// done.

void IN_SprintDown( void ) {IN_KeyDown( &kb[KB_BUTTONS5] );}
void IN_SprintUp( void ) {IN_KeyUp( &kb[KB_BUTTONS5] );}


// wbuttons (wolf buttons)
void IN_Wbutton0Down( void )  { IN_KeyDown( &kb[KB_WBUTTONS0] );    }   //----(SA) secondary fire button
void IN_Wbutton0Up( void )    { IN_KeyUp( &kb[KB_WBUTTONS0] );  }
void IN_ZoomDown( void )      { IN_KeyDown( &kb[KB_WBUTTONS1] );    }   //----(SA)	zoom key
void IN_ZoomUp( void )        { IN_KeyUp( &kb[KB_WBUTTONS1] );  }
void IN_QuickGrenDown( void ) { IN_KeyDown( &kb[KB_WBUTTONS2] );    }   //----(SA)	"Quickgrenade"
void IN_QuickGrenUp( void )   { IN_KeyUp( &kb[KB_WBUTTONS2] );  }
void IN_ReloadDown( void )    { IN_KeyDown( &kb[KB_WBUTTONS3] );    }   //----(SA)	manual weapon re-load
void IN_ReloadUp( void )      { IN_KeyUp( &kb[KB_WBUTTONS3] );  }
void IN_LeanLeftDown( void )  { IN_KeyDown( &kb[KB_WBUTTONS4] );    }   //----(SA)	lean left
void IN_LeanLeftUp( void )    { IN_KeyUp( &kb[KB_WBUTTONS4] );  }
void IN_LeanRightDown( void ) { IN_KeyDown( &kb[KB_WBUTTONS5] );    }   //----(SA)	lean right
void IN_LeanRightUp( void )   { IN_KeyUp( &kb[KB_WBUTTONS5] );  }

// JPW NERVE
void IN_MP_DropWeaponDown( void ) {IN_KeyDown( &kb[KB_WBUTTONS6] );}
void IN_MP_DropWeaponUp( void ) {IN_KeyUp( &kb[KB_WBUTTONS6] );}
// jpw

// unused
void IN_Wbutton7Down( void )  { IN_KeyDown( &kb[KB_WBUTTONS7] );    }
void IN_Wbutton7Up( void )    { IN_KeyUp( &kb[KB_WBUTTONS7] );  }

void IN_CenterView( void ) {
	qboolean ok = qtrue;
	if ( cgvm ) {
		ok = VM_Call( cgvm, CG_CHECKCENTERVIEW );
	}
	if ( ok ) {
		cl.viewangles[PITCH] = -SHORT2ANGLE( cl.snap.ps.delta_angles[PITCH] );
	}
}

void IN_Notebook( void ) {
	//if ( cls.state == CA_ACTIVE && !clc.demoplaying ) {
	//VM_Call( uivm, UI_SET_ACTIVE_MENU, UIMENU_NOTEBOOK);	// startup notebook
	//}
}

void IN_Help( void ) {
	if ( clc.state == CA_ACTIVE && !clc.demoplaying ) {
		VM_Call( uivm, UI_SET_ACTIVE_MENU, UIMENU_HELP );        // startup help system
	}
}


//==========================================================================

cvar_t  *cl_yawspeed;
cvar_t  *cl_pitchspeed;

cvar_t  *cl_run;

cvar_t  *cl_anglespeedkey;

cvar_t  *cl_recoilPitch;

cvar_t  *cl_bypassMouseInput;       // NERVE - SMF


int CL_GetClientTeam( int clientNum ) {
	int offset;
	const char *info;

	if ( clientNum < 0 || clientNum >= MAX_CLIENTS ) {
		return TEAM_FREE;
	}

	offset = cl.gameState.stringOffsets[CS_PLAYERS + clientNum];
	if ( !offset ) {
		return TEAM_FREE;
	}

	info = cl.gameState.stringData + offset;
	return atoi( Info_ValueForKey( info, "t" ) );
}

static qboolean CL_IsAimAssistEnemyTeam( int playerTeam, int targetTeam ) {
	if ( playerTeam == TEAM_RED ) {
		return targetTeam == TEAM_BLUE;
	}
	if ( playerTeam == TEAM_BLUE ) {
		return targetTeam == TEAM_RED;
	}
	return qfalse;
}

/*
 * Where the player is really looking.  cl.viewangles is only the input side: the view is
 * cl.viewangles + the playerState's delta_angles, which the server changes whenever it turns the
 * player (every spawn faces the spawn point's direction, teleports).  Using cl.viewangles alone put
 * "the crosshair" anywhere up to 180 degrees away after a spawn.
 */
void CL_ViewAngles( vec3_t out ) {
	int i;

	for ( i = 0; i < 3; i++ ) {
		out[i] = cl.viewangles[i] + SHORT2ANGLE( cl.snap.ps.delta_angles[i] );
	}
}

/* a player entity of the given client in a snapshot, or NULL */
static entityState_t *CL_AimAssistFindPlayer( const clSnapshot_t *snap, int clientNum ) {
	int i;

	for ( i = 0; i < snap->numEntities; i++ ) {
		entityState_t *e = &cl.parseEntities[( snap->parseEntitiesNum + i ) & ( MAX_PARSE_ENTITIES - 1 )];
		if ( e->eType == ET_PLAYER && e->clientNum == clientNum ) {
			return e;
		}
	}
	return NULL;
}

/* the snapshot before cl.snap (a dropped packet or two is skipped), or NULL */
static clSnapshot_t *CL_AimAssistPrevSnap( void ) {
	int back;

	for ( back = 1; back <= 3; back++ ) {
		clSnapshot_t *s = &cl.snapshots[( cl.snap.messageNum - back ) & PACKET_MASK];
		if ( s->valid && s->messageNum == cl.snap.messageNum - back && s->serverTime < cl.snap.serverTime
			&& cl.parseEntitiesNum - s->parseEntitiesNum <= MAX_PARSE_ENTITIES ) {
			return s;
		}
	}
	return NULL;
}

/* body of a player as a vertical segment (feet to head), around its origin */
static void CL_AimAssistBody( const entityState_t *e, float *bottom, float *top ) {
	if ( e->animMovetype ) {        // crouched
		*bottom = -16.0f;
		*top = 16.0f;
	} else {
		*bottom = -18.0f;
		*top = 40.0f;
	}
}

/* direction angles from one point to another */
static void CL_AimAssistAngles( const vec3_t from, const vec3_t to, vec3_t angles ) {
	vec3_t d;

	VectorSubtract( to, from, d );
	vectoangles( d, angles );
}

/*
 * Controller and touch aim assist.  Never for the mouse: any mouse movement turns it off until the
 * player next aims with a stick or by touch.
 *
 * Each enemy has a "bubble" around its body (a vertical segment, feet to head): cl_controllerAimAssistRadius
 * world units, seen as an angle that shrinks with distance (at least cl_controllerAimAssistMinAngle, at most
 * cl_controllerAimAssistMaxAngle).  The target is the enemy whose bubble the crosshair is deepest in, if it
 * can be seen (no wall between); the current target is preferred until it clearly leaves.  Inside the inner
 * half of the bubble the assist is at full strength, fading to nothing at its edge:
 *
 *   tracking  - the view turns along with the target by cl_controllerAimAssistTrack of how fast the direction
 *               to it changes (its movement and the player's own strafing alike), measured between the two
 *               latest snapshots, so it works with the look stick still.
 *   slowdown  - look input (stick or swipe) is multiplied by cl_controllerAimAssistSlowdown, so it is easier
 *               to stay on the target and not overshoot it.
 *   pull      - while the player is aiming or moving, the crosshair eases toward the nearest point of the
 *               target's body (cl_controllerAimAssistPull per second of the gap); never with the thumbs still.
 *
 * Nothing happens to a still target with the thumbs still.  Flicking
 * (a fast turn) fades both, and steering against the target's motion lets go of its tracking.
 *
 * lookYaw/lookPitch: this frame's look input in degrees; lookSpeed: 0..1, 1 = a full stick (or as fast by
 * swipe).  Returns the factor for the look input; track gets the degrees to add this frame (PITCH, YAW).
 */
static float CL_AimAssist( float lookYaw, float lookPitch, float lookSpeed, vec2_t track ) {
	aimAssistVis_t *v = &cl_aimAssistVis;
	clSnapshot_t *prev;
	vec3_t eye, viewDir, prevEye;
	float frac = 1.0f;
	float radius, maxAngle, minAngle, slowdown, strength;
	float bestRatio = 1.0f;
	float frameSec = cls.frametime * 0.001f;
	entityState_t *best = NULL, *bestOld = NULL;
	int playerTeam, padTime, i;
	qboolean stickOff;
	qboolean entDebugged = qfalse;

	track[0] = track[1] = 0.0f;
	memset( v, 0, sizeof( *v ) );
	v->target = -1;

	if ( !cl_controllerAimAssist->integer ) {
		v->off = "off (cl_controllerAimAssist 0)";
		return 1.0f;
	}
	if ( clc.state != CA_ACTIVE || !cl.snap.valid ) {
		v->off = "not in a game";
		return 1.0f;
	}
	if ( cl.snap.ps.pm_type != PM_NORMAL || ( cl.snap.ps.pm_flags & PMF_FOLLOW ) || cl.snap.ps.stats[STAT_HEALTH] <= 0 ) {
		v->off = "not playing";
		return 1.0f;
	}
	if ( !cl_lastControllerLookTime || cl_lastMouseMoveTime >= cl_lastControllerLookTime ) {
		v->off = "mouse (aim with a stick or touch to turn on)";
		return 1.0f;
	}
	// a held stick sends no new events, so a stick off centre right now counts as in use
	stickOff = cl.joystickAxis[j_side_axis->integer] || cl.joystickAxis[j_forward_axis->integer]
		|| cl.joystickAxis[j_yaw_axis->integer] || cl.joystickAxis[j_pitch_axis->integer];
	padTime = max( cl_lastPadTime, cl_lastControllerLookTime );
	if ( !stickOff && cls.realtime - padTime > 5000 ) {
		v->off = "controller idle";
		return 1.0f;
	}
	playerTeam = cl.snap.ps.persistant[PERS_TEAM];
	if ( playerTeam != TEAM_RED && playerTeam != TEAM_BLUE ) {
		v->off = "no team";
		return 1.0f;
	}

	radius   = Com_Clamp( 4.0f, 200.0f, cl_controllerAimAssistRadius->value );
	maxAngle = Com_Clamp( 1.0f, 30.0f, cl_controllerAimAssistMaxAngle->value );
	minAngle = Com_Clamp( 0.5f, maxAngle, cl_controllerAimAssistMinAngle->value );
	slowdown = Com_Clamp( 0.1f, 1.0f, cl_controllerAimAssistSlowdown->value );
	strength = Com_Clamp( 0.0f, 1.0f, cl_controllerAimAssistTrack->value );
	v->armed = qtrue;

	// Where the player sees from and looks: the eye cgame rendered (cl.snap is up to a snapshot behind the
	// predicted player) and the current view angles.  Other players are drawn between the two latest
	// snapshots at cl.serverTime, so they are placed there too.
	VectorCopy( cl.cgameClientLerpOrigin, eye );
	if ( VectorLength( eye ) == 0.0f ) {
		VectorCopy( cl.snap.ps.origin, eye );
		eye[2] += cl.snap.ps.viewheight;
	}
	{
		vec3_t view;
		CL_ViewAngles( view );
		AngleVectors( view, viewDir, NULL, NULL );
	}
	prev = CL_AimAssistPrevSnap();
	if ( prev ) {
		frac = Com_Clamp( 0.0f, 1.5f, (float)( cl.serverTime - prev->serverTime ) / (float)( cl.snap.serverTime - prev->serverTime ) );
	}

	for ( i = 0; i < cl.snap.numEntities; i++ ) {
		entityState_t *e = &cl.parseEntities[( cl.snap.parseEntitiesNum + i ) & ( MAX_PARSE_ENTITIES - 1 )];
		entityState_t *old;
		vec3_t pos, pt, dir, start, end;
		float bottom, top, horiz, rayHoriz, z, dist, off, bubble, ratio;
		trace_t tr;

		if ( e->eType != ET_PLAYER || e->clientNum == cl.snap.ps.clientNum || e->number == cl.snap.ps.clientNum ) {
			continue;
		}
		if ( e->eFlags & ( EF_DEAD | EF_NODRAW ) ) {
			continue;
		}
		if ( !CL_IsAimAssistEnemyTeam( playerTeam, CL_GetClientTeam( e->clientNum ) ) ) {
			continue;
		}
		v->nEnemy++;

		old = prev ? CL_AimAssistFindPlayer( prev, e->clientNum ) : NULL;
		VectorCopy( e->pos.trBase, pos );
		if ( old && Distance( old->pos.trBase, e->pos.trBase ) < 120.0f ) {
			int k;
			for ( k = 0; k < 3; k++ ) {
				pos[k] = old->pos.trBase[k] + ( e->pos.trBase[k] - old->pos.trBase[k] ) * frac;
			}
		}

		// the point of the body closest to where the crosshair points (the same height the view ray has there)
		CL_AimAssistBody( e, &bottom, &top );
		VectorCopy( pos, pt );
		horiz = sqrtf( ( pos[0] - eye[0] ) * ( pos[0] - eye[0] ) + ( pos[1] - eye[1] ) * ( pos[1] - eye[1] ) );
		rayHoriz = sqrtf( viewDir[0] * viewDir[0] + viewDir[1] * viewDir[1] );
		z = rayHoriz > 0.01f ? eye[2] + viewDir[2] / rayHoriz * horiz : pos[2];
		pt[2] = Com_Clamp( pos[2] + bottom, pos[2] + top, z );

		VectorSubtract( pt, eye, dir );
		dist = VectorNormalize( dir );
		if ( dist < 1.0f ) {
			continue;
		}
		off = RAD2DEG( acos( Com_Clamp( -1.0f, 1.0f, DotProduct( dir, viewDir ) ) ) );
		bubble = Com_Clamp( minAngle, maxAngle, RAD2DEG( atan2( radius, dist ) ) );
		ratio = off / bubble;
		if ( cl_controllerAimAssistDebug->integer >= 2 && cls.realtime - cl_aaEntDebugTime >= 500 ) {
			trace_t dt;
			CM_BoxTrace( &dt, eye, pt, vec3_origin, vec3_origin, 0, CONTENTS_SOLID, qfalse );
			Com_Printf( "aim assist: enemy %d eye (%.0f %.0f %.0f) point (%.0f %.0f %.0f) dist %.0f off %.2f bubble %.2f wall %.2f\n",
				e->clientNum, eye[0], eye[1], eye[2], pt[0], pt[1], pt[2], dist, off, bubble, dt.fraction );
			entDebugged = qtrue;
		}
		// no wall between: the eye sees the closest point or the head
		VectorCopy( eye, start );
		VectorCopy( pt, end );
		CM_BoxTrace( &tr, start, end, vec3_origin, vec3_origin, 0, CONTENTS_SOLID, qfalse );
		if ( tr.fraction < 1.0f ) {
			VectorCopy( pos, end );
			end[2] += top - 4.0f;
			CM_BoxTrace( &tr, start, end, vec3_origin, vec3_origin, 0, CONTENTS_SOLID, qfalse );
			if ( tr.fraction < 1.0f ) {
				continue;
			}
		}
		v->nVisible++;
		if ( v->nVisible == 1 || off / bubble < v->nearOff / v->nearBubble ) {
			v->nearOff = off;
			v->nearBubble = bubble;
		}

		if ( e->clientNum == cl_aaLockedTarget ) {
			ratio *= 0.75f;
		}
		if ( ratio >= bestRatio ) {
			continue;
		}
		bestRatio = ratio;
		best = e;
		bestOld = old;
		VectorCopy( pt, v->targetPoint );
		v->offAngle = off;
		v->bubble = bubble;
		v->dist = dist;
	}

	if ( entDebugged ) {
		cl_aaEntDebugTime = cls.realtime;
	}
	if ( !best ) {
		cl_aaLockedTarget = -1;
		return 1.0f;
	}
	cl_aaLockedTarget = best->clientNum;
	v->target = best->clientNum;

	{
		// full strength inside the inner half of the bubble, nothing at its edge; a fast turn fades it all
		float ratio = v->offAngle / v->bubble;
		float b = Com_Clamp( 0.0f, 1.0f, ( 1.0f - ratio ) / 0.5f );
		float flick = 1.0f - Com_Clamp( 0.0f, 1.0f, ( lookSpeed - 0.6f ) * 2.5f );
		float scale;

		b = b * b * ( 3.0f - 2.0f * b ) * flick;
		v->blend = b;
		scale = 1.0f - b * ( 1.0f - slowdown );
		v->scale = scale;

		// tracking: how fast the direction to the target's middle turned between the two latest snapshots
		if ( strength > 0.0f && prev && bestOld && Distance( bestOld->pos.trBase, best->pos.trBase ) < 120.0f ) {
			float dt = ( cl.snap.serverTime - prev->serverTime ) * 0.001f;
			vec3_t p0, p1, a0, a1;

			if ( dt > 0.0f && dt <= 0.25f ) {
				float yawRate, pitchRate;
				float yawRel = 1.0f, pitchRel = 1.0f;

				VectorCopy( prev->ps.origin, prevEye );
				prevEye[2] += prev->ps.viewheight;
				VectorCopy( bestOld->pos.trBase, p0 );
				VectorCopy( best->pos.trBase, p1 );
				p0[2] += 8.0f;
				p1[2] += 8.0f;
				{
					vec3_t curEye;
					VectorCopy( cl.snap.ps.origin, curEye );
					curEye[2] += cl.snap.ps.viewheight;
					CL_AimAssistAngles( prevEye, p0, a0 );
					CL_AimAssistAngles( curEye, p1, a1 );
				}
				yawRate   = Com_Clamp( -360.0f, 360.0f, AngleSubtract( a1[YAW],   a0[YAW] )   / dt );
				pitchRate = Com_Clamp( -180.0f, 180.0f, AngleSubtract( a1[PITCH], a0[PITCH] ) / dt );
				v->rateYaw = yawRate;
				v->ratePitch = pitchRate;

				// steering against the target's motion lets go of it
				if ( yawRate * lookYaw < 0.0f && Q_fabs( lookYaw ) > Q_fabs( yawRate ) * frameSec * 0.25f ) {
					yawRel = 0.2f;
				}
				if ( pitchRate * lookPitch < 0.0f && Q_fabs( lookPitch ) > Q_fabs( pitchRate ) * frameSec * 0.25f ) {
					pitchRel = 0.2f;
				}
				// vertical following at 60%: jumps and crouches would otherwise bob the view
				track[YAW]   = yawRate   * frameSec * strength * b * yawRel;
				track[PITCH] = pitchRate * frameSec * strength * b * pitchRel * 0.6f;
				v->trackYaw = frameSec > 0.0f ? track[YAW] / frameSec : 0.0f;
				v->trackPitch = frameSec > 0.0f ? track[PITCH] / frameSec : 0.0f;
			}
		}

		// Pull: while the player is aiming or moving (never with the thumbs still, so it cannot aim by itself),
		// ease the crosshair toward the nearest point of the target's body: cl_controllerAimAssistPull degrees per
		// second for every degree it is off (so it slows as it arrives and never overshoots), at most 30 deg/s.
		// Steering away from the target lets go of it.
		{
			float pull = Com_Clamp( 0.0f, 20.0f, cl_controllerAimAssistPull->value );
			qboolean input = lookYaw != 0.0f || lookPitch != 0.0f
				|| cl.joystickAxis[j_side_axis->integer] || cl.joystickAxis[j_forward_axis->integer];

			if ( pull > 0.0f && input && frameSec > 0.0f ) {
				vec3_t view, toAngles;
				float dYaw, dPitch, len, step;

				CL_ViewAngles( view );
				CL_AimAssistAngles( eye, v->targetPoint, toAngles );
				dYaw = AngleSubtract( toAngles[YAW], view[YAW] );
				dPitch = AngleSubtract( toAngles[PITCH], view[PITCH] );
				len = sqrtf( dYaw * dYaw + dPitch * dPitch );
				if ( len > 0.05f && dYaw * lookYaw >= 0.0f && dPitch * lookPitch >= 0.0f ) {
					step = Com_Clamp( 0.0f, 30.0f, len * pull ) * frameSec * b;
					if ( step > len ) {
						step = len;
					}
					track[YAW]   += dYaw / len * step;
					track[PITCH] += dPitch / len * step;
					v->pull = step / frameSec;
				}
			}
		}
		return scale;
	}
}

/* cl_controllerAimAssistDebug: one console line every quarter second */
static void CL_AimAssistDebugPrint( void ) {
	static int last;
	const aimAssistVis_t *v = &cl_aimAssistVis;

	if ( cl_controllerAimAssistDebug->integer < 2 || cls.realtime - last < 250 ) {
		return;
	}
	last = cls.realtime;
	if ( v->off ) {
		Com_Printf( "aim assist: OFF %s\n", v->off );
	} else if ( v->target < 0 ) {
		Com_Printf( "aim assist: no target (enemies %d, visible %d, nearest visible %.1f deg off, bubble %.1f)\n",
			v->nEnemy, v->nVisible, v->nearOff, v->nearBubble );
	} else {
		Com_Printf( "aim assist: target %d dist %.0f off %.2f/%.2f deg blend %.2f slow %.2f rate (y %.1f p %.1f) track (y %.1f p %.1f) pull %.1f deg/s\n",
			v->target, v->dist, v->offAngle, v->bubble, v->blend, v->scale, v->rateYaw, v->ratePitch, v->trackYaw, v->trackPitch, v->pull );
	}
}

/*
================
CL_AdjustAngles

Moves the local angle positions
================
*/
void CL_AdjustAngles( void ) {
	float speed;

	// Same reasoning as the gate in CL_JoystickMove: a digital look
	// button (KB_LEFT/RIGHT/LOOKUP/LOOKDOWN, bound to the right stick's
	// PAD0_RIGHTSTICK_* keys) can already be "active" from before a menu
	// opened, or the stick can cross the digital threshold while the
	// player is using it as the menu cursor - either way this function
	// reads kb[...].active unconditionally every frame with no gate of
	// its own, so it keeps spinning the camera under the menu even
	// though CL_ParseBinding already blocks *starting* a new +left/
	// +right/+lookup/+lookdown while KEYCATCH_UI is set.
	if ( Key_GetCatcher( ) & KEYCATCH_UI ) {
		return;
	}

	if ( kb[KB_SPEED].active ) {
		speed = 0.001 * cls.frametime * cl_anglespeedkey->value;
	} else {
		speed = 0.001 * cls.frametime;
	}

	if ( !kb[KB_STRAFE].active ) {
		cl.viewangles[YAW] -= speed * cl_yawspeed->value * CL_KeyState( &kb[KB_RIGHT] );
		cl.viewangles[YAW] += speed * cl_yawspeed->value * CL_KeyState( &kb[KB_LEFT] );
	}

	cl.viewangles[PITCH] -= speed * cl_pitchspeed->value * CL_KeyState( &kb[KB_LOOKUP] );
	cl.viewangles[PITCH] += speed * cl_pitchspeed->value * CL_KeyState( &kb[KB_LOOKDOWN] );
}

/*
================
CL_KeyMove

Sets the usercmd_t based on key states
================
*/
void CL_KeyMove( usercmd_t *cmd ) {
	int movespeed;
	int forward, side, up;
	// Rafael Kick
	int kick;
	// done

	// Same reasoning as CL_JoystickMove/CL_AdjustAngles: kb[...].active
	// (bound to PAD0_LEFTSTICK_*/PAD0_A etc.) is read unconditionally
	// every frame with no gate of its own, so movement can keep applying
	// under an open menu the same way camera turn did.
	if ( Key_GetCatcher( ) & KEYCATCH_UI ) {
		cmd->forwardmove = 0;
		cmd->rightmove = 0;
		cmd->upmove = 0;
		return;
	}

	//
	// adjust for speed key / running
	// the walking flag is to keep animations consistant
	// even during acceleration and develeration
	//
	if ( kb[KB_SPEED].active ^ cl_run->integer ) {
		movespeed = 127;
		cmd->buttons &= ~BUTTON_WALKING;
	} else {
		cmd->buttons |= BUTTON_WALKING;
		movespeed = 64;
	}

	forward = 0;
	side = 0;
	up = 0;
	if ( kb[KB_STRAFE].active ) {
		side += movespeed * CL_KeyState( &kb[KB_RIGHT] );
		side -= movespeed * CL_KeyState( &kb[KB_LEFT] );
	}

	side += movespeed * CL_KeyState( &kb[KB_MOVERIGHT] );
	side -= movespeed * CL_KeyState( &kb[KB_MOVELEFT] );

//----(SA)	added
	if ( cmd->buttons & BUTTON_ACTIVATE ) {
		if ( side > 0 ) {
			cmd->wbuttons |= WBUTTON_LEANRIGHT;
		} else if ( side < 0 ) {
			cmd->wbuttons |= WBUTTON_LEANLEFT;
		}

		side = 0;   // disallow the strafe when holding 'activate'
	}
//----(SA)	end

	up += movespeed * CL_KeyState( &kb[KB_UP] );
	up -= movespeed * CL_KeyState( &kb[KB_DOWN] );

	forward += movespeed * CL_KeyState( &kb[KB_FORWARD] );
	forward -= movespeed * CL_KeyState( &kb[KB_BACK] );

	// Rafael Kick
	kick = CL_KeyState( &kb[KB_KICK] );
	// done

	if ( !( cl.snap.ps.persistant[PERS_HWEAPON_USE] ) ) {
		cmd->forwardmove = ClampChar( forward );
		cmd->rightmove = ClampChar( side );
		cmd->upmove = ClampChar( up );

		// Rafael - Kick
		cmd->wolfkick = ClampChar( kick );
		// done

	}
}

/*
=================
CL_MouseEvent
=================
*/
void CL_MouseEvent( int dx, int dy, int time ) {
	if ( dx || dy ) {
		cl_lastMouseMoveTime = max( time, cls.realtime );
	}

	if ( Key_GetCatcher( ) & KEYCATCH_UI ) {
		// NERVE - SMF - if we just want to pass it along to game
		if ( cl_bypassMouseInput->integer == 1 ) {
			cl.mouseDx[cl.mouseIndex] += dx;
			cl.mouseDy[cl.mouseIndex] += dy;
		} else {
			VM_Call( uivm, UI_MOUSE_EVENT, dx, dy );
		}

	} else if (Key_GetCatcher( ) & KEYCATCH_CGAME) {
		VM_Call( cgvm, CG_MOUSE_EVENT, dx, dy );
	} else {
		cl.mouseDx[cl.mouseIndex] += dx;
		cl.mouseDy[cl.mouseIndex] += dy;
	}
}

/*
=================
CL_JoystickEvent

Joystick values stay set until changed
=================
*/
void CL_JoystickEvent( int axis, int value, int time ) {
	if ( axis < 0 || axis >= MAX_JOYSTICK_AXIS ) {
		Com_Error( ERR_DROP, "CL_JoystickEvent: bad axis %i", axis );
	}

	if ( abs( value ) > 512 || ( axis != j_yaw_axis->integer && axis != j_pitch_axis->integer && value ) ) {
		cl_lastPadTime = max( time, cls.realtime );
	}

	/*
	 * Refresh the controller-look timestamp for any intentional stick input.
	 * 512 is well above deadzone-filtered noise (~0) but low enough to catch
	 * slow precision movements — the input curve squares raw values so a 35%
	 * raw push only reaches ~4000, meaning the old 4096 threshold required
	 * aggressive flicks before aim assist would activate.
	 */
	if ( abs( value ) > 512 && ( axis == j_yaw_axis->integer || axis == j_pitch_axis->integer ) ) {
		cl_lastControllerLookTime = max( time, cls.realtime );
	}

	cl.joystickAxis[axis] = value;
}

/*
=================
CL_TouchLook

Touch-screen look (the browser client's swipe area): a drag moves the view by how far the finger
moved, like a trackpad, rather than setting a turn speed like a stick.  Counts as controller input,
so the controller aim assist applies to it.  Pixels add up here and are used in CL_JoystickMove.
=================
*/
void CL_TouchLook( float dx, float dy ) {
	// Acceleration: a slow, careful swipe counts each pixel once (cl_touchLookSens degrees); a fast flick
	// counts up to (1 + cl_touchLookAccel) times, from 600 px/s to full at 1800 px/s, so a quick flick can turn
	// the player around (the swipe side is only a few hundred pixels wide).  The speed comes from the touch
	// events themselves (one call per touchmove), not the frame rate, smoothed over a couple of events.
	static int lastTime;
	static float speed;     // CSS pixels per second
	int now = Sys_Milliseconds();
	float px = sqrtf( dx * dx + dy * dy );
	float gain, t;
	int dt;

	if ( dx == 0.0f && dy == 0.0f ) {
		return;
	}
	dt = now - lastTime;
	lastTime = now;
	if ( dt > 100 ) {
		speed = 0.0f;                   // a new swipe
	}
	speed = speed * 0.4f + ( px / ( Com_Clamp( 8.0f, 50.0f, (float)dt ) * 0.001f ) ) * 0.6f;
	t = Com_Clamp( 0.0f, 1.0f, ( speed - 600.0f ) / 1200.0f );
	gain = 1.0f + Com_Clamp( 0.0f, 4.0f, cl_touchLookAccel->value ) * t * t * ( 3.0f - 2.0f * t );

	cl_touchDx += dx * gain;
	cl_touchDy += dy * gain;
	cl_lastControllerLookTime = cls.realtime;
	cl_lastPadTime = cls.realtime;
}

/*
=================
CL_JoystickMove
=================
*/
void CL_JoystickMove( usercmd_t *cmd ) {
	float anglespeed;

	// A menu owns input focus - let CL_MouseMove's Key_GetCatcher() check
	// keep the mouse path inert (deltas go to UI_MOUSE_EVENT instead of
	// cl.mouseDx/Dy), but cl.joystickAxis has no such divergence: it's
	// always populated by SE_JOYSTICK_AXIS events and always consumed
	// here into cl.viewangles regardless of menu state. Without this,
	// the same right-stick tilt driving the gamepad's virtual menu
	// cursor (see IN_GamepadMove in sdl_input.c) also spins the 3D
	// camera underneath the menu at the same time.
	if ( ( Key_GetCatcher( ) & KEYCATCH_UI ) && !CL_VsayMenuOpen( ) ) {
		cl_touchDx = cl_touchDy = 0.0f;
		memset( &cl_aimAssistVis, 0, sizeof( cl_aimAssistVis ) );
		cl_aimAssistVis.off = "menu open";
		cl_aimAssistVis.target = -1;
		return;
	}

	float aimAssistScale = 1.0f;
	qboolean walking;
	vec2_t aimAssistTrack = { 0.0f, 0.0f };
	float zoomScale = 1.0f;
	float touchYaw, touchPitch;
	float lookYawDelta;
	float lookPitchDelta;
	float rawYaw;
	float rawPitch;
	float stickMagnitude;

	float yaw     = j_yaw->value     * cl.joystickAxis[j_yaw_axis->integer];
	float right   = j_side->value    * cl.joystickAxis[j_side_axis->integer];
	float forward = j_forward->value * cl.joystickAxis[j_forward_axis->integer];
	float pitch   = j_pitch->value   * cl.joystickAxis[j_pitch_axis->integer];
	float up      = j_up->value      * cl.joystickAxis[j_up_axis->integer];

	walking = !( kb[KB_SPEED].active ^ cl_run->integer );
	if ( walking ) {
		cmd->buttons |= BUTTON_WALKING;

		// RTCW clears BUTTON_WALKING if analog movement exceeds 64,
		// so clamp stick movement into the walking range while active.
		right = Com_Clamp( -64.0f, 64.0f, right );
		forward = Com_Clamp( -64.0f, 64.0f, forward );
		up = Com_Clamp( -64.0f, 64.0f, up );
	}

	if ( kb[KB_SPEED].active ) {
		anglespeed = 0.001 * cls.frametime * cl_anglespeedkey->value;
	} else {
		anglespeed = 0.001 * cls.frametime;
	}

	// Modern-shooter look feel, applied to the base turn rate:
	//  - zoom scaling: while scoped/zoomed the view turns by the same fraction of
	//    the picture per stick position instead of the same degrees, so zoomed aim
	//    isn't twitchy (cgame publishes the fov it renders as cg_actualFov)
	//  - ramp: holding the stick near full deflection speeds the turn up a bit
	//    over a few tenths of a second, for a quick flick-around without raising
	//    the speed used for fine aim
	{
		static float lookHold;
		float baseFov = Cvar_VariableValue( "cg_fov" );
		float curFov = Cvar_VariableValue( "cg_actualFov" );
		float dt = cls.frametime * 0.001f;
		float mag = sqrtf( (float)cl.joystickAxis[j_yaw_axis->integer] * cl.joystickAxis[j_yaw_axis->integer] +
						   (float)cl.joystickAxis[j_pitch_axis->integer] * cl.joystickAxis[j_pitch_axis->integer] ) / 32767.0f;
		float ramp = Com_Clamp( 0.0f, 2.0f, cl_controllerLookRamp->value );
		float t;

		if ( baseFov < 90.0f ) {
			baseFov = 90.0f;
		} else if ( baseFov > 160.0f ) {
			baseFov = 160.0f;
		}
		if ( cl_controllerZoomScale->integer && curFov > 1.0f && curFov < baseFov - 0.5f ) {
			zoomScale = Com_Clamp( 0.05f, 1.0f, tan( DEG2RAD( curFov * 0.5f ) ) / tan( DEG2RAD( baseFov * 0.5f ) ) );
			anglespeed *= zoomScale;
		}

		if ( mag > 0.9f ) {
			lookHold += dt;
		} else {
			lookHold -= dt * 4.0f;        // lets go faster than it builds
		}
		lookHold = Com_Clamp( 0.0f, 0.4f, lookHold );
		t = lookHold / 0.4f;
		anglespeed *= 1.0f + ramp * t * t * ( 3.0f - 2.0f * t );
	}

	// Normalised look-stick magnitude (0–1) used to scale aim assist
	rawYaw        = (float)abs( cl.joystickAxis[j_yaw_axis->integer]   ) / 32767.0f;
	rawPitch      = (float)abs( cl.joystickAxis[j_pitch_axis->integer] ) / 32767.0f;
	stickMagnitude = rawYaw > rawPitch ? rawYaw : rawPitch;

	// touch swipe: degrees per pixel (already scaled by CL_TouchLook's acceleration), slower up/down, scaled
	// with zoom like the stick (drag right turns right)
	touchYaw   = -cl_touchDx * cl_touchLookSens->value * zoomScale;
	touchPitch =  cl_touchDy * cl_touchLookSens->value * cl_touchLookPitch->value * zoomScale;
	cl_touchDx = cl_touchDy = 0.0f;
	if ( ( touchYaw != 0.0f || touchPitch != 0.0f ) && cls.frametime > 0 ) {
		// as fast as a full stick turn (about 375 deg/s) counts as a full stick for the assist's flick fade
		float rate = sqrtf( touchYaw * touchYaw + touchPitch * touchPitch ) / ( cls.frametime * 0.001f ) / 375.0f;
		if ( rate > stickMagnitude ) {
			stickMagnitude = Com_Clamp( 0.0f, 1.0f, rate );
		}
	}

	lookYawDelta   = anglespeed * ( kb[KB_STRAFE].active ? right : yaw ) + touchYaw;
	lookPitchDelta = anglespeed * ( kb[KB_MLOOK].active ? forward : pitch ) + touchPitch;

	// aim assist (controller and touch only; see CL_AimAssist): slows the look input over a target and turns
	// the view along with it, look input or not
	aimAssistScale = CL_AimAssist( lookYawDelta, lookPitchDelta, stickMagnitude, aimAssistTrack );
	CL_AimAssistDebugPrint();

	if ( !kb[KB_STRAFE].active ) {
		cl.viewangles[YAW] += ( anglespeed * yaw + touchYaw ) * aimAssistScale + aimAssistTrack[YAW];
		cmd->rightmove = ClampChar( cmd->rightmove + (int)right );
	} else {
		cl.viewangles[YAW] += ( anglespeed * right + touchYaw ) * aimAssistScale + aimAssistTrack[YAW];
		cmd->rightmove = ClampChar( cmd->rightmove + (int)yaw );
	}
	if ( kb[KB_MLOOK].active ) {
		cl.viewangles[PITCH] += ( anglespeed * forward + touchPitch ) * aimAssistScale + aimAssistTrack[PITCH];
		cmd->forwardmove = ClampChar( cmd->forwardmove + (int)pitch );
	} else {
		cl.viewangles[PITCH] += ( anglespeed * pitch + touchPitch ) * aimAssistScale + aimAssistTrack[PITCH];
		cmd->forwardmove = ClampChar( cmd->forwardmove + (int)forward );
	}

	cmd->upmove = ClampChar( cmd->upmove + (int)up );
}

/*
=================
CL_DroneJoystickMove

Drone-sim spectator flight control scheme: left stick = yaw + throttle,
right stick = roll + pitch. All translation comes from throttle +
attitude via PM_DroneMove's camera-relative thrust (see bg_pmove.c),
so forwardmove/rightmove are left at zero here.
=================
*/
/*
=================
CL_DroneThrottleValue

Reads the button-sourced throttle and rescales it from the button's
observed [min,max] analog range (set by /dronecal, see
j_drone_throttle_btn_min/max's comment in cl_main.c) back out to a
full 0..32767 span. Some browsers' standard-gamepad-mapping trigger
synthesis only exercises part of a raw HID axis's 0..1 output range,
which without this rescale shows up as "dead" travel at one end of the
physical control.
=================
*/
static int CL_DroneThrottleValue( void ) {
	float raw, lo, hi, frac;

	if ( !j_drone_throttle_isbutton->integer ) {
		// plain axis: -32768..32767 -> 0..32767 (throttle is unipolar)
		return ( IN_GetRawGamepadAxis( j_drone_throttle_axis->integer ) + 32768 ) / 2;
	}

	raw = IN_GetGamepadAnalogButton( j_drone_throttle_axis->integer ) / 32767.0f;
	lo = j_drone_throttle_btn_min->value;
	hi = j_drone_throttle_btn_max->value;
	if ( hi - lo < 0.01f ) {
		return (int)( raw * 32767.0f );
	}
	frac = ( raw - lo ) / ( hi - lo );
	if ( frac < 0.0f ) frac = 0.0f;
	if ( frac > 1.0f ) frac = 1.0f;
	return (int)( frac * 32767.0f );
}

// FPV-style stick shaping: small deadzone, then blend of linear and cubic
// (expo) so the center is fine-grained and full deflection still reaches
// the full rate. Input/output in -1..1.
static float CL_DroneShape( float x ) {
	float dz = j_drone_deadzone->value;
	float e  = j_drone_expo->value;
	float ax = Q_fabs( x );

	if ( ax <= dz ) {
		return 0.0f;
	}
	ax = ( ax - dz ) / ( 1.0f - dz );
	ax = ( 1.0f - e ) * ax + e * ax * ax * ax;
	return x < 0 ? -ax : ax;
}

/*
=================
CL_DroneAttitude

Drone orientation is kept as a 3x3 axis (forward/left/up), not as
Euler angles: stick input rotates the drone about its OWN axes (acro
mode), so pitch can carry through vertical into a full loop/flip and
roll/yaw stay body-relative while inverted. The result is converted back
into the equivalent pitch/yaw/roll for cl.viewangles each frame (pitch
stays within +-90, roll absorbs the flip) - the same orientation, just
the usual Euler description of it.
=================
*/
static vec3_t droneAxis[3];
static qboolean droneAxisValid;

static void CL_DroneRotate( vec3_t axis[3], const vec3_t dir, float degrees ) {
	vec3_t d, t;
	int i;

	if ( degrees == 0.0f ) {
		return;
	}
	VectorCopy( dir, d );
	for ( i = 0; i < 3; i++ ) {
		RotatePointAroundVector( t, d, axis[i], degrees );
		VectorCopy( t, axis[i] );
	}
}

static int droneLastDelta[3];
static float droneLastYaw;

/*
The server sets ps.delta_angles whenever it forces a view angle (respawn, new
map/round, /team change) so that cmd->angles + delta_angles = the angle it
wants. A client that accumulates its own absolute attitude never sees that, so
after a respawn its stored attitude (and the controls) are left in whatever
orientation it had. Detect a delta change and re-adopt the server's angles.
*/
static void CL_DroneSyncAttitude( void ) {
	vec3_t a;
	qboolean changed = qfalse;
	int i;

	for ( i = 0; i < 3; i++ ) {
		if ( cl.snap.ps.delta_angles[i] != droneLastDelta[i] ) {
			changed = qtrue;
		}
		droneLastDelta[i] = cl.snap.ps.delta_angles[i];
	}

	if ( !droneAxisValid ) {
		// just entered drone mode: continue from what the server shows now
		for ( i = 0; i < 3; i++ ) {
			a[i] = cl.viewangles[i] + SHORT2ANGLE( cl.snap.ps.delta_angles[i] );
		}
		AnglesToAxis( a, droneAxis );
		droneAxisValid = qtrue;
	} else if ( changed ) {
		AnglesToAxis( cl.snap.ps.viewangles, droneAxis );
	}
}

static void CL_DroneApplyRates( float yawDeg, float pitchDeg, float rollDeg ) {
	vec3_t f0, r0, u0, d;
	float yaw, pitch, roll;

	CL_DroneSyncAttitude();

	// body-frame rotations about the drone's current up / left / forward
	VectorCopy( droneAxis[2], d ); CL_DroneRotate( droneAxis, d, yawDeg );
	VectorCopy( droneAxis[1], d ); CL_DroneRotate( droneAxis, d, pitchDeg );
	VectorCopy( droneAxis[0], d ); CL_DroneRotate( droneAxis, d, rollDeg );

	// re-orthonormalize so drift can't accumulate
	VectorNormalize( droneAxis[0] );
	CrossProduct( droneAxis[0], droneAxis[1], droneAxis[2] );
	VectorNormalize( droneAxis[2] );
	CrossProduct( droneAxis[2], droneAxis[0], droneAxis[1] );
	VectorNormalize( droneAxis[1] );

	// axis -> Euler. Straight up/down leaves yaw undefined: keep the last one.
	if ( Q_fabs( droneAxis[0][2] ) > 0.9999f ) {
		yaw = droneLastYaw;
	} else {
		yaw = RAD2DEG( atan2( droneAxis[0][1], droneAxis[0][0] ) );
	}
	pitch = -RAD2DEG( asin( Com_Clamp( -1.0f, 1.0f, droneAxis[0][2] ) ) );

	{
		vec3_t ang;
		ang[PITCH] = pitch; ang[YAW] = yaw; ang[ROLL] = 0;
		AngleVectors( ang, f0, r0, u0 );
	}
	// positive roll tips the up vector toward the right vector
	roll = RAD2DEG( atan2( DotProduct( droneAxis[2], r0 ), DotProduct( droneAxis[2], u0 ) ) );

	// cmd->angles is sent relative to the server's delta_angles, so subtract
	// them: the server then ends up with exactly (pitch, yaw, roll)
	droneLastYaw = yaw;
	cl.viewangles[PITCH] = pitch - SHORT2ANGLE( cl.snap.ps.delta_angles[PITCH] );
	cl.viewangles[YAW]   = yaw   - SHORT2ANGLE( cl.snap.ps.delta_angles[YAW] );
	cl.viewangles[ROLL]  = roll  - SHORT2ANGLE( cl.snap.ps.delta_angles[ROLL] );
}

void CL_DroneJoystickMove( usercmd_t *cmd ) {
	float anglespeed;
	float yawRate, throttle, roll, pitch;

	if ( ( Key_GetCatcher( ) & KEYCATCH_UI ) && !CL_VsayMenuOpen( ) ) {
		return;
	}

	// IN_GetRawGamepadAxis reads the raw SDL_Joystick axis slot directly,
	// bypassing cl.joystickAxis[]'s digital-key translation layer - that
	// layer's write target is itself determined by these same j_*_axis
	// cvars (see IN_GetRawGamepadAxis's comment in sdl_input.c), so
	// reading it back here would be self-referential and never reflect
	// the actual physical axis these cvars are supposed to select.
	yawRate  = j_drone_yaw->value      * 32767.0f * CL_DroneShape( IN_GetRawGamepadAxis( j_drone_yaw_axis->integer ) / 32767.0f );    // left stick X
	// throttle is a 0..32767 lever: |j_drone_throttle| scales it into upmove's
	// 0..127 range (127/32767 ~= 0.0039 for full travel); a negative sign
	// inverts the lever (idle<->full) instead of making upmove negative
	throttle = CL_DroneThrottleValue();
	if ( j_drone_throttle->value < 0 ) {
		throttle = 32767 - throttle;
	}
	throttle *= Q_fabs( j_drone_throttle->value );                                         // left stick Y
	roll     = j_drone_roll->value     * 32767.0f * CL_DroneShape( IN_GetRawGamepadAxis( j_drone_roll_axis->integer ) / 32767.0f );     // right stick X
	pitch    = j_drone_pitch->value    * 32767.0f * CL_DroneShape( IN_GetRawGamepadAxis( j_drone_pitch_axis->integer ) / 32767.0f );   // right stick Y

	if ( kb[KB_SPEED].active ) {
		anglespeed = 0.001 * cls.frametime * cl_anglespeedkey->value;
	} else {
		anglespeed = 0.001 * cls.frametime;
	}

	CL_DroneApplyRates( anglespeed * yawRate, anglespeed * pitch, anglespeed * roll );

	cmd->upmove = ClampChar( (int)throttle );
}

/*
=================
CL_MouseMove
=================
*/
void CL_MouseMove(usercmd_t *cmd) {
	float mx, my;

	// allow mouse smoothing
	if ( m_filter->integer ) {
		mx = ( cl.mouseDx[0] + cl.mouseDx[1] ) * 0.5f;
		my = ( cl.mouseDy[0] + cl.mouseDy[1] ) * 0.5f;
	} else {
		mx = cl.mouseDx[cl.mouseIndex];
		my = cl.mouseDy[cl.mouseIndex];
	}
	cl.mouseIndex ^= 1;
	cl.mouseDx[cl.mouseIndex] = 0;
	cl.mouseDy[cl.mouseIndex] = 0;

	if (mx == 0.0f && my == 0.0f)
		return;
	
	if (cl_mouseAccel->value != 0.0f)
	{
		if(cl_mouseAccelStyle->integer == 0)
		{
			float accelSensitivity;
			float rate;
			
			rate = sqrt(mx * mx + my * my) / (float) frame_msec;

			accelSensitivity = cl_sensitivity->value + rate * cl_mouseAccel->value;
			mx *= accelSensitivity;
			my *= accelSensitivity;
			
			if(cl_showMouseRate->integer)
				Com_Printf("rate: %f, accelSensitivity: %f\n", rate, accelSensitivity);
		}
		else
		{
			float rate[2];
			float power[2];

			// sensitivity remains pretty much unchanged at low speeds
			// cl_mouseAccel is a power value to how the acceleration is shaped
			// cl_mouseAccelOffset is the rate for which the acceleration will have doubled the non accelerated amplification
			// NOTE: decouple the config cvars for independent acceleration setup along X and Y?

			rate[0] = fabs(mx) / (float) frame_msec;
			rate[1] = fabs(my) / (float) frame_msec;
			power[0] = powf(rate[0] / cl_mouseAccelOffset->value, cl_mouseAccel->value);
			power[1] = powf(rate[1] / cl_mouseAccelOffset->value, cl_mouseAccel->value);

			mx = cl_sensitivity->value * (mx + ((mx < 0) ? -power[0] : power[0]) * cl_mouseAccelOffset->value);
			my = cl_sensitivity->value * (my + ((my < 0) ? -power[1] : power[1]) * cl_mouseAccelOffset->value);

			if(cl_showMouseRate->integer)
				Com_Printf("ratex: %f, ratey: %f, powx: %f, powy: %f\n", rate[0], rate[1], power[0], power[1]);
		}
	}
	else
	{
		// Rafael - mg42
		if ( cl.snap.ps.persistant[PERS_HWEAPON_USE] ) {
			mx *= 2.5; //(accelSensitivity * 0.1);
			my *= 2; //(accelSensitivity * 0.075);
		} else
		{
			mx *= cl_sensitivity->value;
			my *= cl_sensitivity->value;
		}
	}

	// ingame FOV
	mx *= cl.cgameSensitivity;
	my *= cl.cgameSensitivity;

	// add mouse X/Y movement to cmd
	if ( kb[KB_STRAFE].active ) {
		cmd->rightmove = ClampChar( cmd->rightmove + m_side->value * mx );
	} else {
		cl.viewangles[YAW] -= m_yaw->value * mx;
	}

	if ( ( kb[KB_MLOOK].active || cl_freelook->integer ) && !kb[KB_STRAFE].active ) {
		cl.viewangles[PITCH] += m_pitch->value * my;
	} else {
		cmd->forwardmove = ClampChar( cmd->forwardmove - m_forward->value * my );
	}
}


/*
==============
CL_CmdButtons
==============
*/
void CL_CmdButtons( usercmd_t *cmd ) {
	int i;

	//
	// figure button bits
	// send a button bit even if the key was pressed and released in
	// less than a frame
	//
	for ( i = 0 ; i < 7 ; i++ ) {
		if ( kb[KB_BUTTONS0 + i].active || kb[KB_BUTTONS0 + i].wasPressed ) {
			cmd->buttons |= 1 << i;
		}
		kb[KB_BUTTONS0 + i].wasPressed = qfalse;
	}

	for ( i = 0 ; i < 7 ; i++ ) {
		if ( kb[KB_WBUTTONS0 + i].active || kb[KB_WBUTTONS0 + i].wasPressed ) {
			cmd->wbuttons |= 1 << i;
		}
		kb[KB_WBUTTONS0 + i].wasPressed = qfalse;
	}

	if ( Key_GetCatcher( ) && !cl_bypassMouseInput->integer ) {
		cmd->buttons |= BUTTON_TALK;
	}

	// allow the game to know if any key at all is
	// currently pressed, even if it isn't bound to anything
	if ( anykeydown && ( Key_GetCatcher( ) == 0 || cl_bypassMouseInput->integer ) ) {
		cmd->buttons |= BUTTON_ANY;
	}
}


/*
==============
CL_FinishMove
==============
*/
void CL_FinishMove( usercmd_t *cmd ) {
	int i;

	// copy the state that the cgame is currently sending
	cmd->weapon = cl.cgameUserCmdValue;

	cmd->holdable = cl.cgameUserHoldableValue;  //----(SA)	modified

	cmd->mpSetup = cl.cgameMpSetup;             // NERVE - SMF
	cmd->identClient = cl.cgameMpIdentClient;   // NERVE - SMF

	// send the current server time so the amount of movement
	// can be determined without allowing cheating
	cmd->serverTime = cl.serverTime;

	for ( i = 0 ; i < 3 ; i++ ) {
		cmd->angles[i] = ANGLE2SHORT( cl.viewangles[i] );
	}
}


/*
=================
CL_CreateCmd
=================
*/
usercmd_t CL_CreateCmd( void ) {
	usercmd_t cmd;
	vec3_t oldAngles;
	float recoilAdd;

	VectorCopy( cl.viewangles, oldAngles );

	// keyboard angle adjustment
	CL_AdjustAngles();

	memset( &cmd, 0, sizeof( cmd ) );

	CL_CmdButtons( &cmd );

	// get basic movement from keyboard
	CL_KeyMove( &cmd );

	// get basic movement from mouse
	CL_MouseMove( &cmd );

	// get basic movement from joystick
	if ( cl.snap.valid && cl.snap.ps.pm_type == PM_DRONE ) {
		CL_DroneJoystickMove( &cmd );
	} else {
		droneAxisValid = qfalse;
		cl.viewangles[ROLL] = 0;
		CL_JoystickMove( &cmd );
	}

	// check to make sure the angles haven't wrapped (the drone's flip
	// legitimately swings pitch/yaw/roll around vertical, so skip it there)
	if ( !droneAxisValid && cl.viewangles[PITCH] - oldAngles[PITCH] > 90 ) {
		cl.viewangles[PITCH] = oldAngles[PITCH] + 90;
	} else if ( !droneAxisValid && oldAngles[PITCH] - cl.viewangles[PITCH] > 90 ) {
		cl.viewangles[PITCH] = oldAngles[PITCH] - 90;
	}

	// RF, set the kickAngles so aiming is effected
	recoilAdd = cl_recoilPitch->value;
	if ( fabs( cl.viewangles[PITCH] + recoilAdd ) < 40 ) {
		cl.viewangles[PITCH] += recoilAdd;
	}
	// the recoilPitch has been used, so clear it out
	cl_recoilPitch->value = 0;

	// store out the final values
	CL_FinishMove( &cmd );

	// draw debug graphs of turning for mouse testing
	if ( cl_debugMove->integer ) {
		if ( cl_debugMove->integer == 1 ) {
			SCR_DebugGraph( fabs(cl.viewangles[YAW] - oldAngles[YAW]) );
		}
		if ( cl_debugMove->integer == 2 ) {
			SCR_DebugGraph( fabs(cl.viewangles[PITCH] - oldAngles[PITCH]) );
		}
	}

	return cmd;
}


/*
=================
CL_CreateNewCommands

Create a new usercmd_t structure for this frame
=================
*/
void CL_CreateNewCommands( void ) {
	int cmdNum;

	// no need to create usercmds until we have a gamestate
	if ( clc.state < CA_PRIMED ) {
		return;
	}

	frame_msec = com_frameTime - old_com_frameTime;

	// if running over 1000fps, act as if each frame is 1ms
	// prevents divisions by zero
	if ( frame_msec < 1 ) {
		frame_msec = 1;
	}

	// if running less than 5fps, truncate the extra time to prevent
	// unexpected moves after a hitch
	if ( frame_msec > 200 ) {
		frame_msec = 200;
	}
	old_com_frameTime = com_frameTime;


	// generate a command for this frame
	cl.cmdNumber++;
	cmdNum = cl.cmdNumber & CMD_MASK;
	cl.cmds[cmdNum] = CL_CreateCmd();
}

/*
=================
CL_ReadyToSendPacket

Returns qfalse if we are over the maxpackets limit
and should choke back the bandwidth a bit by not sending
a packet this frame.  All the commands will still get
delivered in the next packet, but saving a header and
getting more delta compression will reduce total bandwidth.
=================
*/
qboolean CL_ReadyToSendPacket( void ) {
	int oldPacketNum;
	int delta;

	// don't send anything if playing back a demo
	if ( clc.demoplaying || clc.state == CA_CINEMATIC ) {
		return qfalse;
	}

	// If we are downloading, we send no less than 50ms between packets
	if ( *clc.downloadTempName &&
		 cls.realtime - clc.lastPacketSentTime < 50 ) {
		return qfalse;
	}

	// if we don't have a valid gamestate yet, only send
	// one packet a second
	if ( clc.state != CA_ACTIVE &&
		 clc.state != CA_PRIMED &&
		 !*clc.downloadTempName &&
		 cls.realtime - clc.lastPacketSentTime < 1000 ) {
		return qfalse;
	}

	// send every frame for loopbacks
	if ( clc.netchan.remoteAddress.type == NA_LOOPBACK ) {
		return qtrue;
	}

	// send every frame for LAN
	if ( cl_lanForcePackets->integer && Sys_IsLANAddress( clc.netchan.remoteAddress ) ) {
		return qtrue;
	}

	// check for exceeding cl_maxpackets
	if ( cl_maxpackets->integer < 25 ) {
		Cvar_Set( "cl_maxpackets", "25" );
	} else if ( cl_maxpackets->integer > 125 ) {
		Cvar_Set( "cl_maxpackets", "125" );
	}
	oldPacketNum = ( clc.netchan.outgoingSequence - 1 ) & PACKET_MASK;
	delta = cls.realtime -  cl.outPackets[ oldPacketNum ].p_realtime;
	if ( delta < 1000 / cl_maxpackets->integer ) {
		// the accumulated commands will go out in the next packet
		return qfalse;
	}

	return qtrue;
}

/*
===================
CL_WritePacket

Create and send the command packet to the server
Including both the reliable commands and the usercmds

During normal gameplay, a client packet will contain something like:

4	sequence number
2	qport
4	serverid
4	acknowledged sequence number
4	clc.serverCommandSequence
<optional reliable commands>
1	clc_move or clc_moveNoDelta
1	command count
<count * usercmds>

===================
*/
void CL_WritePacket( void ) {
	msg_t buf;
	byte data[MAX_MSGLEN];
	int i, j;
	usercmd_t   *cmd, *oldcmd;
	usercmd_t nullcmd;
	int packetNum;
	int oldPacketNum;
	int count, key;

	// don't send anything if playing back a demo
	if ( clc.demoplaying || clc.state == CA_CINEMATIC ) {
		return;
	}

	memset( &nullcmd, 0, sizeof( nullcmd ) );
	oldcmd = &nullcmd;

	MSG_Init( &buf, data, sizeof( data ) );

	MSG_Bitstream( &buf );
	// write the current serverId so the server
	// can tell if this is from the current gameState
	MSG_WriteLong( &buf, cl.serverId );

	// write the last message we received, which can
	// be used for delta compression, and is also used
	// to tell if we dropped a gamestate
	MSG_WriteLong( &buf, clc.serverMessageSequence );

	// write the last reliable message we received
	MSG_WriteLong( &buf, clc.serverCommandSequence );

	// write any unacknowledged clientCommands
	for ( i = clc.reliableAcknowledge + 1 ; i <= clc.reliableSequence ; i++ ) {
		MSG_WriteByte( &buf, clc_clientCommand );
		MSG_WriteLong( &buf, i );
		MSG_WriteString( &buf, clc.reliableCommands[ i & ( MAX_RELIABLE_COMMANDS - 1 ) ] );
	}

	// we want to send all the usercmds that were generated in the last
	// few packet, so even if a couple packets are dropped in a row,
	// all the cmds will make it to the server
	if ( cl_packetdup->integer < 0 ) {
		Cvar_Set( "cl_packetdup", "0" );
	} else if ( cl_packetdup->integer > 5 ) {
		Cvar_Set( "cl_packetdup", "5" );
	}
	oldPacketNum = ( clc.netchan.outgoingSequence - 1 - cl_packetdup->integer ) & PACKET_MASK;
	count = cl.cmdNumber - cl.outPackets[ oldPacketNum ].p_cmdNumber;
	if ( count > MAX_PACKET_USERCMDS ) {
		count = MAX_PACKET_USERCMDS;
		Com_Printf( "MAX_PACKET_USERCMDS\n" );
	}

#ifdef USE_VOIP
	if (clc.voipOutgoingDataSize > 0)
	{
		if((clc.voipFlags & VOIP_SPATIAL) || Com_IsVoipTarget(clc.voipTargets, sizeof(clc.voipTargets), -1))
		{
			MSG_WriteByte (&buf, clc_voipOpus);
			MSG_WriteByte (&buf, clc.voipOutgoingGeneration);
			MSG_WriteLong (&buf, clc.voipOutgoingSequence);
			MSG_WriteByte (&buf, clc.voipOutgoingDataFrames);
			MSG_WriteData (&buf, clc.voipTargets, sizeof(clc.voipTargets));
			MSG_WriteByte(&buf, clc.voipFlags);
			MSG_WriteShort (&buf, clc.voipOutgoingDataSize);
			MSG_WriteData (&buf, clc.voipOutgoingData, clc.voipOutgoingDataSize);

			// If we're recording a demo, we have to fake a server packet with
			//  this VoIP data so it gets to disk; the server doesn't send it
			//  back to us, and we might as well eliminate concerns about dropped
			//  and misordered packets here.
			if(clc.demorecording && !clc.demowaiting)
			{
				const int voipSize = clc.voipOutgoingDataSize;
				msg_t fakemsg;
				byte fakedata[MAX_MSGLEN];
				MSG_Init (&fakemsg, fakedata, sizeof (fakedata));
				MSG_Bitstream (&fakemsg);
				MSG_WriteLong (&fakemsg, clc.reliableAcknowledge);
				MSG_WriteByte (&fakemsg, svc_voipOpus);
				MSG_WriteShort (&fakemsg, clc.clientNum);
				MSG_WriteByte (&fakemsg, clc.voipOutgoingGeneration);
				MSG_WriteLong (&fakemsg, clc.voipOutgoingSequence);
				MSG_WriteByte (&fakemsg, clc.voipOutgoingDataFrames);
				MSG_WriteShort (&fakemsg, clc.voipOutgoingDataSize );
				MSG_WriteBits (&fakemsg, clc.voipFlags, VOIP_FLAGCNT);
				MSG_WriteData (&fakemsg, clc.voipOutgoingData, voipSize);
				MSG_WriteByte (&fakemsg, svc_EOF);
				CL_WriteDemoMessage (&fakemsg, 0);
			}

			clc.voipOutgoingSequence += clc.voipOutgoingDataFrames;
			clc.voipOutgoingDataSize = 0;
			clc.voipOutgoingDataFrames = 0;
		}
		else
		{
			// We have data, but no targets. Silently discard all data
			clc.voipOutgoingDataSize = 0;
			clc.voipOutgoingDataFrames = 0;
		}
	}
#endif

	if ( count >= 1 ) {
		if ( cl_showSend->integer ) {
			Com_Printf( "(%i)", count );
		}

		// begin a client move command
		if ( cl_nodelta->integer || !cl.snap.valid || clc.demowaiting
			 || clc.serverMessageSequence != cl.snap.messageNum ) {
			MSG_WriteByte( &buf, clc_moveNoDelta );
		} else {
			MSG_WriteByte( &buf, clc_move );
		}

		// write the command count
		MSG_WriteByte( &buf, count );

		// use the checksum feed in the key
		key = clc.checksumFeed;
		// also use the message acknowledge
		key ^= clc.serverMessageSequence;
		// also use the last acknowledged server command in the key
		key ^= MSG_HashKey(clc.serverCommands[ clc.serverCommandSequence & (MAX_RELIABLE_COMMANDS-1) ], 32);

		// write all the commands, including the predicted command
		for ( i = 0 ; i < count ; i++ ) {
			j = ( cl.cmdNumber - count + i + 1 ) & CMD_MASK;
			cmd = &cl.cmds[j];
			MSG_WriteDeltaUsercmdKey( &buf, key, oldcmd, cmd );
			oldcmd = cmd;
		}
	}

	//
	// deliver the message
	//
	packetNum = clc.netchan.outgoingSequence & PACKET_MASK;
	cl.outPackets[ packetNum ].p_realtime = cls.realtime;
	cl.outPackets[ packetNum ].p_serverTime = oldcmd->serverTime;
	cl.outPackets[ packetNum ].p_cmdNumber = cl.cmdNumber;
	clc.lastPacketSentTime = cls.realtime;

	if ( cl_showSend->integer ) {
		Com_Printf( "%i ", buf.cursize );
	}

	CL_Netchan_Transmit( &clc.netchan, &buf );
}

/*
=================
CL_SendCmd

Called every frame to builds and sends a command packet to the server.
=================
*/
void CL_SendCmd( void ) {
	// don't send any message if not connected
	if ( clc.state < CA_CONNECTED ) {
		return;
	}

	// don't send commands if paused
	if ( com_sv_running->integer && sv_paused->integer && cl_paused->integer ) {
		return;
	}

	// we create commands even if a demo is playing,
	CL_CreateNewCommands();

	// don't send a packet if the last packet was sent too recently
	if ( !CL_ReadyToSendPacket() ) {
		if ( cl_showSend->integer ) {
			Com_Printf( ". " );
		}
		return;
	}

	CL_WritePacket();
}

/*
============
CL_InitInput
============
*/
void CL_InitInput( void ) {
	Cmd_AddCommand( "centerview",IN_CenterView );

	Cmd_AddCommand( "+moveup",IN_UpDown );
	Cmd_AddCommand( "-moveup",IN_UpUp );
	Cmd_AddCommand( "+movedown",IN_DownDown );
	Cmd_AddCommand( "-movedown",IN_DownUp );
	Cmd_AddCommand( "+left",IN_LeftDown );
	Cmd_AddCommand( "-left",IN_LeftUp );
	Cmd_AddCommand( "+right",IN_RightDown );
	Cmd_AddCommand( "-right",IN_RightUp );
	Cmd_AddCommand( "+forward",IN_ForwardDown );
	Cmd_AddCommand( "-forward",IN_ForwardUp );
	Cmd_AddCommand( "+back",IN_BackDown );
	Cmd_AddCommand( "-back",IN_BackUp );
	Cmd_AddCommand( "+lookup", IN_LookupDown );
	Cmd_AddCommand( "-lookup", IN_LookupUp );
	Cmd_AddCommand( "+lookdown", IN_LookdownDown );
	Cmd_AddCommand( "-lookdown", IN_LookdownUp );
	Cmd_AddCommand( "+strafe", IN_StrafeDown );
	Cmd_AddCommand( "-strafe", IN_StrafeUp );
	Cmd_AddCommand( "+moveleft", IN_MoveleftDown );
	Cmd_AddCommand( "-moveleft", IN_MoveleftUp );
	Cmd_AddCommand( "+moveright", IN_MoverightDown );
	Cmd_AddCommand( "-moveright", IN_MoverightUp );
	Cmd_AddCommand( "+speed", IN_SpeedDown );
	Cmd_AddCommand( "-speed", IN_SpeedUp );

	Cmd_AddCommand( "+attack", IN_Button0Down );   // ---- id   (primary firing)
	Cmd_AddCommand( "-attack", IN_Button0Up );

	Cmd_AddCommand( "+button1", IN_Button1Down );
	Cmd_AddCommand( "-button1", IN_Button1Up );

	Cmd_AddCommand( "+useitem", IN_UseItemDown );
	Cmd_AddCommand( "-useitem", IN_UseItemUp );

	Cmd_AddCommand( "+salute",   IN_Button3Down ); //----(SA) salute
	Cmd_AddCommand( "-salute",   IN_Button3Up );

	Cmd_AddCommand( "+button4", IN_Button4Down );
	Cmd_AddCommand( "-button4", IN_Button4Up );

	// Rafael Activate
	Cmd_AddCommand( "+activate", IN_ActivateDown );
	Cmd_AddCommand( "-activate", IN_ActivateUp );
	// done.

	// Rafael Kick
	Cmd_AddCommand( "+kick", IN_KickDown );
	Cmd_AddCommand( "-kick", IN_KickUp );
	// done

	Cmd_AddCommand( "+sprint", IN_SprintDown );
	Cmd_AddCommand( "-sprint", IN_SprintUp );


	// wolf buttons
	Cmd_AddCommand( "+attack2",      IN_Wbutton0Down );   //----(SA) secondary firing
	Cmd_AddCommand( "-attack2",      IN_Wbutton0Up );
	Cmd_AddCommand( "+zoom",     IN_ZoomDown );       //
	Cmd_AddCommand( "-zoom",     IN_ZoomUp );
	Cmd_AddCommand( "+quickgren",    IN_QuickGrenDown );  //
	Cmd_AddCommand( "-quickgren",    IN_QuickGrenUp );
	Cmd_AddCommand( "+reload",       IN_ReloadDown );     //
	Cmd_AddCommand( "-reload",       IN_ReloadUp );
	Cmd_AddCommand( "+leanleft", IN_LeanLeftDown );
	Cmd_AddCommand( "-leanleft", IN_LeanLeftUp );
	Cmd_AddCommand( "+leanright",    IN_LeanRightDown );
	Cmd_AddCommand( "-leanright",    IN_LeanRightUp );
// JPW NERVE multiplayer buttons
	Cmd_AddCommand( "+dropweapon",   IN_MP_DropWeaponDown );  // JPW NERVE drop two-handed weapon
	Cmd_AddCommand( "-dropweapon",   IN_MP_DropWeaponUp );
// jpw
	Cmd_AddCommand( "+wbutton7", IN_Wbutton7Down );   //
	Cmd_AddCommand( "-wbutton7", IN_Wbutton7Up );
//----(SA) end

	Cmd_AddCommand( "+mlook", IN_MLookDown );
	Cmd_AddCommand( "-mlook", IN_MLookUp );

#ifdef USE_VOIP
	Cmd_AddCommand( "+voiprecord", IN_VoipRecordDown );
	Cmd_AddCommand( "-voiprecord", IN_VoipRecordUp );
#endif

//	Cmd_AddCommand( "notebook", IN_Notebook );
	Cmd_AddCommand( "help",IN_Help );

	cl_nodelta = Cvar_Get( "cl_nodelta", "0", 0 );
	cl_debugMove = Cvar_Get( "cl_debugMove", "0", 0 );
	cl_controllerAimAssist = Cvar_Get( "cl_controllerAimAssist", "1", CVAR_ARCHIVE );
	// extra turn speed (as a fraction, 1 = +100%) after holding the look stick at full
	// deflection for ~0.4s; 0 disables.  With j_yaw -0.0055 (about 180 deg/s at full tilt) a held
	// full tilt reaches about 360 deg/s, like a modern shooter's turn-around boost.
	cl_controllerLookRamp = Cvar_Get( "cl_controllerLookRamp", "1", CVAR_ARCHIVE );
	// scale controller look speed down with scope/binocular zoom
	cl_controllerZoomScale = Cvar_Get( "cl_controllerZoomScale", "1", CVAR_ARCHIVE );
	/*
	 * Aim assist (controller and touch, never the mouse; see CL_AimAssist).
	 * Track (0-1): share of a target's movement across the view that the view follows.
	 * Slowdown (0.1-1): look speed over a target (1 = none).
	 * Radius (world units): size of the bubble around a target's body (a player is about 32 wide).
	 * MaxAngle / MinAngle (degrees): the most a close target's bubble can cover, and the least a far one's does
	 * (a far player is a few pixels wide; a stick cannot be held inside a 1 degree circle).
	 */
	cl_controllerAimAssistTrack     = Cvar_Get( "cl_controllerAimAssistTrack",     "0.6",  CVAR_ARCHIVE );
	// Pull (per second): while aiming or moving, the crosshair eases toward the target by this many degrees per
	// second for each degree it is off (2 = covers about 85% of the gap in a second); 0 turns it off
	cl_controllerAimAssistPull      = Cvar_Get( "cl_controllerAimAssistPull",      "2",    CVAR_ARCHIVE );
	cl_controllerAimAssistSlowdown  = Cvar_Get( "cl_controllerAimAssistSlowdown",  "0.65", CVAR_ARCHIVE );
	cl_controllerAimAssistRadius    = Cvar_Get( "cl_controllerAimAssistRadius",    "60",   CVAR_ARCHIVE );
	cl_controllerAimAssistMaxAngle  = Cvar_Get( "cl_controllerAimAssistMaxAngle",  "10",   CVAR_ARCHIVE );
	cl_controllerAimAssistMinAngle  = Cvar_Get( "cl_controllerAimAssistMinAngle",  "3",    CVAR_ARCHIVE );
	cl_controllerAimAssistDebug     = Cvar_Get( "cl_controllerAimAssistDebug",     "0",    CVAR_CHEAT );
	// touch swipe look (browser client): degrees per CSS pixel, and the up/down share of it
	cl_touchLookSens  = Cvar_Get( "cl_touchLookSens",  "0.2", CVAR_ARCHIVE );
	cl_touchLookPitch = Cvar_Get( "cl_touchLookPitch", "0.7", CVAR_ARCHIVE );
	// extra turn for a fast swipe: up to (1 + this) times cl_touchLookSens per pixel (from 600 px/s, full at 1800)
	cl_touchLookAccel = Cvar_Get( "cl_touchLookAccel", "1.5", CVAR_ARCHIVE );

	// on-screen readout of raw SDL joystick/gamecontroller state vs. the
	// engine's own K_PAD0_* key state - see SCR_DrawGamepadDebugOverlay.
	// Works identically on the native client and the WASM build, both of
	// which go through the same SDL input layer.
	Cvar_Get( "cl_debugGamepad", "0", CVAR_ARCHIVE );
}

/*
============
CL_ShutdownInput
============
*/
void CL_ShutdownInput(void)
{
	Cmd_RemoveCommand("centerview");

	Cmd_RemoveCommand("+moveup");
	Cmd_RemoveCommand("-moveup");
	Cmd_RemoveCommand("+movedown");
	Cmd_RemoveCommand("-movedown");
	Cmd_RemoveCommand("+left");
	Cmd_RemoveCommand("-left");
	Cmd_RemoveCommand("+right");
	Cmd_RemoveCommand("-right");
	Cmd_RemoveCommand("+forward");
	Cmd_RemoveCommand("-forward");
	Cmd_RemoveCommand("+back");
	Cmd_RemoveCommand("-back");
	Cmd_RemoveCommand("+lookup");
	Cmd_RemoveCommand("-lookup");
	Cmd_RemoveCommand("+lookdown");
	Cmd_RemoveCommand("-lookdown");
	Cmd_RemoveCommand("+strafe");
	Cmd_RemoveCommand("-strafe");
	Cmd_RemoveCommand("+moveleft");
	Cmd_RemoveCommand("-moveleft");
	Cmd_RemoveCommand("+moveright");
	Cmd_RemoveCommand("-moveright");
	Cmd_RemoveCommand("+speed");
	Cmd_RemoveCommand("-speed");

	Cmd_RemoveCommand("+attack");
	Cmd_RemoveCommand("-attack");

	Cmd_RemoveCommand("+button1");
	Cmd_RemoveCommand("-button1");

	Cmd_RemoveCommand("+useitem");
	Cmd_RemoveCommand("-useitem");

	Cmd_RemoveCommand("+salute");
	Cmd_RemoveCommand("-salute");

	Cmd_RemoveCommand("+button4");
	Cmd_RemoveCommand("-button4");

	Cmd_RemoveCommand("+activate");
	Cmd_RemoveCommand("-activate");

	Cmd_RemoveCommand("+kick");
	Cmd_RemoveCommand("-kick");

	Cmd_RemoveCommand("+sprint");
	Cmd_RemoveCommand("-sprint");

	Cmd_RemoveCommand("+attack2");
	Cmd_RemoveCommand("-attack2");
	Cmd_RemoveCommand("+zoom");
	Cmd_RemoveCommand("-zoom");
	Cmd_RemoveCommand("+quickgren");
	Cmd_RemoveCommand("-quickgren");
	Cmd_RemoveCommand("+reload");
	Cmd_RemoveCommand("-reload");
	Cmd_RemoveCommand("+leanleft");
	Cmd_RemoveCommand("-leanleft");
	Cmd_RemoveCommand("+leanright");
	Cmd_RemoveCommand("-leanright");
	Cmd_RemoveCommand("+dropweapon");
	Cmd_RemoveCommand("-dropweapon");
	Cmd_RemoveCommand("+wbutton7");
	Cmd_RemoveCommand("-wbutton7");

	Cmd_RemoveCommand("+mlook");
	Cmd_RemoveCommand("-mlook");

#ifdef USE_VOIP
	Cmd_RemoveCommand("+voiprecord");
	Cmd_RemoveCommand("-voiprecord");
#endif

	Cmd_RemoveCommand("help");
}

/*
============
CL_ClearKeys
============
*/
void CL_ClearKeys( void ) {
	memset( kb, 0, sizeof( kb ) );
}
