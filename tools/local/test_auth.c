// reads "auth <public key|-> <signature|-> <nonce hex> <address|-> <names|-> <result>" lines (from ticketvec auth, made
// with Go) and checks G_AuthVerify gives the same result; then tests the helpers and the nonce, and runs the client's
// whole side (derive, build the message, sign, encode) against the server's check
#include <stdio.h>
#include <string.h>
#include "bg_ed25519.h"
#include "g_auth_core.h"

static const char *name[] = { "ok", "format", "sig", "address" };
static int fails;
#define CHECK( cond, msg ) do { if ( !( cond ) ) { fails++; printf( "FAIL: %s (line %d)\n", msg, __LINE__ ); } } while ( 0 )

int main( void ) {
	char kind[8], pk[100], sig[200], nonceh[64], addr[300], names[300], expect[20];
	int total = 0, bad = 0, oks = 0;

	while ( scanf( "%7s %99s %199s %63s %299s %299s %19s", kind, pk, sig, nonceh, addr, names, expect ) == 7 ) {
		unsigned char nonce[16];
		char id[64], *p;
		int rc;
		for ( p = names; *p; p++ ) if ( *p == '|' ) *p = ' ';
		for ( p = addr; *p; p++ ) if ( *p == 0x1f ) *p = ' ';
		CHECK( BG_IdHexDecode( nonceh, nonce, 16 ), "nonce hex" );
		rc = G_AuthVerify( !strcmp( pk, "-" ) ? "" : pk, !strcmp( sig, "-" ) ? "" : sig, nonce, addr, !strcmp( names, "-" ) ? "" : names, id, sizeof id );
		total++;
		if ( rc == AUTH_OK ) {
			oks++;
			if ( strcmp( id, pk ) ) { bad++; printf( "key id %s is not the key %s\n", id, pk ); }
		}
		if ( strcmp( name[rc], expect ) ) {
			if ( bad++ < 5 ) printf( "MISMATCH line %d: got %s want %s (address %s names %s)\n", total, name[rc], expect, addr, names );
		}
	}
	printf( "%d identity vectors (%d accepted), %d mismatches\n", total, oks, bad );

	{   // the client's side, in C: derive the key, build the message, sign, encode; the server's check takes it
		unsigned char seed[32], pkb[32], nonce[16], msg[255], sigb[64];
		char pkt[64], sigt[128], id[64];
		int i, n;
		for ( i = 0; i < 32; i++ ) seed[i] = (unsigned char)( 7 * i + 1 );
		for ( i = 0; i < 16; i++ ) nonce[i] = (unsigned char)( 200 - i );
		BG_Ed25519PublicKey( pkb, seed );
		n = BG_IdAuthMessage( msg, sizeof msg, nonce, "Play.Example.COM:27960" );
		CHECK( n == 12 + 16 + 22, "message length" );
		CHECK( BG_Ed25519Sign( sigb, msg, n, seed ), "sign" );
		CHECK( BG_IdBase64URL( pkt, sizeof pkt, pkb, 32 ) == 43, "key text" );
		CHECK( BG_IdBase64URL( sigt, sizeof sigt, sigb, 64 ) == 86, "signature text" );
		CHECK( G_AuthVerify( pkt, sigt, nonce, "play.example.com:27960", "play.example.com:27960", id, sizeof id ) == AUTH_OK && !strcmp( id, pkt ), "the client's own signature verifies, address in either case" );
		CHECK( G_AuthVerify( pkt, sigt, nonce, "PLAY.example.com:27960", "", id, sizeof id ) == AUTH_OK, "case is ignored" );
		CHECK( G_AuthVerify( pkt, sigt, nonce, "other.example.com:27960", "", id, sizeof id ) == AUTH_SIG, "a signature for one address is not valid for another" );
		CHECK( G_AuthVerify( pkt, sigt, nonce, "play.example.com:27960", "other:1", id, sizeof id ) == AUTH_ADDRESS, "signed, but the server does not answer to that name" );
		nonce[0] ^= 1;
		CHECK( G_AuthVerify( pkt, sigt, nonce, "play.example.com:27960", "", id, sizeof id ) == AUTH_SIG, "another challenge" );
	}
	{   // helpers
		unsigned char b[8], m[255], nonce[16] = { 0 };
		char out[16];
		CHECK( BG_IdHexDecode( "00ff10Ab", b, 4 ) && b[1] == 255 && b[3] == 0xab, "hex" );
		CHECK( !BG_IdHexDecode( "00ff10A", b, 4 ) && !BG_IdHexDecode( "00ff10Ag", b, 4 ) && !BG_IdHexDecode( NULL, b, 4 ), "bad hex" );
		CHECK( BG_IdBase64URL( out, sizeof out, (const unsigned char *)"\xfb\xff", 2 ) == 3 && !strcmp( out, "-_8" ), "base64url alphabet and no padding" );
		CHECK( BG_IdBase64URL( out, sizeof out, (const unsigned char *)"f", 1 ) == 2 && !strcmp( out, "Zg" ), "one byte" );
		CHECK( BG_IdBase64URL( out, 3, (const unsigned char *)"foo", 3 ) == 0, "too small" );
		CHECK( BG_IdAuthMessage( m, sizeof m, nonce, "a b" ) == 0 && BG_IdAuthMessage( m, sizeof m, nonce, "a\tb" ) == 0, "a space or control character is not an address" );
		CHECK( BG_IdAuthMessage( m, sizeof m, nonce, "" ) == 28, "an empty address is signed as nothing" );
	}
	{   // the challenge: stable for the same inputs, different for any change, never the same as another client's
		unsigned char a[16], b[16];
		G_AuthNonce( a, "key", 1, 3, 1000 ); G_AuthNonce( b, "key", 1, 3, 1000 );
		CHECK( !memcmp( a, b, 16 ), "same inputs, same nonce" );
		G_AuthNonce( b, "key", 2, 3, 1000 ); CHECK( memcmp( a, b, 16 ), "counter" );
		G_AuthNonce( b, "key", 1, 4, 1000 ); CHECK( memcmp( a, b, 16 ), "client" );
		G_AuthNonce( b, "key", 1, 3, 1001 ); CHECK( memcmp( a, b, 16 ), "time" );
		G_AuthNonce( b, "kex", 1, 3, 1000 ); CHECK( memcmp( a, b, 16 ), "key" );
	}
	printf( "%s\n", fails ? "SOME CHECKS FAILED" : "identity helpers, the client's side and the nonce ok" );
	return bad != 0 || fails != 0;
}
