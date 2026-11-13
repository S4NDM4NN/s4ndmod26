// g_auth.c
//
// Player identity by key: the player's id is an Ed25519 public key that their cgame keeps (cg_identity.c).  When a
// player enters the game the server sends a challenge,
//
//   s4nd_challenge <32 hex digits>
//
// and the client answers with the client command
//
//   s4nd_auth <public key> <signature> <address>
//
// A good answer proves the client holds the key's private half and that it signed for an address this server answers
// to.  The key becomes sess.keyId (kept across map changes) and the stats lines carry it as "pid".  No answer, or a
// bad one: the player plays as before, without an identity.  The wire format and what is signed: bg_identity.h; the
// checking: g_auth_core.c (tested against Go in tools/local/test_ticket.sh).
//
//   s4nd_auth_key       a secret the challenges are made from, set by s4nd-agent (new at each start).  Empty (a server
//                       started by hand): the challenges come from the clock alone, which is enough to tell
//                       connections apart but lets someone who can guess the time guess a challenge, so a replayed
//                       answer could pass.  Set it on any server where that matters.
//   s4nd_public_names   the addresses (comma or space separated) this server answers to, e.g.
//                       "play.example.com:27960,203.0.113.5:27960".  A signature for any other address is turned
//                       down.  Empty: any address is accepted, so a hostile server that relays this server's
//                       challenge to a player could get a signature that works here.
//
// Neither is CVAR_ROM (see g_ticket.c: the engine resets those that were set on the command line).

#include "g_local.h"
#include "g_auth_core.h"

#define AUTH_TRIES 3            // answers per player per map; each one costs a signature check

vmCvar_t s4nd_auth_key;
vmCvar_t s4nd_public_names;

static void Tell( gentity_t *ent, const char *msg ) {
	trap_SendServerCommand( ent - g_entities, va( "print \"^3[s4nd]^7 %s\n\"", msg ) );
}

// sends the client its challenge, once per connection (the connection starts with a cleared client, so pers.authSent
// is 0 for a new one).  A player who is already identified from before a map change needs none.
void G_Auth_Challenge( gentity_t *ent ) {
	gclient_t *cl = ent->client;
	static unsigned int counter;
	char hex[BG_ID_NONCE_BYTES * 2 + 1];
	int i;

	if ( !cl || ( ent->r.svFlags & SVF_BOT ) || cl->pers.authSent || cl->sess.keyId[0] ) {
		return;
	}
	G_AuthNonce( cl->pers.authNonce, s4nd_auth_key.string, ++counter, (int)( ent - g_entities ), trap_Milliseconds() );
	cl->pers.authSent = qtrue;
	for ( i = 0; i < BG_ID_NONCE_BYTES; i++ ) {
		Com_sprintf( hex + 2 * i, 3, "%02x", cl->pers.authNonce[i] );
	}
	trap_SendServerCommand( ent - g_entities, va( "s4nd_challenge %s", hex ) );
	G_LogPrintf( "Auth: client %d challenged\n", (int)( ent - g_entities ) );
}

void G_Auth_Command( gentity_t *ent ) {
	gclient_t *cl = ent->client;
	char pk[BG_ID_KEY_CHARS + 8], sig[BG_ID_SIG_CHARS + 8], address[BG_ID_MAX_ADDRESS + 8], names[MAX_CVAR_VALUE_STRING], id[BG_ID_KEY_CHARS + 8];
	char *p;
	int rc;

	if ( !cl || ( ent->r.svFlags & SVF_BOT ) ) {
		return;
	}
	if ( trap_Argc() != 4 ) {
		Tell( ent, "usage: s4nd_auth <public key> <signature> <address>" );
		return;
	}
	if ( cl->sess.keyId[0] || !cl->pers.authSent || cl->pers.authTries >= AUTH_TRIES ) {
		return;         // already identified, nothing asked, or enough attempts
	}
	cl->pers.authTries++;
	G_LogPrintf( "Auth: client %d answered (try %d)\n", (int)( ent - g_entities ), cl->pers.authTries );

	// longer than any good value by a few characters: a long one is refused as a bad form, not cut into a good one
	trap_Argv( 1, pk, sizeof( pk ) );
	trap_Argv( 2, sig, sizeof( sig ) );
	trap_Argv( 3, address, sizeof( address ) );
	Q_strncpyz( names, s4nd_public_names.string, sizeof( names ) );
	for ( p = names; *p; p++ ) {
		if ( *p == ',' ) {
			*p = ' ';
		}
	}

	rc = G_AuthVerify( pk, sig, cl->pers.authNonce, address, names, id, sizeof( id ) );
	if ( rc != AUTH_OK ) {
		switch ( rc ) {
		case AUTH_ADDRESS:
			Tell( ent, "that signature is for another server address" );
			break;
		case AUTH_SIG:
			Tell( ent, "the signature does not match (a stale challenge?)" );
			break;
		default:
			Tell( ent, "not a valid key or signature" );
			break;
		}
		G_LogPrintf( "Auth: client %d: refused (%d)\n", (int)( ent - g_entities ), rc );
		return;
	}

	Q_strncpyz( cl->sess.keyId, id, sizeof( cl->sess.keyId ) );
	Tell( ent, va( "identified as key %.10s", id ) );
	G_LogPrintf( "Auth: client %d is key %s\n", (int)( ent - g_entities ), id );
}
