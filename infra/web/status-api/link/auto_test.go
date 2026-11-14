package link

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"s4ndmod26/status-api/accounts/accountstest"
	"s4ndmod26/status-api/ticket"
)

// a player with a real keypair: the key is the public half, URL-safe base64, as the game writes it
type player struct {
	pub ed25519.PublicKey
	key ed25519.PrivateKey
	id  string
}

func newPlayer() player {
	pub, key, _ := ed25519.GenerateKey(nil)
	return player{pub: pub, key: key, id: base64.RawURLEncoding.EncodeToString(pub)}
}

func (p player) sign(code, server string) string {
	return base64.RawURLEncoding.EncodeToString(ed25519.Sign(p.key, LinkMessage(NormalizeCode(code), server)))
}

type fixedAuth struct{ id uint64 }

func (f fixedAuth) Authenticate(*http.Request) (ticket.Account, bool) {
	return ticket.Account{ID: f.id, Name: "t"}, true
}

type autoEnv struct {
	h        *Handler
	mux      *http.ServeMux
	srvPub   ed25519.PublicKey
	srvKey   ed25519.PrivateKey
	srvID    string
	other    string // a second listed server
	otherKey ed25519.PrivateKey
	otherPub ed25519.PublicKey
	now      time.Time
}

func newAutoEnv(t *testing.T, store Backend, account uint64) *autoEnv {
	mk := func() (ed25519.PublicKey, ed25519.PrivateKey, string) {
		pub, key, _ := ed25519.GenerateKey(nil)
		sum := sha256.Sum256(pub)
		var b [10]byte
		copy(b[:], sum[:10])
		return pub, key, ticket.ServerID(b)
	}
	e := &autoEnv{now: time.Unix(1_800_000_000, 0)}
	e.srvPub, e.srvKey, e.srvID = mk()
	e.otherPub, e.otherKey, e.other = mk()
	e.h = &Handler{Store: store, Auth: fixedAuth{account}, Servers: ticket.StaticDirectory{e.srvID: e.srvID, e.other: e.other}, Now: func() time.Time { return e.now }}
	e.mux = http.NewServeMux()
	e.h.Mount(e.mux)
	return e
}

func (e *autoEnv) post(path string, body []byte, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", path, strings.NewReader(string(body)))
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, r)
	return w
}

func (e *autoEnv) code(t *testing.T, server string) string {
	w := e.post("/api/link/auto-code", []byte(`{"server_id":"`+server+`"}`), nil)
	if w.Code != 200 {
		t.Fatalf("auto-code: %d %s", w.Code, w.Body)
	}
	var out struct {
		Code     string `json:"code"`
		ServerID string `json:"server_id"`
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	if out.ServerID != server {
		t.Fatalf("server id: %q", out.ServerID)
	}
	return out.Code
}

// claim is what a server's agent posts for a player who joined signed in; pub/key are the relaying server's.
func (e *autoEnv) claim(server string, pub ed25519.PublicKey, key ed25519.PrivateKey, p player, code, sig string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(ClaimBody{Server: server, PublicKey: base64.StdEncoding.EncodeToString(pub), Key: p.id, Code: code, Sent: e.now.Unix(), Auto: true, Sig: sig})
	return e.post("/api/link/claim", body, map[string]string{"X-S4nd-Signature": base64.StdEncoding.EncodeToString(ed25519.Sign(key, body))})
}

func runAutoFlow(t *testing.T, e *autoEnv, store Backend, account uint64) {
	p := newPlayer()
	code := e.code(t, e.srvID)

	// the player's signature is for this code and this server: the right one links the key at once, with no confirmation
	if w := e.claim(e.srvID, e.srvPub, e.srvKey, p, code, p.sign(code, e.srvID)); w.Code != 200 || !strings.Contains(w.Body.String(), `"linked"`) {
		t.Fatalf("auto claim: %d %s", w.Code, w.Body)
	}
	if got, ok := store.AccountOf(p.id); !ok || got != account {
		t.Fatalf("not linked: %d %v", got, ok)
	}
	if len(store.PendingFor(account)) != 0 {
		t.Errorf("an automatic link waited for confirmation")
	}
	// a code works once
	if w := e.claim(e.srvID, e.srvPub, e.srvKey, p, code, p.sign(code, e.srvID)); w.Code != 400 {
		t.Errorf("code reused: %d", w.Code)
	}

	// refusals: another player's signature (a server cannot swap in a key), a signature for another server, the code at another server,
	// a typed code at the automatic claim and the other way round
	q := newPlayer()
	c2 := e.code(t, e.srvID)
	if w := e.claim(e.srvID, e.srvPub, e.srvKey, q, c2, p.sign(c2, e.srvID)); w.Code != 400 {
		t.Errorf("a signature by another key: %d", w.Code)
	}
	c3 := e.code(t, e.srvID)
	if w := e.claim(e.srvID, e.srvPub, e.srvKey, q, c3, q.sign(c3, e.other)); w.Code != 400 {
		t.Errorf("a signature for another server: %d", w.Code)
	}
	c4 := e.code(t, e.srvID)
	if w := e.claim(e.other, e.otherPub, e.otherKey, q, c4, q.sign(c4, e.other)); w.Code != 400 {
		t.Errorf("the code used at another server: %d", w.Code)
	}
	if _, ok := store.AccountOf(q.id); ok {
		t.Errorf("a refused claim linked a key")
	}
	typed, _, _ := store.NewCode(account)
	if w := e.claim(e.srvID, e.srvPub, e.srvKey, q, typed, q.sign(typed, e.srvID)); w.Code != 400 {
		t.Errorf("a typed code at the automatic claim: %d", w.Code)
	}
	auto := e.code(t, e.srvID)
	if err := store.Claim(auto, q.id, e.srvID); err != ErrBadCode {
		t.Errorf("an automatic code used by hand: %v", err)
	}

	// a key linked to one account cannot be taken by another account's code
	p2 := newPlayer()
	if err := store.ClaimAuto(e.code(t, e.srvID), p2.id, e.srvID); err != nil {
		t.Fatal(err)
	}
	// only a listed server can be named; a newer code replaces the older one for the same server
	if w := e.post("/api/link/auto-code", []byte(`{"server_id":"nosuchserver"}`), nil); w.Code != 404 {
		t.Errorf("an unlisted server: %d", w.Code)
	}
	first := e.code(t, e.srvID)
	second := e.code(t, e.srvID)
	if err := store.ClaimAuto(first, newPlayer().id, e.srvID); err != ErrBadCode {
		t.Errorf("the replaced code still worked: %v", err)
	}
	if err := store.ClaimAuto(second, newPlayer().id, e.srvID); err != nil {
		t.Errorf("the newer code: %v", err)
	}
}

func TestAutoLinkFileStore(t *testing.T) {
	store := &Store{Path: filepath.Join(t.TempDir(), "l.json"), Now: func() time.Time { return time.Unix(1_800_000_000, 0) }}
	runAutoFlow(t, newAutoEnv(t, store, 7), store, 7)
}

func TestAutoLinkPostgres(t *testing.T) {
	acc := accountstest.New(t)
	ctx := context.Background()
	a, err := acc.Register(ctx, "linker", "linker@example.com", "linker has a long password")
	if err != nil {
		t.Fatal(err)
	}
	pg := NewPG(acc.Pool())
	if err := pg.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pg.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	e := newAutoEnv(t, pg, a.ID)
	pg.Now = func() time.Time { return e.now }
	runAutoFlow(t, e, pg, a.ID)

	// a typed code still needs the account holder's confirmation, and expires
	p := newPlayer()
	typed, _, _ := pg.NewCode(a.ID)
	if err := pg.Claim(typed, p.id, e.srvID); err != nil {
		t.Fatal(err)
	}
	if _, ok := pg.AccountOf(p.id); ok || len(pg.PendingFor(a.ID)) != 1 {
		t.Fatalf("typed code: linked %v pending %d", ok, len(pg.PendingFor(a.ID)))
	}
	if err := pg.Confirm(a.ID, p.id); err != nil {
		t.Fatal(err)
	}
	if _, ok := pg.AccountOf(p.id); !ok {
		t.Fatal("not linked after confirming")
	}
	// deleting the account unlinks its keys
	if err := acc.Delete(ctx, a.ID, "linker has a long password"); err != nil {
		t.Fatalf("delete the account: %v", err)
	}
	if _, ok := pg.AccountOf(p.id); ok {
		t.Errorf("a deleted account's key is still linked")
	}
}
