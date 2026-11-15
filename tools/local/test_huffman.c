/* Tests Huff_Decompress (iortcw/code/qcommon/huffman.c), which the server runs on unauthenticated
 * out-of-band packets (sv_main.c, SV_ConnectionlessPacket).  Built with AddressSanitizer by test_huffman.sh.
 *
 *  1. Malformed packets: every size from a header-only packet up, random bytes, a message buffer that is
 *     exactly the packet size so any read past the end hits an ASan redzone.
 *  2. Valid text packets still decode to what was compressed. */
#include "q_shared.h"
#include "qcommon.h"
#include <stdio.h>
#include <stdlib.h>

void Com_Error( int code, const char *fmt, ... ) { fprintf( stderr, "Com_Error\n" ); exit( 2 ); }
void Com_Printf( const char *fmt, ... ) {}
void Com_DPrintf( const char *fmt, ... ) {}

int main( void ) {
	setvbuf( stdout, NULL, _IONBF, 0 );
	int seed, len, n = 0, good = 0, i;

	for ( seed = 0; seed < 3000; seed++ ) {
		srand( seed );
		for ( len = 13; len <= 80; len++ ) {
			byte *data = malloc( len );
			msg_t msg;
			for ( i = 0; i < len; i++ ) {
				data[i] = rand();
			}
			data[12] = rand() & 3; /* the claimed length */
			memset( &msg, 0, sizeof( msg ) );
			msg.data = data; msg.maxsize = len; msg.cursize = len;
			Huff_Decompress( &msg, 12 );
			free( data );
			n++;
		}
	}
	printf( "malformed: %d packets, no out-of-bounds read\n", n );

	/* a one byte payload cannot hold the two byte length header */
	{
		byte *data = malloc( 13 );
		msg_t msg;
		memset( data, 0x55, 13 );
		memset( &msg, 0, sizeof( msg ) );
		msg.data = data; msg.maxsize = 13; msg.cursize = 13;
		Huff_Decompress( &msg, 12 );
		if ( msg.cursize != 12 ) {
			printf( "FAIL: one byte payload left cursize %d, want 12\n", msg.cursize );
			return 1;
		}
		free( data );
	}

	/* what the server really sends: text that compresses well */
	for ( seed = 0; seed < 500; seed++ ) {
		static const char *text = "getstatus challenge infoResponse sv_hostname map mp_beach players";
		byte orig[400], *buf = malloc( 16384 );
		int plen, tlen = (int)strlen( text );
		msg_t msg;
		srand( seed );
		plen = 250 + rand() % 150;
		for ( i = 0; i < plen; i++ ) {
			orig[i] = text[rand() % tlen];
		}
		memset( buf, 0, 16384 );
		memcpy( buf + 12, orig, plen );
		memset( &msg, 0, sizeof( msg ) );
		msg.data = buf; msg.maxsize = 16384; msg.cursize = 12 + plen;
		Huff_Compress( &msg, 12 );
		Huff_Decompress( &msg, 12 );
		if ( msg.cursize != 12 + plen || memcmp( msg.data + 12, orig, plen ) ) {
			printf( "FAIL: text packet %d did not round-trip\n", seed ); fflush( stdout ); free( buf );
			return 1;
		}
		free( buf );
		good++;
	}
	printf( "round trip: %d text packets decode to what was compressed\n", good );
	return 0;
}
