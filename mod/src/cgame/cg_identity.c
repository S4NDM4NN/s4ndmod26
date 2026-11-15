// cg_identity.c
//
// The player's identity: an Ed25519 key pair kept by the client.  The 32-byte seed is the whole secret and lives in
// the mod folder (identity.dat, 33 bytes: the seed, then 'w' or 's' for how good the randomness behind it was); the
// public key, URL-safe base64, is the player's id.  When the server sends s4nd_challenge <hex> the client signs it
// (with the address it connected to) and answers s4nd_auth <key> <signature> <address>.  The protocol and what is
// signed: game/bg_identity.h; the server's side: game/g_auth.c.
//
// Where the seed's randomness comes from.  cgame has no random number source, and the original engine offers none, so
// the seed is made from what is at hand: the clock, the addresses of things in the module (random where the system
// randomizes where libraries load), cl_guid, the player's name, and the jitter of frame times and mouse movement
// gathered while the game runs.  The key is only made when the first challenge arrives, so that loading and the
// first seconds of play have added to the pool.  That is far less than 256 bits and is marked 'w' (weak) in the file:
// someone who knows roughly when and how a player first connected can try the likely seeds against their (public)
// key.  Treat a weak key as an identity for stats and a nickname, not as something that protects anything.  An engine
// that can offer real randomness would write 's' (CG_Identity_EngineEntropy below is where that call goes).

#include "cg_local.h"
#include "../game/bg_ed25519.h"
#include "../game/bg_identity.h"

// ".dat": a client on a pure server may only read loose .cfg, .menu, .game and .dat files (FS_FOpenFileRead), and the
// seed has to be read back after connecting
#define SEED_FILE "identity.dat"
#define SAMPLE_BYTES 128

static struct {
	qboolean loaded;                    // a seed is in use (read from the file or made)
	unsigned char seed[32];
	unsigned char pub[32];
	char id[BG_ID_KEY_CHARS + 1];       // the public key as text
	char quality;                       // 'w' or 's'

	unsigned char pool[64];             // everything gathered so far, hashed together
	unsigned char sample[SAMPLE_BYTES]; // the latest frames' timing and mouse, waiting to be hashed in
	int sampleLen;
	int lastCmd;
	int frames;
} ident;

// pool = SHA-512( pool || data ), data of at most 300 bytes
static void Mix( const void *data, int len ) {
	unsigned char in[64 + 300];

	if ( len > 300 ) {
		len = 300;
	}
	memcpy( in, ident.pool, 64 );
	memcpy( in + 64, data, len );
	BG_Sha512( ident.pool, in, 64 + len );
}

static void MixString( const char *s ) {
	Mix( s, (int)strlen( s ) );
}

static void MixCvar( const char *name ) {
	char buf[256];

	trap_Cvar_VariableStringBuffer( name, buf, sizeof( buf ) );
	MixString( buf );
}

static void MixPointer( const void *p ) {
	unsigned long long v = (unsigned long long)(size_t)p;

	Mix( &v, sizeof( v ) );
}

// One byte from an engine with a real random source.  None of the engines this runs on offers one through the game
// interface yet: returns qfalse.  When one does, fill the 32 bytes and return qtrue; the seed is then marked strong.
static qboolean CG_Identity_EngineEntropy( unsigned char out[32] ) {
	(void)out;
	return qfalse;
}

static void Derive( void ) {
	BG_Ed25519PublicKey( ident.pub, ident.seed );
	BG_IdBase64URL( ident.id, sizeof( ident.id ), ident.pub, 32 );
	ident.loaded = qtrue;
}

static qboolean ReadSeed( void ) {
	fileHandle_t f;
	unsigned char buf[33];
	int len = trap_FS_FOpenFile( SEED_FILE, &f, FS_READ );

	if ( len != 33 ) {
		CG_Printf( "^3[s4nd]^7 no player id file (%s: %d)\n", SEED_FILE, len );
		if ( len > 0 ) {
			trap_FS_FCloseFile( f );
		}
		return qfalse;
	}
	trap_FS_Read( buf, 33, f );
	trap_FS_FCloseFile( f );
	if ( buf[32] != 'w' && buf[32] != 's' ) {
		CG_Printf( "^3[s4nd]^7 %s is not a player id file\n", SEED_FILE );
		return qfalse;
	}
	memcpy( ident.seed, buf, 32 );
	ident.quality = (char)buf[32];
	Derive();
	CG_Printf( "^3[s4nd]^7 using player id %.10s\n", ident.id );
	return qtrue;
}

static void MakeSeed( void ) {
	unsigned char buf[33];
	fileHandle_t f;
	qtime_t qt;
	int ms, i;
	char local;

	if ( CG_Identity_EngineEntropy( ident.seed ) ) {
		ident.quality = 's';
	} else {
		ident.quality = 'w';
		memset( &qt, 0, sizeof( qt ) );
		trap_RealTime( &qt );
		Mix( &qt, sizeof( qt ) );
		ms = trap_Milliseconds();
		Mix( &ms, sizeof( ms ) );
		Mix( &cg.time, sizeof( cg.time ) );
		MixPointer( &local );               // the stack, and the module's data and code
		MixPointer( &ident );
		MixPointer( (void *)MakeSeed );
		MixPointer( (void *)Mix );
		MixCvar( "cl_guid" );
		MixCvar( "name" );
		MixCvar( "cl_currentServerAddress" );
		MixCvar( "sensitivity" );
		MixCvar( "r_mode" );
		MixCvar( "model" );
		MixCvar( "com_hunkmegs" );
		MixCvar( "fs_homepath" );
		Mix( ident.sample, ident.sampleLen );
		Mix( &ident.frames, sizeof( ident.frames ) );
		// time spent in a few hashes: the machine's speed and load, in milliseconds
		for ( i = 0; i < 8; i++ ) {
			ms = trap_Milliseconds();
			Mix( &ms, sizeof( ms ) );
		}
		memcpy( ident.seed, ident.pool, 32 );
	}
	Derive();

	memcpy( buf, ident.seed, 32 );
	buf[32] = (unsigned char)ident.quality;
	if ( trap_FS_FOpenFile( SEED_FILE, &f, FS_WRITE ) >= 0 ) {
		trap_FS_Write( buf, 33, f );
		trap_FS_FCloseFile( f );
	} else {
		CG_Printf( "^3[s4nd]^7 could not write %s: this player id lasts until you quit\n", SEED_FILE );
	}
	CG_Printf( "^3[s4nd]^7 made a player id %.10s (%s)\n", ident.id,
			   ident.quality == 's' ? "strong randomness" : "weak randomness: fine for stats, do not rely on it to protect anything" );
}

// at the start of the cgame: use the seed that is already there
void CG_Identity_Init( void ) {
	memset( &ident, 0, sizeof( ident ) );
	ReadSeed();
}

// every frame: keep the timing and the mouse for the pool
void CG_Identity_Frame( void ) {
	usercmd_t cmd;
	int n, ms;

	ident.frames++;
	if ( ident.loaded ) {
		return;                 // nothing more to gather
	}
	n = trap_GetCurrentCmdNumber();
	if ( n == ident.lastCmd || !trap_GetUserCmd( n, &cmd ) ) {
		return;
	}
	ident.lastCmd = n;
	ms = trap_Milliseconds();
	if ( ident.sampleLen + 24 > SAMPLE_BYTES ) {
		Mix( ident.sample, ident.sampleLen );
		ident.sampleLen = 0;
	}
	memcpy( ident.sample + ident.sampleLen, &ms, 4 );
	memcpy( ident.sample + ident.sampleLen + 4, &cmd.serverTime, 4 );
	memcpy( ident.sample + ident.sampleLen + 8, cmd.angles, 12 );
	ident.sample[ident.sampleLen + 20] = (unsigned char)cmd.forwardmove;
	ident.sample[ident.sampleLen + 21] = (unsigned char)cmd.rightmove;
	ident.sample[ident.sampleLen + 22] = (unsigned char)cmd.buttons;
	ident.sample[ident.sampleLen + 23] = (unsigned char)cmd.wbuttons;
	ident.sampleLen += 24;
}

// s4nd_challenge <32 hex digits>: sign it and answer
void CG_Identity_Challenge( const char *hex ) {
	unsigned char nonce[BG_ID_NONCE_BYTES], msg[255], sig[64];
	char address[BG_ID_MAX_ADDRESS + 1], sigText[BG_ID_SIG_CHARS + 1];
	int n;

	if ( !BG_IdHexDecode( hex, nonce, BG_ID_NONCE_BYTES ) ) {
		CG_Printf( "^3[s4nd]^7 ignoring a malformed challenge\n" );
		return;
	}
	CG_Printf( "^3[s4nd]^7 challenge from the server\n" );
	if ( !ident.loaded ) {
		MakeSeed();
	}
	trap_Cvar_VariableStringBuffer( "cl_currentServerAddress", address, sizeof( address ) );
	n = BG_IdAuthMessage( msg, sizeof( msg ), nonce, address );
	if ( !n ) {
		// an address nobody could sign for (a space, too long): play without an identity here
		CG_Printf( "^3[s4nd]^7 no player id here: cannot sign for the server address \"%s\"\n", address );
		return;
	}
	if ( !BG_Ed25519Sign( sig, msg, n, ident.seed ) ) {
		return;
	}
	BG_IdBase64URL( sigText, sizeof( sigText ), sig, 64 );
	CG_Printf( "^3[s4nd]^7 answering the challenge for %s\n", address );
	trap_SendClientCommand( va( "s4nd_auth %s %s %s", ident.id, sigText, address[0] ? address : "-" ) );
}

// /s4nd_id: shows the player id
void CG_Identity_Show_f( void ) {
	if ( !ident.loaded ) {
		CG_Printf( "^3[s4nd]^7 no player id yet: it is made the first time a server asks for one\n" );
		return;
	}
	CG_Printf( "^3[s4nd]^7 player id (%s randomness): %s\n", ident.quality == 's' ? "strong" : "weak", ident.id );
}
