// g_ticket_core.h
//
// Reading a join ticket (the format is in infra/web/status-api/ticket/ticket.go, which makes them).  No game types:
// g_ticket.c is the game side, tools/local/test_ticket.sh tests this against the Go code.
#ifndef G_TICKET_CORE_H
#define G_TICKET_CORE_H

#define TICKET_SIZE 95              // bytes before base64
#define TICKET_ID_BYTES 10          // the bytes behind a server's 16-letter id

enum {
	TICKET_OK = 0,
	TICKET_FORMAT,                  // not a ticket (wrong length or characters)
	TICKET_VERSION,
	TICKET_SIG,                     // not signed by the site's key, or changed since
	TICKET_SERVER,                  // made for another server
	TICKET_EXPIRED
};

typedef struct {
	unsigned long long account;
	unsigned int expires;           // Unix seconds
	unsigned char id[8];            // unique per ticket: what a server remembers to refuse a second use
} ticketInfo_t;

// A server id as the agent prints it (16 letters and digits, base32) to its 10 bytes.  1 when valid.
int G_TicketParseServerID( const char *id, unsigned char out[TICKET_ID_BYTES] );

// base64 (standard or URL-safe alphabet, padding optional) to bytes.  Returns the length, or -1.
int G_TicketBase64Decode( const char *in, unsigned char *out, int outMax );

// Checks a ticket against the site's public key (32 bytes) for the server with the given id bytes at time now.
// Returns TICKET_OK and fills info, or why not.  Does not know about earlier use: see G_TicketUse.
int G_TicketVerify( const char *token, const unsigned char siteKey[32], const unsigned char serverId[TICKET_ID_BYTES],
					unsigned int now, ticketInfo_t *info );

// Records that the ticket with this id was used.  Returns 1 the first time and 0 if it was used already and has not
// expired yet.  Entries are dropped once expired; if the table is full the one expiring soonest goes.
int G_TicketUse( const unsigned char id[8], unsigned int expires, unsigned int now );
void G_TicketResetUsed( void );

#endif
