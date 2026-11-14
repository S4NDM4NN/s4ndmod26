// bg_identity.h
//
// What the player identity in cgame (cg_identity.c) and the game module (g_auth.c) must agree on, byte for byte, so
// it lives in one shared file.  The protocol:
//
//   server -> client   s4nd_challenge <32 hex digits>                       16 random bytes, one per connection
//   client -> server   s4nd_auth <public key> <signature> <address>
//
// public key and signature are unpadded URL-safe base64 (43 and 86 characters; the key is the player's id), and the
// signature is Ed25519 over   "s4nd-auth-v1" + the 16 challenge bytes + the address in lower case,   where the address
// is what the client believes it connected to (cl_currentServerAddress).  Signing the address means a hostile server
// that relays another server's challenge to a player gets a signature that only that other server's address would
// match, and a server that knows its own names (s4nd_public_names) turns it down.
#ifndef BG_IDENTITY_H
#define BG_IDENTITY_H

#define BG_ID_NONCE_BYTES 16
#define BG_ID_KEY_CHARS 43              // a public key (32 bytes) as unpadded base64url
#define BG_ID_SIG_CHARS 86              // a signature (64 bytes) as unpadded base64url
#define BG_ID_MAX_ADDRESS 200           // the longest address that is signed (the message must stay within 255 bytes)

// exactly 2*outLen hex digits to bytes.  1 when valid.
int BG_IdHexDecode( const char *hex, unsigned char *out, int outLen );

// bytes to unpadded base64url, NUL-terminated.  Returns the length, or 0 if out is too small.
int BG_IdBase64URL( char *out, int outSize, const unsigned char *in, int len );

// The bytes a client signs: the prefix, the nonce and the address in lower case.  The address must be 1 to
// BG_ID_MAX_ADDRESS characters of printable ASCII with no spaces ("" is allowed and signed as nothing).  Returns the
// length, or 0 if it does not fit or the address is not acceptable.
int BG_IdAuthMessage( unsigned char *msg, int msgSize, const unsigned char nonce[BG_ID_NONCE_BYTES], const char *address );

// The bytes a player's key signs to link itself to the account that is signed in on the site when they joined: "s4nd-link-v1\n" + the
// code the page gave the game (upper case, without dashes or spaces: the form the site checks) + "\n" + the id of the server (lower case).
// The code is 4 to 16 letters, digits or dashes; the server id is 1 to 32 lower case letters or digits.  Returns the length, or 0 if
// either is not acceptable or it does not fit.  The site builds the same bytes (infra/web/status-api/link, LinkMessage).
int BG_IdLinkMessage( unsigned char *msg, int msgSize, const char *code, const char *serverId );

#endif
