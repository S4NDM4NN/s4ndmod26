// bg_ed25519.h
#ifndef BG_ED25519_H
#define BG_ED25519_H

// 1 when sig (64 bytes) is a valid Ed25519 signature by the public key pk (32 bytes) over msg (at most 255 bytes),
// else 0.
int BG_Ed25519Verify( const unsigned char *sig, const unsigned char *msg, int msgLen, const unsigned char *pk );

// The public key (32 bytes) for a secret seed (32 bytes).  The seed is the whole secret: keep it, derive from it.
void BG_Ed25519PublicKey( unsigned char *pk, const unsigned char *seed );

// Signs msg (at most 255 bytes) with the key made from seed; sig is 64 bytes.  Returns 1, or 0 for a bad argument.
int BG_Ed25519Sign( unsigned char *sig, const unsigned char *msg, int msgLen, const unsigned char *seed );

// SHA-512 of at most 367 bytes (three blocks) into out (64 bytes).  Returns 1, or 0 for a bad argument.  For mixing
// entropy and deriving values; the one hash this file has anyway.
int BG_Sha512( unsigned char *out, const unsigned char *in, int len );

#endif
