// g_auth_core.h
//
// The game module's side of the player identity protocol (bg_identity.h), with no game types so that
// tools/local/test_ticket.sh can test it against Go.  g_auth.c is the glue.
#ifndef G_AUTH_CORE_H
#define G_AUTH_CORE_H

#include "bg_identity.h"

enum {
	AUTH_OK = 0,
	AUTH_FORMAT,                    // the key, signature or address is not in the right form
	AUTH_SIG,                       // not a signature by that key over this challenge and address
	AUTH_ADDRESS                    // signed, but for an address this server does not answer to
};

// Checks "s4nd_auth <pkB64> <sigB64> <address>" against the challenge this client was sent.  address is the third
// argument as sent ("-" means none).  publicNames is the server's s4nd_public_names: addresses (space separated) that
// are this server, compared without regard to case; "" accepts any address (then a signature made for another server
// would be accepted too: set the names on a server where that matters).  On AUTH_OK keyId gets the player's id: the
// public key as base64url, at least BG_ID_KEY_CHARS + 1 bytes.
int G_AuthVerify( const char *pkB64, const char *sigB64, const unsigned char nonce[BG_ID_NONCE_BYTES], const char *address,
				  const char *publicNames, char *keyId, int keyIdSize );

// The challenge for one client: 16 bytes nobody outside the server can predict (they come from key, which the agent
// makes up fresh at each start, and a counter), different for every challenge.
void G_AuthNonce( unsigned char out[BG_ID_NONCE_BYTES], const char *key, unsigned int counter, int clientNum, int time );

#endif
