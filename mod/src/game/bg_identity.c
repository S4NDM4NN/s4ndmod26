// bg_identity.c -- see bg_identity.h

#include <string.h>

#include "bg_identity.h"

static const char authPrefix[] = "s4nd-auth-v1";

int BG_IdHexDecode( const char *hex, unsigned char *out, int outLen ) {
	int i;

	if ( !hex || (int)strlen( hex ) != 2 * outLen ) {
		return 0;
	}
	for ( i = 0; i < outLen; i++ ) {
		int v = 0, k;
		for ( k = 0; k < 2; k++ ) {
			int c = hex[2 * i + k], d;
			if ( c >= '0' && c <= '9' ) {
				d = c - '0';
			} else if ( c >= 'a' && c <= 'f' ) {
				d = c - 'a' + 10;
			} else if ( c >= 'A' && c <= 'F' ) {
				d = c - 'A' + 10;
			} else {
				return 0;
			}
			v = ( v << 4 ) | d;
		}
		out[i] = (unsigned char)v;
	}
	return 1;
}

int BG_IdBase64URL( char *out, int outSize, const unsigned char *in, int len ) {
	static const char alphabet[] = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";
	int need = ( len * 4 + 2 ) / 3, n = 0, i;

	if ( !out || outSize < need + 1 || len < 0 ) {
		return 0;
	}
	for ( i = 0; i + 2 < len; i += 3 ) {
		unsigned int v = ( (unsigned int)in[i] << 16 ) | ( (unsigned int)in[i + 1] << 8 ) | in[i + 2];
		out[n++] = alphabet[( v >> 18 ) & 63];
		out[n++] = alphabet[( v >> 12 ) & 63];
		out[n++] = alphabet[( v >> 6 ) & 63];
		out[n++] = alphabet[v & 63];
	}
	if ( len - i == 1 ) {
		unsigned int v = (unsigned int)in[i] << 16;
		out[n++] = alphabet[( v >> 18 ) & 63];
		out[n++] = alphabet[( v >> 12 ) & 63];
	} else if ( len - i == 2 ) {
		unsigned int v = ( (unsigned int)in[i] << 16 ) | ( (unsigned int)in[i + 1] << 8 );
		out[n++] = alphabet[( v >> 18 ) & 63];
		out[n++] = alphabet[( v >> 12 ) & 63];
		out[n++] = alphabet[( v >> 6 ) & 63];
	}
	out[n] = 0;
	return n;
}

int BG_IdAuthMessage( unsigned char *msg, int msgSize, const unsigned char nonce[BG_ID_NONCE_BYTES], const char *address ) {
	int prefixLen = (int)sizeof( authPrefix ) - 1, addrLen, i, n;

	if ( !msg || !nonce || !address ) {
		return 0;
	}
	addrLen = (int)strlen( address );
	if ( addrLen > BG_ID_MAX_ADDRESS ) {
		return 0;
	}
	for ( i = 0; i < addrLen; i++ ) {
		if ( (unsigned char)address[i] <= ' ' || (unsigned char)address[i] > '~' ) {
			return 0;       // a space would split the command; anything else is not an address
		}
	}
	n = prefixLen + BG_ID_NONCE_BYTES + addrLen;
	if ( n > msgSize || n > 255 ) {
		return 0;
	}
	memcpy( msg, authPrefix, prefixLen );
	memcpy( msg + prefixLen, nonce, BG_ID_NONCE_BYTES );
	for ( i = 0; i < addrLen; i++ ) {
		char c = address[i];
		msg[prefixLen + BG_ID_NONCE_BYTES + i] = (unsigned char)( c >= 'A' && c <= 'Z' ? c - 'A' + 'a' : c );
	}
	return n;
}
