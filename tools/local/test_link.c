// reads "link <code as typed> <server id as given> <message hex|->" lines (from ticketvec link, made with Go's link.LinkMessage) and
// checks BG_IdLinkMessage builds the same bytes, or refuses where Go's side would not sign ("-"); then signs one with the Ed25519 code and
// checks the signature verifies the way the site does (over those bytes, with the key from the same seed)
#include <stdio.h>
#include <string.h>
#include "bg_ed25519.h"
#include "bg_identity.h"

static int fails;
#define CHECK( cond, msg ) do { if ( !( cond ) ) { fails++; printf( "FAIL: %s (line %d)\n", msg, __LINE__ ); } } while ( 0 )

int main( void ) {
	char kind[8], code[64], id[64], hex[600];
	int total = 0, bad = 0;

	while ( scanf( "%7s %63s %63s %599s", kind, code, id, hex ) == 4 ) {
		unsigned char msg[255];
		char got[600];
		int n, i;
		if ( !strcmp( id, "-" ) ) id[0] = 0;       // an empty server id
		n = BG_IdLinkMessage( msg, sizeof msg, code, id );
		got[0] = 0;
		if ( n ) {
			for ( i = 0; i < n; i++ ) sprintf( got + 2 * i, "%02x", msg[i] );
		} else {
			strcpy( got, "-" );
		}
		total++;
		if ( strcmp( got, hex ) && bad++ < 5 ) printf( "MISMATCH line %d: code %s id %s: got %s want %s\n", total, code, id, got, hex );
	}
	printf( "%d link message vectors, %d mismatches\n", total, bad );
	fails += bad;

	{   // sign one and check it verifies over the same bytes with the public key of the seed
		unsigned char seed[32], pk[32], msg[255], sig[64];
		int i, n;
		for ( i = 0; i < 32; i++ ) seed[i] = (unsigned char)( 11 * i + 3 );
		BG_Ed25519PublicKey( pk, seed );
		n = BG_IdLinkMessage( msg, sizeof msg, "abcd-efgh", "J2565D27T43VZBJL" );
		CHECK( n == 13 + 8 + 1 + 16, "message length" );
		CHECK( !memcmp( msg, "s4nd-link-v1\nABCDEFGH\nj2565d27t43vzbjl", n ), "message bytes" );
		CHECK( BG_Ed25519Sign( sig, msg, n, seed ), "sign" );
		CHECK( BG_Ed25519Verify( sig, msg, n, pk ), "the signature verifies" );
		msg[14] ^= 1;
		CHECK( !BG_Ed25519Verify( sig, msg, n, pk ), "another code does not" );
	}
	CHECK( BG_IdLinkMessage( NULL, 255, "ABCDEFGH", "x" ) == 0, "no buffer" );
	{
		unsigned char tiny[20];
		CHECK( BG_IdLinkMessage( tiny, sizeof tiny, "ABCDEFGH", "abcdefghij234567" ) == 0, "a buffer that is too small" );
	}
	if ( fails ) { printf( "%d FAILED\n", fails ); return 1; }
	printf( "link message ok\n" );
	return 0;
}
