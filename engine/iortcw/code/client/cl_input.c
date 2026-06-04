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
static cvar_t *cl_controllerAimAssistCone = NULL;
static cvar_t *cl_controllerAimAssistSlowdown = NULL;
static cvar_t *cl_controllerAimAssistWindow = NULL;
static cvar_t *cl_controllerAimAssistPull = NULL;
static cvar_t *cl_controllerAimAssistPullMax = NULL;
static cvar_t *cl_controllerAimAssistDebug = NULL;

static int cl_lastMouseMoveTime = 0;
static int cl_lastControllerLookTime = 0;
static int cl_lastAimAssistDebugTime = 0;
static int cl_aaEntityDebugTime = 0;   /* separate timer so entity verbose never conflicts */

/* target lock: once selected, the locked target gets preference until it clearly leaves the cone */
static int cl_aaLockedTarget = -1;

typedef struct {
	qboolean active;    /* true = target found inside the cone this frame */
	qboolean scanning;  /* true = system is live and searching             */
	qboolean hasTargetPoint;
	float yawDelta;
	float pitchDelta;
	float outerAngle;
	float innerAngle;
	float outerBlend;
	float innerBlend;
	vec3_t targetPoint;
	/* pipeline stage counts — filled every frame, shown in SCANNING overlay */
	int nTotal;         /* cl.snap.numEntities                             */
	int nPlayer;        /* entities with eType == ET_PLAYER (excl. self)   */
	int nEnemy;         /* player entities that passed team filter          */
	int nLOS;           /* enemies that also have line of sight             */
} aimAssistVis_t;

static aimAssistVis_t cl_aimAssistVis;

void CL_GetAimAssistVisState( qboolean *active, qboolean *scanning,
	qboolean *hasTargetPoint, vec3_t targetPoint,
	float *yawDelta, float *pitchDelta,
	float *outerAngle, float *innerAngle,
	float *outerBlend, float *innerBlend,
	int *nTotal, int *nPlayer, int *nEnemy, int *nLOS ) {
	*active      = cl_aimAssistVis.active;
	*scanning    = cl_aimAssistVis.scanning;
	*hasTargetPoint = cl_aimAssistVis.hasTargetPoint;
	VectorCopy( cl_aimAssistVis.targetPoint, targetPoint );
	*yawDelta    = cl_aimAssistVis.yawDelta;
	*pitchDelta  = cl_aimAssistVis.pitchDelta;
	*outerAngle  = cl_aimAssistVis.outerAngle;
	*innerAngle  = cl_aimAssistVis.innerAngle;
	*outerBlend  = cl_aimAssistVis.outerBlend;
	*innerBlend  = cl_aimAssistVis.innerBlend;
	*nTotal      = cl_aimAssistVis.nTotal;
	*nPlayer     = cl_aimAssistVis.nPlayer;
	*nEnemy      = cl_aimAssistVis.nEnemy;
	*nLOS        = cl_aimAssistVis.nLOS;
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
 * Two-zone aim assist modeled after modern console shooters:
 *
 *   Outer zone  (outerAngle deg)  — rotational slowdown/friction only.
 *                                   Crosshair moves at `slowdown` fraction
 *                                   of normal speed when over a target.
 *
 *   Inner zone  (40% of outer)   — tracking magnetism in addition to
 *                                   slowdown.  A small angular correction
 *                                   is applied each frame to help keep the
 *                                   crosshair on the target as they move.
 *
 * Both effects are reduced proportionally when the look stick is pushed
 * past 50% (fast flick) so rapid turns are never artificially slowed.
 * Tracking pull is frametime-normalised to 60 Hz so it feels identical
 * regardless of frame rate.
 */
static float CL_GetControllerAimAssist( vec2_t pullAngles, float stickMagnitude,
	float lookYawDelta, float lookPitchDelta ) {
	int i;
	int bestClientNum;
	int playerTeam;
	float bestDot;
	float bestDist;
	float outerAngle;
	float innerAngle;
	float outerConeDot;
	float innerConeDot;
	float slowdown;
	float pullStrength;
	float pullMax;
	float scale;
	float outerBlend;
	float innerBlend;
	float frictionBlend;
	float pullBlend;
	float releaseScale;
	float stickFactor;
	float dt;
	float frameRef;
	float offAngle;
	float bestPitchDelta;
	float bestYawDelta;
	int windowAge;
	vec3_t start;
	vec3_t forward;
	vec3_t bestAimPoint;

	pullAngles[0] = 0.0f;
	pullAngles[1] = 0.0f;
	cl_aimAssistVis.active   = qfalse;
	cl_aimAssistVis.scanning = qfalse;
	cl_aimAssistVis.hasTargetPoint = qfalse;
	cl_aimAssistVis.nTotal   = 0;
	cl_aimAssistVis.nPlayer  = 0;
	cl_aimAssistVis.nEnemy   = 0;
	cl_aimAssistVis.nLOS     = 0;

	if ( !cl_controllerAimAssist || !cl_controllerAimAssist->integer ) {
		return 1.0f;
	}

	if ( clc.state != CA_ACTIVE || !cl.snap.valid ) {
		return 1.0f;
	}

	if ( cl.snap.ps.pm_type != PM_NORMAL || ( cl.snap.ps.pm_flags & PMF_FOLLOW ) ) {
		return 1.0f;
	}

	/* SDL event times can be ahead of cls.realtime; clamp to avoid negatives */
	windowAge = cls.realtime - cl_lastControllerLookTime;
	if ( windowAge < 0 ) {
		windowAge = 0;
	}
	if ( windowAge > cl_controllerAimAssistWindow->integer ) {
		return 1.0f;
	}

	if ( cl_lastMouseMoveTime >= cl_lastControllerLookTime ) {
		return 1.0f;
	}

	// All gate checks passed — the system is live and scanning for targets
	cl_aimAssistVis.scanning = qtrue;

	outerAngle   = Com_Clamp( 2.0f, 30.0f, cl_controllerAimAssistCone->value );
	innerAngle   = outerAngle * 0.25f;
	cl_aimAssistVis.outerAngle = outerAngle;
	cl_aimAssistVis.innerAngle = innerAngle;
	outerConeDot = cos( DEG2RAD( outerAngle ) );
	innerConeDot = cos( DEG2RAD( innerAngle ) );
	slowdown     = Com_Clamp( 0.1f, 1.0f, cl_controllerAimAssistSlowdown->value );
	pullStrength = Com_Clamp( 0.0f, 1.0f, cl_controllerAimAssistPull->value );
	pullMax      = Com_Clamp( 0.0f, 15.0f, cl_controllerAimAssistPullMax->value );
	bestDot      = outerConeDot;
	bestDist     = 0.0f;
	bestClientNum  = -1;
	bestPitchDelta = 0.0f;
	bestYawDelta   = 0.0f;
	playerTeam = cl.snap.ps.persistant[PERS_TEAM];
	VectorClear( bestAimPoint );

	/*
	 * Use snapshot origin + viewangles for both the eye position and the
	 * cone-detection forward vector.  Entity positions are also from the
	 * snapshot, so all three pieces of data are internally consistent —
	 * the same time-slice.  cl.viewangles accumulates client input every
	 * frame and can be many degrees ahead of the snapshot, which would
	 * push the cone away from where the snapshot entities actually are.
	 *
	 * The pull angle deltas below still reference cl.viewangles so that
	 * the correction is applied relative to where the view *currently* is.
	 */
	VectorCopy( cl.snap.ps.origin, start );
	start[2] += cl.snap.ps.viewheight;
	AngleVectors( cl.snap.ps.viewangles, forward, NULL, NULL );

	/*
	 * Verbose entity-level debug uses its OWN timer so it is never shadowed
	 * by the 50ms per-frame debug output that resets cl_lastAimAssistDebugTime.
	 */
	{
		qboolean doEntityDump = ( cl_controllerAimAssistDebug->integer >= 2
			&& cls.realtime - cl_aaEntityDebugTime >= 1000 );
		if ( doEntityDump ) {
			cl_aaEntityDebugTime = cls.realtime;
			Com_Printf( "[AA] SCAN  ents=%d  localTeam=%d  ps=%d  eye=(%.0f,%.0f,%.0f)  outerDot=%.3f\n",
				cl.snap.numEntities,
				playerTeam,
				cl.snap.ps.persistant[PERS_TEAM],
				start[0], start[1], start[2], outerConeDot );
		}

		cl_aimAssistVis.nTotal = cl.snap.numEntities;

		if ( !CL_IsAimAssistEnemyTeam( playerTeam, TEAM_RED )
			&& !CL_IsAimAssistEnemyTeam( playerTeam, TEAM_BLUE ) ) {
			if ( doEntityDump ) {
				Com_Printf( "[AA]  local team %d -> no team aim assist\n", playerTeam );
			}
			goto aa_debug_done;
		}

		for ( i = 0; i < cl.snap.numEntities; i++ ) {
			entityState_t *ent;
			int targetTeam;
			vec3_t aimPoint;
			vec3_t toTarget;
			vec3_t targetAngles;
			float dist;
			float dot;

			ent = &cl.parseEntities[( cl.snap.parseEntitiesNum + i ) & ( MAX_PARSE_ENTITIES - 1 )];

			if ( ent->eType != ET_PLAYER ) {
				if ( doEntityDump ) {
					Com_Printf( "[AA]  num=%d eType=%d -> not player\n",
						ent->number, ent->eType );
				}
				continue;
			}

			if ( ent->number == cl.snap.ps.clientNum
				|| ent->clientNum == cl.snap.ps.clientNum ) {
				continue;  /* self */
			}

			if ( ent->eFlags & EF_DEAD ) {
				if ( doEntityDump ) {
					Com_Printf( "[AA]  num=%d -> dead\n", ent->number );
				}
				continue;
			}

			if ( ent->eFlags & EF_NODRAW ) {
				if ( doEntityDump ) {
					Com_Printf( "[AA]  num=%d -> nodraw/occluded\n", ent->number );
				}
				continue;
			}

			cl_aimAssistVis.nPlayer++;

			targetTeam = CL_GetClientTeam( ent->clientNum );

			if ( !CL_IsAimAssistEnemyTeam( playerTeam, targetTeam ) ) {
				if ( doEntityDump ) {
					if ( targetTeam == TEAM_FREE || targetTeam == TEAM_SPECTATOR ) {
						Com_Printf( "[AA]  num=%d team=%d -> unknown/non-combat team\n",
							ent->number, targetTeam );
					} else {
						Com_Printf( "[AA]  num=%d team=%d -> same/not opposing team\n",
							ent->number, targetTeam );
					}
				}
				continue;
			}

			cl_aimAssistVis.nEnemy++;

			/*
			 * No LOS gating.  The server already PVS-culls the entity list
			 * so anything in the snapshot is in a potentially visible area.
			 * A strict geometry trace blocks too many valid targets on indoor
			 * maps; the cone check below is the real spatial gate.
			 */
			cl_aimAssistVis.nLOS++;

			/*
			 * Aim at true centre of the player's bounding box.
			 * Standing bbox: -24 to +40 → centre = +8 above origin.
			 * Crouching bbox: -24 to +16 → centre = -4, clamp to 0.
			 * The old +20/+8 values targeted the shoulder/neck area which
			 * caused the pull to drag the crosshair upward over the player
			 * when aiming at the lower body.
			 */
			VectorCopy( ent->pos.trBase, aimPoint );
			aimPoint[2] += ent->animMovetype ? 0 : 8;

			VectorSubtract( aimPoint, start, toTarget );
			dist = VectorNormalize( toTarget );
			if ( dist <= 0.0f ) {
				continue;
			}

			dot = DotProduct( forward, toTarget );

			if ( doEntityDump ) {
				Com_Printf( "[AA]  num=%d team=%d dist=%.0f dot=%.3f -> %s\n",
					ent->number, targetTeam, dist, dot,
					dot > outerConeDot ? "IN CONE" : "outside cone" );
			}

			{
				/*
				 * Target lock hysteresis: the currently locked target needs a
				 * lower threshold to win so it isn't displaced by a rival that
				 * is only marginally better-centered.  Any other candidate must
				 * beat the current best by at least half the remaining margin
				 * before it steals the lock.
				 */
				qboolean isLocked  = ( ent->clientNum == cl_aaLockedTarget );
				float    threshold = isLocked
					? ( outerConeDot + ( bestDot - outerConeDot ) * 0.5f )
					: bestDot;

				if ( dot > threshold ) {
					bestDot        = dot;
					bestDist       = dist;
					bestClientNum  = ent->clientNum;
					VectorCopy( aimPoint, bestAimPoint );

					vectoangles( toTarget, targetAngles );
					bestYawDelta   = AngleSubtract( targetAngles[YAW],   cl.viewangles[YAW]   );
					bestPitchDelta = AngleSubtract( targetAngles[PITCH],  cl.viewangles[PITCH] );
				}
			}
		}

aa_debug_done:
		;
	}

	// Debug: always print when the system is active, whether or not a target was found
	if ( cl_controllerAimAssistDebug && cl_controllerAimAssistDebug->integer
		&& cls.realtime - cl_lastAimAssistDebugTime >= 50 ) {
		cl_lastAimAssistDebugTime = cls.realtime;
		if ( bestClientNum < 0 ) {
			Com_Printf( "aim assist: SCANNING  cone=%.1fdeg  stick=%.2f  window=%dms\n",
				outerAngle, stickMagnitude, windowAge );
		}
	}

	if ( bestDot <= outerConeDot ) {
		cl_aaLockedTarget = -1;   /* target left the cone; clear lock */
		return 1.0f;
	}

	/* persist the selected target so next frame prefers it */
	cl_aaLockedTarget = bestClientNum;

	// Expose tracking state for the visual overlay in cl_scrn.c
	cl_aimAssistVis.active     = qtrue;
	cl_aimAssistVis.hasTargetPoint = qtrue;
	VectorCopy( bestAimPoint, cl_aimAssistVis.targetPoint );
	cl_aimAssistVis.yawDelta   = bestYawDelta;
	cl_aimAssistVis.pitchDelta = bestPitchDelta;

	// Smoothstep blend across the outer zone: 0 at the edge, 1 at center
	outerBlend = ( bestDot - outerConeDot ) / ( 1.0f - outerConeDot );
	outerBlend = outerBlend * outerBlend * ( 3.0f - 2.0f * outerBlend );
	offAngle   = RAD2DEG( acos( Com_Clamp( -1.0f, 1.0f, bestDot ) ) );

	// Separate inner-zone blend for tracking magnetism
	innerBlend = Com_Clamp( 0.0f, 1.0f, ( bestDot - innerConeDot ) / ( 1.0f - innerConeDot ) );
	innerBlend = innerBlend * innerBlend * ( 3.0f - 2.0f * innerBlend );

	// Reduce all assistance above 50% stick (fast flick) to avoid impeding turns
	stickFactor = 1.0f - Com_Clamp( 0.0f, 1.0f, ( stickMagnitude - 0.5f ) * 2.0f );

	/*
	 * Break-out assist release:
	 * if the player is steering away from the target on the dominant error
	 * axis, rapidly fade both slowdown and pull. This prevents the reticle
	 * from feeling "locked" on the target edge and lets the player cross
	 * through the box without having to creep the stick.
	 */
	releaseScale = 1.0f;
	if ( Q_fabs( bestYawDelta ) >= Q_fabs( bestPitchDelta ) ) {
		if ( Q_fabs( bestYawDelta ) > 0.001f && Q_fabs( lookYawDelta ) > 0.0005f ) {
			float closeRatio = Q_fabs( lookYawDelta ) / Q_fabs( bestYawDelta );
			if ( bestYawDelta * lookYawDelta < 0.0f ) {
				releaseScale = 0.15f;
			} else if ( closeRatio > 0.5f ) {
				float releaseAmount = Com_Clamp( 0.0f, 1.0f, ( closeRatio - 0.5f ) / 0.5f );
				releaseScale = 1.0f - 0.75f * releaseAmount;
			}
		}
	} else {
		if ( Q_fabs( bestPitchDelta ) > 0.001f && Q_fabs( lookPitchDelta ) > 0.0005f ) {
			float closeRatio = Q_fabs( lookPitchDelta ) / Q_fabs( bestPitchDelta );
			if ( bestPitchDelta * lookPitchDelta < 0.0f ) {
				releaseScale = 0.15f;
			} else if ( closeRatio > 0.5f ) {
				float releaseAmount = Com_Clamp( 0.0f, 1.0f, ( closeRatio - 0.5f ) / 0.5f );
				releaseScale = 1.0f - 0.75f * releaseAmount;
			}
		}
	}

	outerBlend *= releaseScale;
	innerBlend *= releaseScale;

	/*
	 * Modern split:
	 * - friction/slowdown is allowed across most of the cone
	 * - adhesion/pull only comes alive in a much smaller center core
	 *
	 * This avoids the feeling that the green circle border is a sticky wall.
	 */
	frictionBlend = outerBlend;
	pullBlend = Com_Clamp( 0.0f, 1.0f, ( innerBlend - 0.75f ) / 0.25f );
	pullBlend = pullBlend * pullBlend * ( 3.0f - 2.0f * pullBlend );

	cl_aimAssistVis.outerBlend = frictionBlend;
	cl_aimAssistVis.innerBlend = pullBlend;

	/*
	 * Distance falloff — full strength ≤ 200 units, linear fade to 20% at
	 * 1000 units.  Distant players are tiny on screen so the same angular
	 * pull that feels helpful at close range overshoots their whole body at
	 * long range.  Applied to both slowdown and pull so assistance scales
	 * naturally with how large the target actually appears.
	 */
	{
		float distFactor = Com_Clamp( 0.2f, 1.0f,
			1.0f - ( bestDist - 200.0f ) / 800.0f );
		stickFactor *= distFactor;
	}

	// Rotational slowdown: active across the full outer zone
	scale = 1.0f - frictionBlend * ( 1.0f - slowdown ) * stickFactor;

	// Tracking magnetism: inner zone only, frametime-normalised to 60 Hz
	dt       = cls.frametime * 0.001f;
	frameRef = dt * 60.0f;

	if ( pullBlend > 0.0f && pullStrength > 0.0f && pullMax > 0.0f ) {
		float inputDeadband      = 0.0005f;
		float pitchDeadband      = 0.35f;
		float yawDeadband        = 0.35f;
		float pitchAxisScale     = Com_Clamp( 0.0f, 1.0f, Q_fabs( bestPitchDelta ) / pitchDeadband );
		float yawAxisScale       = Com_Clamp( 0.0f, 1.0f, Q_fabs( bestYawDelta )   / yawDeadband );
		float manualAssistCapPct = 0.35f;
		float trackStrength      = pullStrength * pullBlend * stickFactor;
		float clampVal      = pullMax * frameRef;
		float desiredPitch  = bestPitchDelta * trackStrength * frameRef;
		float desiredYaw    = bestYawDelta   * trackStrength * frameRef;
		float manualTowardPitch = 0.0f;
		float manualTowardYaw   = 0.0f;
		float rawPitch;
		float rawYaw;

		if ( Q_fabs( lookPitchDelta ) > inputDeadband
			&& bestPitchDelta * lookPitchDelta > 0.0f ) {
			manualTowardPitch = lookPitchDelta;
		}
		if ( Q_fabs( lookYawDelta ) > inputDeadband
			&& bestYawDelta * lookYawDelta > 0.0f ) {
			manualTowardYaw = lookYawDelta;
		}

		/*
		 * Modern/fair assist rule: only supplement under-steer and only in a
		 * tiny center core. Outside that, friction is enough.
		 * If the player's own turn already covers the intended correction on
		 * an axis, add no extra pull on that axis. This avoids the springy
		 * "hold then snap past" behavior against static targets.
		 */
		if ( offAngle > 1.25f ) {
			desiredPitch = 0.0f;
			desiredYaw   = 0.0f;
		}

		rawPitch = desiredPitch - manualTowardPitch;
		rawYaw   = desiredYaw   - manualTowardYaw;

		if ( bestPitchDelta > 0.0f && rawPitch < 0.0f ) {
			rawPitch = 0.0f;
		} else if ( bestPitchDelta < 0.0f && rawPitch > 0.0f ) {
			rawPitch = 0.0f;
		}

		if ( bestYawDelta > 0.0f && rawYaw < 0.0f ) {
			rawYaw = 0.0f;
		} else if ( bestYawDelta < 0.0f && rawYaw > 0.0f ) {
			rawYaw = 0.0f;
		}

		/*
		 * Never let magnetism contribute more than a small fraction of the
		 * player's current turn rate. This keeps it in "trace assist" territory
		 * instead of feeling like a lock-on or a tug.
		 */
		if ( Q_fabs( manualTowardPitch ) > inputDeadband ) {
			rawPitch = Com_Clamp( -Q_fabs( manualTowardPitch ) * manualAssistCapPct,
				Q_fabs( manualTowardPitch ) * manualAssistCapPct, rawPitch );
		} else {
			rawPitch = 0.0f;
		}
		if ( Q_fabs( manualTowardYaw ) > inputDeadband ) {
			rawYaw = Com_Clamp( -Q_fabs( manualTowardYaw ) * manualAssistCapPct,
				Q_fabs( manualTowardYaw ) * manualAssistCapPct, rawYaw );
		} else {
			rawYaw = 0.0f;
		}

		/*
		 * Fade magnetism out near the target center so slowdown "holds" the
		 * crosshair instead of the pull snapping it across the box.
		 */
		rawPitch *= pitchAxisScale;
		rawYaw   *= yawAxisScale;

		/* never pull further than the actual angular error — prevents overshoot/oscillation */
		if ( bestPitchDelta != 0.0f && Q_fabs( rawPitch ) > Q_fabs( bestPitchDelta ) ) {
			rawPitch = bestPitchDelta;
		}
		if ( bestYawDelta != 0.0f && Q_fabs( rawYaw ) > Q_fabs( bestYawDelta ) ) {
			rawYaw = bestYawDelta;
		}

		pullAngles[0] = Com_Clamp( -clampVal, clampVal, rawPitch );
		pullAngles[1] = Com_Clamp( -clampVal, clampVal, rawYaw );
	}

	if ( cl_controllerAimAssistDebug && cl_controllerAimAssistDebug->integer
		&& cls.realtime - cl_lastAimAssistDebugTime >= 50 ) {
		cl_lastAimAssistDebugTime = cls.realtime;
		Com_Printf( "aim assist: TRACKING target=%d  dist=%.0fu  off=%.2fdeg"
			"  scale=%.2f  pull=(y:%.3f p:%.3f)  raw=(y:%.2f p:%.2f)"
			"  outer=%.2f  inner=%.2f  stick=%.2f  window=%dms\n",
			bestClientNum,
			bestDist,
			RAD2DEG( acos( Com_Clamp( -1.0f, 1.0f, bestDot ) ) ),
			scale,
			pullAngles[1], pullAngles[0],
			bestYawDelta, bestPitchDelta,
			frictionBlend, pullBlend,
			stickFactor,
			windowAge );
	}

	return scale;
}

/*
================
CL_AdjustAngles

Moves the local angle positions
================
*/
void CL_AdjustAngles( void ) {
	float speed;

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
CL_JoystickMove
=================
*/
void CL_JoystickMove( usercmd_t *cmd ) {
	float anglespeed;
	float aimAssistScale = 1.0f;
	qboolean walking;
	vec2_t aimAssistPull = { 0.0f, 0.0f };
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

	// Normalised look-stick magnitude (0–1) used to scale aim assist
	rawYaw        = (float)abs( cl.joystickAxis[j_yaw_axis->integer]   ) / 32767.0f;
	rawPitch      = (float)abs( cl.joystickAxis[j_pitch_axis->integer] ) / 32767.0f;
	stickMagnitude = rawYaw > rawPitch ? rawYaw : rawPitch;
	lookYawDelta   = anglespeed * ( kb[KB_STRAFE].active ? right : yaw );
	lookPitchDelta = anglespeed * ( kb[KB_MLOOK].active ? forward : pitch );

	/*
	 * Always call aim assist so the debug vis state is updated every frame.
	 * Previously this was gated on stick movement, which left active=true
	 * and stale delta values when the stick came to rest — the debug arrow
	 * would freeze in place indefinitely pointing the same direction.
	 *
	 * When the stick IS at rest, zero out the pull and reset scale so the
	 * view is never moved without intentional stick input.
	 */
	aimAssistScale = CL_GetControllerAimAssist( aimAssistPull, stickMagnitude,
		lookYawDelta, lookPitchDelta );
	if ( fabs( yaw ) == 0.0f && fabs( pitch ) == 0.0f ) {
		aimAssistScale  = 1.0f;
		aimAssistPull[0] = 0.0f;
		aimAssistPull[1] = 0.0f;
	}

	if ( !kb[KB_STRAFE].active ) {
		cl.viewangles[YAW] += anglespeed * yaw * aimAssistScale + aimAssistPull[1];
		cmd->rightmove = ClampChar( cmd->rightmove + (int)right );
	} else {
		cl.viewangles[YAW] += anglespeed * right * aimAssistScale + aimAssistPull[1];
		cmd->rightmove = ClampChar( cmd->rightmove + (int)yaw );
	}
	if ( kb[KB_MLOOK].active ) {
		cl.viewangles[PITCH] += anglespeed * forward * aimAssistScale + aimAssistPull[0];
		cmd->forwardmove = ClampChar( cmd->forwardmove + (int)pitch );
	} else {
		cl.viewangles[PITCH] += anglespeed * pitch * aimAssistScale + aimAssistPull[0];
		cmd->forwardmove = ClampChar( cmd->forwardmove + (int)forward );
	}

	cmd->upmove = ClampChar( cmd->upmove + (int)up );
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
	CL_JoystickMove( &cmd );

	// check to make sure the angles haven't wrapped
	if ( cl.viewangles[PITCH] - oldAngles[PITCH] > 90 ) {
		cl.viewangles[PITCH] = oldAngles[PITCH] + 90;
	} else if ( oldAngles[PITCH] - cl.viewangles[PITCH] > 90 ) {
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
	/*
	 * Cone (degrees): detection radius.  Inner zone = 40% of this.
	 * Slowdown (0.1–1.0): speed fraction at cone centre.  0.5 = half speed.
	 * Pull (0.0–1.0): fraction of angular error corrected per frame.
	 * PullMax (degrees/frame): hard cap on per-frame tracking nudge.
	 *
	 * These defaults aim for a conservative first-pass controller baseline:
	 *   outer zone  → noticeable slowdown without overholding the reticle
	 *   inner zone  → very light trace assist near center only
	 */
	cl_controllerAimAssistCone      = Cvar_Get( "cl_controllerAimAssistCone",      "12",   CVAR_ARCHIVE );
	cl_controllerAimAssistSlowdown  = Cvar_Get( "cl_controllerAimAssistSlowdown",  "0.72", CVAR_ARCHIVE );
	cl_controllerAimAssistWindow    = Cvar_Get( "cl_controllerAimAssistWindow",    "500",  CVAR_ARCHIVE );
	cl_controllerAimAssistPull      = Cvar_Get( "cl_controllerAimAssistPull",      "0.03", CVAR_ARCHIVE );
	cl_controllerAimAssistPullMax   = Cvar_Get( "cl_controllerAimAssistPullMax",   "0.30", CVAR_ARCHIVE );
	cl_controllerAimAssistDebug     = Cvar_Get( "cl_controllerAimAssistDebug",     "0",    CVAR_ARCHIVE );
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
