// Package link ties a player's key (the Ed25519 public key their cgame holds, see mod/src/game/bg_identity.h) to a
// site account.
//
// The account holder asks the site for a short one-time code and types `/link CODE` in the game.  The game server
// writes the request down, its agent signs it and posts it to the site (Claim), and the site parks the key as
// pending.  The account holder then confirms the key's fingerprint on the site (Confirm), so a code that was shown to
// the wrong person cannot attach a stranger's key quietly.
package link

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	CodeTTL        = 10 * time.Minute
	PendingTTL     = 24 * time.Hour
	MaxCodes       = 5                                  // active codes per account
	MaxLinksPerAcc = 20                                 // confirmed keys per account
	KeyChars       = 43                                 // URL-safe base64 of 32 bytes
	codeAlphabet   = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no 0/O/1/I
	codeLen        = 8
)

var (
	ErrBadCode   = errors.New("that code is not valid (it may have expired or been used)")
	ErrBadKey    = errors.New("not a player key")
	ErrTooMany   = errors.New("too many open codes or links")
	ErrNotFound  = errors.New("no such pending key")
	ErrKeyLinked = errors.New("that key is already linked to an account")
)

// NormalizeCode upper-cases a code and drops the dash and spaces, so ABCD-efgh and ABCDEFGH are the same.
func NormalizeCode(s string) string {
	s = strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(s)))
	if len(s) != codeLen {
		return ""
	}
	for _, c := range s {
		if !strings.ContainsRune(codeAlphabet, c) {
			return ""
		}
	}
	return s
}

// ValidKey reports whether s looks like a player key (43 URL-safe base64 characters).
func ValidKey(s string) bool {
	if len(s) != KeyChars {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// Fingerprint is what a person compares: the start of the key (cgame shows the same ten characters).
func Fingerprint(key string) string {
	if len(key) < 10 {
		return key
	}
	return key[:10]
}

type code struct {
	Account uint64    `json:"account"`
	Expires time.Time `json:"expires"`
}

// Pending is a key that asked to be linked and awaits the account holder's confirmation.
type Pending struct {
	Key         string    `json:"key"`
	Fingerprint string    `json:"fingerprint"`
	Server      string    `json:"server"`
	Asked       time.Time `json:"asked"`
	account     uint64
}

// Link is a confirmed key.
type Link struct {
	Key         string    `json:"key"`
	Fingerprint string    `json:"fingerprint"`
	Linked      time.Time `json:"linked"`
}

// Store holds codes, pending keys and links, in memory and (with Path set) in a JSON file so a restart of the site
// keeps them.
type Store struct {
	Path string
	Now  func() time.Time

	mu      sync.Mutex
	loaded  bool
	Codes   map[string]code      `json:"codes"`   // hash of code -> account
	Pending map[string]pendingDB `json:"pending"` // key -> request
	Links   map[string]linkDB    `json:"links"`   // key -> account
}

type pendingDB struct {
	Account uint64    `json:"account"`
	Server  string    `json:"server"`
	Asked   time.Time `json:"asked"`
}
type linkDB struct {
	Account uint64    `json:"account"`
	Linked  time.Time `json:"linked"`
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// hashCode keeps live codes out of the file: it is only a lookup key, the code is single use and short lived.
func hashCode(c string) string {
	h := sha256.Sum256([]byte("s4nd-link-code\x00" + c))
	return hex.EncodeToString(h[:16])
}

func (s *Store) load() {
	if s.loaded {
		return
	}
	s.loaded = true
	s.Codes, s.Pending, s.Links = map[string]code{}, map[string]pendingDB{}, map[string]linkDB{}
	if s.Path == "" {
		return
	}
	b, err := os.ReadFile(s.Path)
	if err != nil {
		return
	}
	var f struct {
		Codes   map[string]code      `json:"codes"`
		Pending map[string]pendingDB `json:"pending"`
		Links   map[string]linkDB    `json:"links"`
	}
	if json.Unmarshal(b, &f) == nil {
		if f.Codes != nil {
			s.Codes = f.Codes
		}
		if f.Pending != nil {
			s.Pending = f.Pending
		}
		if f.Links != nil {
			s.Links = f.Links
		}
	}
}

func (s *Store) save() {
	if s.Path == "" {
		return
	}
	b, _ := json.Marshal(struct {
		Codes   map[string]code      `json:"codes"`
		Pending map[string]pendingDB `json:"pending"`
		Links   map[string]linkDB    `json:"links"`
	}{s.Codes, s.Pending, s.Links})
	if os.MkdirAll(filepath.Dir(s.Path), 0o700) != nil {
		return
	}
	tmp := s.Path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		os.Rename(tmp, s.Path)
	}
}

// expire drops what has run out; call with mu held.
func (s *Store) expire(now time.Time) {
	for k, c := range s.Codes {
		if now.After(c.Expires) {
			delete(s.Codes, k)
		}
	}
	for k, p := range s.Pending {
		if now.Sub(p.Asked) > PendingTTL {
			delete(s.Pending, k)
		}
	}
}

// NewCode makes a one-time code for account.
func (s *Store) NewCode(account uint64) (string, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	now := s.now()
	s.expire(now)
	n := 0
	for _, c := range s.Codes {
		if c.Account == account {
			n++
		}
	}
	if n >= MaxCodes {
		return "", time.Time{}, ErrTooMany
	}
	var raw [codeLen]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", time.Time{}, err
	}
	out := make([]byte, codeLen)
	for i, b := range raw {
		out[i] = codeAlphabet[int(b)%len(codeAlphabet)] // 256 mod 32 == 0: no bias
	}
	cs := string(out)
	exp := now.Add(CodeTTL)
	s.Codes[hashCode(cs)] = code{Account: account, Expires: exp}
	s.save()
	return cs[:4] + "-" + cs[4:], exp, nil
}

// Claim uses a code on behalf of a key (a signed request from a game server).  The key becomes pending for the
// code's account.
func (s *Store) Claim(rawCode, key, server string) error {
	c := NormalizeCode(rawCode)
	if c == "" || !ValidKey(key) {
		if c == "" {
			return ErrBadCode
		}
		return ErrBadKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	now := s.now()
	s.expire(now)
	h := hashCode(c)
	entry, ok := s.Codes[h]
	if !ok {
		return ErrBadCode
	}
	if l, linked := s.Links[key]; linked && l.Account != entry.Account {
		return ErrKeyLinked
	}
	delete(s.Codes, h) // single use, even if the rest fails
	s.Pending[key] = pendingDB{Account: entry.Account, Server: server, Asked: now}
	s.save()
	return nil
}

// PendingFor lists the keys waiting for account's confirmation.
func (s *Store) PendingFor(account uint64) []Pending {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	s.expire(s.now())
	var out []Pending
	for k, p := range s.Pending {
		if p.Account == account {
			out = append(out, Pending{Key: k, Fingerprint: Fingerprint(k), Server: p.Server, Asked: p.Asked})
		}
	}
	return out
}

// Confirm turns a pending key of account into a link.
func (s *Store) Confirm(account uint64, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	s.expire(s.now())
	p, ok := s.Pending[key]
	if !ok || p.Account != account {
		return ErrNotFound
	}
	n := 0
	for _, l := range s.Links {
		if l.Account == account {
			n++
		}
	}
	if n >= MaxLinksPerAcc {
		return ErrTooMany
	}
	delete(s.Pending, key)
	s.Links[key] = linkDB{Account: account, Linked: s.now()}
	s.save()
	return nil
}

// Reject drops a pending key, or removes a link, of account.
func (s *Store) Reject(account uint64, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	found := false
	if p, ok := s.Pending[key]; ok && p.Account == account {
		delete(s.Pending, key)
		found = true
	}
	if l, ok := s.Links[key]; ok && l.Account == account {
		delete(s.Links, key)
		found = true
	}
	if !found {
		return ErrNotFound
	}
	s.save()
	return nil
}

// LinksFor lists the confirmed keys of account.
func (s *Store) LinksFor(account uint64) []Link {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	var out []Link
	for k, l := range s.Links {
		if l.Account == account {
			out = append(out, Link{Key: k, Fingerprint: Fingerprint(k), Linked: l.Linked})
		}
	}
	return out
}

// AccountOf is the account a key is linked to (stats can then be shown under the account).
func (s *Store) AccountOf(key string) (uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	l, ok := s.Links[key]
	return l.Account, ok
}
