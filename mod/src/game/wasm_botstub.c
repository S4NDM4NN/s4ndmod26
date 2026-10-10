// wasm_botstub.c -- no-op replacements for the Omni-bot interface
// (g_rtcwbot_interface.cpp) so the game module can be built for the browser,
// where there are no bots.  Only compiled for the WASM qagame (see
// WASM_S4ND_QAGAME in iortcw/Makefile); the file name deliberately does not
// match the g_*.c glob used by the native bjam build.

#include "g_local.h"
#include "g_rtcwbot_interface.h"

// Entity naming used by bot goals and a few script actions.  Map entities only matter for bots,
// so a client's name is all a replay needs.
const char *_GetEntityName( gentity_t *_ent ) {
	if ( _ent && _ent->inuse && _ent->client ) {
		return _ent->client->pers.netname;
	}
	return "";
}

int Bot_Interface_Init() {
	return 0;
}
void Bot_Interface_InitHandles() {
}
int Bot_Interface_Shutdown() {
	return 0;
}
void Bot_Interface_Update() {
}
int Bot_Interface_ConsoleCommand() {
	return 0;
}

void Bot_Util_SendTrigger( gentity_t *_ent, gentity_t *_activator, const char *_tagname, const char *_action ) {
}
qboolean Bot_Util_CheckForSuicide( gentity_t *ent ) {
	return qfalse;
}
int Bot_WeaponGameToBot( int weapon ) {
	return 0;
}

void Bot_Queue_EntityCreated( gentity_t *pEnt ) {
}
void Bot_Event_EntityDeleted( gentity_t *pEnt ) {
}
void Bot_Event_ClientConnected( int _client, qboolean _isbot ) {
}
void Bot_Event_ClientDisConnected( int _client ) {
}
void Bot_Event_Drowning( int _client ) {
}
void Bot_Event_ResetWeapons( int _client ) {
}
void Bot_Event_AddWeapon( int _client, int _weaponId ) {
}
void Bot_Event_RemoveWeapon( int _client, int _weaponId ) {
}
void Bot_Event_TakeDamage( int _client, gentity_t *_ent ) {
}
void Bot_Event_Death( int _client, gentity_t *_killer, const char *_meansofdeath ) {
}
void Bot_Event_KilledSomeone( int _client, gentity_t *_victim, const char *_meansofdeath ) {
}
void Bot_Event_Revived( int _client, gentity_t *_whodoneit ) {
}
void Bot_Event_Healed( int _client, gentity_t *_whodoneit ) {
}
void Bot_Event_FireWeapon( int _client, int _weaponId, gentity_t *_projectile ) {
}
void Bot_Event_ChatMessage( int _client, gentity_t *_source, int _type, const char *_message ) {
}
void Bot_Event_VoiceMacro( int _client, gentity_t *_source, int _type, const char *_message ) {
}
void Bot_Event_RecievedAmmo( int _client, gentity_t *_whodoneit ) {
}
void Bot_AddDynamiteGoal( gentity_t *_ent, int _team, const char *_tag ) {
}
void Bot_AddFallenTeammateGoals( gentity_t *_teammate, int _team ) {
}
void AddDeferredGoal( gentity_t *ent ) {
}
