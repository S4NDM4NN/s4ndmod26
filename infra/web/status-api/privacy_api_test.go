package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"s4ndmod26/status-api/accounts"
	"s4ndmod26/status-api/stats"
)

type fakeEraser struct{ erased []string }

func (f *fakeEraser) PreviewErase(_ context.Context, key string) (stats.EraseResult, error) {
	return stats.EraseResult{Games: 2, Rows: 3, Events: 5}, nil
}
func (f *fakeEraser) EraseKey(_ context.Context, key string) (stats.EraseResult, error) {
	f.erased = append(f.erased, key)
	return stats.EraseResult{Games: 2, Rows: 3, Events: 5, Index: 1}, nil
}

type privacyEnvT struct {
	mux      *http.ServeMux
	er       *fakeEraser
	unlinked []string
	deleted  []uint64
	audits   []string
	admin    bool
}

func newPrivacyEnv(dir playerDirectory) *privacyEnvT {
	e := &privacyEnvT{er: &fakeEraser{}, admin: true}
	p := &privacyAPI{
		admin: func(*http.Request) (accounts.Account, bool) {
			return accounts.Account{ID: 9, Username: "root"}, e.admin
		},
		stats:  e.er,
		dir:    dir,
		unlink: func(a uint64, k string) error { e.unlinked = append(e.unlinked, k); return nil },
		delete: func(_ context.Context, _ accounts.Account, id uint64) error {
			e.deleted = append(e.deleted, id)
			return nil
		},
		audit: func(_ context.Context, _ accounts.Account, d string) { e.audits = append(e.audits, d) },
	}
	e.mux = http.NewServeMux()
	p.mount(e.mux)
	return e
}

func (e *privacyEnvT) post(path string, body any) (int, map[string]any) {
	b, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, httptest.NewRequest("POST", path, strings.NewReader(string(b))))
	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func TestPrivacyLookupAndErase(t *testing.T) {
	e := newPrivacyEnv(fakeDir{})
	// admins only
	e.admin = false
	if c, _ := e.post("/api/admin/privacy/lookup", map[string]any{"who": keyA}); c != 403 {
		t.Errorf("non-admin lookup: %d", c)
	}
	if c, _ := e.post("/api/admin/privacy/erase", map[string]any{"who": keyA, "confirm": keyA}); c != 403 || len(e.er.erased) != 0 {
		t.Errorf("non-admin erase: %d %v", c, e.er.erased)
	}
	e.admin = true

	// a key that belongs to an account: counted alone, with the account named and the other keys mentioned
	c, out := e.post("/api/admin/privacy/lookup", map[string]any{"who": keyA})
	tg := out["target"].(map[string]any)
	if c != 200 || tg["kind"] != "key" || tg["linked_to"] != "annsmith" || tg["other_keys"] != float64(1) || len(tg["keys"].([]any)) != 1 {
		t.Fatalf("lookup key: %d %v", c, out)
	}
	if out["would_change"].(map[string]any)["games"] != float64(2) {
		t.Errorf("counts: %v", out)
	}
	// by username: the account and all its keys
	_, out = e.post("/api/admin/privacy/lookup", map[string]any{"who": "AnnSmith"})
	if tg := out["target"].(map[string]any); tg["kind"] != "account" || len(tg["keys"].([]any)) != 2 {
		t.Errorf("lookup account: %v", out)
	}
	if c, _ := e.post("/api/admin/privacy/lookup", map[string]any{"who": "nobody-here"}); c != 400 {
		t.Errorf("unknown username: %d", c)
	}
	if c, _ := e.post("/api/admin/privacy/lookup", map[string]any{"who": "x"}); c != 400 {
		t.Errorf("junk: %d", c)
	}
	// nothing was changed by looking
	if len(e.er.erased) != 0 {
		t.Fatal("a lookup erased something")
	}

	// erasing needs the confirmation
	if c, _ := e.post("/api/admin/privacy/erase", map[string]any{"who": keyA}); c != 400 {
		t.Errorf("no confirmation: %d", c)
	}
	if c, _ := e.post("/api/admin/privacy/erase", map[string]any{"who": keyA, "confirm": keyB}); c != 400 || len(e.er.erased) != 0 {
		t.Errorf("wrong confirmation: %d %v", c, e.er.erased)
	}
	// one key of an account: that key only, unlinked, the account stays
	c, out = e.post("/api/admin/privacy/erase", map[string]any{"who": keyA, "confirm": keyA})
	if c != 200 || len(e.er.erased) != 1 || e.er.erased[0] != keyA || len(e.unlinked) != 1 || len(e.deleted) != 0 {
		t.Fatalf("erase one key: %d %v erased %v unlinked %v deleted %v", c, out, e.er.erased, e.unlinked, e.deleted)
	}
	// the whole account by username, and the account itself
	e.er.erased, e.unlinked = nil, nil
	c, out = e.post("/api/admin/privacy/erase", map[string]any{"who": "annsmith", "confirm": "annsmith", "delete_account": true})
	if c != 200 || len(e.er.erased) != 2 || len(e.unlinked) != 2 || len(e.deleted) != 1 || out["account_deleted"] != true {
		t.Fatalf("erase account: %d %v erased %v deleted %v", c, out, e.er.erased, e.deleted)
	}
	// a key nobody linked: erased, nothing to unlink or delete (even if delete_account is set)
	e.er.erased, e.unlinked, e.deleted = nil, nil, nil
	if c, _ := e.post("/api/admin/privacy/erase", map[string]any{"who": keyC, "confirm": keyC, "delete_account": true}); c != 200 || len(e.er.erased) != 1 || len(e.unlinked) != 0 || len(e.deleted) != 0 {
		t.Errorf("unlinked key: %d %v %v %v", c, e.er.erased, e.unlinked, e.deleted)
	}
	// the log says what was done by key fingerprint and never by name
	for _, a := range e.audits {
		if strings.Contains(a, "annsmith") || strings.Contains(strings.ToLower(a), "ann") && !strings.Contains(a, "AAAAAAAAAA") && !strings.Contains(a, "games") {
			t.Errorf("audit text: %q", a)
		}
		if strings.Contains(a, keyA) {
			t.Errorf("the whole key is in the audit text: %q", a)
		}
	}
	if len(e.audits) != 3 {
		t.Errorf("audits: %v", e.audits)
	}
}

func TestPrivacyWithoutAccounts(t *testing.T) {
	e := newPrivacyEnv(nil)
	if c, _ := e.post("/api/admin/privacy/lookup", map[string]any{"who": "someone"}); c != 400 {
		t.Errorf("a username on a site without accounts: %d", c)
	}
	if c, _ := e.post("/api/admin/privacy/erase", map[string]any{"who": keyD, "confirm": keyD}); c != 200 || len(e.er.erased) != 1 {
		t.Errorf("a key without accounts: %d", c)
	}
}
