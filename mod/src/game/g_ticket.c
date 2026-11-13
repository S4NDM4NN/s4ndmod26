// g_ticket.c
//
// Join tickets: the player's client sends the ticket s4ndmod.com gave it for this server in the client command
// "s4nd_ticket <ticket>" after it joined.  A good one attaches the account to the player (sess.accountId, kept across
// map changes) and from then on the stats lines carry it.  No ticket, or a bad one: the player plays as before,
// without an identity.  The format and what a ticket means: infra/web/status-api/ticket/ticket.go; the reading is
// g_ticket_core.c.
//
//   s4nd_server_id    this server's id, set by s4nd-agent (+set).  Tickets are only taken for it.  A server info
//                     variable, so it shows in getstatus (the site can check which server answers at an address).
//   s4nd_ticket_key   the site's Ed25519 public key (base64), set by s4nd-agent from TICKET_PUBLIC_KEY.
//
// Neither is CVAR_ROM, and must not be: the engine (Cvar_Get) resets a read-only variable that was set on the command
// line to the game's default when the game registers it, which would empty the value s4nd-agent passes with +set.  An
// admin with rcon can change them, which is fine: rcon is the server's own admin.
//
// Either one missing means this server does not take tickets.

#include "g_local.h"
#include "g_ticket_core.h"

#define TICKET_SKEW 30          // seconds the server's clock may run ahead of the site's before a fresh ticket looks expired
#define TICKET_TRIES 3          // attempts per player per map; each one costs a signature check

vmCvar_t s4nd_server_id;
vmCvar_t s4nd_ticket_key;

// the decimal form of an account id, "0" for none (the stats files and logs use it; printf has no portable 64-bit
// format across the platforms the mod builds for)
void G_Ticket_AccountString( unsigned long long account, char *out, int size ) {
	char tmp[24];
	int n = 0, i;

	if ( size < 2 ) {
		if ( size > 0 ) {
			out[0] = 0;
		}
		return;
	}
	do {
		tmp[n++] = (char)( '0' + (int)( account % 10 ) );
		account /= 10;
	} while ( account && n < (int)sizeof( tmp ) );
	for ( i = 0; i < n && i < size - 1; i++ ) {
		out[i] = tmp[n - 1 - i];
	}
	out[i] = 0;
}

static void Tell( gentity_t *ent, const char *msg ) {
	trap_SendServerCommand( ent - g_entities, va( "print \"^3[s4nd]^7 %s\n\"", msg ) );
}

// whether this server takes tickets: fills the site key and this server's id
static qboolean Ready( unsigned char key[32], unsigned char id[TICKET_ID_BYTES] ) {
	return G_TicketParseServerID( s4nd_server_id.string, id ) && G_TicketBase64Decode( s4nd_ticket_key.string, key, 33 ) == 32;
}

void G_Ticket_Command( gentity_t *ent ) {
	gclient_t *cl = ent->client;
	char token[MAX_TOKEN_CHARS], acct[24];
	unsigned char key[33], id[TICKET_ID_BYTES];
	ticketInfo_t info;
	qtime_t qt;
	unsigned int now;
	int rc;

	if ( !cl || ( ent->r.svFlags & SVF_BOT ) ) {
		return;
	}
	if ( trap_Argc() != 2 ) {
		Tell( ent, "usage: s4nd_ticket <ticket>" );
		return;
	}
	if ( cl->sess.accountId ) {
		return;         // one identity per connection: nothing to switch to, and nothing to report twice
	}
	if ( !Ready( key, id ) ) {
		Tell( ent, "this server does not take tickets" );
		return;
	}
	if ( cl->pers.ticketTries >= TICKET_TRIES ) {
		return;
	}
	cl->pers.ticketTries++;

	trap_Argv( 1, token, sizeof( token ) );
	memset( &qt, 0, sizeof( qt ) );
	now = (unsigned int)trap_RealTime( &qt );

	// the clock may be a little ahead of the site's: judge the ticket as of TICKET_SKEW seconds ago
	rc = G_TicketVerify( token, key, id, now > TICKET_SKEW ? now - TICKET_SKEW : 0, &info );
	if ( rc == TICKET_OK && !info.account ) {
		rc = TICKET_SIG;        // the site never makes account 0: that value means "no identity" here
	}
	if ( rc == TICKET_OK && !G_TicketUse( info.id, info.expires + TICKET_SKEW, now ) ) {
		Tell( ent, "that ticket was already used" );
		G_LogPrintf( "Ticket: client %d: ticket used twice\n", (int)( ent - g_entities ) );
		return;
	}
	if ( rc != TICKET_OK ) {
		switch ( rc ) {
		case TICKET_EXPIRED:
			Tell( ent, "ticket expired (ask for a new one; is your clock right?)" );
			break;
		case TICKET_SERVER:
			Tell( ent, "that ticket is for another server" );
			break;
		default:
			Tell( ent, "not a valid ticket" );
			break;
		}
		G_LogPrintf( "Ticket: client %d: refused (%d)\n", (int)( ent - g_entities ), rc );
		return;
	}

	cl->sess.accountId = info.account;
	G_Ticket_AccountString( info.account, acct, sizeof( acct ) );
	Tell( ent, va( "signed in as account %s", acct ) );
	G_LogPrintf( "Ticket: client %d is account %s\n", (int)( ent - g_entities ), acct );
}
