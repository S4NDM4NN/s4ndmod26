package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"s4ndmod26/status-api/accounts"
	"s4ndmod26/status-api/accounts/accountstest"
	"s4ndmod26/status-api/demostore"
)

// Who an upload is made as: an old key, a redeemed key, or a logged-in account with the demo_upload permission.
func TestUploaderWithAccounts(t *testing.T) {
	ctx := context.Background()
	acc := accountstest.New(t)
	old := demoAccounts
	demoAccounts = acc
	t.Cleanup(func() { demoAccounts = old })

	keyFile := t.TempDir() + "/keys"
	os.WriteFile(keyFile, []byte("travis secret-for-travis-1234\nother another-secret-key-99\n"), 0o600)
	keys, err := demostore.LoadKeys(keyFile, "")
	if err != nil {
		t.Fatal(err)
	}
	s := &demoService{keys: keys}
	req := func(hdr map[string]string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		return r
	}
	who := func(r *http.Request) string {
		o, ok := s.uploader(r)
		if !ok {
			return "-"
		}
		return o
	}

	if got := who(req(nil)); got != "-" {
		t.Errorf("no credentials: %q", got)
	}
	if got := who(req(map[string]string{"X-Upload-Key": "secret-for-travis-1234"})); got != "travis" {
		t.Errorf("old key, nobody redeemed it: %q", got)
	}

	a, _ := acc.Register(ctx, "grace", "grace@example.com", "graces long password")
	tok, _, _ := acc.Login(ctx, "grace", "graces long password", "1.1.1.1")
	bearer := map[string]string{"Authorization": "Bearer " + tok}
	if got := who(req(bearer)); got != "-" {
		t.Errorf("an account without the permission: %q", got)
	}
	if err := acc.RedeemDemoKey(ctx, a.ID, "travis"); err != nil {
		t.Fatal(err)
	}
	// granted, but the email is not confirmed: neither the key nor the login works yet
	if got := who(req(map[string]string{"X-Upload-Key": "secret-for-travis-1234"})); got != "-" {
		t.Errorf("redeemed key, email unconfirmed: %q", got)
	}
	acc.MarkVerified(ctx, a.ID)
	want := accounts.OwnerName(a.ID)
	if got := who(req(map[string]string{"X-Upload-Key": "secret-for-travis-1234"})); got != want {
		t.Errorf("redeemed key: %q, want %q", got, want)
	}
	if got := who(req(bearer)); got != want {
		t.Errorf("logged in with the permission: %q, want %q", got, want)
	}
	if got := who(req(map[string]string{"X-Upload-Key": "another-secret-key-99"})); got != "other" {
		t.Errorf("a different old key: %q", got)
	}
	// taking the permission away stops both
	acc.Revoke(ctx, a.ID, accounts.PermDemoUpload)
	if got := who(req(bearer)); got != "-" {
		t.Errorf("after revoke, login: %q", got)
	}
	if got := who(req(map[string]string{"X-Upload-Key": "secret-for-travis-1234"})); got != "-" {
		t.Errorf("after revoke, redeemed key: %q", got)
	}
}
