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
	if w, out := rc.do("GET", "/api/admin/accounts?q=us", nil); w.Code != 200 || len(out["accounts"].([]any)) != 1 {
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
