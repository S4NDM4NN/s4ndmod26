package link

import (
	"bytes"
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

	"s4ndmod26/status-api/ticket"
)

const playerKey = "668LLuczGtzrbF6JYzK0rwRVyUgzzmk8xKx1tItdHYM"

type env struct {
	h      *Handler
	mux    *http.ServeMux
	srvPub ed25519.PublicKey
	srvKey ed25519.PrivateKey
	srvID  string
	now    time.Time
}

func newEnv(t *testing.T) *env {
	pub, key, _ := ed25519.GenerateKey(nil)
	sum := sha256.Sum256(pub)
	var b [10]byte
	copy(b[:], sum[:10])
	id := ticket.ServerID(b)
	dir, err := ticket.ParseServers(id + "=play.example:27960")
	if err != nil {
		t.Fatal(err)
	}
	e := &env{srvPub: pub, srvKey: key, srvID: id, now: time.Unix(1_800_000_000, 0)}
	e.h = &Handler{Store: &Store{Path: filepath.Join(t.TempDir(), "l.json"), Now: func() time.Time { return e.now }},
		Auth: ticket.TestAccount{Account: ticket.Account{ID: 7, Name: "t"}, Token: strings.Repeat("x", 24)}, Servers: dir,
		Now: func() time.Time { return e.now }}
	e.mux = http.NewServeMux()
	e.h.Mount(e.mux)
	return e
}

func (e *env) do(method, path, token string, body []byte, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, r)
	return w
}

func (e *env) claim(code, key string, sent time.Time, sign ed25519.PrivateKey) *httptest.ResponseRecorder {
	body, _ := json.Marshal(ClaimBody{Server: e.srvID, PublicKey: base64.StdEncoding.EncodeToString(e.srvPub), Key: key, Code: code, Sent: sent.Unix()})
	return e.do("POST", "/api/link/claim", "", body, map[string]string{"X-S4nd-Signature": base64.StdEncoding.EncodeToString(ed25519.Sign(sign, body))})
}

func (e *env) code(t *testing.T) string {
	w := e.do("POST", "/api/link/codes", strings.Repeat("x", 24), nil, nil)
	if w.Code != 200 {
		t.Fatalf("codes: %d %s", w.Code, w.Body)
	}
	var out struct{ Code string }
	json.Unmarshal(w.Body.Bytes(), &out)
	return out.Code
}

func TestFlow(t *testing.T) {
	e := newEnv(t)
	tok := strings.Repeat("x", 24)
	code := e.code(t)
	if w := e.claim(strings.ToLower(code), playerKey, e.now, e.srvKey); w.Code != 200 {
		t.Fatalf("claim: %d %s", w.Code, w.Body)
	}
	// a code works once
	if w := e.claim(code, playerKey, e.now, e.srvKey); w.Code != 400 {
		t.Fatalf("reuse: %d", w.Code)
	}
	w := e.do("GET", "/api/link", tok, nil, nil)
	var l struct {
		Links   []Link
		Pending []Pending
	}
	json.Unmarshal(w.Body.Bytes(), &l)
	if len(l.Links) != 0 || len(l.Pending) != 1 || l.Pending[0].Fingerprint != "668LLuczGt" {
		t.Fatalf("before confirm: %s", w.Body)
	}
	if _, ok := e.h.Store.AccountOf(playerKey); ok {
		t.Fatal("linked before confirmation")
	}
	body, _ := json.Marshal(map[string]string{"key": playerKey})
	if w := e.do("POST", "/api/link/confirm", tok, body, nil); w.Code != 200 {
		t.Fatalf("confirm: %d %s", w.Code, w.Body)
	}
	if acct, ok := e.h.Store.AccountOf(playerKey); !ok || acct != 7 {
		t.Fatal("not linked")
	}
	// survives a restart of the site
	again := &Store{Path: e.h.Store.(*Store).Path}
	if acct, ok := again.AccountOf(playerKey); !ok || acct != 7 {
		t.Fatal("link lost on reload")
	}
	if w := e.do("DELETE", "/api/link/"+playerKey, tok, nil, nil); w.Code != 204 {
		t.Fatalf("delete: %d", w.Code)
	}
	if _, ok := e.h.Store.AccountOf(playerKey); ok {
		t.Fatal("still linked")
	}
}

func TestClaimRefusals(t *testing.T) {
	e := newEnv(t)
	code := e.code(t)
	_, other, _ := ed25519.GenerateKey(nil)
	if w := e.claim(code, playerKey, e.now, other); w.Code != 401 {
		t.Errorf("wrong signer: %d", w.Code)
	}
	if w := e.claim(code, playerKey, e.now.Add(-time.Hour), e.srvKey); w.Code != 400 {
		t.Errorf("stale: %d", w.Code)
	}
	if w := e.claim(code, "short", e.now, e.srvKey); w.Code != 400 {
		t.Errorf("bad key: %d", w.Code)
	}
	if w := e.claim("AAAA-AAAA", playerKey, e.now, e.srvKey); w.Code != 400 {
		t.Errorf("unknown code: %d", w.Code)
	}
	// an unlisted server, even with a good signature
	e.h.Servers = ticket.StaticDirectory{}
	if w := e.claim(code, playerKey, e.now, e.srvKey); w.Code != 403 {
		t.Errorf("unlisted: %d", w.Code)
	}
	e.h.Servers = ticket.StaticDirectory{e.srvID: e.srvID}
	// refusals above must not have spent the code
	e.now = e.now.Add(CodeTTL + time.Second)
	if w := e.claim(code, playerKey, e.now, e.srvKey); w.Code != 400 {
		t.Errorf("expired: %d", w.Code)
	}
}

func TestAccountsAreSeparate(t *testing.T) {
	e := newEnv(t)
	code := e.code(t)
	e.claim(code, playerKey, e.now, e.srvKey)
	body, _ := json.Marshal(map[string]string{"key": playerKey})
	if w := e.do("POST", "/api/link/confirm", "", body, nil); w.Code != 401 {
		t.Errorf("no login: %d", w.Code)
	}
	if err := e.h.Store.Confirm(8, playerKey); err != ErrNotFound {
		t.Errorf("another account confirmed it: %v", err)
	}
	if err := e.h.Store.Reject(8, playerKey); err != ErrNotFound {
		t.Errorf("another account rejected it: %v", err)
	}
}

func TestCodeLimitAndForm(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < MaxCodes; i++ {
		e.code(t)
	}
	if w := e.do("POST", "/api/link/codes", strings.Repeat("x", 24), nil, nil); w.Code != 429 {
		t.Errorf("sixth code: %d", w.Code)
	}
	if NormalizeCode("abcd-efgh") != "ABCDEFGH" || NormalizeCode("ABCD-EFG0") != "" || NormalizeCode("ABC") != "" {
		t.Error("NormalizeCode")
	}
}
