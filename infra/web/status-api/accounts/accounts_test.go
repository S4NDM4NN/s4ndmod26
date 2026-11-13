package accounts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPasswordHash(t *testing.T) {
	h, err := hashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("hash %q", h)
	}
	if ok, _ := checkPassword("correct horse battery", h); !ok {
		t.Error("right password refused")
	}
	if ok, _ := checkPassword("correct horse batterz", h); ok {
		t.Error("wrong password accepted")
	}
	h2, _ := hashPassword("correct horse battery")
	if h == h2 {
		t.Error("two hashes of one password are equal: no salt")
	}
	for _, bad := range []string{"", "plain", "$argon2id$v=19$m=0,t=2,p=1$AA$AA", "$bcrypt$x$y$z$w", "$argon2id$v=19$m=99999999,t=2,p=1$AA$AA"} {
		if _, err := checkPassword("x", bad); err == nil {
			t.Errorf("accepted hash %q", bad)
		}
	}
}

func TestInputs(t *testing.T) {
	for _, u := range []string{"ab", "has space", "way-too-long-username-here-x", "bad!", ""} {
		if usernameRe.MatchString(u) {
			t.Errorf("username %q accepted", u)
		}
	}
	for _, e := range []string{"", "nope", "a@b", "Name <a@b.co>", "a@b.co,c@d.co"} {
		if _, err := normalizeEmail(e); err == nil {
			t.Errorf("email %q accepted", e)
		}
	}
	if _, err := normalizeEmail("me@example.com"); err != nil {
		t.Error(err)
	}
	if checkNewPassword("short", "bob") == nil || checkNewPassword("bobbobbobbob", "bobbobbobbob") == nil || checkNewPassword(strings.Repeat("x", 200), "bob") == nil {
		t.Error("bad passwords accepted")
	}
}

func TestLimiter(t *testing.T) {
	var l limiter
	now := time.Now()
	for i := 0; i < 3; i++ {
		if !l.allow("k", 3, time.Minute, now) {
			t.Fatal("early refusal")
		}
	}
	if l.allow("k", 3, time.Minute, now) {
		t.Error("fourth allowed")
	}
	if !l.allow("k", 3, time.Minute, now.Add(2*time.Minute)) {
		t.Error("still blocked after the window")
	}
}

// The rest need a real Postgres: set STATS_TEST_DSN (tools/local/accounts_test.sh starts a throwaway one).
func newTestService(t *testing.T) *Service {
	t.Helper()
	dsn := os.Getenv("STATS_TEST_DSN")
	if dsn == "" {
		t.Skip("STATS_TEST_DSN not set")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("a_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	s := New(pool)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil { // twice is fine
		t.Fatal(err)
	}
	return s
}

type client struct {
	t   *testing.T
	mux *http.ServeMux
	tok string
}

func (c *client) do(method, path string, body any) (*httptest.ResponseRecorder, map[string]any) {
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.RemoteAddr = "10.0.0.1:1234"
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if c.tok != "" {
		r.AddCookie(&http.Cookie{Name: CookieName, Value: c.tok})
	}
	w := httptest.NewRecorder()
	c.mux.ServeHTTP(w, r)
	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

func TestAccountLifecycle(t *testing.T) {
	s := newTestService(t)
	mux := http.NewServeMux()
	(&Handler{S: s}).Mount(mux)
	c := &client{t: t, mux: mux}

	if w, _ := c.do("GET", "/api/account", nil); w.Code != 401 {
		t.Fatalf("anonymous /api/account: %d", w.Code)
	}
	reg := map[string]string{"username": "Alice", "email": "alice@example.com", "password": "long enough pw"}
	if w, out := c.do("POST", "/api/account/register", reg); w.Code != 201 || out["username"] != "Alice" {
		t.Fatalf("register: %d %v", w.Code, out)
	}
	// username and email are unique whatever the case
	for _, dup := range []map[string]string{
		{"username": "alice", "email": "other@example.com", "password": "long enough pw"},
		{"username": "Other", "email": "ALICE@example.com", "password": "long enough pw"},
	} {
		if w, _ := c.do("POST", "/api/account/register", dup); w.Code != 409 {
			t.Errorf("duplicate %v: %d", dup, w.Code)
		}
	}
	// no JSON content type: refused (a cross-site form cannot send it)
	r := httptest.NewRequest("POST", "/api/account/login", strings.NewReader(`{"login":"Alice","password":"long enough pw"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 415 {
		t.Errorf("form post: %d", w.Code)
	}

	if w, _ := c.do("POST", "/api/account/login", map[string]string{"login": "alice", "password": "wrong password"}); w.Code != 401 {
		t.Errorf("wrong password: %d", w.Code)
	}
	if w, _ := c.do("POST", "/api/account/login", map[string]string{"login": "nobody", "password": "wrong password"}); w.Code != 401 {
		t.Errorf("no such user: %d", w.Code)
	}
	w, out := c.do("POST", "/api/account/login", map[string]string{"login": "ALICE@example.com", "password": "long enough pw"})
	if w.Code != 200 {
		t.Fatalf("login: %d %v", w.Code, out)
	}
	cookie := w.Result().Cookies()
	if len(cookie) != 1 || cookie[0].Name != CookieName || !cookie[0].HttpOnly || cookie[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie %+v", cookie)
	}
	c.tok = cookie[0].Value
	// the database holds a hash of the token, not the token
	var n int
	s.pool.QueryRow(context.Background(), `SELECT count(*) FROM account_sessions WHERE token_hash = $1`, []byte(c.tok)).Scan(&n)
	if n != 0 {
		t.Error("token stored in the clear")
	}
	if w, out := c.do("GET", "/api/account", nil); w.Code != 200 || out["username"] != "Alice" || out["email_verified"] != false {
		t.Fatalf("me: %d %v", w.Code, out)
	}

	// a Bearer token works for scripts
	r = httptest.NewRequest("GET", "/api/account", nil)
	r.Header.Set("Authorization", "Bearer "+c.tok)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("bearer: %d", w.Code)
	}

	// password change ends the other sessions
	other, _, _ := s.Login(context.Background(), "alice", "long enough pw", "10.0.0.2")
	if w, _ := c.do("POST", "/api/account/password", map[string]string{"current": "nope nope nope", "new": "another long pw"}); w.Code != 401 {
		t.Errorf("change with wrong current: %d", w.Code)
	}
	if w, _ := c.do("POST", "/api/account/password", map[string]string{"current": "long enough pw", "new": "another long pw"}); w.Code != 204 {
		t.Fatalf("change: %d", w.Code)
	}
	if _, ok := s.BySession(context.Background(), other); ok {
		t.Error("another session survived a password change")
	}
	if w, _ := c.do("GET", "/api/account", nil); w.Code != 200 {
		t.Error("the session that changed the password was ended")
	}
	if _, _, err := s.Login(context.Background(), "alice", "long enough pw", "10.0.0.3"); err == nil {
		t.Error("old password still works")
	}

	// logout
	c.do("POST", "/api/account/logout", nil)
	if w, _ := c.do("GET", "/api/account", nil); w.Code != 401 {
		t.Errorf("after logout: %d", w.Code)
	}

	// delete
	_, out = c.do("POST", "/api/account/login", map[string]string{"login": "alice", "password": "another long pw"})
	c.tok, _ = out["token"].(string)
	if w, _ := c.do("DELETE", "/api/account", map[string]string{"password": "wrong wrong wrong"}); w.Code != 401 {
		t.Errorf("delete with wrong password: %d", w.Code)
	}
	if w, _ := c.do("DELETE", "/api/account", map[string]string{"password": "another long pw"}); w.Code != 204 {
		t.Fatalf("delete: %d", w.Code)
	}
	if _, err := s.ByUsername(context.Background(), "alice"); err != ErrNoAccount {
		t.Errorf("account still there: %v", err)
	}
	s.pool.QueryRow(context.Background(), `SELECT count(*) FROM account_sessions`).Scan(&n)
	if n != 0 {
		t.Errorf("%d sessions left", n)
	}
}

func TestLoginThrottle(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	if _, err := s.Register(ctx, "bob", "bob@example.com", "bobs long password"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, _, err := s.Login(ctx, "bob", "guess number "+fmt.Sprint(i), "9.9.9.9"); err != ErrLogin {
			t.Fatalf("try %d: %v", i, err)
		}
	}
	if _, _, err := s.Login(ctx, "bob", "bobs long password", "9.9.9.9"); err != ErrThrottled {
		t.Errorf("sixth try: %v", err)
	}
	// someone else's address is not locked out by it
	if _, _, err := s.Login(ctx, "bob", "bobs long password", "8.8.8.8"); err != nil {
		t.Errorf("another address: %v", err)
	}
}

func TestPermissionsAndAdmin(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	mux := http.NewServeMux()
	(&Handler{S: s}).Mount(mux)
	root, _ := s.Register(ctx, "root", "root@example.com", "root long password")
	user, _ := s.Register(ctx, "user", "user@example.com", "user long password")

	if err := s.Grant(ctx, root.ID, "wizard", 0); err != ErrBadPerm {
		t.Errorf("unknown permission: %v", err)
	}
	if err := s.Grant(ctx, 999, PermAdmin, 0); err != ErrNoAccount {
		t.Errorf("grant to nobody: %v", err)
	}
	s.Grant(ctx, root.ID, PermAdmin, 0)
	s.Grant(ctx, root.ID, PermAdmin, 0) // again: no error
	rootTok, _, _ := s.Login(ctx, "root", "root long password", "1.1.1.1")
	userTok, _, _ := s.Login(ctx, "user", "user long password", "1.1.1.2")
	rc := &client{t: t, mux: mux, tok: rootTok}
	uc := &client{t: t, mux: mux, tok: userTok}

	// admin permission counts only once the email is confirmed
	if w, _ := rc.do("GET", "/api/admin/accounts", nil); w.Code != 403 {
		t.Errorf("admin without a confirmed email: %d", w.Code)
	}
	s.MarkVerified(ctx, root.ID)
	if w, out := rc.do("GET", "/api/admin/accounts?q=us", nil); w.Code != 200 || len(out["accounts"].([]any)) != 1 || out["total"] != float64(1) {
		t.Errorf("admin list: %d %v", w.Code, out)
	}
	if w, _ := uc.do("GET", "/api/admin/accounts", nil); w.Code != 403 {
		t.Errorf("non-admin list: %d", w.Code)
	}
	path := fmt.Sprintf("/api/admin/accounts/%d/permissions/%s", user.ID, PermHostServer)
	if w, _ := uc.do("PUT", path, nil); w.Code != 403 {
		t.Errorf("non-admin grant: %d", w.Code)
	}
	if w, _ := rc.do("PUT", path, nil); w.Code != 204 {
		t.Fatalf("grant: %d", w.Code)
	}
	if a, _ := s.ByID(ctx, user.ID); len(a.Permissions) != 1 || a.Permissions[0] != PermHostServer || a.Has(PermHostServer) {
		t.Errorf("after grant: %+v (Has must be false until verified)", a)
	}
	s.MarkVerified(ctx, user.ID)
	if a, _ := s.ByID(ctx, user.ID); !a.Has(PermHostServer) || a.Has(PermAdmin) {
		t.Errorf("after verify: %+v", a)
	}
	if w, _ := rc.do("DELETE", path, nil); w.Code != 204 {
		t.Fatalf("revoke: %d", w.Code)
	}
	if a, _ := s.ByID(ctx, user.ID); len(a.Permissions) != 0 {
		t.Errorf("after revoke: %+v", a)
	}
	// an admin cannot lock the last admin out by accident
	if w, _ := rc.do("DELETE", fmt.Sprintf("/api/admin/accounts/%d/permissions/admin", root.ID), nil); w.Code != 400 {
		t.Errorf("self-revoke: %d", w.Code)
	}
	// deleting an account deletes its permissions
	s.Grant(ctx, user.ID, PermDemoUpload, 0)
	if err := s.Delete(ctx, user.ID, "user long password"); err != nil {
		t.Fatal(err)
	}
	var n int
	s.pool.QueryRow(ctx, `SELECT count(*) FROM account_permissions WHERE account_id = $1`, int64(user.ID)).Scan(&n)
	if n != 0 {
		t.Error("permissions outlived the account")
	}
}

func TestVerifyAndReset(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	mail := &Capture{}
	s.Mail, s.PublicURL = mail, "https://example.test"
	mux := http.NewServeMux()
	(&Handler{S: s}).Mount(mux)
	c := &client{t: t, mux: mux}

	c.do("POST", "/api/account/register", map[string]string{"username": "carol", "email": "carol@example.com", "password": "carols long pw"})
	m, ok := mail.Last()
	if !ok || m.To != "carol@example.com" || !strings.Contains(m.Body, "https://example.test/account/#verify=") {
		t.Fatalf("verification mail: %+v", m)
	}
	tok := mail.Token()
	if w, _ := c.do("POST", "/api/account/verify", map[string]string{"token": "nonsense"}); w.Code != 400 {
		t.Errorf("bad token: %d", w.Code)
	}
	if w, _ := c.do("POST", "/api/account/verify", map[string]string{"token": tok}); w.Code != 204 {
		t.Fatalf("verify: %d", w.Code)
	}
	if a, _ := s.ByUsername(ctx, "carol"); !a.Verified {
		t.Error("not verified")
	}
	if w, _ := c.do("POST", "/api/account/verify", map[string]string{"token": tok}); w.Code != 400 {
		t.Errorf("token used twice: %d", w.Code)
	}

	// reset: the same answer for an address with no account, and no mail for it
	before := len(mail.Mail)
	if w, _ := c.do("POST", "/api/account/reset", map[string]string{"email": "nobody@example.com"}); w.Code != 204 {
		t.Errorf("reset for nobody: %d", w.Code)
	}
	if len(mail.Mail) != before {
		t.Error("mail sent to an address with no account")
	}
	old, _, _ := s.Login(ctx, "carol", "carols long pw", "5.5.5.5")
	if w, _ := c.do("POST", "/api/account/reset", map[string]string{"email": "Carol@Example.com"}); w.Code != 204 {
		t.Fatalf("reset: %d", w.Code)
	}
	if m, _ := mail.Last(); !strings.Contains(m.Body, "#reset=") {
		t.Fatalf("reset mail: %+v", m)
	}
	rt := mail.Token()
	if w, _ := c.do("POST", "/api/account/reset/confirm", map[string]string{"token": rt, "new": "short"}); w.Code != 400 {
		t.Errorf("weak new password: %d", w.Code)
	}
	if w, _ := c.do("POST", "/api/account/reset/confirm", map[string]string{"token": rt, "new": "a brand new password"}); w.Code != 204 {
		t.Fatalf("confirm (the token must survive a rejected password): %d", w.Code)
	}
	if _, ok := s.BySession(ctx, old); ok {
		t.Error("a session survived the reset")
	}
	if _, _, err := s.Login(ctx, "carol", "a brand new password", "5.5.5.6"); err != nil {
		t.Errorf("new password: %v", err)
	}
	if w, _ := c.do("POST", "/api/account/reset/confirm", map[string]string{"token": rt, "new": "yet another password"}); w.Code != 400 {
		t.Errorf("reset token used twice: %d", w.Code)
	}

	// an expired token
	now := time.Now()
	s.Now = func() time.Time { return now }
	s.RequestReset(ctx, "carol@example.com", "7.7.7.7")
	rt = mail.Token()
	s.Now = func() time.Time { return now.Add(2 * time.Hour) }
	if err := s.ConfirmReset(ctx, rt, "late late late pw"); err != ErrToken {
		t.Errorf("expired reset: %v", err)
	}
	s.Now = nil

	// a reset proves the address too
	d, _ := s.Register(ctx, "dave", "dave@example.com", "daves long password")
	s.RequestReset(ctx, "dave@example.com", "7.7.7.8")
	if err := s.ConfirmReset(ctx, mail.Token(), "daves new password"); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.ByID(ctx, d.ID); !a.Verified {
		t.Error("reset did not confirm the address")
	}
}

func TestRedeemDemoKey(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	mux := http.NewServeMux()
	var adopted [][2]string
	h := &Handler{S: s}
	h.Mount(mux)
	c := &client{t: t, mux: mux}

	eve, _ := s.Register(ctx, "eve", "eve@example.com", "eves long password")
	frank, _ := s.Register(ctx, "frank", "frank@example.com", "franks long password")
	c.tok, _, _ = s.Login(ctx, "eve", "eves long password", "3.3.3.3")
	if w, _ := c.do("POST", "/api/account/demo-key", map[string]string{"key": "x"}); w.Code != 404 {
		t.Errorf("demo tool off: %d", w.Code)
	}
	h.DemoKeyLabel = func(secret string) (string, bool) {
		if secret == "secret-for-travis-1234" {
			return "travis", true
		}
		return "", false
	}
	h.AdoptDemos = func(_ context.Context, label, owner string) error {
		adopted = append(adopted, [2]string{label, owner})
		return nil
	}
	if w, _ := c.do("POST", "/api/account/demo-key", map[string]string{"key": "guess"}); w.Code != 400 {
		t.Errorf("wrong key: %d", w.Code)
	}
	if w, out := c.do("POST", "/api/account/demo-key", map[string]string{"key": "secret-for-travis-1234"}); w.Code != 200 || out["label"] != "travis" {
		t.Fatalf("redeem: %d %v", w.Code, out)
	}
	if len(adopted) != 1 || adopted[0] != [2]string{"travis", OwnerName(eve.ID)} {
		t.Errorf("adopted %v", adopted)
	}
	a, _ := s.ByID(ctx, eve.ID)
	if len(a.Permissions) != 1 || a.Permissions[0] != PermDemoUpload || a.Has(PermDemoUpload) {
		t.Errorf("after redeem: %+v (granted, but not effective before the email is confirmed)", a)
	}
	// again from the same account is fine; another account cannot take the key
	if w, _ := c.do("POST", "/api/account/demo-key", map[string]string{"key": "secret-for-travis-1234"}); w.Code != 200 {
		t.Errorf("redeem twice: %d", w.Code)
	}
	fc := &client{t: t, mux: mux}
	fc.tok, _, _ = s.Login(ctx, "frank", "franks long password", "3.3.3.4")
	if w, _ := fc.do("POST", "/api/account/demo-key", map[string]string{"key": "secret-for-travis-1234"}); w.Code != 409 {
		t.Errorf("second account: %d", w.Code)
	}
	if id, ok := s.AccountOfDemoKey(ctx, "travis"); !ok || id != eve.ID {
		t.Errorf("AccountOfDemoKey: %d %v", id, ok)
	}
	if a, _ := s.ByID(ctx, frank.ID); len(a.Permissions) != 0 {
		t.Errorf("the refused account got %v", a.Permissions)
	}
	if _, out := c.do("GET", "/api/account", nil); fmt.Sprint(out["demo_keys"]) != "[travis]" {
		t.Errorf("/api/account demo_keys: %v", out["demo_keys"])
	}
}

func TestAdminListPaging(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	mux := http.NewServeMux()
	(&Handler{S: s}).Mount(mux)
	root, _ := s.Register(ctx, "root", "root@example.com", "root long password")
	s.Grant(ctx, root.ID, PermAdmin, 0)
	s.MarkVerified(ctx, root.ID)
	for i := 0; i < 11; i++ {
		if _, err := s.Register(ctx, fmt.Sprintf("player%02d", i), fmt.Sprintf("p%02d@example.com", i), "player long password"); err != nil {
			t.Fatal(err)
		}
	}
	tok, _, _ := s.Login(ctx, "root", "root long password", "2.2.2.2")
	c := &client{t: t, mux: mux, tok: tok}
	seen := map[string]bool{}
	for page := 1; page <= 3; page++ {
		w, out := c.do("GET", fmt.Sprintf("/api/admin/accounts?page=%d&per=5", page), nil)
		if w.Code != 200 || out["total"] != float64(12) || out["page"] != float64(page) {
			t.Fatalf("page %d: %d %v", page, w.Code, out)
		}
		got := out["accounts"].([]any)
		want := 5
		if page == 3 {
			want = 2
		}
		if len(got) != want {
			t.Errorf("page %d has %d accounts, want %d", page, len(got), want)
		}
		for _, a := range got {
			name := a.(map[string]any)["username"].(string)
			if seen[name] {
				t.Errorf("%s on two pages", name)
			}
			seen[name] = true
		}
	}
	if len(seen) != 12 {
		t.Errorf("saw %d accounts over the pages", len(seen))
	}
	if _, out := c.do("GET", "/api/admin/accounts?q=player0&per=500", nil); out["total"] != float64(10) || out["per"] != float64(25) {
		t.Errorf("filter / per cap: %v", out)
	}
	if _, out := c.do("GET", "/api/admin/accounts?page=99&per=5", nil); len(out["accounts"].([]any)) != 0 || out["total"] != float64(12) {
		t.Errorf("past the end: %v", out)
	}
}

func TestAdminDeleteSuspendAudit(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	mux := http.NewServeMux()
	h := &Handler{S: s}
	h.Mount(mux)
	root, _ := s.Register(ctx, "root", "root@example.com", "root long password")
	user, _ := s.Register(ctx, "user", "user@example.com", "user long password")
	s.Grant(ctx, root.ID, PermAdmin, 0)
	s.MarkVerified(ctx, root.ID)
	s.MarkVerified(ctx, user.ID)
	s.Grant(ctx, user.ID, PermDemoUpload, 0)
	rootTok, _, _ := s.Login(ctx, "root", "root long password", "1.1.1.1")
	userTok, _, _ := s.Login(ctx, "user", "user long password", "1.1.1.2")
	rc := &client{t: t, mux: mux, tok: rootTok}
	uc := &client{t: t, mux: mux, tok: userTok}

	// not for ordinary accounts
	if w, _ := uc.do("POST", fmt.Sprintf("/api/admin/accounts/%d/suspend", root.ID), map[string]string{"reason": "x"}); w.Code != 403 {
		t.Errorf("non-admin suspend: %d", w.Code)
	}
	if w, _ := uc.do("DELETE", fmt.Sprintf("/api/admin/accounts/%d", root.ID), nil); w.Code != 403 {
		t.Errorf("non-admin delete: %d", w.Code)
	}
	// not yourself, and not the last admin
	if w, _ := rc.do("DELETE", fmt.Sprintf("/api/admin/accounts/%d", root.ID), nil); w.Code != 400 {
		t.Errorf("delete self: %d", w.Code)
	}
	if w, _ := rc.do("POST", fmt.Sprintf("/api/admin/accounts/%d/suspend", root.ID), map[string]string{}); w.Code != 400 {
		t.Errorf("suspend self: %d", w.Code)
	}

	// suspend: sessions end, login refused (after the right password only), permissions stop counting
	if w, _ := rc.do("POST", fmt.Sprintf("/api/admin/accounts/%d/suspend", user.ID), map[string]string{"reason": "spamming"}); w.Code != 204 {
		t.Fatalf("suspend: %d", w.Code)
	}
	if w, _ := uc.do("GET", "/api/account", nil); w.Code != 401 {
		t.Errorf("session of a suspended account: %d", w.Code)
	}
	if _, _, err := s.Login(ctx, "user", "user long password", "1.1.1.3"); err != ErrSuspended {
		t.Errorf("login while suspended: %v", err)
	}
	if _, _, err := s.Login(ctx, "user", "wrong wrong wrong", "1.1.1.4"); err != ErrLogin {
		t.Errorf("wrong password on a suspended account must not reveal it: %v", err)
	}
	if a, _ := s.ByID(ctx, user.ID); !a.Suspended || a.SuspendedWhy != "spamming" || a.Has(PermDemoUpload) {
		t.Errorf("suspended account: %+v", a)
	}
	if w, out := rc.do("GET", "/api/admin/accounts?q=user", nil); w.Code != 200 || out["accounts"].([]any)[0].(map[string]any)["suspended"] != true {
		t.Errorf("list does not show the suspension: %v", out)
	}
	// back in
	if w, _ := rc.do("POST", fmt.Sprintf("/api/admin/accounts/%d/unsuspend", user.ID), nil); w.Code != 204 {
		t.Fatalf("unsuspend: %d", w.Code)
	}
	if _, _, err := s.Login(ctx, "user", "user long password", "1.1.1.5"); err != nil {
		t.Errorf("login after unsuspend: %v", err)
	}
	if a, _ := s.ByID(ctx, user.ID); !a.Has(PermDemoUpload) {
		t.Error("permission did not come back")
	}

	// the last admin cannot be removed by another admin who is not active
	boss, _ := s.Register(ctx, "boss", "boss@example.com", "boss long password")
	s.Grant(ctx, boss.ID, PermAdmin, 0) // unconfirmed: cannot act, so root is the only active admin
	bossAdmin, _ := s.ByID(ctx, boss.ID)
	if _, err := s.AdminDelete(ctx, bossAdmin, root.ID); err != ErrLastAdmin {
		t.Errorf("deleting the only active admin: %v", err)
	}
	// and an admin cannot delete themselves through the account page either
	if err := s.Delete(ctx, root.ID, "root long password"); err != ErrLastAdmin {
		t.Errorf("self-delete of the only admin: %v", err)
	}

	// delete, asking for the uploads to go
	var purged []string
	h.PurgeDemos = func(_ context.Context, owner string) (int, error) { purged = append(purged, owner); return 3, nil }
	s.RedeemDemoKey(ctx, user.ID, "oldkey")
	if w, out := rc.do("DELETE", fmt.Sprintf("/api/admin/accounts/%d", user.ID), map[string]any{"purge_uploads": true}); w.Code != 200 || out["uploads_removed"] != float64(3) {
		t.Fatalf("delete: %d %v", w.Code, out)
	}
	if len(purged) != 1 || purged[0] != OwnerName(user.ID) {
		t.Errorf("purged %v", purged)
	}
	if _, err := s.ByID(ctx, user.ID); err != ErrNoAccount {
		t.Errorf("account still there: %v", err)
	}
	if _, ok := s.AccountOfDemoKey(ctx, "oldkey"); ok {
		t.Error("the demo key is still tied to the deleted account")
	}
	// someone else can take the freed key
	if err := s.RedeemDemoKey(ctx, boss.ID, "oldkey"); err != nil {
		t.Errorf("redeeming the freed key: %v", err)
	}
	if w, _ := rc.do("DELETE", "/api/admin/accounts/99999", nil); w.Code != 404 {
		t.Errorf("delete nobody: %d", w.Code)
	}

	// the log: newest first, names kept after the account is gone, paged
	s.Grant(ctx, boss.ID, PermHostServer, 0)
	rc.do("PUT", fmt.Sprintf("/api/admin/accounts/%d/permissions/%s", boss.ID, PermDemoUpload), nil)
	w, out := rc.do("GET", "/api/admin/audit?per=3", nil)
	if w.Code != 200 || out["total"] != float64(4) { // suspend, unsuspend, delete, and the grant made through the API
		t.Errorf("audit total: %v", out["total"])
	}
	entries := out["entries"].([]any)
	if len(entries) != 3 || entries[0].(map[string]any)["action"] != "grant" {
		t.Errorf("newest entries: %v", entries)
	}
	_, all := rc.do("GET", "/api/admin/audit?per=100", nil)
	var del map[string]any
	for _, e := range all["entries"].([]any) {
		if e.(map[string]any)["action"] == "delete" {
			del = e.(map[string]any)
		}
	}
	if del == nil || del["target"] != "user" || del["actor"] != "root" || del["detail"] != "uploads removed: 3" {
		t.Errorf("delete entry: %v", del)
	}
	if w, _ := uc.do("GET", "/api/admin/audit", nil); w.Code != 401 && w.Code != 403 {
		t.Errorf("audit for a non-admin: %d", w.Code)
	}
}
