// reads "ed <pk hex> <sig hex> <msg hex|-> <expect>" lines and checks G_Ed25519Verify against them
#include <stdio.h>
#include <string.h>
#include "g_ed25519.h"
static int unhex(const char *s, unsigned char *out, int max) {
	int n = 0;
	if (!strcmp(s, "-")) return 0;
	for (; s[0] && s[1] && n < max; s += 2, n++) { unsigned v; sscanf(s, "%2x", &v); out[n] = (unsigned char)v; }
	return n;
}
int main(void) {
	char kind[8], pk[80], sig[140], msg[600]; int expect, total = 0, bad = 0, yes = 0;
	while (scanf("%7s %79s %139s %599s %d", kind, pk, sig, msg, &expect) == 5) {
		unsigned char k[32], s[64], m[300];
		int n = unhex(msg, m, 300);
		unhex(pk, k, 32); unhex(sig, s, 64);
		int got = G_Ed25519Verify(s, m, n, k);
		total++; yes += expect;
		if (got != expect) { if (bad++ < 5) printf("MISMATCH line %d: got %d want %d (msg len %d)\n", total, got, expect, n); }
	}
	printf("%d vectors (%d valid), %d mismatches\n", total, yes, bad);
	return bad != 0;
}
