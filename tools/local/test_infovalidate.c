/* Tests Info_Validate (the game module's copy in mod/src/game/q_shared.c and the engine's in
 * iortcw/code/qcommon/q_shared.c; test_infovalidate.sh builds this against each).  ClientUserinfoChanged turns a
 * userinfo that fails it into the name "badinfo". */
#include "q_shared.h"
#include <stdio.h>
#include <stdlib.h>

void QDECL Com_Error( int level, const char *fmt, ... ) { fprintf( stderr, "Com_Error\n" ); exit( 2 ); }
void QDECL Com_Printf( const char *fmt, ... ) {}

static int bad;
static void expect( const char *s, qboolean want, const char *what ) {
	if ( Info_Validate( s ) != want ) {
		printf( "FAIL: %s: Info_Validate gave %d, want %d\n", what, !want, want );
		bad++;
	}
}

int main( void ) {
	expect( "", qtrue, "empty" );
	expect( "\\name\\Sandman\\rate\\25000", qtrue, "plain userinfo" );
	expect( "\\name\\M\xfcller\\ip\\127.0.0.1:27960", qtrue, "accented name (Latin-1 byte)" );
	expect( "\\name\\\xc3\xa9t\xc3\xa9", qtrue, "accented name (UTF-8 bytes)" );
	expect( "\\name\\a\"b", qfalse, "double quote" );
	expect( "\\name\\a;quit", qfalse, "semicolon" );
	expect( "\\name\\a\nb", qfalse, "newline" );
	expect( "\\name\\a\rb", qfalse, "carriage return" );
	expect( "\\name\\a\tb", qfalse, "tab" );
	expect( "\\name\\a\x1b[2Jb", qfalse, "escape" );
	expect( "\\name\\a\x01" "b", qfalse, "control character 1" );
	expect( "\\name\\a\x7f" "b", qfalse, "DEL" );
	expect( "\\name\\^1red ^7name", qtrue, "colour codes" );
	if ( bad ) {
		return 1;
	}
	printf( "Info_Validate: all cases right\n" );
	return 0;
}
