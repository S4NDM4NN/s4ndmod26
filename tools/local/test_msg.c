/* Tests MSG_WriteBits' overflow detection (iortcw/code/qcommon/msg.c).  A message whose bit count reaches the
 * buffer size exactly must be flagged as overflowed: its length is (bit >> 3) + 1, one more than the buffer holds,
 * and whoever sends or copies `cursize` bytes then reads past the end.  The buffer is exactly maxsize bytes so
 * AddressSanitizer catches that read.  Built by test_msg.sh. */
#include "q_shared.h"
#include "qcommon.h"
#include <stdio.h>
#include <stdlib.h>

cvar_t *cl_shownet;
void Com_Error( int code, const char *fmt, ... ) { fprintf( stderr, "Com_Error\n" ); exit( 2 ); }
void Com_Printf( const char *fmt, ... ) {}
void Com_DPrintf( const char *fmt, ... ) {}

int main( void ) {
	int maxsize, kind, i, bad = 0, runs = 0;
	for ( maxsize = 4; maxsize <= 64; maxsize++ ) {
		for ( kind = 0; kind < 3; kind++ ) {
			byte *buf = malloc( maxsize ), *copy = malloc( maxsize + 1 );
			msg_t m;
			memset( buf, 0, maxsize );
			MSG_Init( &m, buf, maxsize );
			for ( i = 0; i < 2000 && !m.overflowed; i++ ) {
				if ( kind == 0 ) {
					MSG_WriteByte( &m, i * 37 );
				} else if ( kind == 1 ) {
					MSG_WriteBits( &m, i & 1, 1 );
				} else {
					MSG_WriteBits( &m, i, 5 );
				}
				if ( !m.overflowed ) {
					memcpy( copy, m.data, m.cursize ); /* what sending the message does: reads cursize bytes */
					if ( m.cursize > maxsize ) {
						bad++;
					}
				}
			}
			if ( !m.overflowed ) {
				bad++; /* 2000 writes into at most 64 bytes must overflow */
			}
			free( buf ); free( copy );
			runs++;
		}
	}
	if ( bad ) {
		printf( "FAIL: %d of %d runs had a message longer than its buffer without the overflow flag\n", bad, runs );
		return 1;
	}
	printf( "msg: %d runs, the overflow flag is always set before the length exceeds the buffer\n", runs );
	return 0;
}
