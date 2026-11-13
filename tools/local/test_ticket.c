// reads "ticket <site pk b64> <server id> <now> <token|-> <expected>" lines (from ticketvec) and checks G_TicketVerify,
// then tests the decoders and the used-ticket table by hand
#include <stdio.h>
#include <string.h>
#include "g_ticket_core.h"

static const char *name[] = { "ok", "format", "version", "sig", "server", "expired" };

static int fails;
#define CHECK( cond, msg ) do { if ( !( cond ) ) { fails++; printf( "FAIL: %s (line %d)\n", msg, __LINE__ ); } } while ( 0 )

int main( void ) {
	char pkb[80], sid[40], token[400], expect[160], kind[8];
	unsigned int now;
	int total = 0, bad = 0, oks = 0;

	while ( scanf( "%7s %79s %39s %u %399s %159s", kind, pkb, sid, &now, token, expect ) == 6 ) {
		unsigned char pk[32], id[TICKET_ID_BYTES];
		ticketInfo_t info;
		char got[160];
		int rc, i;

		CHECK( G_TicketBase64Decode( pkb, pk, 32 ) == 32, "site key decodes" );
		CHECK( G_TicketParseServerID( sid, id ), "server id parses" );
		rc = G_TicketVerify( !strcmp( token, "-" ) ? "" : token, pk, id, now, &info );
		if ( rc == TICKET_OK ) {
			char idhex[17];
			for ( i = 0; i < 8; i++ ) sprintf( idhex + 2 * i, "%02x", info.id[i] );
			snprintf( got, sizeof( got ), "ok:%llu:%u:%s", info.account, info.expires, idhex );
			oks++;
		} else {
			snprintf( got, sizeof( got ), "%s", name[rc] );
		}
		total++;
		if ( strcmp( got, expect ) ) {
			if ( bad++ < 5 ) printf( "MISMATCH line %d: got %s want %s\n", total, got, expect );
		}
	}
	printf( "%d ticket vectors (%d accepted), %d mismatches\n", total, oks, bad );

	{   // decoders
		unsigned char b[64], id[TICKET_ID_BYTES];
		CHECK( G_TicketBase64Decode( "AQID", b, 64 ) == 3 && b[0] == 1 && b[2] == 3, "base64" );
		CHECK( G_TicketBase64Decode( "AQI=", b, 64 ) == 2, "padded" );
		CHECK( G_TicketBase64Decode( "AQI", b, 64 ) == 2, "unpadded" );
		CHECK( G_TicketBase64Decode( "-_-_", b, 64 ) == 3 && b[0] == 0xfb && b[1] == 0xff, "url-safe alphabet" );
		CHECK( G_TicketBase64Decode( "+/+/", b, 64 ) == 3 && b[0] == 0xfb && b[1] == 0xff, "standard alphabet, same bytes" );
		CHECK( G_TicketBase64Decode( "A", b, 64 ) == -1, "one character is not a byte" );
		CHECK( G_TicketBase64Decode( "AQ!D", b, 64 ) == -1, "bad character" );
		CHECK( G_TicketBase64Decode( "AQ=D", b, 64 ) == -1, "padding in the middle" );
		CHECK( G_TicketBase64Decode( "AQIDBA", b, 3 ) == -1, "too long for the buffer" );
		CHECK( G_TicketBase64Decode( NULL, b, 64 ) == -1, "null" );
		{   // the dev key has a '+': the agent hands it to the game server URL-safe (the engine splits its command line at '+')
			unsigned char std[33], url[33];
			CHECK( G_TicketBase64Decode( "INwwC5jddNA4F1U8AO7cmrlGeot3KAgPMKfqicm+FAc=", std, 33 ) == 32, "standard form of a key" );
			CHECK( G_TicketBase64Decode( "INwwC5jddNA4F1U8AO7cmrlGeot3KAgPMKfqicm-FAc", url, 33 ) == 32, "URL-safe unpadded form of the same key" );
			CHECK( memcmp( std, url, 32 ) == 0, "both forms give the same key bytes" );
		}
		CHECK( G_TicketParseServerID( "dsdov4sv6poa3pyb", id ), "dev server id" );
		CHECK( G_TicketParseServerID( "DSDOV4SV6POA3PYB", id ), "upper case" );
		CHECK( !G_TicketParseServerID( "dsdov4sv6poa3py", id ), "short" );
		CHECK( !G_TicketParseServerID( "dsdov4sv6poa3pyb0", id ), "long" );
		CHECK( !G_TicketParseServerID( "dsdov4sv6poa3py1", id ), "1 is not base32" );
		CHECK( !G_TicketParseServerID( NULL, id ), "null id" );
	}
	{   // single use
		unsigned char a[8] = { 1, 2, 3, 4, 5, 6, 7, 8 }, b[8] = { 9, 2, 3, 4, 5, 6, 7, 8 };
		G_TicketResetUsed();
		CHECK( G_TicketUse( a, 2000, 1000 ), "first use" );
		CHECK( !G_TicketUse( a, 2000, 1001 ), "second use refused" );
		CHECK( G_TicketUse( b, 2000, 1001 ), "another ticket is separate" );
		CHECK( !G_TicketUse( b, 2000, 1999 ), "still refused until it expires" );
		CHECK( G_TicketUse( a, 3000, 2000 ), "forgotten once expired (it could not be presented any more)" );
		CHECK( !G_TicketUse( a, 0, 2000 ), "an expiry of 0 is never recorded" );
		{   // fill the table past its size with live tickets: the oldest-expiring go, the newest stay refused
			int i;
			unsigned char k[8];
			G_TicketResetUsed();
			for ( i = 0; i < 1500; i++ ) {
				memset( k, 0, 8 ); k[0] = i & 255; k[1] = i >> 8;
				CHECK( G_TicketUse( k, 5000 + i, 1000 ), "fill" );
			}
			memset( k, 0, 8 ); k[0] = 1499 & 255; k[1] = 1499 >> 8;
			CHECK( !G_TicketUse( k, 5000 + 1499, 1000 ), "the newest ticket is still remembered after the table filled" );
		}
	}
	printf( "%s\n", fails ? "SOME CHECKS FAILED" : "decoders and single-use table ok" );
	return bad != 0 || fails != 0;
}
