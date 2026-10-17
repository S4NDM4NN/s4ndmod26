/*
===========================================================================
Per-weapon stats: slots, counters and the OSP-format printer.  See bg_wstats.h.
===========================================================================
*/

#include "q_shared.h"
#include "bg_public.h"
#include "bg_wstats.h"

#include <string.h>
#include <stdio.h>

static const char *wsNames[WS_MAX] = {
	"Knife", "Luger", "Colt", "MP-40", "Thompson", "Sten", "Sniper", "CrewGun", "Panzer", "Venom", "Flame",
	"Grenade", "Dynamite", "Airstrike", "Artillery", "Syringe", "Healthpacks", "Ammopacks", "World"
};

const char *G_WStats_Name( int slot ) {
	return ( slot >= 0 && slot < WS_MAX ) ? wsNames[slot] : "?";
}

int G_WStats_SlotForMOD( int mod, const char *inflictorAiName ) {
	switch ( mod ) {
	case MOD_KNIFE:
	case MOD_KNIFE2:
	case MOD_KNIFE_STEALTH:
	case MOD_KNIFE_THROWN:
		return WS_KNIFE;
	case MOD_LUGER:             return WS_LUGER;
	case MOD_COLT:              return WS_COLT;
	case MOD_MP40:              return WS_MP40;
	case MOD_THOMPSON:          return WS_THOMPSON;
	case MOD_STEN:              return WS_STEN;
	case MOD_MAUSER:
	case MOD_SNIPERRIFLE:
		return WS_SNIPER;
	case MOD_MACHINEGUN:        return WS_CREWGUN;
	case MOD_PANZERFAUST:
	case MOD_ROCKET:
	case MOD_ROCKET_SPLASH:
		return WS_PANZER;
	case MOD_VENOM:
	case MOD_VENOM_FULL:
		return WS_VENOM;
	case MOD_FLAMETHROWER:      return WS_FLAMETHROWER;
	case MOD_GRENADE:
	case MOD_GRENADE_SPLASH:
	case MOD_GRENADE_LAUNCHER:
	case MOD_GRENADE_PINEAPPLE:
		return WS_GRENADE;
	case MOD_DYNAMITE:
	case MOD_DYNAMITE_SPLASH:
		return WS_DYNAMITE;
	case MOD_AIRSTRIKE:
		// artillery (binoculars) and airstrike (smoke canister) bombs both use MOD_AIRSTRIKE
		return ( inflictorAiName && !strcmp( inflictorAiName, "artillery" ) ) ? WS_ARTILLERY : WS_AIRSTRIKE;
	case MOD_ARTY:              return WS_ARTILLERY;
	case MOD_SYRINGE:
	case MOD_POISON:            // the syringe's poison
		return WS_SYRINGE;

	// nothing player-fired behind these: the map, the environment, \kill
	case MOD_MORTAR:
	case MOD_MORTAR_SPLASH:
	case MOD_WATER:
	case MOD_SLIME:
	case MOD_LAVA:
	case MOD_CRUSH:
	case MOD_FALLING:
	case MOD_TRIGGER_HURT:
	case MOD_TELEFRAG:
	case MOD_EXPLOSIVE:
	case MOD_SUICIDE:
	case MOD_POISONGAS:
	case MOD_BAT:
		return WS_WORLD;

	default:
		return WS_NONE;         // goomba and the like: counted in the totals only
	}
}

int G_WStats_DeathSlot( int mod, const char *inflictorAiName ) {
	int slot = G_WStats_SlotForMOD( mod, inflictorAiName );

	return slot == WS_NONE ? WS_WORLD : slot;
}

int G_WStats_SlotForWeapon( int weapon ) {
	switch ( weapon ) {
	case WP_KNIFE:
	case WP_KNIFE2:
		return WS_KNIFE;
	case WP_LUGER:              return WS_LUGER;
	case WP_COLT:               return WS_COLT;
	case WP_MP40:               return WS_MP40;
	case WP_THOMPSON:           return WS_THOMPSON;
	case WP_STEN:               return WS_STEN;
	case WP_MAUSER:
	case WP_SNIPERRIFLE:
		return WS_SNIPER;
	case WP_PANZERFAUST:        return WS_PANZER;
	case WP_VENOM:
	case WP_VENOM_FULL:
		return WS_VENOM;
	case WP_FLAMETHROWER:       return WS_FLAMETHROWER;
	case WP_GRENADE_LAUNCHER:
	case WP_GRENADE_PINEAPPLE:
		return WS_GRENADE;
	case WP_DYNAMITE:
	case WP_DYNAMITE2:
		return WS_DYNAMITE;
	case WP_SMOKE_GRENADE:      return WS_AIRSTRIKE;
	case WP_ARTY:               return WS_ARTILLERY;
	case WP_MEDIC_SYRINGE:      return WS_SYRINGE;
	case WP_MEDKIT:             return WS_HEALTHPACKS;
	case WP_AMMO:               return WS_AMMOPACKS;
	default:                    return WS_NONE;
	}
}

static int ValidSlot( int slot ) {
	return slot >= 0 && slot < WS_MAX;
}

void G_WStats_Reset( wstat_t *ws ) {
	memset( ws, 0, sizeof( wstat_t ) * WS_MAX );
}

void G_WStats_Attempt( wstat_t *ws, int slot ) {
	if ( ValidSlot( slot ) ) {
		ws[slot].atts++;
	}
}

void G_WStats_RefundAttempt( wstat_t *ws, int slot ) {
	if ( ValidSlot( slot ) && ws[slot].atts > 0 ) {
		ws[slot].atts--;
	}
}

// Blasts and flames can reach several players, so a hit is counted for every enemy reached and these rows can go past 100%
// (ET does the same).  Everything else is one hit per shot at most.
static int Uncapped( int slot ) {
	switch ( slot ) {
	case WS_PANZER:
	case WS_GRENADE:
	case WS_DYNAMITE:
	case WS_AIRSTRIKE:
	case WS_ARTILLERY:
	case WS_FLAMETHROWER:
		return 1;
	}
	return 0;
}

void G_WStats_Hit( wstat_t *ws, int slot ) {
	if ( !ValidSlot( slot ) ) {
		return;
	}
	if ( Uncapped( slot ) || ws[slot].hits < ws[slot].atts ) {
		ws[slot].hits++;
	}
}

void G_WStats_Kill( wstat_t *ws, int slot ) {
	if ( ValidSlot( slot ) ) {
		ws[slot].kills++;
	}
}

void G_WStats_Death( wstat_t *ws, int slot ) {
	if ( ValidSlot( slot ) ) {
		ws[slot].deaths++;
	}
}

void G_WStats_Headshot( wstat_t *ws, int slot ) {
	if ( ValidSlot( slot ) ) {
		ws[slot].headshots++;
	}
}

int G_WStats_Efficiency( int kills, int deaths ) {
	return ( kills + deaths <= 0 ) ? 0 : 100 * kills / ( kills + deaths );
}

// ---- printing ------------------------------------------------------------------------------------------------------

// the table rows, in print order; headshots are shown for the real weapons (knife..flamethrower), not for the rest
#define WS_LAST_TABLE_SLOT WS_SYRINGE
#define WS_LAST_HS_SLOT    WS_FLAMETHROWER

static int RowUsed( const wstat_t *w ) {
	return w->atts || w->hits || w->kills || w->deaths || w->headshots;
}

// append with a size guard (snprintf into the tail)
static void Add( char *out, int size, const char *fmt, ... ) __attribute__( ( format( printf, 3, 4 ) ) );
#include <stdarg.h>
static void Add( char *out, int size, const char *fmt, ... ) {
	int len = (int)strlen( out );
	va_list ap;

	if ( len >= size - 1 ) {
		return;
	}
	va_start( ap, fmt );
	vsnprintf( out + len, size - len, fmt, ap );
	va_end( ap );
}

void G_WStats_FormatPlayer( char *out, int size, const wstatReport_t *r, int color ) {
	// colour prefixes (empty strings when colour is off)
	const char *cW  = color ? "^7" : "";
	const char *cY  = color ? "^3" : "";
	const char *cG  = color ? "^2" : "";
	const char *cR  = color ? "^1" : "";
	const char *cC  = color ? "^5" : "";
	int i, any = 0;
	const wstat_t *ws = r->ws;

	out[0] = 0;
	Add( out, size, "%sAccuracy info for: %s%s %s(%s%d%s Round%s)\n\n", cW, cY, r->name, cW, cG, r->rounds, cW, r->rounds == 1 ? "" : "s" );
	Add( out, size, "%sWeapon     Acrcy Hits/Atts Kll Dth HS\n", cW );
	Add( out, size, "-------------------------------------\n" );

	for ( i = 0; i <= WS_LAST_TABLE_SLOT; i++ ) {
		const wstat_t *w = &ws[i];

		if ( !RowUsed( w ) ) {
			continue;
		}
		any = 1;
		Add( out, size, "%s%-9s: ", cY, G_WStats_Name( i ) );
		if ( w->atts || w->hits ) {
			Add( out, size, "%s%5.1f %s%4d/%-4d ", cW, w->atts ? w->hits * 100.0f / w->atts : 0.0f, cC, w->hits, w->atts );
		} else {
			Add( out, size, "                " );          // 16 blanks where the accuracy and hits/attempts go
		}
		Add( out, size, "%s%3d %s%3d", cG, w->kills, cR, w->deaths );
		if ( i <= WS_LAST_HS_SLOT ) {
			Add( out, size, " %s%2d", cY, w->headshots );
		}
		Add( out, size, "\n" );
	}
	if ( ws[WS_WORLD].deaths ) {
		any = 1;
		Add( out, size, "%s%-9s: ", cY, G_WStats_Name( WS_WORLD ) );
		Add( out, size, "                " );
		Add( out, size, "    %s%3d\n", cR, ws[WS_WORLD].deaths );
	}
	if ( !any ) {
		Add( out, size, "%sNo weapon info available.\n", cY );
	}

	// class specific lines: only for players who threw packs or revived
	if ( ws[WS_HEALTHPACKS].atts || ws[WS_SYRINGE].atts ) {
		Add( out, size, "\n" );
		if ( ws[WS_HEALTHPACKS].atts ) {
			Add( out, size, "%sHealthpacks: %d/%d\n", cW, ws[WS_HEALTHPACKS].hits, ws[WS_HEALTHPACKS].atts );
		}
		Add( out, size, "%sRevivals   : %d\n", cW, ws[WS_SYRINGE].hits );
	}
	if ( ws[WS_AMMOPACKS].atts ) {
		Add( out, size, "\n%sAmmopacks: %d/%d\n", cW, ws[WS_AMMOPACKS].hits, ws[WS_AMMOPACKS].atts );
	}

	Add( out, size, "\n%sDamage Given: %-6d  Bodies Gibbed: %d\n", cW, r->damageGiven, r->gibs );
	Add( out, size, "%sDamage Recvd: %-6d  Team Damage  : %d\n", cW, r->damageReceived, r->teamDamage );
}

// ---- topshots / bottomshots ----------------------------------------------------------------------------------------

// accuracy in 1/100 percent, so rows compare without floats
static int Acc( const wstat_t *w ) {
	return w->atts ? w->hits * 10000 / w->atts : 0;
}

// is a better than b (best) / worse than b (worst)?  more shots break ties.
static int TopBetter( const wstat_t *a, const wstat_t *b, int best ) {
	int x = Acc( a ), y = Acc( b );

	if ( x != y ) {
		return best ? x > y : x < y;
	}
	return a->atts > b->atts;
}

int G_WStats_TopMinAtts( int slot, int base ) {
	int min = base;

	switch ( slot ) {
	case WS_SNIPER:             // few, careful shots
		min = base / 2;
		break;
	case WS_PANZER:             // a handful of rockets or throws per life
	case WS_GRENADE:
	case WS_DYNAMITE:
	case WS_AIRSTRIKE:
	case WS_ARTILLERY:
		min = base * 3 / 10;
		break;
	}
	return min < 1 ? 1 : min;
}

int G_WStats_BuildTop( wstatTopRow_t *rows, int maxRows, const wstat_t *const *players, int n, int best, int minAtts, int slot ) {
	int count = 0, i, s;
	int first = slot < 0 ? 0 : slot, last = slot < 0 ? WS_ARTILLERY : slot;

	if ( slot >= 0 && slot > WS_ARTILLERY ) {
		return 0;
	}
	for ( s = first; s <= last; s++ ) {
		for ( i = 0; i < n; i++ ) {
			const wstat_t *w;
			wstatTopRow_t row;
			int at;

			if ( !players[i] ) {
				continue;
			}
			w = &players[i][s];
			if ( w->atts < G_WStats_TopMinAtts( s, minAtts ) || w->atts <= 0 ) {
				continue;
			}
			row.slot = s;
			row.client = i;
			row.hits = w->hits;
			row.atts = w->atts;
			row.kills = w->kills;
			row.deaths = w->deaths;

			if ( slot < 0 ) {
				// keep only the best/worst player for this weapon
				if ( count > 0 && rows[count - 1].slot == s ) {
					if ( !TopBetter( w, &players[rows[count - 1].client][s], best ) ) {
						continue;
					}
					rows[count - 1] = row;
				} else if ( count < maxRows ) {
					rows[count++] = row;
				}
			} else {
				// insertion sort into the first maxRows
				for ( at = count; at > 0 && TopBetter( w, &players[rows[at - 1].client][s], best ); at-- ) {}
				if ( at >= maxRows ) {
					continue;
				}
				if ( count < maxRows ) {
					count++;
				}
				memmove( &rows[at + 1], &rows[at], ( count - at - 1 ) * sizeof( rows[0] ) );
				rows[at] = row;
			}
		}
	}
	return count;
}

void G_WStats_FormatTop( char *out, int size, const wstatTopRow_t *rows, int numRows, const char *const *names, int best, int color ) {
	const char *cW = color ? "^7" : "", *cY = color ? "^3" : "", *cG = color ? "^2" : "", *cR = color ? "^1" : "", *cC = color ? "^5" : "";
	int i;

	out[0] = 0;
	if ( numRows <= 0 ) {
		Add( out, size, "%sNo qualifying %sshot info available.\n", cY, best ? "top" : "bottom" );
		return;
	}
	Add( out, size, "%s%s Match Accuracies:\n", best ? cG : cR, best ? "BEST" : "WORST" );
	Add( out, size, "%sWP         Acrcy Hits/Atts Kll Dth Player\n", cY );
	for ( i = 0; i < numRows; i++ ) {
		const wstatTopRow_t *r = &rows[i];

		Add( out, size, "%s%-9s: %s%5.1f %s%4d/%-4d %s%3d %s%3d %s%s\n", cY, G_WStats_Name( r->slot ), cW,
			 r->atts ? r->hits * 100.0f / r->atts : 0.0f, cC, r->hits, r->atts, cG, r->kills, cR, r->deaths, cW, names[r->client] );
	}
}
