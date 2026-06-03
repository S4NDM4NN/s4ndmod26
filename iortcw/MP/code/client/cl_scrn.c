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

// cl_scrn.c -- master for refresh, status bar, console, chat, notify, etc

#include "client.h"

qboolean scr_initialized;           // ready to draw

cvar_t      *cl_timegraph;
cvar_t      *cl_debuggraph;
cvar_t      *cl_graphheight;
cvar_t      *cl_graphscale;
cvar_t      *cl_graphshift;

/*
================
SCR_DrawNamedPic

Coordinates are 640*480 virtual values
=================
*/
void SCR_DrawNamedPic( float x, float y, float width, float height, const char *picname ) {
	qhandle_t hShader;

	assert( width != 0 );

	hShader = re.RegisterShader( picname );
	SCR_AdjustFrom640( &x, &y, &width, &height );
	re.DrawStretchPic( x, y, width, height, 0, 0, 1, 1, hShader );
}


/*
================
SCR_AdjustFrom640

Adjusted for resolution and screen aspect ratio
================
*/
void SCR_AdjustFrom640( float *x, float *y, float *w, float *h ) {
	float xscale;
	float yscale;

#if 0
	// adjust for wide screens
	if ( cls.glconfig.vidWidth * 480 > cls.glconfig.vidHeight * 640 ) {
		*x += 0.5 * ( cls.glconfig.vidWidth - ( cls.glconfig.vidHeight * 640 / 480 ) );
	}
#endif

	// scale for screen sizes
	xscale = cls.glconfig.vidWidth / 640.0;
	yscale = cls.glconfig.vidHeight / 480.0;
	if ( x ) {
		*x *= xscale;
	}
	if ( y ) {
		*y *= yscale;
	}
	if ( w ) {
		*w *= xscale;
	}
	if ( h ) {
		*h *= yscale;
	}
}

/*
================
SCR_FillRect

Coordinates are 640*480 virtual values
=================
*/
void SCR_FillRect( float x, float y, float width, float height, const float *color ) {
	re.SetColor( color );

	SCR_AdjustFrom640( &x, &y, &width, &height );
	re.DrawStretchPic( x, y, width, height, 0, 0, 0, 0, cls.whiteShader );

	re.SetColor( NULL );
}


/*
================
SCR_DrawPic

Coordinates are 640*480 virtual values
=================
*/
void SCR_DrawPic( float x, float y, float width, float height, qhandle_t hShader ) {
	SCR_AdjustFrom640( &x, &y, &width, &height );
	re.DrawStretchPic( x, y, width, height, 0, 0, 1, 1, hShader );
}



/*
** SCR_DrawChar
** chars are drawn at 640*480 virtual screen size
*/
static void SCR_DrawChar( int x, int y, float size, int ch ) {
	int row, col;
	float frow, fcol;
	float ax, ay, aw, ah;

	ch &= 255;

	if ( ch == ' ' ) {
		return;
	}

	if ( y < -size ) {
		return;
	}

	ax = x;
	ay = y;
	aw = size;
	ah = size;
	SCR_AdjustFrom640( &ax, &ay, &aw, &ah );

	row = ch >> 4;
	col = ch & 15;

	frow = row * 0.0625;
	fcol = col * 0.0625;
	size = 0.0625;

	re.DrawStretchPic( ax, ay, aw, ah,
					   fcol, frow,
					   fcol + size, frow + size,
					   cls.charSetShader );
}

/*
** SCR_DrawSmallChar
** small chars are drawn at native screen resolution
*/
void SCR_DrawSmallChar( int x, int y, int ch ) {
	int row, col;
	float frow, fcol;
	float size;

	ch &= 255;

	if ( ch == ' ' ) {
		return;
	}

	if ( y < -SMALLCHAR_HEIGHT ) {
		return;
	}

	row = ch >> 4;
	col = ch & 15;

	frow = row * 0.0625;
	fcol = col * 0.0625;
	size = 0.0625;

	re.DrawStretchPic( x, y, SMALLCHAR_WIDTH, SMALLCHAR_HEIGHT,
					   fcol, frow,
					   fcol + size, frow + size,
					   cls.charSetShader );
}


/*
==================
SCR_DrawBigString[Color]

Draws a multi-colored string with a drop shadow, optionally forcing
to a fixed color.

Coordinates are at 640 by 480 virtual resolution
==================
*/
void SCR_DrawStringExt( int x, int y, float size, const char *string, float *setColor, qboolean forceColor,
		qboolean noColorEscape ) {
	vec4_t color;
	const char  *s;
	int xx;

	// draw the drop shadow
	color[0] = color[1] = color[2] = 0;
	color[3] = setColor[3];
	re.SetColor( color );
	s = string;
	xx = x;
	while ( *s ) {
		if ( !noColorEscape && Q_IsColorString( s ) ) {
			s += 2;
			continue;
		}
		SCR_DrawChar( xx + 2, y + 2, size, *s );
		xx += size;
		s++;
	}


	// draw the colored text
	s = string;
	xx = x;
	re.SetColor( setColor );
	while ( *s ) {
		if ( Q_IsColorString( s ) ) {
			if ( !forceColor ) {
				memcpy( color, g_color_table[ColorIndex( *( s + 1 ) )], sizeof( color ) );
				color[3] = setColor[3];
				re.SetColor( color );
			}
			if ( !noColorEscape ) {
				s += 2;
				continue;
			}
		}
		SCR_DrawChar( xx, y, size, *s );
		xx += size;
		s++;
	}
	re.SetColor( NULL );
}


void SCR_DrawBigString( int x, int y, const char *s, float alpha, qboolean noColorEscape ) {
	float color[4];

	color[0] = color[1] = color[2] = 1.0;
	color[3] = alpha;
	SCR_DrawStringExt( x, y, BIGCHAR_WIDTH, s, color, qfalse, noColorEscape );
}

void SCR_DrawBigStringColor( int x, int y, const char *s, vec4_t color, qboolean noColorEscape ) {
	SCR_DrawStringExt( x, y, BIGCHAR_WIDTH, s, color, qtrue, noColorEscape );
}


/*
==================
SCR_DrawSmallString[Color]

Draws a multi-colored string with a drop shadow, optionally forcing
to a fixed color.
==================
*/
void SCR_DrawSmallStringExt( int x, int y, const char *string, float *setColor, qboolean forceColor,
		qboolean noColorEscape ) {
	vec4_t color;
	const char  *s;
	int xx;

	// draw the colored text
	s = string;
	xx = x;
	re.SetColor( setColor );
	while ( *s ) {
		if ( Q_IsColorString( s ) ) {
			if ( !forceColor ) {
				memcpy( color, g_color_table[ColorIndex( *( s + 1 ) )], sizeof( color ) );
				color[3] = setColor[3];
				re.SetColor( color );
			}
			if ( !noColorEscape ) {
				s += 2;
				continue;
			}
		}
		SCR_DrawSmallChar( xx, y, *s );
		xx += SMALLCHAR_WIDTH;
		s++;
	}
	re.SetColor( NULL );
}



/*
** SCR_Strlen -- skips color escape codes
*/
static int SCR_Strlen( const char *str ) {
	const char *s = str;
	int count = 0;

	while ( *s ) {
		if ( Q_IsColorString( s ) ) {
			s += 2;
		} else {
			count++;
			s++;
		}
	}

	return count;
}

/*
** SCR_GetBigStringWidth
*/
int SCR_GetBigStringWidth( const char *str ) {
	return SCR_Strlen( str ) * BIGCHAR_WIDTH;
}


//===============================================================================

/*
=================
SCR_DrawDemoRecording
=================
*/
void SCR_DrawDemoRecording( void ) {
	char string[1024];
	int pos;

	if ( !clc.demorecording ) {
		return;
	}

	pos = FS_FTell( clc.demofile );
	sprintf( string, "RECORDING %s: %ik", clc.demoName, pos / 1024 );

	SCR_DrawStringExt( 320 - strlen( string ) * 4, 20, 8, string, g_color_table[7], qtrue, qfalse );
}

#ifdef USE_VOIP
/*
=================
SCR_DrawVoipMeter
=================
*/
void SCR_DrawVoipMeter( void ) {
	char	buffer[16];
	char	string[256];
	int limit, i;

	if (!cl_voipShowMeter->integer)
		return;  // player doesn't want to show meter at all.
	else if (!cl_voipSend->integer)
		return;  // not recording at the moment.
	else if (clc.state != CA_ACTIVE)
		return;  // not connected to a server.
	else if (!clc.voipEnabled)
		return;  // server doesn't support VoIP.
	else if (clc.demoplaying)
		return;  // playing back a demo.
	else if (!cl_voip->integer)
		return;  // client has VoIP support disabled.

	limit = (int) (clc.voipPower * 10.0f);
	if (limit > 10)
		limit = 10;

	for (i = 0; i < limit; i++)
		buffer[i] = '*';
	while (i < 10)
		buffer[i++] = ' ';
	buffer[i] = '\0';

	sprintf( string, "VoIP: [%s]", buffer );
	SCR_DrawStringExt( 320 - strlen( string ) * 4, 10, 8, string, g_color_table[7], qtrue, qfalse );
}
#endif

/*
=================
SCR_ShowPing
=================
*/
void SCR_ShowPing( void ) {
	char	string[11];
	int	ping, w, x;

	if (!cl_showPing->integer)
		return;

	if ( Cvar_VariableIntegerValue( "ui_limboMode" ) )
		return;

	ping = cl.snap.ping;

	Com_sprintf( string, sizeof( string ), "ping: %i", ping );

	w = strlen( string ) * TINYCHAR_WIDTH;
	x = 320 - ( w * 0.5 );

	if ( ping < 999 )
		SCR_DrawStringExt( x, 386, TINYCHAR_HEIGHT, string, g_color_table[7], qtrue, qfalse );
	else
		return;
}

/*
===============================================================================

DEBUG GRAPH

===============================================================================
*/

static int current;
static float values[1024];

/*
==============
SCR_DebugGraph
==============
*/
void SCR_DebugGraph( float value ) {
	values[current] = value;
	current = (current + 1) % ARRAY_LEN(values);
}

/*
==============
SCR_DrawDebugGraph
==============
*/
void SCR_DrawDebugGraph( void ) {
	int a, x, y, w, i, h;
	float v;

	//
	// draw the graph
	//
	w = cls.glconfig.vidWidth;
	x = 0;
	y = cls.glconfig.vidHeight;
	re.SetColor( g_color_table[0] );
	re.DrawStretchPic( x, y - cl_graphheight->integer,
					   w, cl_graphheight->integer, 0, 0, 0, 0, cls.whiteShader );
	re.SetColor( NULL );

	for ( a = 0 ; a < w ; a++ )
	{
		i = (ARRAY_LEN(values)+current-1-(a % ARRAY_LEN(values))) % ARRAY_LEN(values);
		v = values[i];
		v = v * cl_graphscale->integer + cl_graphshift->integer;

		if ( v < 0 ) {
			v += cl_graphheight->integer * ( 1 + (int)( -v / cl_graphheight->integer ) );
		}
		h = (int)v % cl_graphheight->integer;
		re.DrawStretchPic( x + w - 1 - a, y - h, 1, h, 0, 0, 0, 0, cls.whiteShader );
	}
}

//=============================================================================

/*
==================
SCR_Init
==================
*/
void SCR_Init( void ) {
	cl_timegraph = Cvar_Get( "timegraph", "0", CVAR_CHEAT );
	cl_debuggraph = Cvar_Get( "debuggraph", "0", CVAR_CHEAT );
	cl_graphheight = Cvar_Get( "graphheight", "32", CVAR_CHEAT );
	cl_graphscale = Cvar_Get( "graphscale", "1", CVAR_CHEAT );
	cl_graphshift = Cvar_Get( "graphshift", "0", CVAR_CHEAT );

	scr_initialized = qtrue;
}


/*
==============
SCR_DrawLine2D

Draws a 2-px-wide line in 640x480 virtual coordinates by stepping 2x2
squares along the path.
==============
*/
static void SCR_DrawLine2D( float x1, float y1, float x2, float y2, const float *color ) {
	float dx = x2 - x1;
	float dy = y2 - y1;
	float len = sqrt( dx * dx + dy * dy );
	int i, steps;

	if ( len < 1.0f ) {
		return;
	}
	steps = (int)len;
	for ( i = 0; i <= steps; i++ ) {
		float t = (float)i / steps;
		SCR_FillRect( x1 + dx * t - 1.0f, y1 + dy * t - 1.0f, 2.0f, 2.0f, color );
	}
}

/*
================
SCR_DrawCircle2D

Approximates a circle in 640x480 virtual coordinates using `steps` dots.
radius is in virtual pixels.  Drawn dashed (every other segment) so it is
clearly distinct from solid game UI elements.
================
*/
static void SCR_DrawCircle2D( float cx, float cy, float radius, int steps,
	const float *color ) {
	int i;
	for ( i = 0; i < steps; i++ ) {
		float angle;
		float px;
		float py;
		if ( i & 1 ) {
			continue;	/* dashed — skip every other dot */
		}
		angle = ( (float)i / steps ) * 2.0f * M_PI;
		px    = cx + radius * cos( angle ) - 1.0f;
		py    = cy + radius * sin( angle ) - 1.0f;
		SCR_FillRect( px, py, 2.0f, 2.0f, color );
	}
}

/*
=======================
SCR_WorldToVirtual

Projects a world-space point into 640×480 virtual screen coordinates using
the current snapshot's eye origin and view angles.  Returns qfalse if the
point is behind the camera.
=======================
*/
static qboolean SCR_WorldToVirtual( const vec3_t pt, float *sx, float *sy ) {
	vec3_t eye, diff, fwd, right, up;
	float  fwd_proj, rt_proj, up_proj, fov_x, tanX, tanY;

	if ( clc.state != CA_ACTIVE || !cl.snap.valid ) {
		return qfalse;
	}

	/*
	 * Use the same eye origin as the aim assist scan so the projection is
	 * consistent with the snapshot entities.
	 */
	VectorCopy( cl.snap.ps.origin, eye );
	eye[2] += cl.snap.ps.viewheight;

	VectorSubtract( pt, eye, diff );

	/*
	 * Use the snapshot's authoritative view angles, NOT cl.viewangles.
	 *
	 * cl.viewangles accumulates client joystick/mouse input every frame.
	 * In follow-spectator mode the rendered 3D scene uses the followed
	 * player's angles (from cl.snap.ps.viewangles), so cl.viewangles
	 * diverges from the actual camera direction and the boxes drift.
	 * cl.snap.ps.viewangles is consistent with the snapshot entity
	 * positions and with what cgame uses as its base render direction
	 * in all modes (normal play, follow-spectator, dead, etc.).
	 */
	AngleVectors( cl.snap.ps.viewangles, fwd, right, up );
	fwd_proj = DotProduct( diff, fwd );
	if ( fwd_proj < 1.0f ) {
		return qfalse;   /* behind camera */
	}

	rt_proj = DotProduct( diff, right );
	up_proj = DotProduct( diff, up );

	fov_x = Cvar_VariableValue( "cg_fov" );
	if ( fov_x < 10.0f ) {
		fov_x = 90.0f;
	}

	/*
	 * The 640×480 virtual canvas has a fixed 4:3 aspect ratio.
	 * tanX = tan(hfov/2) maps to the 320-px half-width.
	 * tanY = tanX * (480/640) — NOT tan((fov_x*0.75)/2): tan is nonlinear.
	 */
	tanX = (float)tan( DEG2RAD( fov_x * 0.5f ) );
	tanY = tanX * ( 480.0f / 640.0f );

	*sx = 320.0f + ( rt_proj / fwd_proj ) * ( 320.0f / tanX );
	*sy = 240.0f - ( up_proj / fwd_proj ) * ( 240.0f / tanY );
	return qtrue;
}

/*
=======================
SCR_DrawPlayerBoxes

Draws a colored 2D bounding box around every visible player in the current
snapshot when cl_controllerAimAssistDebug >= 1.  Team colour is determined
by the same CL_GetClientTeam() call used by the aim assist, so the boxes
directly confirm whether team detection is correct:

  RED   — TEAM_RED  (Axis)
  BLUE  — TEAM_BLUE (Allies)
  WHITE — unknown / spectator (config string not yet received)

The local player's own entity is never boxed.  Dead or hidden (EF_NODRAW)
entities are also skipped, matching aim assist eligibility exactly.
=======================
*/
void SCR_DrawPlayerBoxes( void ) {
	int      i;
	int      selfClient;
	int      debugLevel;
	qboolean doDebugPrint;

	debugLevel = Cvar_VariableIntegerValue( "cl_controllerAimAssistDebug" );
	if ( !debugLevel ) {
		return;
	}
	if ( clc.state != CA_ACTIVE || !cl.snap.valid ) {
		return;
	}

	selfClient   = cl.snap.ps.clientNum;
	doDebugPrint = ( debugLevel >= 2 );

	for ( i = 0; i < cl.snap.numEntities; i++ ) {
		entityState_t *ent;
		int   targetTeam;
		float color[4];
		float top, bot, hw;
		float minX, minY, maxX, maxY;
		qboolean anyVisible;
		int   c;
		vec3_t base;
		/* 8 AABB corners relative to entity origin */
		vec3_t corners[8];

		ent = &cl.parseEntities[
			( cl.snap.parseEntitiesNum + i ) & ( MAX_PARSE_ENTITIES - 1 )];

		if ( ent->eType != ET_PLAYER ) {
			continue;
		}
		if ( ent->number == selfClient || ent->clientNum == selfClient ) {
			continue;
		}
		if ( ent->eFlags & ( EF_DEAD | EF_NODRAW ) ) {
			continue;
		}

		targetTeam = CL_GetClientTeam( ent->clientNum );

		/* Colour by team — matches aim assist enemy logic */
		if ( targetTeam == TEAM_RED ) {
			color[0] = 1.0f; color[1] = 0.2f; color[2] = 0.2f; color[3] = 0.9f;
		} else if ( targetTeam == TEAM_BLUE ) {
			color[0] = 0.2f; color[1] = 0.5f; color[2] = 1.0f; color[3] = 0.9f;
		} else {
			/* Unknown / spectator — white so it's obvious */
			color[0] = 1.0f; color[1] = 1.0f; color[2] = 1.0f; color[3] = 0.6f;
		}

		/*
		 * Standard RTCW player AABB (matches cg_predict.c solid decoding):
		 *   hw  = ±15 in X/Y
		 *   bot = -24  (feet below origin)
		 *   top = +40 standing, +24 crouched
		 */
		hw  = 15.0f;
		bot = -24.0f;
		top = ent->animMovetype ? 24.0f : 40.0f;

		VectorCopy( ent->pos.trBase, base );

		if ( doDebugPrint ) {
			vec3_t eye;
			float  dotF, dotR, dotU;
			vec3_t fwd2, rt2, up2, diff2;
			VectorCopy( cl.snap.ps.origin, eye );
			eye[2] += cl.snap.ps.viewheight;
			AngleVectors( cl.snap.ps.viewangles, fwd2, rt2, up2 );
			VectorSubtract( base, eye, diff2 );
			dotF = DotProduct( diff2, fwd2 );
			dotR = DotProduct( diff2, rt2 );
			dotU = DotProduct( diff2, up2 );
			Com_Printf( "[BOX] client=%d team=%d base=(%.0f,%.0f,%.0f) eye=(%.0f,%.0f,%.0f) fwd=%.1f rt=%.1f up=%.1f\n",
				ent->clientNum, targetTeam,
				base[0], base[1], base[2],
				eye[0], eye[1], eye[2],
				dotF, dotR, dotU );
		}

		corners[0][0] = base[0] - hw; corners[0][1] = base[1] - hw; corners[0][2] = base[2] + bot;
		corners[1][0] = base[0] + hw; corners[1][1] = base[1] - hw; corners[1][2] = base[2] + bot;
		corners[2][0] = base[0] - hw; corners[2][1] = base[1] + hw; corners[2][2] = base[2] + bot;
		corners[3][0] = base[0] + hw; corners[3][1] = base[1] + hw; corners[3][2] = base[2] + bot;
		corners[4][0] = base[0] - hw; corners[4][1] = base[1] - hw; corners[4][2] = base[2] + top;
		corners[5][0] = base[0] + hw; corners[5][1] = base[1] - hw; corners[5][2] = base[2] + top;
		corners[6][0] = base[0] - hw; corners[6][1] = base[1] + hw; corners[6][2] = base[2] + top;
		corners[7][0] = base[0] + hw; corners[7][1] = base[1] + hw; corners[7][2] = base[2] + top;

		minX = 640.0f; minY = 480.0f; maxX = 0.0f; maxY = 0.0f;
		anyVisible = qfalse;

		for ( c = 0; c < 8; c++ ) {
			float sx, sy;
			if ( !SCR_WorldToVirtual( corners[c], &sx, &sy ) ) {
				continue;
			}
			anyVisible = qtrue;
			if ( sx < minX ) { minX = sx; }
			if ( sx > maxX ) { maxX = sx; }
			if ( sy < minY ) { minY = sy; }
			if ( sy > maxY ) { maxY = sy; }
		}

		/* Always draw a small center dot at the chest/aim point even
		 * if the full bounding box is partially off-screen.
		 * This is a simpler projection and easier to verify visually. */
		{
			vec3_t aimPt;
			float  dotX, dotY;
			VectorCopy( base, aimPt );
			aimPt[2] += ent->animMovetype ? 0.0f : 8.0f;
			if ( SCR_WorldToVirtual( aimPt, &dotX, &dotY ) ) {
				dotX = Com_Clamp( 2.0f, 638.0f, dotX );
				dotY = Com_Clamp( 2.0f, 478.0f, dotY );
				SCR_FillRect( dotX - 3.0f, dotY - 3.0f, 6.0f, 6.0f, color );
			}
		}

		if ( !anyVisible || maxX <= minX || maxY <= minY ) {
			continue;
		}

		/* Clamp to virtual screen bounds */
		minX = Com_Clamp( 0.0f, 639.0f, minX );
		minY = Com_Clamp( 0.0f, 479.0f, minY );
		maxX = Com_Clamp( 1.0f, 640.0f, maxX );
		maxY = Com_Clamp( 1.0f, 480.0f, maxY );

		/* Draw two-pixel-wide edges for visibility */
		SCR_FillRect( minX,     minY,     maxX - minX, 2.0f,        color );  /* top    */
		SCR_FillRect( minX,     maxY - 2, maxX - minX, 2.0f,        color );  /* bottom */
		SCR_FillRect( minX,     minY,     2.0f,        maxY - minY, color );  /* left   */
		SCR_FillRect( maxX - 2, minY,     2.0f,        maxY - minY, color );  /* right  */

		/*
		 * Draw client name above the box.
		 *
		 * IMPORTANT: SCR_DrawSmallChar (called by SCR_DrawSmallStringExt) draws
		 * at NATIVE SCREEN PIXEL coordinates, not virtual 640×480 coords.
		 * Convert virtual (minX, minY) → pixels before passing to it.
		 */
		{
			char       label[MAX_NAME_LENGTH + 8];
			const char *name = NULL;
			int        labelOffset;
			vec4_t     nameColor;
			int        px, py;

			labelOffset = cl.gameState.stringOffsets[CS_PLAYERS + ent->clientNum];
			if ( labelOffset ) {
				name = Info_ValueForKey( cl.gameState.stringData + labelOffset, "n" );
			}

			if ( name && *name ) {
				Com_sprintf( label, sizeof( label ), "%s [%d]", name, ent->clientNum );
			} else {
				Com_sprintf( label, sizeof( label ), "#%d", ent->clientNum );
			}

			/* Strip color escapes so the fixed-width SMALLCHAR draw is positioned correctly */
			Q_CleanStr( label );

			nameColor[0] = color[0];
			nameColor[1] = color[1];
			nameColor[2] = color[2];
			nameColor[3] = 1.0f;

			/* Virtual → pixel: same scale factor as SCR_AdjustFrom640 */
			px = (int)( minX * cls.glconfig.vidWidth  / 640.0f );
			py = (int)( minY * cls.glconfig.vidHeight / 480.0f ) - SMALLCHAR_HEIGHT;

			if ( py < 0 ) {
				py = 0;
			}

			SCR_DrawSmallStringExt( px, py, label, nameColor, qtrue, qtrue );
		}
	}
}

/*
=======================
SCR_DrawAimAssistOverlay

Two dashed circles show the outer (slowdown) and inner (magnetism) cone
boundaries at all times while cl_controllerAimAssistDebug is non-zero, so
the player can see exactly when a target should trigger.  A directional
arrow appears when a target is actively being tracked.

  SCANNING (white)  — system armed, no target inside either cone yet
  SLOWDOWN (yellow) — target entered the outer friction zone
  TRACKING (green)  — target in inner zone; arrow shows pull direction
=======================
*/
void SCR_DrawAimAssistOverlay( void ) {
	qboolean active;
	qboolean scanning;
	qboolean hasTargetPoint;
	qboolean haveArrow;
	vec3_t targetPoint;
	float yawDelta;
	float pitchDelta;
	float outerAngle;
	float innerAngle;
	float outerBlend;
	float innerBlend;
	int nTotal;
	int nPlayer;
	int nEnemy;
	int nLOS;
	float cx;
	float cy;
	float outerRadius;
	float innerRadius;
	float arrowStartX;
	float arrowStartY;
	float arrowEndX;
	float arrowEndY;
	float dx;
	float dy;
	float len;
	float perpX;
	float perpY;
	float outerColor[4];
	float innerColor[4];
	float arrowColor[4];
	char label[48];
	int labelX;

	if ( !Cvar_VariableIntegerValue( "cl_controllerAimAssistDebug" ) ) {
		return;
	}

	CL_GetAimAssistVisState( &active, &scanning,
		&hasTargetPoint, targetPoint,
		&yawDelta, &pitchDelta,
		&outerAngle, &innerAngle,
		&outerBlend, &innerBlend,
		&nTotal, &nPlayer, &nEnemy, &nLOS );

	cx = 320.0f;
	cy = 240.0f;

	/*
	 * Convert cone half-angles to screen-space radii.
	 * For FOV=90, tan(45°)=1, so the 320-px half-width maps directly:
	 * radius_px = tan(coneAngle_rad) * 320.
	 */
	outerRadius = tanf( DEG2RAD( outerAngle ) ) * 320.0f;
	innerRadius = tanf( DEG2RAD( innerAngle ) ) * 320.0f;

	/* Outer cone — yellow, dims when system is idle */
	if ( active ) {
		outerColor[0] = 1.0f; outerColor[1] = 0.8f;
		outerColor[2] = 0.0f; outerColor[3] = 0.5f + outerBlend * 0.4f;
	} else if ( scanning ) {
		outerColor[0] = 0.9f; outerColor[1] = 0.9f;
		outerColor[2] = 0.2f; outerColor[3] = 0.5f;
	} else {
		outerColor[0] = 0.5f; outerColor[1] = 0.5f;
		outerColor[2] = 0.5f; outerColor[3] = 0.3f;
	}

	/* Inner cone — green, dims when system is idle */
	if ( active && innerBlend > 0.0f ) {
		innerColor[0] = 0.0f; innerColor[1] = 1.0f;
		innerColor[2] = 0.0f; innerColor[3] = 0.4f + innerBlend * 0.6f;
	} else if ( active || scanning ) {
		innerColor[0] = 0.0f; innerColor[1] = 0.8f;
		innerColor[2] = 0.2f; innerColor[3] = 0.35f;
	} else {
		innerColor[0] = 0.3f; innerColor[1] = 0.5f;
		innerColor[2] = 0.3f; innerColor[3] = 0.25f;
	}

	SCR_DrawCircle2D( cx, cy, outerRadius, 120, outerColor );
	SCR_DrawCircle2D( cx, cy, innerRadius,  60, innerColor );

	haveArrow = qfalse;

	/*
	 * Directional arrow — only when target is inside the cone.
	 * Anchor this to the projected aim-assist target point itself instead of
	 * reconstructing direction from yaw/pitch deltas. The assist selection is
	 * made from a concrete world-space aim point, so using that same point for
	 * the overlay keeps the arrow aligned with what the scan actually chose.
	 */
	if ( active ) {
		qboolean projectedTarget = qfalse;
		float targetX;
		float targetY;
		float dirX;
		float dirY;

		if ( hasTargetPoint && SCR_WorldToVirtual( targetPoint, &targetX, &targetY ) ) {
			projectedTarget = qtrue;
			dx  = targetX - cx;
			dy  = targetY - cy;
			len = sqrt( dx * dx + dy * dy );
		} else {
			/* Fallback for frames where projection is unavailable. */
			dx  = yawDelta;
			dy  = pitchDelta;
			len = sqrt( dx * dx + dy * dy );
		}

		if ( len > 0.1f ) {
			dirX = dx / len;
			dirY = dy / len;
			perpX = -dirY;
			perpY =  dirX;
			arrowStartX = cx;
			arrowStartY = cy;
			arrowEndX   = cx + dirX * 36.0f;
			arrowEndY   = cy + dirY * 36.0f;
			haveArrow   = qtrue;

			if ( projectedTarget ) {
				SCR_FillRect( targetX - 3.0f, targetY - 3.0f, 6.0f, 6.0f, innerColor );
			}

			if ( innerBlend > 0.0f ) {
				arrowColor[0] = 0.0f; arrowColor[1] = 1.0f;
				arrowColor[2] = 0.0f; arrowColor[3] = 0.6f + innerBlend * 0.4f;
			} else {
				arrowColor[0] = 1.0f; arrowColor[1] = 0.8f;
				arrowColor[2] = 0.0f; arrowColor[3] = 0.5f + outerBlend * 0.4f;
			}
		}
	}

	if ( haveArrow ) {
		SCR_DrawLine2D( arrowStartX, arrowStartY, arrowEndX, arrowEndY, arrowColor );
		SCR_DrawLine2D( arrowEndX, arrowEndY,
			arrowEndX - ( dx / len ) * 8.0f + perpX * 8.0f,
			arrowEndY - ( dy / len ) * 8.0f + perpY * 8.0f, arrowColor );
		SCR_DrawLine2D( arrowEndX, arrowEndY,
			arrowEndX - ( dx / len ) * 8.0f - perpX * 8.0f,
			arrowEndY - ( dy / len ) * 8.0f - perpY * 8.0f, arrowColor );
	}

	/* Status label below the crosshair */
	if ( active && innerBlend > 0.0f ) {
		Com_sprintf( label, sizeof( label ), "TRACKING" );
		arrowColor[0] = 0.0f; arrowColor[1] = 1.0f; arrowColor[2] = 0.0f; arrowColor[3] = 1.0f;
	} else if ( active ) {
		Com_sprintf( label, sizeof( label ), "SLOWDOWN" );
		arrowColor[0] = 1.0f; arrowColor[1] = 0.8f; arrowColor[2] = 0.0f; arrowColor[3] = 1.0f;
	} else if ( scanning ) {
		/*
		 * Show the entity pipeline counts so it's obvious where detection
		 * is failing without having to open the console:
		 *   ents  = total entities in snapshot
		 *   plr   = ET_PLAYER entities (excl. self)
		 *   enm   = enemies (passed team filter)
		 *   los   = enemies with line of sight
		 * If los>0 but still scanning, enemies are outside the cone circles.
		 */
		Com_sprintf( label, sizeof( label ), "SCANNING ents:%d plr:%d enm:%d",
			nTotal, nPlayer, nEnemy );
		arrowColor[0] = 0.8f; arrowColor[1] = 0.8f; arrowColor[2] = 0.8f; arrowColor[3] = 0.9f;
	} else {
		/* idle — circles still drawn, no label */
		return;
	}

	labelX = (int)cx - (int)( strlen( label ) ) * 4;
	SCR_DrawStringExt( labelX, (int)cy + 16, 8, label, arrowColor, qtrue, qfalse );
}


//=======================================================

/*
==================
SCR_DrawScreenField

This will be called twice if rendering in stereo mode
==================
*/
void SCR_DrawScreenField( stereoFrame_t stereoFrame ) {
	qboolean uiFullscreen;

	re.BeginFrame( stereoFrame );

	uiFullscreen = (uivm && VM_Call( uivm, UI_IS_FULLSCREEN ));

	// wide aspect ratio screens need to have the sides cleared
	// unless they are displaying game renderings
	if ( uiFullscreen || clc.state < CA_LOADING ) {
		if ( cls.glconfig.vidWidth * 480 > cls.glconfig.vidHeight * 640 ) {
			re.SetColor( g_color_table[0] );
			re.DrawStretchPic( 0, 0, cls.glconfig.vidWidth, cls.glconfig.vidHeight, 0, 0, 0, 0, cls.whiteShader );
			re.SetColor( NULL );
		}
	}

	// if the menu is going to cover the entire screen, we
	// don't need to render anything under it
	if ( uivm && !uiFullscreen ) {
		switch( clc.state ) {
		default:
			Com_Error( ERR_FATAL, "SCR_DrawScreenField: bad clc.state" );
			break;
		case CA_CINEMATIC:
			SCR_DrawCinematic();
			break;
		case CA_DISCONNECTED:
			// force menu up
			S_StopAllSounds();
			VM_Call( uivm, UI_SET_ACTIVE_MENU, UIMENU_MAIN );
			break;
		case CA_CONNECTING:
		case CA_CHALLENGING:
		case CA_CONNECTED:
			// connecting clients will only show the connection dialog
			// refresh to update the time
			VM_Call( uivm, UI_REFRESH, cls.realtime );
			VM_Call( uivm, UI_DRAW_CONNECT_SCREEN, qfalse );
			break;
//			// Ridah, if the cgame is valid, fall through to there
//			if (!cls.cgameStarted || !com_sv_running->integer) {
//				// connecting clients will only show the connection dialog
//				VM_Call( uivm, UI_DRAW_CONNECT_SCREEN, qfalse );
//				break;
//			}
		case CA_LOADING:
		case CA_PRIMED:
			// draw the game information screen and loading progress
			CL_CGameRendering( stereoFrame );

			// also draw the connection information, so it doesn't
			// flash away too briefly on local or lan games
			//if (!com_sv_running->value || Cvar_VariableIntegerValue("sv_cheats"))	// Ridah, don't draw useless text if not in dev mode
			VM_Call( uivm, UI_REFRESH, cls.realtime );
			VM_Call( uivm, UI_DRAW_CONNECT_SCREEN, qtrue );
			break;
		case CA_ACTIVE:
			// always supply STEREO_CENTER as vieworg offset is now done by the engine.
			CL_CGameRendering( stereoFrame );
			SCR_ShowPing();
			SCR_DrawDemoRecording();
#ifdef USE_VOIP
			SCR_DrawVoipMeter();
#endif
			SCR_DrawPlayerBoxes();
			SCR_DrawAimAssistOverlay();
			break;
		}
	}

	// the menu draws next
	if ( Key_GetCatcher( ) & KEYCATCH_UI && uivm ) {
		VM_Call( uivm, UI_REFRESH, cls.realtime );
	}

	// console draws next
	Con_DrawConsole();

	// debug graph can be drawn on top of anything
	if ( cl_debuggraph->integer || cl_timegraph->integer || cl_debugMove->integer ) {
		SCR_DrawDebugGraph();
	}
}

/*
==================
SCR_UpdateScreen

This is called every frame, and can also be called explicitly to flush
text to the screen.
==================
*/
void SCR_UpdateScreen( void ) {
	static int recursive;

	if ( !scr_initialized ) {
		return;             // not initialized yet
	}

	if ( ++recursive > 2 ) {
		Com_Error( ERR_FATAL, "SCR_UpdateScreen: recursively called" );
	}
	recursive = 1;

	// If there is no VM, there are also no rendering commands issued. Stop the renderer in
	// that case.
	if( uivm || com_dedicated->integer )
	{
		// XXX
		int in_anaglyphMode = Cvar_VariableIntegerValue("r_anaglyphMode");
		// if running in stereo, we need to draw the frame twice
		if ( cls.glconfig.stereoEnabled || in_anaglyphMode) {
			SCR_DrawScreenField( STEREO_LEFT );
			SCR_DrawScreenField( STEREO_RIGHT );
		} else {
			SCR_DrawScreenField( STEREO_CENTER );
		}

		if ( com_speeds->integer ) {
			re.EndFrame( &time_frontend, &time_backend );
		} else {
			re.EndFrame( NULL, NULL );
		}
	}

	recursive = 0;
}
