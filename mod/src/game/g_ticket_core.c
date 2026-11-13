// g_ticket_core.c -- see g_ticket_core.h

#include <string.h>

#include "g_ed25519.h"
#include "g_ticket_core.h"

#define PAYLOAD_SIZE 31
static const char ticketPrefix[] = "s4nd-ticket-v1";     // what the signature covers in front of the payload

int G_TicketParseServerID( const char *id, unsigned char out[TICKET_ID_BYTES] ) {
	unsigned int acc = 0;
	int bits = 0, n = 0, i;

	if ( !id || strlen( id ) != 16 ) {
		return 0;
	}
	for ( i = 0; i < 16; i++ ) {
		int c = id[i], v;
		if ( c >= 'a' && c <= 'z' ) {
			v = c - 'a';
		} else if ( c >= 'A' && c <= 'Z' ) {
			v = c - 'A';
		} else if ( c >= '2' && c <= '7' ) {
			v = c - '2' + 26;
		} else {
			return 0;
		}
		acc = ( acc << 5 ) | (unsigned int)v;
		bits += 5;
		if ( bits >= 8 ) {
			bits -= 8;
			out[n++] = (unsigned char)( ( acc >> bits ) & 0xff );
			acc &= ( 1u << bits ) - 1;
		}
	}
	return n == TICKET_ID_BYTES;     // 16 * 5 = 80 bits: exactly 10 bytes, nothing left over
}

static int b64val( int c ) {
	if ( c >= 'A' && c <= 'Z' ) return c - 'A';
	if ( c >= 'a' && c <= 'z' ) return c - 'a' + 26;
	if ( c >= '0' && c <= '9' ) return c - '0' + 52;
	if ( c == '+' || c == '-' ) return 62;
	if ( c == '/' || c == '_' ) return 63;
	return -1;
}

int G_TicketBase64Decode( const char *in, unsigned char *out, int outMax ) {
	unsigned int acc = 0;
	int bits = 0, n = 0, chars = 0;

	if ( !in ) {
		return -1;
	}
	for ( ; *in && *in != '='; in++ ) {
		int v = b64val( (unsigned char)*in );
		if ( v < 0 ) {
			return -1;
		}
		acc = ( acc << 6 ) | (unsigned int)v;
		bits += 6;
		chars++;
		if ( bits >= 8 ) {
			bits -= 8;
			if ( n >= outMax ) {
				return -1;
			}
			out[n++] = (unsigned char)( ( acc >> bits ) & 0xff );
			acc &= ( 1u << bits ) - 1;
		}
	}
	// only '=' may follow, and a single leftover character cannot be a byte
	while ( *in == '=' ) {
		in++;
	}
	if ( *in || chars % 4 == 1 ) {
		return -1;
	}
	return n;
}

int G_TicketVerify( const char *token, const unsigned char siteKey[32], const unsigned char serverId[TICKET_ID_BYTES],
					unsigned int now, ticketInfo_t *info ) {
	unsigned char raw[TICKET_SIZE + 1], msg[sizeof( ticketPrefix ) - 1 + PAYLOAD_SIZE];
	int i, n;

	if ( !token || strlen( token ) != 127 ) {
		return TICKET_FORMAT;
	}
	n = G_TicketBase64Decode( token, raw, sizeof( raw ) );
	if ( n != TICKET_SIZE ) {
		return TICKET_FORMAT;
	}
	if ( raw[0] != 1 ) {
		return TICKET_VERSION;
	}
	memcpy( msg, ticketPrefix, sizeof( ticketPrefix ) - 1 );
	memcpy( msg + sizeof( ticketPrefix ) - 1, raw, PAYLOAD_SIZE );
	if ( !G_Ed25519Verify( raw + PAYLOAD_SIZE, msg, sizeof( msg ), siteKey ) ) {
		return TICKET_SIG;
	}
	if ( memcmp( raw + 9, serverId, TICKET_ID_BYTES ) != 0 ) {
		return TICKET_SERVER;
	}
	if ( info ) {
		info->account = 0;
		for ( i = 0; i < 8; i++ ) {
			info->account = ( info->account << 8 ) | raw[1 + i];
		}
		info->expires = ( (unsigned int)raw[19] << 24 ) | ( (unsigned int)raw[20] << 16 ) | ( (unsigned int)raw[21] << 8 ) | raw[22];
		memcpy( info->id, raw + 23, 8 );
	}
	{
		unsigned int expires = ( (unsigned int)raw[19] << 24 ) | ( (unsigned int)raw[20] << 16 ) | ( (unsigned int)raw[21] << 8 ) | raw[22];
		if ( now >= expires ) {
			return TICKET_EXPIRED;
		}
	}
	return TICKET_OK;
}

#define USED_MAX 1024       // a server sees far fewer joins than this in the 5 minutes a ticket can live

static struct {
	unsigned char id[8];
	unsigned int expires;       // 0: free
} used[USED_MAX];

void G_TicketResetUsed( void ) {
	memset( used, 0, sizeof( used ) );
}

int G_TicketUse( const unsigned char id[8], unsigned int expires, unsigned int now ) {
	int i, slot = -1, soonest = 0;

	if ( !expires ) {
		return 0;       // zero marks a free slot, and a ticket expiring at 0 was never valid
	}
	for ( i = 0; i < USED_MAX; i++ ) {
		if ( used[i].expires && used[i].expires <= now ) {
			used[i].expires = 0;                    // expired: it can no longer be presented, so forget it
		}
		if ( used[i].expires && memcmp( used[i].id, id, 8 ) == 0 ) {
			return 0;
		}
		if ( !used[i].expires ) {
			if ( slot < 0 ) {
				slot = i;
			}
		} else if ( used[i].expires < used[soonest].expires || !used[soonest].expires ) {
			soonest = i;
		}
	}
	if ( slot < 0 ) {
		slot = soonest;
	}
	memcpy( used[slot].id, id, 8 );
	used[slot].expires = expires;
	return 1;
}
