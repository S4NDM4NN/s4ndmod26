/*
 * Standalone test for mod/src/game/bg_wstats.c.  Not part of the game build (it lives here, outside mod/src/game, because the
 * game's Jamfile globs g_*.c): tools/local/test_wstats.sh compiles and runs it.
 * The two expected blocks are real OSP `stats` dumps (a medic and a lieutenant), reproduced character for character.
 */
#include "q_shared.h"
#include "bg_public.h"
#include "bg_wstats.h"

#include <stdio.h>
#include <string.h>

static int failures, checks;

static void Check( int ok, const char *what ) {
	checks++;
	if ( !ok ) {
		failures++;
		printf( "FAIL: %s\n", what );
	}
}

static void CheckText( const char *got, const char *want, const char *what ) {
	checks++;
	if ( strcmp( got, want ) ) {
		failures++;
		printf( "FAIL: %s\n--- got ---\n%s--- want ---\n%s---\n", what, got, want );
	}
}

static void Set( wstat_t *ws, int slot, int hits, int atts, int kills, int deaths, int hs ) {
	ws[slot].hits = hits;
	ws[slot].atts = atts;
	ws[slot].kills = kills;
	ws[slot].deaths = deaths;
	ws[slot].headshots = hs;
}

static void TestMedicDump( void ) {
	wstat_t ws[WS_MAX];
	wstatReport_t r;
	char out[2048];
	static const char want[] =
		"Accuracy info for: S4NDM4NN (1 Round)\n"
		"\n"
		"Weapon     Acrcy Hits/Atts Kll Dth HS\n"
		"-------------------------------------\n"
		"Luger    :  46.2    6/13     1   0  0\n"
		"Colt     :                   0   1  0\n"
		"MP-40    :  31.1   55/177    1   1  1\n"
		"Thompson :                   0   6  0\n"
		"Sten     :                   0   1  0\n"
		"Panzer   :                   0   2  0\n"
		"Syringe  :  75.0    6/8      0   0\n"
		"\n"
		"Healthpacks: 30/42\n"
		"Revivals   : 6\n"
		"\n"
		"Damage Given: 914     Bodies Gibbed: 0\n"
		"Damage Recvd: 2569    Team Damage  : 0\n";

	G_WStats_Reset( ws );
	Set( ws, WS_LUGER, 6, 13, 1, 0, 0 );
	Set( ws, WS_COLT, 0, 0, 0, 1, 0 );
	Set( ws, WS_MP40, 55, 177, 1, 1, 1 );
	Set( ws, WS_THOMPSON, 0, 0, 0, 6, 0 );
	Set( ws, WS_STEN, 0, 0, 0, 1, 0 );
	Set( ws, WS_PANZER, 0, 0, 0, 2, 0 );
	Set( ws, WS_SYRINGE, 6, 8, 0, 0, 0 );
	Set( ws, WS_HEALTHPACKS, 30, 42, 0, 0, 0 );
	r.name = "S4NDM4NN"; r.rounds = 1; r.ws = ws; r.damageGiven = 914; r.damageReceived = 2569; r.teamDamage = 0; r.gibs = 0;
	G_WStats_FormatPlayer( out, sizeof( out ), &r, 0 );
	CheckText( out, want, "medic dump" );
}

static void TestLieutenantDump( void ) {
	wstat_t ws[WS_MAX];
	wstatReport_t r;
	char out[2048];
	static const char want[] =
		"Accuracy info for: S4NDM4NN (1 Round)\n"
		"\n"
		"Weapon     Acrcy Hits/Atts Kll Dth HS\n"
		"-------------------------------------\n"
		"MP-40    :  30.6   11/36     0   1  1\n"
		"Airstrike:   0.0    0/1      0   1\n"
		"\n"
		"Ammopacks: 10/12\n"
		"\n"
		"Damage Given: 190     Bodies Gibbed: 0\n"
		"Damage Recvd: 504     Team Damage  : 42\n";

	G_WStats_Reset( ws );
	Set( ws, WS_MP40, 11, 36, 0, 1, 1 );
	Set( ws, WS_AIRSTRIKE, 0, 1, 0, 1, 0 );
	Set( ws, WS_AMMOPACKS, 10, 12, 0, 0, 0 );
	r.name = "S4NDM4NN"; r.rounds = 1; r.ws = ws; r.damageGiven = 190; r.damageReceived = 504; r.teamDamage = 42; r.gibs = 0;
	G_WStats_FormatPlayer( out, sizeof( out ), &r, 0 );
	CheckText( out, want, "lieutenant dump" );
}

static void TestExtras( void ) {
	wstat_t ws[WS_MAX];
	wstatReport_t r;
	char out[2048];

	// nothing yet
	G_WStats_Reset( ws );
	r.name = "x"; r.rounds = 0; r.ws = ws; r.damageGiven = r.damageReceived = r.teamDamage = r.gibs = 0;
	G_WStats_FormatPlayer( out, sizeof( out ), &r, 0 );
	Check( strstr( out, "(0 Rounds)" ) && strstr( out, "No weapon info available." ), "empty block" );

	// world deaths: a row with deaths only
	ws[WS_WORLD].deaths = 3;
	G_WStats_FormatPlayer( out, sizeof( out ), &r, 0 );
	Check( strstr( out, "World    :                       3\n" ) != NULL, "world row is deaths only" );

	// colour only adds codes
	G_WStats_FormatPlayer( out, sizeof( out ), &r, 1 );
	Check( strstr( out, "^3" ) != NULL, "colour adds codes" );

	// a tiny buffer must not overflow or crash
	{
		char small[16];
		G_WStats_FormatPlayer( small, sizeof( small ), &r, 0 );
		Check( strlen( small ) < sizeof( small ), "small buffer is cut, not overrun" );
	}
}

static void TestMapping( void ) {
	Check( G_WStats_SlotForMOD( MOD_ROCKET_SPLASH, NULL ) == WS_PANZER, "rocket splash -> panzer" );
	Check( G_WStats_SlotForMOD( MOD_MAUSER, NULL ) == WS_SNIPER && G_WStats_SlotForMOD( MOD_SNIPERRIFLE, NULL ) == WS_SNIPER, "mauser + scope -> sniper" );
	Check( G_WStats_SlotForMOD( MOD_KNIFE_THROWN, NULL ) == WS_KNIFE, "thrown knife -> knife" );
	Check( G_WStats_SlotForMOD( MOD_MACHINEGUN, NULL ) == WS_CREWGUN, "mounted mg42 -> crew gun" );
	Check( G_WStats_SlotForMOD( MOD_POISON, NULL ) == WS_SYRINGE, "syringe poison -> syringe" );
	Check( G_WStats_SlotForMOD( MOD_AIRSTRIKE, "air strike" ) == WS_AIRSTRIKE, "airstrike bombs" );
	Check( G_WStats_SlotForMOD( MOD_AIRSTRIKE, "artillery" ) == WS_ARTILLERY, "artillery shells share MOD_AIRSTRIKE" );
	Check( G_WStats_SlotForMOD( MOD_AIRSTRIKE, NULL ) == WS_AIRSTRIKE, "no inflictor -> airstrike" );
	Check( G_WStats_SlotForMOD( MOD_MORTAR_SPLASH, NULL ) == WS_WORLD, "map mortar is a world death" );
	Check( G_WStats_SlotForMOD( MOD_SUICIDE, NULL ) == WS_WORLD && G_WStats_SlotForMOD( MOD_FALLING, NULL ) == WS_WORLD, "suicide and falling -> world" );
	// a goomba kills without a weapon: no kill row, but the victim's death still lands somewhere
	Check( G_WStats_SlotForMOD( MOD_GOOMBA, NULL ) == WS_NONE, "goomba kill has no weapon row" );
	Check( G_WStats_DeathSlot( MOD_GOOMBA, NULL ) == WS_WORLD, "goomba death -> world" );
	Check( G_WStats_DeathSlot( MOD_MP40, NULL ) == WS_MP40, "death by mp40" );

	Check( G_WStats_SlotForWeapon( WP_MAUSER ) == WS_SNIPER && G_WStats_SlotForWeapon( WP_SNIPERRIFLE ) == WS_SNIPER, "weapon -> sniper" );
	Check( G_WStats_SlotForWeapon( WP_SMOKE_GRENADE ) == WS_AIRSTRIKE && G_WStats_SlotForWeapon( WP_ARTY ) == WS_ARTILLERY, "smoke canister / binoculars" );
	Check( G_WStats_SlotForWeapon( WP_MEDKIT ) == WS_HEALTHPACKS && G_WStats_SlotForWeapon( WP_AMMO ) == WS_AMMOPACKS, "packs" );
	Check( G_WStats_SlotForWeapon( WP_PLIERS ) == WS_NONE, "pliers have no stat" );
}

static void TestCounters( void ) {
	wstat_t ws[WS_MAX];

	G_WStats_Reset( ws );
	G_WStats_Attempt( ws, WS_MP40 ); G_WStats_Attempt( ws, WS_MP40 );
	G_WStats_Hit( ws, WS_MP40 ); G_WStats_Hit( ws, WS_MP40 ); G_WStats_Hit( ws, WS_MP40 );   // third is capped
	Check( ws[WS_MP40].atts == 2 && ws[WS_MP40].hits == 2, "hits never exceed attempts" );
	G_WStats_RefundAttempt( ws, WS_MP40 );
	Check( ws[WS_MP40].atts == 1, "a gibbing shot is refunded" );
	G_WStats_RefundAttempt( ws, WS_COLT );
	Check( ws[WS_COLT].atts == 0, "refund never goes negative" );
	G_WStats_Attempt( ws, WS_NONE ); G_WStats_Kill( ws, WS_MAX ); G_WStats_Death( ws, -5 );    // out-of-range slots are ignored
	G_WStats_Kill( ws, WS_PANZER ); G_WStats_Death( ws, WS_WORLD ); G_WStats_Headshot( ws, WS_STEN );
	Check( ws[WS_PANZER].kills == 1 && ws[WS_WORLD].deaths == 1 && ws[WS_STEN].headshots == 1, "kill / death / headshot" );

	Check( G_WStats_Efficiency( 10, 5 ) == 66 && G_WStats_Efficiency( 3, 2 ) == 60 && G_WStats_Efficiency( 1, 4 ) == 20, "efficiency truncates (OSP dump rows)" );
	Check( G_WStats_Efficiency( 2, 11 ) == 15 && G_WStats_Efficiency( 0, 0 ) == 0, "efficiency 2/13 and 0/0" );
}

static void TestTop( void ) {
	wstat_t a[WS_MAX], b[WS_MAX], c[WS_MAX];
	const wstat_t *players[4] = { a, b, c, NULL };
	const char *names[4] = { "Ann", "Bob", "Cy", "gone" };
	wstatTopRow_t rows[32];
	char out[1024];
	int n;

	G_WStats_Reset( a ); G_WStats_Reset( b ); G_WStats_Reset( c );
	Set( a, WS_LUGER, 9, 20, 2, 1, 0 );  Set( b, WS_LUGER, 5, 10, 1, 0, 0 );  Set( c, WS_LUGER, 3, 9, 3, 0, 0 );   // c is under 10 shots
	Set( a, WS_MP40, 30, 100, 4, 2, 1 ); Set( b, WS_MP40, 40, 100, 5, 1, 2 );
	Set( c, WS_PANZER, 3, 3, 3, 0, 0 );                                                                          // too few shots

	n = G_WStats_BuildTop( rows, 32, players, 4, 1, 10, -1 );
	Check( n == 2 && rows[0].slot == WS_LUGER && rows[0].client == 1 && rows[1].slot == WS_MP40 && rows[1].client == 1,
		   "best: one row per weapon with enough shots, Bob wins Luger (50%) and MP-40 (40%)" );
	n = G_WStats_BuildTop( rows, 32, players, 4, 0, 10, -1 );
	Check( n == 2 && rows[0].client == 0 && rows[1].client == 0, "worst: Ann has the lowest accuracy on both" );
	n = G_WStats_BuildTop( rows, 3, players, 4, 1, 10, WS_MP40 );
	Check( n == 2 && rows[0].client == 1 && rows[1].client == 0, "one weapon lists players best first" );
	n = G_WStats_BuildTop( rows, 1, players, 4, 1, 10, WS_MP40 );
	Check( n == 1 && rows[0].client == 1, "one weapon honours the row limit" );
	Check( G_WStats_BuildTop( rows, 32, players, 4, 1, 200, -1 ) == 0, "nobody qualifies with a high minimum" );

	n = G_WStats_BuildTop( rows, 32, players, 4, 1, 10, WS_LUGER );
	G_WStats_FormatTop( out, sizeof( out ), rows, n, names, 1, 0 );
	CheckText( out, "BEST Match Accuracies:\nWP         Acrcy Hits/Atts Kll Dth Player\n"
					"Luger    :  50.0    5/10     1   0 Bob\nLuger    :  45.0    9/20     2   1 Ann\n", "topshots text" );
	G_WStats_FormatTop( out, sizeof( out ), rows, 0, names, 0, 0 );
	CheckText( out, "No qualifying bottomshot info available.\n", "empty bottomshots text" );
}

int main( void ) {
	TestMedicDump();
	TestLieutenantDump();
	TestExtras();
	TestMapping();
	TestCounters();
	TestTop();
	printf( "%d checks, %d failed\n", checks, failures );
	return failures ? 1 : 0;
}
