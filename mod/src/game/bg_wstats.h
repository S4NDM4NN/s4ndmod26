/*
===========================================================================
Per-weapon stats (OSP style)

For every weapon a player uses: attempts (shots), hits, kills, deaths (times killed by it) and headshots.  The slots and
the output follow OSP's stats (osp-wolf-StatsInfo.txt) for the weapons that exist in multiplayer.  Packs and the syringe
are rows too: for the health and ammo packs hits = used and attempts = dropped; for the syringe hits = revives.  Deaths with
no weapon behind them (map mortar, falling, water, crush, \kill, ...) go in the World row.

This header has no dependency on the rest of the game module so the mapping and the printer can be tested on their own
(see tools/local/test_wstats.c and test_wstats.sh).
===========================================================================
*/

#ifndef BG_WSTATS_H
#define BG_WSTATS_H

typedef struct {
	int atts;       // shots fired / items thrown or dropped
	int hits;       // shots that hit an enemy; packs: used; syringe: revives
	int kills;
	int deaths;     // times killed by this weapon
	int headshots;
} wstat_t;

// printing order = OSP's order, without the mortar (map-fired, see WS_WORLD)
typedef enum {
	WS_NONE = -1,
	WS_KNIFE,
	WS_LUGER,
	WS_COLT,
	WS_MP40,
	WS_THOMPSON,
	WS_STEN,
	WS_SNIPER,          // Mauser and its scoped mode
	WS_CREWGUN,         // the mounted MG42
	WS_PANZER,
	WS_VENOM,
	WS_FLAMETHROWER,
	WS_GRENADE,
	WS_DYNAMITE,
	WS_AIRSTRIKE,
	WS_ARTILLERY,
	WS_SYRINGE,
	WS_HEALTHPACKS,     // not a table row: printed as "Healthpacks: used/dropped"
	WS_AMMOPACKS,       // likewise "Ammopacks: used/dropped"
	WS_WORLD,           // deaths only
	WS_MAX
} wstatSlot_t;

// Slot for a means of death (MOD_*), or WS_NONE for kills that are not a weapon (goomba, ...).  inflictorAiName is the
// aiName of the entity that did the damage (may be NULL): artillery and airstrike bombs share MOD_AIRSTRIKE and are told
// apart by it ("artillery" vs "air strike").
int G_WStats_SlotForMOD( int mod, const char *inflictorAiName );

// Where a death goes: the weapon's slot, or WS_WORLD when no weapon killed the player (so deaths always add up).
int G_WStats_DeathSlot( int mod, const char *inflictorAiName );

// Slot for a weapon being fired (WP_*), or WS_NONE.
int G_WStats_SlotForWeapon( int weapon );

const char *G_WStats_Name( int slot );

void G_WStats_Reset( wstat_t *ws );
void G_WStats_Attempt( wstat_t *ws, int slot );
void G_WStats_RefundAttempt( wstat_t *ws, int slot );       // a shot that only gibbed a body does not count (OSP)
void G_WStats_Hit( wstat_t *ws, int slot );                 // never more hits than attempts for explosives: see .c
void G_WStats_Kill( wstat_t *ws, int slot );
void G_WStats_Death( wstat_t *ws, int slot );
void G_WStats_Headshot( wstat_t *ws, int slot );

// What the stats block needs about one player.
typedef struct {
	const char *name;           // colour codes already stripped
	int rounds;                 // rounds played, for "(N Rounds)"
	const wstat_t *ws;          // WS_MAX entries
	int damageGiven, damageReceived, teamDamage, gibs;
} wstatReport_t;

// OSP's "Accuracy info for: ..." block (the `stats` command).  With colour on, the OSP/ET colours are added (title name
// yellow, row columns yellow/white/cyan/green/red/yellow); with it off the text matches OSP's own dump character for character.
void G_WStats_FormatPlayer( char *out, int size, const wstatReport_t *r, int color );

// Efficiency as OSP prints it: kills / (kills + deaths), truncated, 0 when there is nothing.
int G_WStats_Efficiency( int kills, int deaths );

#endif
