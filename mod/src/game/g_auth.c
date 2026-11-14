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

#define AUTH_TRIES 6            // answers per player per map; each one costs a signature check
#define AUTH_RESENDS 6          // times the challenge is sent per connection (the first, then re-sends until it is answered)
#define AUTH_RESEND_MS 3000     // between sends: the engine drops a client command that comes within 0.8 s of the last one, and at join
                                // there is a burst, so the first answer was sometimes eaten before the game saw it (sv_client.c)

extern vmCvar_t g_replayLoadFile;       // set when this server only plays a recorded game back (g_replay.c)

vmCvar_t s4nd_auth_key;
vmCvar_t s4nd_public_names;

static void Tell( gentity_t *ent, const char *msg ) {
	trap_SendServerCommand( ent - g_entities, va( "print \"^3[s4nd]^7 %s\n\"", msg ) );
}

static void SendChallenge( gentity_t *ent ) {
	gclient_t *cl = ent->client;
	char hex[BG_ID_NONCE_BYTES * 2 + 1];
	int i;

	for ( i = 0; i < BG_ID_NONCE_BYTES; i++ ) {
		Com_sprintf( hex + 2 * i, 3, "%02x", cl->pers.authNonce[i] );
	}
	trap_SendServerCommand( ent - g_entities, va( "s4nd_challenge %s", hex ) );
	cl->pers.authSends++;
	cl->pers.authNextSend = level.time + AUTH_RESEND_MS;
}

// sends the client its challenge when it enters the game (the connection starts with a cleared client, so pers.authSent is 0 for a new
// one).  A player who is already identified from before a map change needs none.  Until the player answers, G_Auth_Frame sends the same
// challenge again.
void G_Auth_Challenge( gentity_t *ent ) {
	gclient_t *cl = ent->client;
	static unsigned int counter;

	if ( !cl || ( ent->r.svFlags & SVF_BOT ) || cl->pers.authSent || cl->sess.keyId[0] ) {
		return;
	}
	if ( g_replayLoadFile.string[0] ) {
		return;         // a replay being watched (the play of the game in the browser): nobody to identify, nothing is recorded
	}
	G_AuthNonce( cl->pers.authNonce, s4nd_auth_key.string, ++counter, (int)( ent - g_entities ), trap_Milliseconds() );
	cl->pers.authSent = qtrue;
	SendChallenge( ent );
	G_LogPrintf( "Auth: client %d challenged\n", (int)( ent - g_entities ) );
}

// every frame: a player who was challenged and has not answered is sent the same challenge again every few seconds, a limited number of
// times.  The answer can be lost on the way (the engine's flood protection ignores a command that follows another within 0.8 s, and a
// client sends several when it joins), and a challenge that arrives before the client's game code is ready is not acted on.
void G_Auth_Frame( void ) {
	int i;

	for ( i = 0; i < level.maxclients; i++ ) {
		gentity_t *ent = &g_entities[i];
		gclient_t *cl = &level.clients[i];

		if ( cl->pers.connected != CON_CONNECTED || ( ent->r.svFlags & SVF_BOT ) || !cl->pers.authSent || cl->sess.keyId[0]
			 || cl->pers.authSends >= AUTH_RESENDS || cl->pers.authTries >= AUTH_TRIES || level.time < cl->pers.authNextSend ) {
			continue;
		}
		SendChallenge( ent );
		G_LogPrintf( "Auth: client %d challenged again (send %d)\n", i, cl->pers.authSends );
	}
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
		// already identified, nothing asked, or enough attempts
		G_LogPrintf( "Auth: client %d: answer ignored (%s)\n", (int)( ent - g_entities ),
					 cl->sess.keyId[0] ? "already identified" : !cl->pers.authSent ? "no challenge sent" : "too many answers" );
		return;
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

// /link CODE: the player holds a one-time code from their s4ndmod.com account and wants their key tied to it.  The
// request goes to s4nd-agent, which signs it and posts it to the site (agent/linkrelay.go); the site asks the account
// holder to confirm the key there.  The server only passes it on: it never learns the account.
//
//   s4nd_link   set to 1 by s4nd-agent when it has a site to send to.
#define LINK_FILE "link.log"
#define LINK_MIN_MSEC 5000      // between requests from one player

vmCvar_t s4nd_link;

void G_Link_Command( gentity_t *ent ) {
	gclient_t *cl = ent->client;
	char code[24];
	fileHandle_t f;
	char *p;
	int len;

	if ( !cl || ( ent->r.svFlags & SVF_BOT ) ) {
		return;
	}
	if ( trap_Argc() != 2 ) {
		Tell( ent, "usage: /link CODE   (get the code on your s4ndmod.com account page)" );
		return;
	}
	if ( !s4nd_link.integer ) {
		Tell( ent, "this server cannot link accounts" );
		return;
	}
	if ( !cl->sess.keyId[0] ) {
		Tell( ent, "you have no player id on this server yet: wait for it to say you are identified, then try again" );
		return;
	}
	if ( cl->pers.linkLast && level.time - cl->pers.linkLast < LINK_MIN_MSEC ) {
		Tell( ent, "wait a few seconds before trying again" );
		return;
	}
	cl->pers.linkLast = level.time;

	// only the characters a code is made of, so nothing else can reach the file (a newline would forge a line)
	trap_Argv( 1, code, sizeof( code ) );
	len = (int)strlen( code );
	if ( len < 4 || len > 16 ) {
		Tell( ent, "that does not look like a link code" );
		return;
	}
	for ( p = code; *p; p++ ) {
		if ( !( ( *p >= '0' && *p <= '9' ) || ( *p >= 'a' && *p <= 'z' ) || ( *p >= 'A' && *p <= 'Z' ) || *p == '-' ) ) {
			Tell( ent, "that does not look like a link code" );
			return;
		}
	}

	if ( trap_FS_FOpenFile( LINK_FILE, &f, FS_APPEND ) < 0 ) {
		Tell( ent, "could not send the request" );
		return;
	}
	{
		char line[128];
		Com_sprintf( line, sizeof( line ), "link %s %s\n", cl->sess.keyId, code );
		trap_FS_Write( line, (int)strlen( line ), f );
	}
	trap_FS_FCloseFile( f );
	Tell( ent, va( "link request sent for key %.10s: now confirm it on your s4ndmod.com account page", cl->sess.keyId ) );
	G_LogPrintf( "Auth: client %d asked to link key %.10s\n", (int)( ent - g_entities ), cl->sess.keyId );
}
