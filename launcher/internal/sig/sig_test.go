package sig

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"
)

func newKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func TestSignVerify(t *testing.T) {
	pub, priv := newKey(t)
	other, _ := newKey(t)
	h := "ab12cd"
	s := Sign(priv, h)

	if err := Verify([]ed25519.PublicKey{pub}, h, s); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	if err := Verify([]ed25519.PublicKey{other, pub}, h, s); err != nil {
		t.Fatalf("rotation: any listed key should verify: %v", err)
	}
	if err := Verify([]ed25519.PublicKey{other}, h, s); !errors.Is(err, ErrBadSig) {
		t.Errorf("wrong key: %v", err)
	}
	if err := Verify([]ed25519.PublicKey{pub}, "ab12ce", s); !errors.Is(err, ErrBadSig) {
		t.Errorf("different file hash: %v", err)
	}
	if err := Verify([]ed25519.PublicKey{pub}, h, ""); !errors.Is(err, ErrNoSig) {
		t.Errorf("missing signature: %v", err)
	}
	if err := Verify(nil, h, s); !errors.Is(err, ErrNoKeys) {
		t.Errorf("no keys: %v", err)
	}
	if err := Verify([]ed25519.PublicKey{pub}, h, "!!!not base64"); !errors.Is(err, ErrBadSig) {
		t.Errorf("garbage signature: %v", err)
	}
	// case/whitespace in the hash must not matter (hex from different tools)
	if err := Verify([]ed25519.PublicKey{pub}, " AB12CD\n", s); err != nil {
		t.Errorf("hash normalisation: %v", err)
	}
}

func TestParseKeys(t *testing.T) {
	pub, _ := newKey(t)
	line := base64.StdEncoding.EncodeToString(pub)
	keys, err := ParseKeys("# s4ndmod launcher update key\n\n" + line + "\n")
	if err != nil || len(keys) != 1 || !keys[0].Equal(pub) {
		t.Fatalf("keys=%v err=%v", keys, err)
	}
	if _, err := ParseKeys("AAAA\n"); err == nil {
		t.Error("short key should be rejected")
	}
	if keys, err := ParseKeys("# only comments\n"); err != nil || len(keys) != 0 {
		t.Errorf("comments only: %v %v", keys, err)
	}
}
