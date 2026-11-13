// ticketvec prints test vectors for the game module's C code (mod/src/game/g_ed25519.c, g_ticket_core.c), made with
// Go's crypto/ed25519 and the ticket package that sign the real tickets.  tools/local/test_ticket.sh runs both.
//
//	ticketvec ed25519   lines: "ed <pk hex> <sig hex> <msg hex> <1|0>": 1 when Go accepts the signature
//	ticketvec tickets   lines: "ticket <site pk b64> <server id> <now> <ticket> <result>"
//	ticketvec mint <seed base64> <account> <server id> [seconds]
//	                    makes one real ticket (for trying a server by hand) and prints the site public key it
//	                    verifies with (the value for the server's TICKET_PUBLIC_KEY / s4nd_ticket_key)
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	mrand "math/rand"
	"os"
	"strings"
	"time"

	"s4ndmod26/status-api/ticket"
)

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func ed(pk, sig, msg []byte) {
	m := hex.EncodeToString(msg)
	if m == "" {
		m = "-" // keep the columns when the message is empty
	}
	fmt.Printf("ed %s %s %s %d\n", hex.EncodeToString(pk), hex.EncodeToString(sig), m, b2i(ed25519.Verify(pk, msg, sig)))
}

func flipEvery(pk, sig, msg []byte) {
	for i := 0; i < len(sig)*8; i++ {
		s := append([]byte(nil), sig...)
		s[i/8] ^= 1 << (i % 8)
		ed(pk, s, msg)
	}
	for i := 0; i < len(msg)*8; i++ {
		m := append([]byte(nil), msg...)
		m[i/8] ^= 1 << (i % 8)
		ed(pk, sig, m)
	}
	for i := 0; i < len(pk)*8; i++ {
		k := append([]byte(nil), pk...)
		k[i/8] ^= 1 << (i % 8)
		ed(k, sig, msg)
	}
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: ticketvec ed25519|tickets")
		os.Exit(2)
	}
	rng := mrand.New(mrand.NewSource(20261112)) // fixed: the same vectors every run
	read := func(n int) []byte {
		b := make([]byte, n)
		rng.Read(b)
		return b
	}
	switch os.Args[1] {
	case "ed25519":
		// the group order L, to build signatures whose S is not canonical (S + L)
		L, _ := new(big.Int).SetString("7237005577332262213973186563042994240857116359379907606001950938285454250989", 10)
		for n := 0; n < 300; n++ {
			pub, priv, _ := ed25519.GenerateKey(rng)
			// the lengths a ticket and the edge cases use, then random ones up to the limit of 255
			lengths := []int{0, 1, 45, 255}
			n2 := rng.Intn(256)
			if n%2 == 0 {
				n2 = lengths[(n/2)%len(lengths)]
			}
			msg := read(n2)
			sig := ed25519.Sign(priv, msg)
			ed(pub, sig, msg)
			if n < 4 {
				flipEvery(pub, sig, msg)
			}
			// S + L: a second encoding of the same signature, which Go refuses
			s := new(big.Int).SetBytes(reverse(sig[32:]))
			s.Add(s, L)
			sb := s.FillBytes(make([]byte, 32))
			ed(pub, append(append([]byte(nil), sig[:32]...), reverse(sb)...), msg)
			// random garbage as a signature, and as a key
			ed(pub, read(64), msg)
			ed(read(32), sig, msg)
		}
	case "tickets":
		tickets(rng)
	case "mint":
		mint(os.Args[2:])
	}
}

func mint(args []string) {
	if len(args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: ticketvec mint <seed base64> <account> <server id> [seconds]")
		os.Exit(2)
	}
	key, err := ticket.KeyFromSeed(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var account uint64
	if _, err := fmt.Sscan(args[1], &account); err != nil {
		fmt.Fprintln(os.Stderr, "account:", err)
		os.Exit(1)
	}
	ttl := 90 * time.Second
	if len(args) > 3 {
		var secs int
		if _, err := fmt.Sscan(args[3], &secs); err != nil {
			fmt.Fprintln(os.Stderr, "seconds:", err)
			os.Exit(1)
		}
		ttl = time.Duration(secs) * time.Second
	}
	tok, err := ticket.Issue(key, account, args[2], time.Now(), ttl)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("public key:", base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey)))
	fmt.Println("ticket:    ", tok)
}

// result is what a verifier must say about a ticket: "ok:<account>:<expires>:<id hex>" or the reason it is refused.
func result(pub ed25519.PublicKey, token, serverID string, now time.Time) string {
	t, err := ticket.Verify(pub, token, serverID, now)
	switch {
	case err == nil:
		return fmt.Sprintf("ok:%d:%d:%s", t.Account, t.Expires.Unix(), hex.EncodeToString(t.ID[:]))
	case errors.Is(err, ticket.ErrFormat):
		return "format"
	case errors.Is(err, ticket.ErrVersion):
		return "version"
	case errors.Is(err, ticket.ErrSig):
		return "sig"
	case errors.Is(err, ticket.ErrServer):
		return "server"
	case errors.Is(err, ticket.ErrExpired):
		return "expired"
	}
	return "other:" + err.Error()
}

func line(pub ed25519.PublicKey, serverID string, now time.Time, token string) {
	if token == "" {
		token = "-"
	}
	shown := token
	t := token
	if t == "-" {
		t = ""
	}
	fmt.Printf("ticket %s %s %d %s %s\n", base64.StdEncoding.EncodeToString(pub), serverID, now.Unix(), shown, result(pub, t, serverID, now))
}

func randomServerID(rng *mrand.Rand) string {
	var b [10]byte
	rng.Read(b[:])
	return ticket.ServerID(b)
}

func tickets(rng *mrand.Rand) {
	_, priv, _ := ed25519.GenerateKey(rng)
	pub := priv.Public().(ed25519.PublicKey)
	_, otherPriv, _ := ed25519.GenerateKey(rng)
	otherPub := otherPriv.Public().(ed25519.PublicKey)
	t0 := time.Unix(1_800_000_000, 0)

	accounts := []uint64{0, 1, 255, 256, 65535, 1 << 32, 1<<53 + 1, 1<<63 - 1, 1<<64 - 1}
	for n := 0; n < 60; n++ {
		acct := uint64(rng.Int63())<<1 | uint64(rng.Intn(2))
		if n < len(accounts) {
			acct = accounts[n]
		}
		sid, other := randomServerID(rng), randomServerID(rng)
		ttl := time.Duration(1+rng.Intn(300)) * time.Second
		tok, err := ticket.Issue(priv, acct, sid, t0, ttl)
		if err != nil {
			panic(err)
		}
		exp := t0.Add(ttl)
		line(pub, sid, t0, tok)                        // ok
		line(pub, sid, exp.Add(-time.Second), tok)     // the last second: ok
		line(pub, sid, exp, tok)                       // expired at the second
		line(pub, sid, exp.Add(1000*time.Second), tok) // long expired
		line(pub, other, t0, tok)                      // another server
		line(otherPub, sid, t0, tok)                   // another site key
		if n < 4 {
			raw, _ := base64.RawURLEncoding.DecodeString(tok)
			for i := 0; i < len(raw)*8; i++ { // every single bit of a ticket
				bad := append([]byte(nil), raw...)
				bad[i/8] ^= 1 << (i % 8)
				line(pub, sid, t0, base64.RawURLEncoding.EncodeToString(bad))
			}
		}
	}
	// not tickets at all
	sid := randomServerID(rng)
	good, _ := ticket.Issue(priv, 7, sid, t0, time.Minute)
	for _, bad := range []string{"", "x", good[:126], good + "A", good[:100] + "!" + good[101:], strings.Repeat("A", 127), strings.Repeat("_", 127)} {
		line(pub, sid, t0, bad)
	}
	for i := 0; i < 40; i++ {
		b := make([]byte, rng.Intn(200))
		rng.Read(b)
		line(pub, sid, t0, base64.RawURLEncoding.EncodeToString(b))
	}
}

func reverse(b []byte) []byte {
	out := make([]byte, len(b))
	for i := range b {
		out[len(b)-1-i] = b[i]
	}
	return out
}
