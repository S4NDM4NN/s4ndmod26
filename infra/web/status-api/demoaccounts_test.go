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

// Who an upload is made as: only a logged-in account that holds demo_upload (with a confirmed email), and the old
// keys open nothing by themselves; they only upgrade an account.
func TestUploaderIsAnAccount(t *testing.T) {
	ctx := context.Background()
	acc := accountstest.New(t)
	old := demoAccounts
	demoAccounts = acc
	t.Cleanup(func() { demoAccounts = old })

	keyFile := t.TempDir() + "/keys"
	os.WriteFile(keyFile, []byte("travis secret-for-travis-1234\n"), 0o600)
	keys, err := demostore.LoadKeys(keyFile, "")
	if err != nil {
		t.Fatal(err)
	}
	s := &demoService{keys: keys}
	who := func(hdr map[string]string) string {
		r := httptest.NewRequest("GET", "/", nil)
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		o, ok := s.uploader(r)
		if !ok {
			return "-"
		}
		return o
	}
	if got := who(nil); got != "-" {
		t.Errorf("no credentials: %q", got)
	}
	if got := who(map[string]string{"X-Upload-Key": "secret-for-travis-1234"}); got != "-" {
		t.Errorf("an old key on its own: %q", got)
	}

	a, _ := acc.Register(ctx, "grace", "grace@example.com", "graces long password")
	tok, _, _ := acc.Login(ctx, "grace", "graces long password", "1.1.1.1")
	bearer := map[string]string{"Authorization": "Bearer " + tok}
	if got := who(bearer); got != "-" {
		t.Errorf("an account without the permission: %q", got)
	}
	// the upgrade: redeeming the old key grants the permission, but it counts only once the email is confirmed
	if err := acc.RedeemDemoKey(ctx, a.ID, "travis"); err != nil {
		t.Fatal(err)
	}
	if got := who(bearer); got != "-" {
		t.Errorf("redeemed, email unconfirmed: %q", got)
	}
	acc.MarkVerified(ctx, a.ID)
	if got := who(bearer); got != accounts.OwnerName(a.ID) {
		t.Errorf("an upgraded account: %q, want %q", got, accounts.OwnerName(a.ID))
	}
	// the same through a cookie, as the pages send it
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: accounts.CookieName, Value: tok})
	if o, ok := s.uploader(r); !ok || o != accounts.OwnerName(a.ID) {
		t.Errorf("cookie: %q %v", o, ok)
	}
	acc.Revoke(ctx, a.ID, accounts.PermDemoUpload)
	if got := who(bearer); got != "-" {
		t.Errorf("after revoke: %q", got)
	}
	// with accounts off, nothing can be added
	demoAccounts = nil
	if got := who(bearer); got != "-" {
		t.Errorf("accounts off: %q", got)
	}
}
