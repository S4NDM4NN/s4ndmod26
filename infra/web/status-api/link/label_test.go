package link

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"s4ndmod26/status-api/accounts/accountstest"
)

func TestCleanLabel(t *testing.T) {
	long := strings.Repeat("é", 60)
	for in, want := range map[string]string{
		"  Chrome on Windows  ": "Chrome on Windows",
		"a\x00b\x07c\n":         "abc",
		"":                      "",
		"   ":                   "",
		long:                    strings.Repeat("é", MaxLabel),
	} {
		if got := CleanLabel(in); got != want {
			t.Errorf("CleanLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func runLabelFlow(t *testing.T, store Backend, account, other uint64) {
	e := newDirectEnv(store, fixedAuth{account, true})
	e.h.Seen = func(_ context.Context, keys []string) map[string]Seen {
		return map[string]Seen{keys[0]: {At: time.Unix(1700000000, 0), Pool: "community"}}
	}
	get := func() []Link {
		w := httptest.NewRecorder()
		e.mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/link", nil))
		var out struct{ Links []Link }
		json.Unmarshal(w.Body.Bytes(), &out)
		return out.Links
	}
	put := func(env *directEnv, key, label string) int {
		b, _ := json.Marshal(map[string]string{"label": label})
		w := httptest.NewRecorder()
		env.mux.ServeHTTP(w, httptest.NewRequest("PUT", "/api/link/"+key+"/label", strings.NewReader(string(b))))
		return w.Code
	}
	// the page sends what it is (browser) or the launcher what machine it is; it is cleaned and kept
	p := newPlayer()
	code := e.start(t)
	b, _ := json.Marshal(map[string]string{"key": p.id, "code": code, "sig": p.sign(code, DirectScope), "label": "  Firefox on\x01 Linux "})
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/link/direct", strings.NewReader(string(b))))
	if w.Code != 200 {
		t.Fatalf("direct: %d %s", w.Code, w.Body)
	}
	links := get()
	if len(links) != 1 || links[0].Label != "Firefox on Linux" {
		t.Fatalf("links %+v", links)
	}
	if links[0].Seen == nil || links[0].Seen.Pool != "community" || links[0].Seen.At.Unix() != 1700000000 {
		t.Errorf("seen %+v", links[0].Seen)
	}
	// the owner renames it; a later link of the same key (another tab) keeps the owner's name
	if c := put(e, p.id, "Living room PC"); c != 200 {
		t.Fatalf("rename: %d", c)
	}
	code = e.start(t)
	b, _ = json.Marshal(map[string]string{"key": p.id, "code": code, "sig": p.sign(code, DirectScope), "label": "Chrome on Windows"})
	w = httptest.NewRecorder()
	e.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/link/direct", strings.NewReader(string(b))))
	if w.Code != 200 {
		t.Fatalf("relink: %d", w.Code)
	}
	if got := get()[0].Label; got != "Living room PC" {
		t.Errorf("a default replaced the owner's name: %q", got)
	}
	// a link with no label stays unnamed; an empty rename clears the name
	q := newPlayer()
	code = e.start(t)
	if w := e.direct(q, code, q.sign(code, DirectScope)); w.Code != 200 {
		t.Fatalf("unlabelled: %d", w.Code)
	}
	for _, l := range get() {
		if l.Key == q.id && l.Label != "" {
			t.Errorf("unlabelled key has %q", l.Label)
		}
	}
	if c := put(e, p.id, ""); c != 200 || get()[0].Label != "" {
		t.Errorf("clearing: %d %q", c, get()[0].Label)
	}
	// nobody else can name it, and an unknown key is not found
	if c := put(newDirectEnv(store, fixedAuth{other, true}), p.id, "mine now"); c != 404 {
		t.Errorf("another account renamed it: %d", c)
	}
	if c := put(e, newPlayer().id, "x"); c != 404 {
		t.Errorf("unknown key: %d", c)
	}
	if c := put(newDirectEnv(store, fixedAuth{0, false}), p.id, "x"); c != 401 {
		t.Errorf("signed out: %d", c)
	}
	// a key confirmed from a typed /link is called "Original client"
	typed, _, _ := store.NewCode(account)
	r := newPlayer()
	if err := store.Claim(typed, r.id, "abcdefghij234567"); err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(map[string]string{"key": r.id})
	w = httptest.NewRecorder()
	e.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/link/confirm", strings.NewReader(string(b))))
	if w.Code != http.StatusOK {
		t.Fatalf("confirm: %d %s", w.Code, w.Body)
	}
	for _, l := range get() {
		if l.Key == r.id && l.Label != "Original client" {
			t.Errorf("typed link label %q", l.Label)
		}
	}
}

func TestLabelsFileStore(t *testing.T) {
	runLabelFlow(t, &Store{Path: filepath.Join(t.TempDir(), "l.json")}, 7, 8)
}

func TestLabelsPostgres(t *testing.T) {
	acc := accountstest.New(t)
	ctx := context.Background()
	a, _ := acc.Register(ctx, "labeler", "labeler@example.com", "labeler has a long password")
	b, _ := acc.Register(ctx, "labeler2", "labeler2@example.com", "labeler2 has a long password")
	pg := NewPG(acc.Pool())
	if err := pg.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	runLabelFlow(t, pg, a.ID, b.ID)
}
