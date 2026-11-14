package link

import (
	"context"
	"crypto/ed25519"
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

func (p player) sign(code, scope string) string {
	return base64.RawURLEncoding.EncodeToString(ed25519.Sign(p.key, LinkMessage(NormalizeCode(code), scope)))
}

type fixedAuth struct {
	id uint64
	ok bool
}

func (f fixedAuth) Authenticate(*http.Request) (ticket.Account, bool) {
	return ticket.Account{ID: f.id, Name: "t"}, f.ok
}

type directEnv struct {
	h   *Handler
	mux *http.ServeMux
}

func newDirectEnv(store Backend, auth ticket.Authenticator) *directEnv {
	e := &directEnv{h: &Handler{Store: store, Auth: auth, Servers: ticket.StaticDirectory{}}}
	e.mux = http.NewServeMux()
	e.h.Mount(e.mux)
	return e
}

func (e *directEnv) post(path string, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", path, strings.NewReader(string(b)))
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, r)
	return w
}

func (e *directEnv) start(t *testing.T) string {
	w := e.post("/api/link/start", nil)
	if w.Code != 200 {
		t.Fatalf("start: %d %s", w.Code, w.Body)
	}
	var out struct {
		Code  string `json:"code"`
		Scope string `json:"scope"`
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	if out.Scope != DirectScope || NormalizeCode(out.Code) == "" {
		t.Fatalf("start answered %s", w.Body)
	}
	return out.Code
}

func (e *directEnv) direct(p player, code, sig string) *httptest.ResponseRecorder {
	return e.post("/api/link/direct", map[string]string{"key": p.id, "code": code, "sig": sig})
}

func runDirectFlow(t *testing.T, store Backend, account, other uint64) {
	e := newDirectEnv(store, fixedAuth{account, true})
	others := newDirectEnv(store, fixedAuth{other, true}) // another signed-in account
	out := newDirectEnv(store, fixedAuth{0, false})       // nobody signed in

	// the key's own signature over this code links it at once, with no confirmation, and no game server is involved
	p := newPlayer()
	code := e.start(t)
	if w := e.direct(p, strings.ToLower(code), p.sign(code, DirectScope)); w.Code != 200 || !strings.Contains(w.Body.String(), p.id) {
		t.Fatalf("direct: %d %s", w.Code, w.Body)
	}
	if got, ok := store.AccountOf(p.id); !ok || got != account {
		t.Fatalf("not linked: %d %v", got, ok)
	}
	if len(store.PendingFor(account)) != 0 {
		t.Errorf("a direct link waited for confirmation")
	}
	// a code works once
	if w := e.direct(p, code, p.sign(code, DirectScope)); w.Code != 400 {
		t.Errorf("code reused: %d", w.Code)
	}

	// refusals, each with a fresh code: another key's signature (nobody can link a key they do not hold), a signature for another scope
	// (what a relaying server or another page could have), someone else's code, a code typed in game
	q := newPlayer()
	c1 := e.start(t)
	if w := e.direct(q, c1, p.sign(c1, DirectScope)); w.Code != 400 {
		t.Errorf("a signature by another key: %d", w.Code)
	}
	c2 := e.start(t)
	if w := e.direct(q, c2, q.sign(c2, "abcdefghij234567")); w.Code != 400 {
		t.Errorf("a signature for another scope: %d", w.Code)
	}
	c3 := e.start(t)
	if w := others.direct(q, c3, q.sign(c3, DirectScope)); w.Code != 400 {
		t.Errorf("another account's code: %d", w.Code)
	}
	if w := out.post("/api/link/start", nil); w.Code != 401 {
		t.Errorf("start signed out: %d", w.Code)
	}
	if w := out.direct(q, c3, q.sign(c3, DirectScope)); w.Code != 401 {
		t.Errorf("direct signed out: %d", w.Code)
	}
	if _, ok := store.AccountOf(q.id); ok {
		t.Errorf("a refused request linked a key")
	}
	typed, _, _ := store.NewCode(account)
	if w := e.direct(q, typed, q.sign(typed, DirectScope)); w.Code != 400 {
		t.Errorf("a typed code at the direct claim: %d", w.Code)
	}
	direct := e.start(t)
	if err := store.Claim(direct, q.id, "abcdefghij234567"); err != ErrBadCode {
		t.Errorf("a direct code used as a typed one: %v", err)
	}

	// a key linked to one account cannot be taken by another account
	ok2 := e.start(t)
	r := newPlayer()
	if w := e.direct(r, ok2, r.sign(ok2, DirectScope)); w.Code != 200 {
		t.Fatalf("second key: %d", w.Code)
	}
	c4 := others.start(t)
	if w := others.direct(r, c4, r.sign(c4, DirectScope)); w.Code != 409 {
		t.Errorf("a key already linked to another account: %d", w.Code)
	}
	// the same account may link the same key again (a second browser tab), and a newer code replaces an older one
	first := e.start(t)
	second := e.start(t)
	if w := e.direct(newPlayer(), first, newPlayer().sign(first, DirectScope)); w.Code != 400 {
		t.Errorf("the replaced code still worked: %d", w.Code)
	}
	if w := e.direct(r, second, r.sign(second, DirectScope)); w.Code != 200 {
		t.Errorf("the newer code: %d", w.Code)
	}
}

func TestDirectLinkFileStore(t *testing.T) {
	store := &Store{Path: filepath.Join(t.TempDir(), "l.json")}
	runDirectFlow(t, store, 7, 8)
}

func TestDirectLinkPostgres(t *testing.T) {
	acc := accountstest.New(t)
	ctx := context.Background()
	a, err := acc.Register(ctx, "linker", "linker@example.com", "linker has a long password")
	if err != nil {
		t.Fatal(err)
	}
	b, err := acc.Register(ctx, "other", "other@example.com", "other has a long password")
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
	runDirectFlow(t, pg, a.ID, b.ID)

	// a typed code still needs the account holder's confirmation
	p := newPlayer()
	typed, _, _ := pg.NewCode(a.ID)
	if err := pg.Claim(typed, p.id, "abcdefghij234567"); err != nil {
		t.Fatal(err)
	}
	if _, ok := pg.AccountOf(p.id); ok || len(pg.PendingFor(a.ID)) != 1 {
		t.Fatalf("typed code: linked %v pending %d", ok, len(pg.PendingFor(a.ID)))
	}
	if err := pg.Confirm(a.ID, p.id); err != nil {
		t.Fatal(err)
	}
	// the link limit
	for i := 0; i < MaxLinksPerAcc+2; i++ {
		c, _, _ := pg.NewDirectCode(a.ID)
		if err := pg.ClaimDirect(a.ID, c, newPlayer().id); err == ErrTooMany {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if i == MaxLinksPerAcc+1 {
			t.Errorf("no limit on links")
		}
	}
	// deleting the account unlinks its keys
	if err := acc.Delete(ctx, a.ID, "linker has a long password"); err != nil {
		t.Fatalf("delete the account: %v", err)
	}
	if _, ok := pg.AccountOf(p.id); ok {
		t.Errorf("a deleted account's key is still linked")
	}
	_ = time.Second
}
