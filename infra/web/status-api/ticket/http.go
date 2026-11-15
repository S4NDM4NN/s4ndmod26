package ticket

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Account is who a request is made by.  Real accounts (email and password, sessions) come later; until then the only
// one is the dev test account.
type Account struct {
	ID   uint64
	Name string
}

// Authenticator says who a request is from.
type Authenticator interface {
	Authenticate(r *http.Request) (Account, bool)
}

// Directory finds a server by what the client knows about it: its id, or the address it is connecting to (ip:port for
// the desktop game, a host name for the browser).  The registry will be one; StaticDirectory stands in for it.
type Directory interface {
	Lookup(name string) (serverID string, ok bool)
}

// TestAccount is a fixed account that authenticates with one bearer token.  For development only: it is off unless
// TEST_ACCOUNT_TOKEN is set.
type TestAccount struct {
	Account Account
	Token   string
}

func (t TestAccount) Authenticate(r *http.Request) (Account, bool) {
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || got == "" {
		return Account{}, false
	}
	// hash both so the comparison takes the same time whatever the length
	a, b := sha256.Sum256([]byte(got)), sha256.Sum256([]byte(t.Token))
	if subtle.ConstantTimeCompare(a[:], b[:]) != 1 {
		return Account{}, false
	}
	return t.Account, true
}

// StaticDirectory maps names to server ids from a fixed list.
type StaticDirectory map[string]string

func (d StaticDirectory) Lookup(name string) (string, bool) {
	id, ok := d[strings.ToLower(strings.TrimSpace(name))]
	return id, ok
}

// ParseServers reads "id=addr;addr,id=addr": each server id with the addresses clients reach it at.  The id itself
// also works as a name.
func ParseServers(spec string) (StaticDirectory, error) {
	d := StaticDirectory{}
	for _, entry := range strings.Split(spec, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		id, addrs, _ := strings.Cut(entry, "=")
		id = strings.ToLower(strings.TrimSpace(id))
		if _, err := ParseServerID(id); err != nil {
			return nil, fmt.Errorf("server %q: %w", id, err)
		}
		names := []string{id}
		for _, a := range strings.Split(addrs, ";") {
			if a = strings.ToLower(strings.TrimSpace(a)); a != "" {
				names = append(names, a)
			}
		}
		for _, n := range names {
			if prev, dup := d[n]; dup && prev != id {
				return nil, fmt.Errorf("%q names two servers (%s and %s)", n, prev, id)
			}
			d[n] = id
		}
	}
	if len(d) == 0 {
		return nil, fmt.Errorf("no servers")
	}
	return d, nil
}

// Issuer is the HTTP side: POST /api/tickets and GET /api/tickets/key.
type Issuer struct {
	Key     ed25519.PrivateKey
	Auth    Authenticator
	Servers Directory
	TTL     time.Duration
	Now     func() time.Time // time.Now when nil

	mu     sync.Mutex
	recent map[uint64][]time.Time
	perMin int
}

func (i *Issuer) now() time.Time {
	if i.Now != nil {
		return i.Now()
	}
	return time.Now()
}

// allow limits how fast one account can ask for tickets (a stolen session should not mint them freely).
func (i *Issuer) allow(account uint64, now time.Time) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.recent == nil {
		i.recent = map[uint64][]time.Time{}
		if i.perMin == 0 {
			i.perMin = 30
		}
	}
	keep := i.recent[account][:0]
	for _, t := range i.recent[account] {
		if now.Sub(t) < time.Minute {
			keep = append(keep, t)
		}
	}
	if len(keep) >= i.perMin {
		i.recent[account] = keep
		return false
	}
	i.recent[account] = append(keep, now)
	return true
}

type request struct {
	Server string `json:"server"`
}

type response struct {
	Ticket   string `json:"ticket"`
	ServerID string `json:"server_id"`
	Account  uint64 `json:"account"`
	Expires  int64  `json:"expires"` // Unix seconds
}

func (i *Issuer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	acct, ok := i.Auth.Authenticate(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", "Bearer")
		jsonError(w, http.StatusUnauthorized, "log in first")
		return
	}
	var req request
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil || strings.TrimSpace(req.Server) == "" {
		jsonError(w, http.StatusBadRequest, `send {"server": "<server id, or the address you are connecting to>"}`)
		return
	}
	id, ok := i.Servers.Lookup(req.Server)
	if !ok {
		// not a registered server: the player simply plays without an identity there
		jsonError(w, http.StatusNotFound, "that server is not listed")
		return
	}
	now := i.now()
	if !i.allow(acct.ID, now) {
		jsonError(w, http.StatusTooManyRequests, "too many tickets, wait a minute")
		return
	}
	ttl := i.TTL
	if ttl <= 0 {
		ttl = 90 * time.Second
	}
	tok, err := Issue(i.Key, acct.ID, id, now, ttl)
	if err != nil {
		log.Printf("tickets: %v", err)
		jsonError(w, http.StatusInternalServerError, "could not make a ticket")
		return
	}
	log.Printf("tickets: account %d, server %s", acct.ID, id)
	writeJSON(w, response{Ticket: tok, ServerID: id, Account: acct.ID, Expires: now.Add(ttl).Unix()})
}

// Key answers with the public half of the signing key, which servers verify tickets with.
func (i *Issuer) KeyHandler(w http.ResponseWriter, r *http.Request) {
	pub := i.Key.Public().(ed25519.PublicKey)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	writeJSON(w, map[string]string{"algorithm": "ed25519", "public_key": base64.StdEncoding.EncodeToString(pub)})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// FromEnv builds the issuer from the environment, or returns nil (with the reason logged) when tickets are not set up.
//
//	TICKET_SIGNING_KEY    the site's Ed25519 seed, 32 bytes as base64 (generate: openssl rand -base64 32)
//	TICKET_SERVERS        "id=addr;addr,id=addr": the servers tickets can be made for (until the registry exists)
//	TICKET_TTL_SECONDS    how long a ticket lives (90; at most 300)
//	TEST_ACCOUNT_TOKEN    development only: a bearer token (24+ characters) that logs in as the test account
//	TEST_ACCOUNT_ID       the test account's id (1), TEST_ACCOUNT_NAME its name (testplayer)
func FromEnv() *Issuer { return FromEnvWith(nil, nil) }

// Directories looks a name up in each directory in turn.
type Directories []Directory

func (ds Directories) Lookup(name string) (string, bool) {
	for _, d := range ds {
		if d == nil {
			continue
		}
		if id, ok := d.Lookup(name); ok {
			return id, true
		}
	}
	return "", false
}

// FromEnvWith is FromEnv with a real login (auth replaces the test account, and TEST_ACCOUNT_TOKEN is not needed) and
// a registry of servers (extra) used next to the TICKET_SERVERS list, which may then be empty.
func FromEnvWith(auth Authenticator, extra Directory) *Issuer {
	seed := os.Getenv("TICKET_SIGNING_KEY")
	if seed == "" {
		return nil
	}
	key, err := KeyFromSeed(seed)
	if err != nil {
		log.Printf("tickets disabled: %v", err)
		return nil
	}
	var servers Directory
	static, err := ParseServers(os.Getenv("TICKET_SERVERS"))
	switch {
	case err == nil && extra != nil:
		servers = Directories{static, extra}
	case err == nil:
		servers = static
	case extra != nil && strings.TrimSpace(os.Getenv("TICKET_SERVERS")) == "":
		servers = extra // only the registry
	default:
		log.Printf("tickets disabled: TICKET_SERVERS: %v", err)
		return nil
	}
	if auth != nil {
		log.Printf("tickets: on, for logged-in accounts")
		return &Issuer{Key: key, Auth: auth, Servers: servers, TTL: ticketTTL()}
	}
	token := os.Getenv("TEST_ACCOUNT_TOKEN")
	if len(token) < 24 {
		log.Printf("tickets disabled: there is no login yet and TEST_ACCOUNT_TOKEN is missing or shorter than 24 characters")
		return nil
	}
	id := uint64(1)
	if v := os.Getenv("TEST_ACCOUNT_ID"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil && n > 0 {
			id = n
		}
	}
	name := os.Getenv("TEST_ACCOUNT_NAME")
	if name == "" {
		name = "testplayer"
	}
	log.Printf("tickets: on; logging in as the TEST account %q (id %d) only", name, id)
	return &Issuer{Key: key, Auth: TestAccount{Account: Account{ID: id, Name: name}, Token: token}, Servers: servers, TTL: ticketTTL()}
}

func ticketTTL() time.Duration {
	ttl := 90 * time.Second
	if v := os.Getenv("TICKET_TTL_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && time.Duration(n)*time.Second <= MaxLifetime {
			ttl = time.Duration(n) * time.Second
		}
	}
	return ttl
}
