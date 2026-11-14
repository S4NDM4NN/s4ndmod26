package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSiteState is the few routes of the real site the sign-in uses: the device flow and the direct link.
type fakeSiteState struct {
	mu        sync.Mutex
	approveAs string // who the next sign-in is approved for ("" = not yet, "-" = refused)
	links     map[string]string
	codes     map[string]string // link code -> username (the session's account)
	tokens    map[string]string
	loggedOut []string
	polls     int
	n         int
}

func newFakeSite() (*fakeSiteState, *httptest.Server) {
	f := &fakeSiteState{links: map[string]string{}, codes: map[string]string{}, tokens: map[string]string{}}
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("POST /api/device/start", func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, map[string]any{"device_code": "dev-secret-0123456789abcdef", "user_code": "ABCD-EFGH", "verify_url": "https://evil.example/phish", "expires_in": 600, "interval": 1})
	})
	mux.HandleFunc("POST /api/device/poll", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.polls++
		switch f.approveAs {
		case "":
			reply(w, 202, map[string]string{"status": "pending"})
		case "-":
			reply(w, 403, map[string]string{"error": "refused"})
		case "gone":
			reply(w, 410, map[string]string{"error": "gone"})
		default:
			f.n++
			tok := "tok-" + strings.Repeat("x", 20) + string(rune('a'+f.n))
			f.tokens[tok] = f.approveAs
			reply(w, 200, map[string]any{"status": "approved", "token": tok, "username": f.approveAs})
		}
	})
	user := func(w http.ResponseWriter, r *http.Request) (string, bool) {
		f.mu.Lock()
		defer f.mu.Unlock()
		u, ok := f.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		if !ok {
			reply(w, 401, map[string]string{"error": "log in first"})
		}
		return u, ok
	}
	mux.HandleFunc("POST /api/link/start", func(w http.ResponseWriter, r *http.Request) {
		u, ok := user(w, r)
		if !ok {
			return
		}
		f.mu.Lock()
		code := "K7M2Q9XA"
		f.codes[code] = u
		f.mu.Unlock()
		reply(w, 200, map[string]any{"code": code, "scope": "direct"})
	})
	mux.HandleFunc("POST /api/link/direct", func(w http.ResponseWriter, r *http.Request) {
		u, ok := user(w, r)
		if !ok {
			return
		}
		var in struct{ Key, Code, Sig string }
		json.NewDecoder(r.Body).Decode(&in)
		pub, _ := base64.RawURLEncoding.DecodeString(in.Key)
		sig, _ := base64.RawURLEncoding.DecodeString(in.Sig)
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.codes[in.Code] != u || len(pub) != 32 || !ed25519.Verify(pub, []byte("s4nd-link-v1\n"+in.Code+"\ndirect"), sig) {
			reply(w, 400, map[string]string{"error": "bad signature"})
			return
		}
		delete(f.codes, in.Code)
		if o, taken := f.links[in.Key]; taken && o != u {
			reply(w, 409, map[string]string{"error": "that key is already linked to an account"})
			return
		}
		f.links[in.Key] = u
		reply(w, 200, map[string]string{"linked": in.Key})
	})
	mux.HandleFunc("POST /api/account/logout", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.loggedOut = append(f.loggedOut, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		f.mu.Unlock()
		w.WriteHeader(204)
	})
	return f, httptest.NewServer(mux)
}

func testAccount(t *testing.T, srv *httptest.Server) (*Account, *[]string) {
	t.Helper()
	game := t.TempDir()
	var opened []string
	a := NewAccount(func() string { return srv.URL }, func() string { return game }, func(u string) { opened = append(opened, u) })
	a.store = t.TempDir()
	a.poll = 5 * time.Millisecond
	return a, &opened
}

func signIn(t *testing.T, a *Account) AccountState {
	t.Helper()
	if err := a.SignIn(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 400; i++ {
		time.Sleep(10 * time.Millisecond)
		if s := a.State(); s.Phase == "" {
			return s
		}
	}
	t.Fatal("sign-in did not finish")
	return AccountState{}
}

func keyOf(t *testing.T, a *Account) (pub string, q byte) {
	t.Helper()
	seed, q, ok := readIdentity(a.identityFile())
	if !ok {
		t.Fatal("no identity.dat")
	}
	return pubOf(seed), q
}

func TestSignInMakesAStrongKeyWhenThereIsNone(t *testing.T) {
	site, srv := newFakeSite()
	defer srv.Close()
	a, opened := testAccount(t, srv)
	site.approveAs = "hosty"
	s := signIn(t, a)
	if s.Bad || s.Username != "hosty" {
		t.Fatalf("state %+v", s)
	}
	pub, q := keyOf(t, a)
	if q != 's' || site.links[pub] != "hosty" {
		t.Errorf("key %q quality %c links %v", pub, q, site.links)
	}
	// the page that opens is built from the site setting, not from what the site answered
	if len(*opened) != 1 || !strings.HasPrefix((*opened)[0], srv.URL+"/account/device/?code=ABCD-EFGH") {
		t.Errorf("opened %v", *opened)
	}
	if len(site.loggedOut) != 1 {
		t.Errorf("session not ended: %v", site.loggedOut)
	}
	// remembered across a restart, and the key is in the store
	b, _ := testAccount(t, srv)
	b.store = a.store
	b.load()
	if b.State().Username != "hosty" {
		t.Error("username not remembered")
	}
	if _, err := os.Stat(filepath.Join(a.keysDir(), pub+".dat")); err != nil {
		t.Errorf("key not kept: %v", err)
	}
	if fi, _ := os.Stat(a.identityFile()); fi.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Errorf("identity.dat is readable by others: %v", fi.Mode())
	}
}

func TestSignInLinksTheExistingKeyAndNeverReplacesIt(t *testing.T) {
	site, srv := newFakeSite()
	defer srv.Close()
	a, _ := testAccount(t, srv)
	seed := bytes.Repeat([]byte{7}, 32)
	if err := a.install(seed, 'w'); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(a.identityFile())
	site.approveAs = "hosty"
	s := signIn(t, a)
	if s.Bad {
		t.Fatalf("%+v", s)
	}
	after, _ := os.ReadFile(a.identityFile())
	if !bytes.Equal(before, after) {
		t.Error("the existing key was replaced")
	}
	if site.links[pubOf(seed)] != "hosty" {
		t.Errorf("the existing key was not linked: %v", site.links)
	}
	// signing in again as the same account changes nothing
	a.SignOut()
	if a.State().Username != "" {
		t.Error("sign out kept the name")
	}
	if got, _ := os.ReadFile(a.identityFile()); !bytes.Equal(got, before) {
		t.Error("sign out touched the key")
	}
	signIn(t, a)
	if got, _ := os.ReadFile(a.identityFile()); !bytes.Equal(got, before) || len(site.links) != 1 {
		t.Errorf("second sign-in changed things: %v", site.links)
	}
}

func TestSecondAccountOnTheSameComputer(t *testing.T) {
	site, srv := newFakeSite()
	defer srv.Close()
	a, _ := testAccount(t, srv)
	site.approveAs = "alice"
	signIn(t, a)
	aliceKey, _ := keyOf(t, a)

	// bob signs in on the same computer: alice's key is not his
	a.SignOut()
	site.approveAs = "bob"
	s := signIn(t, a)
	if s.Bad || !strings.Contains(s.Message, "belongs to another account") {
		t.Fatalf("state %+v", s)
	}
	bobKey, q := keyOf(t, a)
	if bobKey == aliceKey || q != 's' || site.links[bobKey] != "bob" || site.links[aliceKey] != "alice" {
		t.Fatalf("keys: alice %s bob %s links %v", aliceKey, bobKey, site.links)
	}
	if _, err := os.Stat(filepath.Join(a.keysDir(), aliceKey+".dat")); err != nil {
		t.Error("alice's key was not kept")
	}

	// alice comes back: the game goes back to her key, and bob's stays kept
	a.SignOut()
	site.approveAs = "alice"
	// the game still uses bob's key, which is linked to bob: alice gets her own key back
	s = signIn(t, a)
	if s.Bad {
		t.Fatalf("%+v", s)
	}
	back, _ := keyOf(t, a)
	if back != aliceKey {
		t.Errorf("alice's key was not restored: %s", back)
	}
	if len(site.links) != 2 {
		t.Errorf("links %v", site.links)
	}
	if _, err := os.Stat(filepath.Join(a.keysDir(), bobKey+".dat")); err != nil {
		t.Error("bob's key was not kept")
	}
}

func TestSignInOutcomes(t *testing.T) {
	site, srv := newFakeSite()
	defer srv.Close()
	a, _ := testAccount(t, srv)
	site.approveAs = "-"
	if s := signIn(t, a); !s.Bad || !strings.Contains(s.Message, "refused") || s.Username != "" {
		t.Errorf("refused: %+v", s)
	}
	if _, err := os.Stat(a.identityFile()); err == nil {
		t.Error("a refused sign-in made a key")
	}
	site.approveAs = "gone"
	if s := signIn(t, a); !s.Bad || !strings.Contains(s.Message, "expired") {
		t.Errorf("expired: %+v", s)
	}
	// cancel while waiting
	site.approveAs = ""
	if err := a.SignIn(); err != nil {
		t.Fatal(err)
	}
	if err := a.SignIn(); err != errBusy {
		t.Errorf("second sign-in at once: %v", err)
	}
	time.Sleep(30 * time.Millisecond)
	if s := a.State(); s.Phase != "waiting" || s.Code != "ABCD-EFGH" {
		t.Errorf("waiting state %+v", s)
	}
	a.Cancel()
	for i := 0; i < 100 && a.State().Phase != ""; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if s := a.State(); s.Phase != "" || !strings.Contains(s.Message, "cancelled") {
		t.Errorf("cancel: %+v", s)
	}
	// the site is down
	srv.Close()
	if s := signIn(t, a); !s.Bad || !strings.Contains(s.Message, "reach the site") {
		t.Errorf("site down: %+v", s)
	}
}
