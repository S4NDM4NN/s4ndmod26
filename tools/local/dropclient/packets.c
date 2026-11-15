/* Makes the two kinds of packet tools/local/test_dropclient.sh needs, with the engine's own msg.c and huffman.c:
 *   packets connect '<userinfo>'      the Huffman-coded out-of-band "connect" packet (NET_OutOfBandData)
 *   packets msg <serverId> <ack> <rack>   a client message body: serverId, messageAcknowledge, reliableAcknowledge, clc_EOF
 * Output goes to stdout as raw bytes. */
#include "q_shared.h"
#include "qcommon.h"
#include <stdio.h>
#include <stdlib.h>
void Com_Error(int c, const char *f, ...) { fprintf(stderr, "Com_Error\n"); exit(2); }
cvar_t *cl_shownet; 
void Com_Printf(const char *f, ...) {}
void Com_DPrintf(const char *f, ...) {}
int main(int argc, char **argv) {
	static byte buf[16384]; msg_t m; int i;
	if ( argc < 2 ) return 1;
	MSG_Init( &m, buf, sizeof buf );
	if ( !strcmp( argv[1], "connect" ) ) {
		static byte string[MAX_MSGLEN * 2]; msg_t mb; char line[1024]; int len;
		snprintf( line, sizeof line, "connect \"%s\"", argv[2] );
		len = (int)strlen( line );
		memset( string, 0xff, 4 );
		memcpy( string + 4, line, len );
		memset( &mb, 0, sizeof mb ); mb.data = string; mb.maxsize = sizeof string; mb.cursize = len + 4;
		Huff_Compress( &mb, 12 );
		fwrite( mb.data, 1, mb.cursize, stdout );
		return 0;
	} else {
		/* client packet body: serverId, messageAcknowledge, reliableAcknowledge, then EOF */
		MSG_WriteLong( &m, atoi( argv[2] ) );
		MSG_WriteLong( &m, atoi( argv[3] ) );
		MSG_WriteLong( &m, atoi( argv[4] ) );
		MSG_WriteByte( &m, 0 );
	}
	fwrite( m.data, 1, m.cursize, stdout );
	return 0;
}
