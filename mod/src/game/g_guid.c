// g_guid.c
//
// What the stats files and replay sidecars record to tell players apart across games and servers.
//
// Not in g_sha1.c: that file belongs to the optional Lua support and is not part of the normal build, so this carries
// its own small SHA-1 (RFC 3174).

#include "g_local.h"

typedef struct {
	unsigned int h[5];
	unsigned char block[64];
	int blockLen;
	unsigned int totalLen;      // bytes; a guid is far below 2^29
} guidSha1_t;

static unsigned int GuidRol( unsigned int v, int n ) {
	return ( v << n ) | ( v >> ( 32 - n ) );
}

static void GuidSha1Block( guidSha1_t *c ) {
	unsigned int w[80], a, b, d, e, f, k, t, cc;
	int i;

	for ( i = 0; i < 16; i++ ) {
		w[i] = ( (unsigned int)c->block[i * 4] << 24 ) | ( (unsigned int)c->block[i * 4 + 1] << 16 ) |
			   ( (unsigned int)c->block[i * 4 + 2] << 8 ) | (unsigned int)c->block[i * 4 + 3];
	}
	for ( i = 16; i < 80; i++ ) {
		w[i] = GuidRol( w[i - 3] ^ w[i - 8] ^ w[i - 14] ^ w[i - 16], 1 );
	}
	a = c->h[0]; b = c->h[1]; cc = c->h[2]; d = c->h[3]; e = c->h[4];
	for ( i = 0; i < 80; i++ ) {
		if ( i < 20 ) {
			f = ( b & cc ) | ( ~b & d );           k = 0x5A827999;
		} else if ( i < 40 ) {
			f = b ^ cc ^ d;                        k = 0x6ED9EBA1;
		} else if ( i < 60 ) {
			f = ( b & cc ) | ( b & d ) | ( cc & d ); k = 0x8F1BBCDC;
		} else {
			f = b ^ cc ^ d;                        k = 0xCA62C1D6;
		}
		t = GuidRol( a, 5 ) + f + e + k + w[i];
		e = d; d = cc; cc = GuidRol( b, 30 ); b = a; a = t;
	}
	c->h[0] += a; c->h[1] += b; c->h[2] += cc; c->h[3] += d; c->h[4] += e;
	c->blockLen = 0;
}

static void GuidSha1Add( guidSha1_t *c, const unsigned char *p, int n ) {
	while ( n-- > 0 ) {
		c->block[c->blockLen++] = *p++;
		c->totalLen++;
		if ( c->blockLen == 64 ) {
			GuidSha1Block( c );
		}
	}
}

// hex: 41 bytes, lowercase
static void GuidSha1Hex( const unsigned char *data, int len, char *hex ) {
	static const char digits[] = "0123456789abcdef";
	guidSha1_t c;
	unsigned int bits = c.totalLen = 0;
	unsigned char pad = 0x80, zero = 0, tail[8];
	int i, j;

	c.h[0] = 0x67452301; c.h[1] = 0xEFCDAB89; c.h[2] = 0x98BADCFE; c.h[3] = 0x10325476; c.h[4] = 0xC3D2E1F0;
	c.blockLen = 0;

	GuidSha1Add( &c, data, len );
	bits = c.totalLen * 8;
	GuidSha1Add( &c, &pad, 1 );
	while ( c.blockLen != 56 ) {
		GuidSha1Add( &c, &zero, 1 );
	}
	for ( i = 0; i < 4; i++ ) {
		tail[i] = 0;                                   // high half of the 64-bit bit count
		tail[4 + i] = (unsigned char)( bits >> ( 24 - 8 * i ) );
	}
	GuidSha1Add( &c, tail, 8 );

	for ( i = 0; i < 5; i++ ) {
		for ( j = 0; j < 4; j++ ) {
			unsigned char byte = (unsigned char)( c.h[i] >> ( 24 - 8 * j ) );
			hex[i * 8 + j * 2] = digits[byte >> 4];
			hex[i * 8 + j * 2 + 1] = digits[byte & 15];
		}
	}
	hex[40] = 0;
}

/*
================
G_GuidHash

The SHA-1 of "s4ndmod26:" + the client's cl_guid, as 40 lowercase hex digits.  The raw guid never leaves the server
(it is what a server uses for bans and max-lives enforcement), and this hash is public, so it must never be
accepted as proof of who someone is: linking it to an account is done with a one-time code typed in game.

out is left empty (and false returned) for a player with no usable guid: bots, clients without a key file (the
engine sets cl_guid to "" then, or "NO_GUID" in PunkBuster builds) and anything too short to be a real key hash.
================
*/
qboolean G_GuidHash( const char *guid, char *out, int outSize ) {
	char buf[MAX_INFO_STRING];

	if ( outSize > 0 ) {
		out[0] = 0;
	}
	if ( !guid || strlen( guid ) < 32 || !Q_stricmp( guid, "NO_GUID" ) || outSize < 41 ) {
		return qfalse;
	}
	Com_sprintf( buf, sizeof( buf ), "s4ndmod26:%s", guid );
	GuidSha1Hex( (const unsigned char *)buf, strlen( buf ), out );
	return qtrue;
}
