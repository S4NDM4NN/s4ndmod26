// reads "sign <seed hex> <public key hex> <msg hex|-> <signature hex>" lines (from ticketvec sign, made with Go) and
// checks that BG_Ed25519PublicKey and BG_Ed25519Sign give exactly the same bytes, and that the result verifies
#include <stdio.h>
#include <string.h>
#include "bg_ed25519.h"
static int unhex(const char *s, unsigned char *out, int max) {
	int n = 0;
	if (!strcmp(s, "-")) return 0;
	for (; s[0] && s[1] && n < max; s += 2, n++) { unsigned v; sscanf(s, "%2x", &v); out[n] = (unsigned char)v; }
	return n;
}
int main(void) {
	char kind[8], seedh[80], pkh[80], msgh[600], sigh[140];
	int total = 0, bad = 0;
	while (scanf("%7s %79s %79s %599s %139s", kind, seedh, pkh, msgh, sigh) == 5) {
		unsigned char seed[32], pk[32], wantpk[32], sig[64], wantsig[64], msg[300];
		int n = unhex(msgh, msg, 300);
		unhex(seedh, seed, 32); unhex(pkh, wantpk, 32); unhex(sigh, wantsig, 64);
		BG_Ed25519PublicKey(pk, seed);
		memset(sig, 0xAA, sizeof sig);
		int ok = BG_Ed25519Sign(sig, msg, n, seed);
		total++;
		if (!ok || memcmp(pk, wantpk, 32) || memcmp(sig, wantsig, 64) || !BG_Ed25519Verify(sig, msg, n, pk)) {
			if (bad++ < 5) printf("MISMATCH line %d (msg len %d): sign %d pk %s sig %s verify %d\n", total, n, ok,
				memcmp(pk, wantpk, 32) ? "DIFFERENT" : "same", memcmp(sig, wantsig, 64) ? "DIFFERENT" : "same", BG_Ed25519Verify(sig, msg, n, pk));
		}
	}
	/* argument checks */
	{
		unsigned char seed[32] = {1}, sig[64], big[256];
		memset(big, 1, sizeof big);
		if (BG_Ed25519Sign(sig, big, 256, seed) != 0) { printf("a 256-byte message was signed\n"); bad++; }
		if (BG_Ed25519Sign(sig, NULL, 1, seed) != 0) { printf("a NULL message of length 1 was signed\n"); bad++; }
		if (BG_Ed25519Sign(sig, NULL, 0, seed) != 1) { printf("an empty message was refused\n"); bad++; }
	}
	printf("%d signing vectors, %d mismatches\n", total, bad);
	return bad != 0;
}
