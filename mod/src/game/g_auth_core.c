// g_auth_core.c -- see g_auth_core.h

#include <string.h>

#include "bg_ed25519.h"
#include "g_auth_core.h"
#include "g_ticket_core.h"      // G_TicketBase64Decode

static int nameInList( const char *list, const char *name ) {
	size_t n = strlen( name );

	while ( *list ) {
		const char *end;
		size_t len, i;

		while ( *list == ' ' ) {
			list++;
		}
		for ( end = list; *end && *end != ' '; end++ ) {
		}
		len = (size_t)( end - list );
		if ( len == n ) {
			for ( i = 0; i < n; i++ ) {
				char a = list[i], b = name[i];
				a = (char)( a >= 'A' && a <= 'Z' ? a - 'A' + 'a' : a );
				b = (char)( b >= 'A' && b <= 'Z' ? b - 'A' + 'a' : b );
				if ( a != b ) {
					break;
				}
			}
			if ( i == n ) {
				return 1;
			}
		}
		list = end;
	}
	return 0;
}

int G_AuthVerify( const char *pkB64, const char *sigB64, const unsigned char nonce[BG_ID_NONCE_BYTES], const char *address,
				  const char *publicNames, char *keyId, int keyIdSize ) {
	unsigned char pk[33], sig[65], msg[255];
	char canon[BG_ID_KEY_CHARS + 1];
	int msgLen;

	if ( !pkB64 || !sigB64 || !address || !nonce || (int)strlen( pkB64 ) != BG_ID_KEY_CHARS ||
		 (int)strlen( sigB64 ) != BG_ID_SIG_CHARS ||
		 G_TicketBase64Decode( pkB64, pk, 33 ) != 32 || G_TicketBase64Decode( sigB64, sig, 65 ) != 64 ) {
		return AUTH_FORMAT;
	}
	if ( !strcmp( address, "-" ) ) {
		address = "";
	}
	msgLen = BG_IdAuthMessage( msg, sizeof( msg ), nonce, address );
	if ( !msgLen ) {
		return AUTH_FORMAT;
	}
	// the one canonical text of a key: the same key must never be two ids because of unused bits in its base64
	if ( BG_IdBase64URL( canon, sizeof( canon ), pk, 32 ) != BG_ID_KEY_CHARS || strcmp( canon, pkB64 ) != 0 ) {
		return AUTH_FORMAT;
	}
	if ( !BG_Ed25519Verify( sig, msg, msgLen, pk ) ) {
		return AUTH_SIG;
	}
	if ( publicNames && publicNames[0] && !nameInList( publicNames, address ) ) {
		return AUTH_ADDRESS;
	}
	if ( keyId && keyIdSize > BG_ID_KEY_CHARS ) {
		memcpy( keyId, canon, BG_ID_KEY_CHARS + 1 );
	}
	return AUTH_OK;
}

// appends the decimal text of v to buf at pos (buf has size bytes); returns the new position
static int appendUint( char *buf, int pos, int size, unsigned int v ) {
	char tmp[12];
	int n = 0;

	do {
		tmp[n++] = (char)( '0' + (int)( v % 10 ) );
		v /= 10;
	} while ( v );
	while ( n && pos < size - 1 ) {
		buf[pos++] = tmp[--n];
	}
	return pos;
}

void G_AuthNonce( unsigned char out[BG_ID_NONCE_BYTES], const char *key, unsigned int counter, int clientNum, int time ) {
	unsigned char hash[64];
	char buf[200];
	int pos = 0, i;
	static const char label[] = "s4nd-nonce:";

	for ( i = 0; label[i] && pos < (int)sizeof( buf ) - 1; i++ ) {
		buf[pos++] = label[i];
	}
	for ( i = 0; key && key[i] && i < 128 && pos < (int)sizeof( buf ) - 1; i++ ) {
		buf[pos++] = key[i];
	}
	buf[pos++] = ':';
	pos = appendUint( buf, pos, sizeof( buf ), counter );
	buf[pos++] = ':';
	pos = appendUint( buf, pos, sizeof( buf ), (unsigned int)clientNum );
	buf[pos++] = ':';
	pos = appendUint( buf, pos, sizeof( buf ), (unsigned int)time );

	if ( !BG_Sha512( hash, (const unsigned char *)buf, pos ) ) {
		memset( out, 0, BG_ID_NONCE_BYTES );
		return;
	}
	memcpy( out, hash, BG_ID_NONCE_BYTES );
}
