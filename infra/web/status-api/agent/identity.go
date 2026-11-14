package agent

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Identity is the server's own keypair.  The id is derived from the public key, so a registry (s4ndmod.com) can
// recognise a server it has approved by its signature alone; the private key never leaves StateDir.
type Identity struct {
	ID        string
	PublicKey ed25519.PublicKey
	key       ed25519.PrivateKey
	Ephemeral bool // could not be saved, so a new one is made on each start
}

const identityFile = "identity.pem"

// LoadIdentity reads the key from dir, or makes one and saves it there.  If dir cannot be written, the identity
// is kept in memory only (Ephemeral) and the caller should say so: the server gets a new id on every start.
func LoadIdentity(dir string) (*Identity, error) {
	path := filepath.Join(dir, identityFile)
	if b, err := os.ReadFile(path); err == nil {
		blk, _ := pem.Decode(b)
		if blk == nil || blk.Type != "S4ND AGENT ED25519 SEED" || len(blk.Bytes) != ed25519.SeedSize {
			return nil, fmt.Errorf("%s is not an agent identity", path)
		}
		return newIdentity(ed25519.NewKeyFromSeed(blk.Bytes), false), nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	data := pem.EncodeToMemory(&pem.Block{Type: "S4ND AGENT ED25519 SEED", Bytes: key.Seed()})
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return newIdentity(key, true), nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return newIdentity(key, true), nil
	}
	if err := os.Rename(tmp, path); err != nil {
		return newIdentity(key, true), nil
	}
	return newIdentity(key, false), nil
}

func newIdentity(key ed25519.PrivateKey, ephemeral bool) *Identity {
	pub := key.Public().(ed25519.PublicKey)
	sum := sha256.Sum256(pub)
	id := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:10]))
	return &Identity{ID: id, PublicKey: pub, key: key, Ephemeral: ephemeral}
}

// PublicKeyString is the public key as base64 (what a registry stores when it approves a server).
func (id *Identity) PublicKeyString() string { return base64.StdEncoding.EncodeToString(id.PublicKey) }

// Sign signs body; a registry checks it with the public key.
func (id *Identity) Sign(body []byte) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(id.key, body))
}
