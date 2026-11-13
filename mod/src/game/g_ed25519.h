// g_ed25519.h
#ifndef G_ED25519_H
#define G_ED25519_H

// 1 when sig (64 bytes) is a valid Ed25519 signature by the public key pk (32 bytes) over msg (at most 255 bytes),
// else 0.  Verify only.
int G_Ed25519Verify( const unsigned char *sig, const unsigned char *msg, int msgLen, const unsigned char *pk );

#endif
