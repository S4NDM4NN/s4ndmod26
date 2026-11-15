// Package sig is the launcher's update-signing scheme: ed25519 over the sha256 of the launcher binary,
// with a domain-separation prefix.  Keys and signatures are single-line base64.
package sig

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

const prefix = "s4ndmod-launcher-update-v1:"

// Message is what gets signed for a binary whose sha256 (hex) is hashHex.
func Message(hashHex string) []byte {
	return []byte(prefix + strings.ToLower(strings.TrimSpace(hashHex)))
}

// ParseKeys reads public keys, one base64 per line; blank lines and '#' comments are ignored.  More
// than one key is allowed so a key can be rotated without stranding old launchers.
func ParseKeys(text string) ([]ed25519.PublicKey, error) {
	var keys []ed25519.PublicKey
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(line)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("line %d: not a base64 ed25519 public key", i+1)
		}
		keys = append(keys, ed25519.PublicKey(raw))
	}
	return keys, nil
}

var (
	ErrNoKeys    = errors.New("this launcher has no trusted signing key")
	ErrNoSig     = errors.New("the update is not signed")
	ErrBadSig    = errors.New("the update's signature does not verify")
	errBadFormat = errors.New("malformed signature")
)

// Verify checks sigB64 over hashHex against any of keys.
func Verify(keys []ed25519.PublicKey, hashHex, sigB64 string) error {
	if len(keys) == 0 {
		return ErrNoKeys
	}
	sigB64 = strings.TrimSpace(sigB64)
	if sigB64 == "" {
		return ErrNoSig
	}
	raw, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil || len(raw) != ed25519.SignatureSize {
		return fmt.Errorf("%w: %v", ErrBadSig, errBadFormat)
	}
	msg := Message(hashHex)
	for _, k := range keys {
		if ed25519.Verify(k, msg, raw) {
			return nil
		}
	}
	return ErrBadSig
}

// Sign produces the base64 signature for hashHex.  priv is the 64-byte ed25519 private key.
func Sign(priv ed25519.PrivateKey, hashHex string) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, Message(hashHex)))
}

// ParsePrivateKey reads the single-line base64 private key written by cmd/keygen.
func ParsePrivateKey(text string) (ed25519.PrivateKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(text))
	if err != nil || len(raw) != ed25519.PrivateKeySize {
		return nil, errors.New("not a base64 ed25519 private key")
	}
	return ed25519.PrivateKey(raw), nil
}
