// Package ticket is the join ticket s4ndmod.com gives a logged-in player for one game server.
//
// A ticket says "this account may join this server, until this time", signed with the site's Ed25519 key.  It is a
// bearer token: whoever presents it is treated as the account, so it is
//   - for one server only: it carries the server's id (the agent's identity, see agent.Identity), and a server
//     rejects a ticket naming another id;
//   - short-lived: it carries its expiry;
//   - single use: it carries a unique id, which a server remembers until the ticket expires.
//
// Servers check it with the site's public key alone, so a join needs no call to the site.  The same layout is read by
// the game module in C (mod/src/game), so it is fixed-size binary, not JSON:
//
//	byte  0      version (1)
//	bytes 1-8    account id, big-endian
//	bytes 9-18   server id, the 10 bytes behind the agent's 16-letter base32 id
//	bytes 19-22  expiry, Unix seconds, big-endian
//	bytes 23-30  unique id (random)
//	bytes 31-94  Ed25519 signature over "s4nd-ticket-v1" + bytes 0-30
//
// on the wire: those 95 bytes as unpadded base64url (127 characters, safe in a console command).
package ticket

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	version     = 1
	payloadSize = 31
	// Size is the length of a ticket before base64.
	Size = payloadSize + ed25519.SignatureSize
	// Prefix is signed in front of the payload, so a signature made for a ticket can never be valid for anything else
	// the same key signs.
	Prefix = "s4nd-ticket-v1"
	// IDLen is the length of a server id as the agent prints it.
	IDLen = 16
)

var idEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

var (
	ErrFormat   = errors.New("not a ticket")
	ErrVersion  = errors.New("unknown ticket version")
	ErrSig      = errors.New("bad ticket signature")
	ErrServer   = errors.New("ticket is for another server")
	ErrExpired  = errors.New("ticket has expired")
	ErrNotYet   = errors.New("ticket is not valid yet")
	ErrBadID    = errors.New("not a server id")
	ErrLifetime = errors.New("ticket lifetime out of range")
)

// Ticket is what a verified ticket says.
type Ticket struct {
	Account uint64
	Server  string // the server id, 16 lowercase letters and digits
	Expires time.Time
	ID      [8]byte
}

// ParseServerID turns a server id as the agent prints it (16 characters of lowercase base32) into its 10 bytes.
func ParseServerID(id string) ([10]byte, error) {
	var out [10]byte
	if len(id) != IDLen {
		return out, ErrBadID
	}
	b, err := idEncoding.DecodeString(strings.ToUpper(id))
	if err != nil || len(b) != len(out) {
		return out, ErrBadID
	}
	copy(out[:], b)
	return out, nil
}

// ServerID is the inverse of ParseServerID.
func ServerID(b [10]byte) string {
	return strings.ToLower(idEncoding.EncodeToString(b[:]))
}

// MaxLifetime is the longest a ticket may live: it is a bearer token.
const MaxLifetime = 5 * time.Minute

// Issue signs a ticket for account to join the server with the given id, valid from now for ttl (at most
// MaxLifetime).
func Issue(key ed25519.PrivateKey, account uint64, serverID string, now time.Time, ttl time.Duration) (string, error) {
	if ttl <= 0 || ttl > MaxLifetime {
		return "", ErrLifetime
	}
	sid, err := ParseServerID(serverID)
	if err != nil {
		return "", err
	}
	buf := make([]byte, Size)
	buf[0] = version
	binary.BigEndian.PutUint64(buf[1:9], account)
	copy(buf[9:19], sid[:])
	binary.BigEndian.PutUint32(buf[19:23], uint32(now.Add(ttl).Unix()))
	if _, err := rand.Read(buf[23:31]); err != nil {
		return "", err
	}
	copy(buf[payloadSize:], ed25519.Sign(key, signedBytes(buf[:payloadSize])))
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func signedBytes(payload []byte) []byte {
	return append([]byte(Prefix), payload...)
}

// Verify checks token with the site's public key for the server with the given id at time now.  It does not know
// whether the ticket was used before: that is the server's to track, by Ticket.ID until Ticket.Expires.
func Verify(pub ed25519.PublicKey, token, serverID string, now time.Time) (Ticket, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != Size {
		return Ticket{}, ErrFormat
	}
	if raw[0] != version {
		return Ticket{}, ErrVersion
	}
	if !ed25519.Verify(pub, signedBytes(raw[:payloadSize]), raw[payloadSize:]) {
		return Ticket{}, ErrSig
	}
	want, err := ParseServerID(serverID)
	if err != nil {
		return Ticket{}, err
	}
	var got [10]byte
	copy(got[:], raw[9:19])
	if got != want {
		return Ticket{}, ErrServer
	}
	t := Ticket{
		Account: binary.BigEndian.Uint64(raw[1:9]),
		Server:  ServerID(got),
		Expires: time.Unix(int64(binary.BigEndian.Uint32(raw[19:23])), 0),
	}
	copy(t.ID[:], raw[23:31])
	if !now.Before(t.Expires) {
		return Ticket{}, ErrExpired
	}
	return t, nil
}

// KeyFromSeed builds the site's signing key from the 32-byte seed kept in TICKET_SIGNING_KEY (base64, standard or
// URL-safe).
func KeyFromSeed(s string) (ed25519.PrivateKey, error) {
	s = strings.TrimSpace(s)
	var seed []byte
	var err error
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if seed, err = enc.DecodeString(s); err == nil {
			break
		}
	}
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("the ticket signing key must be %d bytes, base64", ed25519.SeedSize)
	}
	return ed25519.NewKeyFromSeed(seed), nil
}
