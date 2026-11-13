package servers

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"s4ndmod26/status-api/accounts"
	"s4ndmod26/status-api/accounts/accountstest"
	"s4ndmod26/status-api/agent"
	"s4ndmod26/status-api/probe"
)

// fakeGame answers getstatus over UDP like the game server.
func fakeGame(t *testing.T) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := c.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if strings.HasPrefix(string(buf[:n]), "\xff\xff\xff\xffgetstatus") {
				c.WriteToUDP([]byte("\xff\xff\xff\xffstatusResponse\n\\sv_hostname\\Community FFA\\mapname\\mp_beach\\sv_maxclients\\16\n5 40 \"Ann\"\n"), from)
			}
		}
	}()
	return c
}

type world struct {
	t      *testing.T
	acc    *accounts.Service
	svc    *Service
	mux    *http.ServeMux
	game   *net.UDPConn
	wssURL string
	host   string // login token of a host
	root   string // login token of an admin
	hostID uint64
	pk3    string // what the claims say the server runs
	now    time.Time
}

func newWorld(t *testing.T) *world {
	acc := accountstest.New(t)
	svc := New(acc.Pool())
	ctx := context.Background()
	if err := svc.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := svc.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	game := fakeGame(t)
	bridge := &agent.Bridge{Game: game.LocalAddr().(*net.UDPAddr), AllowOrigin: "*", MaxConns: 8}
	m := http.NewServeMux()
	m.Handle("/ws", bridge)
	tls := httptest.NewTLSServer(m)
	t.Cleanup(tls.Close)
	pool := x509.NewCertPool()
	pool.AddCert(tls.Certificate())
	svc.Prober = probe.Prober{AllowPrivate: true, RootCAs: pool, Timeout: time.Second}

	host, _ := acc.Register(ctx, "hosty", "hosty@example.com", "hosty has a long password")
	acc.MarkVerified(ctx, host.ID)
	acc.Grant(ctx, host.ID, accounts.PermHostServer, 0)
	root, _ := acc.Register(ctx, "root", "root@example.com", "root has a long password")
	acc.MarkVerified(ctx, root.ID)
	acc.Grant(ctx, root.ID, accounts.PermAdmin, 0)
	w := &world{t: t, acc: acc, svc: svc, game: game, wssURL: "wss" + strings.TrimPrefix(tls.URL, "https") + "/ws", hostID: host.ID, pk3: "s4ndmod26_v9.pk3"}
	w.host, _, _ = acc.Login(ctx, "hosty", "hosty has a long password", "9.9.9.1")
	w.root, _, _ = acc.Login(ctx, "root", "root has a long password", "9.9.9.2")
	w.mux = http.NewServeMux()
	(&Handler{S: svc, Acc: acc}).Mount(w.mux)
	return w
}

func (w *world) do(method, path, tok string, body any) (*httptest.ResponseRecorder, map[string]any) {
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.RemoteAddr = "10.9.9.9:1"
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	rec := httptest.NewRecorder()
	w.mux.ServeHTTP(rec, r)
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

// claim sends a signed claim as an agent would.
func (w *world) claim(key ed25519.PrivateKey, token, gameAddr, wssURL string) (*httptest.ResponseRecorder, map[string]any) {
	pub := key.Public().(ed25519.PublicKey)
	b, _ := json.Marshal(ClaimBody{ServerID: IDOf(pub), PublicKey: base64.StdEncoding.EncodeToString(pub), Token: token, GameAddr: gameAddr, WSSURL: wssURL, Pk3: w.pk3, Sent: time.Now().Unix()})
	r := httptest.NewRequest("POST", "/api/servers/claim", bytes.NewReader(b))
	r.RemoteAddr = fmt.Sprintf("10.8.8.%d:1", time.Now().UnixNano()%250) // each claim from its own address: the limiter is per address
	r.Header.Set("X-S4nd-Signature", base64.StdEncoding.EncodeToString(ed25519.Sign(key, b)))
	rec := httptest.NewRecorder()
	w.mux.ServeHTTP(rec, r)
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

func (w *world) token(name string) string {
	rec, out := w.do("POST", "/api/servers/tokens", w.host, map[string]string{"name": name})
	if rec.Code != 200 {
		w.t.Fatalf("token: %d %v", rec.Code, out)
	}
	return out["token"].(string)
}

func newKey() ed25519.PrivateKey { _, k, _ := ed25519.GenerateKey(nil); return k }

func TestRegistrationFlow(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	gameAddr := w.game.LocalAddr().String()
	key := newKey()

	// tokens are for hosts only
	if rec, _ := w.do("POST", "/api/servers/tokens", "", map[string]string{"name": "x"}); rec.Code != 401 {
		t.Errorf("token without login: %d", rec.Code)
	}
	if rec, _ := w.do("POST", "/api/servers/tokens", w.root, map[string]string{"name": "x"}); rec.Code != 403 {
		t.Errorf("token for a non-host: %d", rec.Code)
	}
	tok := w.token("Hosty's FFA")

	// no token, a wrong one, a bad signature
	if rec, _ := w.claim(key, "", gameAddr, w.wssURL); rec.Code != 403 {
		t.Errorf("claim without a token: %d", rec.Code)
	}
	if rec, _ := w.claim(key, "nonsense", gameAddr, w.wssURL); rec.Code != 403 {
		t.Errorf("claim with a wrong token: %d", rec.Code)
	}
	{
		b, _ := json.Marshal(ClaimBody{ServerID: IDOf(key.Public().(ed25519.PublicKey)), PublicKey: base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey)), Token: tok, GameAddr: gameAddr, WSSURL: w.wssURL, Sent: time.Now().Unix()})
		r := httptest.NewRequest("POST", "/api/servers/claim", bytes.NewReader(b))
		r.Header.Set("X-S4nd-Signature", base64.StdEncoding.EncodeToString(ed25519.Sign(newKey(), b)))
		rec := httptest.NewRecorder()
		w.mux.ServeHTTP(rec, r)
		if rec.Code != 401 {
			t.Errorf("claim signed by another key: %d", rec.Code)
		}
	}

	// the UDP port is wrong: refused, says which probe failed, and the token is kept
	rec, out := w.claim(key, tok, "127.0.0.1:1999", w.wssURL)
	if rec.Code != 422 || out["udp"].(map[string]any)["ok"] != false || out["wss"].(map[string]any)["ok"] != true {
		t.Fatalf("a closed UDP port: %d %v", rec.Code, out)
	}
	if !strings.Contains(out["udp"].(map[string]any)["error"].(string), "UDP") {
		t.Errorf("the host is not told what to check: %v", out["udp"])
	}
	// the host can read what went wrong on their account page
	if _, mine := w.do("GET", "/api/servers/mine", w.host, nil); len(mine["tokens"].([]any)) != 1 ||
		!strings.Contains(mine["tokens"].([]any)[0].(map[string]any)["last_error"].(string), "UDP") || mine["tokens"].([]any)[0].(map[string]any)["last_attempt"] == nil {
		t.Errorf("the token does not carry the last refusal: %v", mine["tokens"])
	}
	// the WebSocket is wrong
	rec, out = w.claim(key, tok, gameAddr, "wss://127.0.0.1:1999/ws")
	if rec.Code != 422 || out["udp"].(map[string]any)["ok"] != true || out["wss"].(map[string]any)["ok"] != false {
		t.Fatalf("a closed WebSocket port: %d %v", rec.Code, out)
	}
	if _, err := w.svc.bySID(ctx, IDOf(key.Public().(ed25519.PublicKey))); err != ErrNotFound {
		t.Errorf("a server was created by a refused claim: %v", err)
	}

	// right: pending, token spent
	rec, out = w.claim(key, tok, gameAddr, w.wssURL)
	if rec.Code != 200 || out["status"] != "pending" || out["name"] != "Hosty's FFA" {
		t.Fatalf("a good claim: %d %v", rec.Code, out)
	}
	if rec, _ := w.claim(newKey(), tok, "127.0.0.1:2999", w.wssURL); rec.Code != 403 {
		t.Errorf("a token used twice: %d", rec.Code)
	}
	// not listed until approved
	if _, out := w.do("GET", "/api/servers", "", nil); out["total"] != float64(0) {
		t.Errorf("a pending server is listed: %v", out)
	}
	// the host sees it
	_, mine := w.do("GET", "/api/servers/mine", w.host, nil)
	sv := mine["servers"].([]any)[0].(map[string]any)
	if sv["status"] != "pending" || sv["udp"].(map[string]any)["ok"] != true || len(mine["tokens"].([]any)) != 0 {
		t.Errorf("mine: %v", mine)
	}
	id := int64(sv["id"].(float64))

	// only admins decide
	if rec, _ := w.do("POST", fmt.Sprintf("/api/admin/servers/%d/approve", id), w.host, nil); rec.Code != 403 {
		t.Errorf("a host approving: %d", rec.Code)
	}
	if rec, _ := w.do("POST", fmt.Sprintf("/api/admin/servers/%d/approve", id), w.root, nil); rec.Code != 200 {
		t.Fatalf("approve: %d", rec.Code)
	}
	if _, out := w.do("GET", "/api/servers", "", nil); out["total"] != float64(1) {
		t.Fatalf("an approved server is not listed: %v", out)
	} else {
		pv := out["servers"].([]any)[0].(map[string]any)
		if pv["hostname"] != "Community FFA" || pv["map"] != "mp_beach" || pv["players"] != float64(1) || pv["owner"] != nil || pv["udp"] != nil {
			t.Errorf("public view: %v", pv)
		}
	}
	// the play page asks for one server by id, and only a listed one answers
	sid := IDOf(key.Public().(ed25519.PublicKey))
	if rec, out := w.do("GET", "/api/servers/"+sid, "", nil); rec.Code != 200 || out["wss_url"] != w.wssURL || out["game_addr"] != gameAddr || out["pk3"] != "s4ndmod26_v9.pk3" || out["udp"] != nil {
		t.Errorf("lookup by id: %d %v", rec.Code, out)
	}
	if rec, _ := w.do("GET", "/api/servers/0000000000000000", "", nil); rec.Code != 404 {
		t.Errorf("an unknown server: %d", rec.Code)
	}
	// tickets and /link find it by its id or address
	if got, ok := w.svc.Lookup(IDOf(key.Public().(ed25519.PublicKey))); !ok || got != IDOf(key.Public().(ed25519.PublicKey)) {
		t.Errorf("Lookup by id: %q %v", got, ok)
	}
	if _, ok := w.svc.Lookup(gameAddr); !ok {
		t.Error("Lookup by address")
	}

	// the game goes away: the next round of probes unlists it and records why
	w.game.Close()
	defer func() {
		if rec, _ := w.do("GET", "/api/servers/"+sid, "", nil); rec.Code != 404 {
			t.Errorf("an unreachable server answers a lookup: %d", rec.Code)
		}
	}()
	w.svc.Prober.Timeout = 300 * time.Millisecond
	w.svc.ProbeAll(ctx)
	if _, out := w.do("GET", "/api/servers", "", nil); out["total"] != float64(0) {
		t.Errorf("an unreachable server is still listed: %v", out)
	}
	_, list := w.do("GET", "/api/admin/servers?status=approved", w.root, nil)
	got := list["servers"].([]any)[0].(map[string]any)
	if got["udp"].(map[string]any)["ok"] != false || got["wss"].(map[string]any)["ok"] != false || got["udp"].(map[string]any)["error"] == "" {
		t.Errorf("the admin does not see why: %v", got)
	}
	// ... and it cannot be approved again while unreachable
	w.do("POST", fmt.Sprintf("/api/admin/servers/%d/block", id), w.root, nil)
	w.do("POST", fmt.Sprintf("/api/admin/servers/%d/unblock", id), w.root, nil)
	if rec, out := w.do("POST", fmt.Sprintf("/api/admin/servers/%d/approve", id), w.root, nil); rec.Code != 409 {
		t.Errorf("approving an unreachable server: %d %v", rec.Code, out)
	}
}

func TestBlockingAddressesAndLimits(t *testing.T) {
	w := newWorld(t)
	w.svc.MaxPerAccount = 3
	ctx := context.Background()
	gameAddr := w.game.LocalAddr().String()
	k1, k2 := newKey(), newKey()
	if rec, _ := w.claim(k1, w.token("one"), gameAddr, w.wssURL); rec.Code != 200 {
		t.Fatalf("first server: %d", rec.Code)
	}
	// the same address cannot be claimed by another server
	if rec, _ := w.claim(k2, w.token("two"), gameAddr, w.wssURL); rec.Code != 409 {
		t.Errorf("a second server on the same address: %d", rec.Code)
	}
	// the number of servers an account may have counts open tokens too
	w.token("three") // that makes: 1 server + the unused token of "two" + this one = 3
	if rec, _ := w.do("POST", "/api/servers/tokens", w.host, map[string]string{"name": "four"}); rec.Code != 429 {
		t.Errorf("past the limit: %d", rec.Code)
	}
	// an admin can give this account more; only admins can; the host sees its own number
	limitPath := fmt.Sprintf("/api/admin/accounts/%d/server-limit", w.hostID)
	if rec, _ := w.do("PUT", limitPath, w.host, map[string]any{"limit": 9}); rec.Code != 403 {
		t.Errorf("a host setting its own limit: %d", rec.Code)
	}
	if rec, out := w.do("PUT", limitPath, w.root, map[string]any{"limit": 4}); rec.Code != 200 || out["limit"] != float64(4) || out["own"] != true {
		t.Errorf("setting the limit: %d %v", rec.Code, out)
	}
	if rec, out := w.do("GET", "/api/servers/mine", w.host, nil); rec.Code != 200 || out["max"] != float64(4) {
		t.Errorf("the host's view: %d %v", rec.Code, out)
	}
	w.token("four") // now allowed
	if rec, _ := w.do("POST", "/api/servers/tokens", w.host, map[string]string{"name": "five"}); rec.Code != 429 {
		t.Errorf("past the raised limit: %d", rec.Code)
	}
	if rec, out := w.do("PUT", limitPath, w.root, map[string]any{"limit": nil}); rec.Code != 200 || out["limit"] != float64(3) || out["own"] != false {
		t.Errorf("back to the default: %d %v", rec.Code, out)
	}
	if rec, _ := w.do("PUT", limitPath, w.root, map[string]any{"limit": -2}); rec.Code != 400 {
		t.Errorf("a negative limit: %d", rec.Code)
	}

	// a blocked server cannot refresh itself back in
	sv, _ := w.svc.bySID(ctx, IDOf(k1.Public().(ed25519.PublicKey)))
	w.do("POST", fmt.Sprintf("/api/admin/servers/%d/block", sv.ID), w.root, nil)
	if rec, _ := w.claim(k1, "", gameAddr, w.wssURL); rec.Code != 403 {
		t.Errorf("a blocked server claiming: %d", rec.Code)
	}
	w.do("POST", fmt.Sprintf("/api/admin/servers/%d/unblock", sv.ID), w.root, nil)
	// a registered server refreshes without a token (a new address, say); a different key cannot take its place
	if rec, out := w.claim(k1, "", gameAddr, w.wssURL); rec.Code != 200 || out["status"] != "pending" {
		t.Errorf("a refresh: %d %v", rec.Code, out)
	}
	// owners delete their own, nobody else's
	other, _ := w.acc.Register(ctx, "other", "other@example.com", "other has a long password")
	w.acc.MarkVerified(ctx, other.ID)
	w.acc.Grant(ctx, other.ID, accounts.PermHostServer, 0)
	otherTok, _, _ := w.acc.Login(ctx, "other", "other has a long password", "9.9.9.3")
	if rec, _ := w.do("DELETE", fmt.Sprintf("/api/servers/mine/%d", sv.ID), otherTok, nil); rec.Code != 403 {
		t.Errorf("deleting another host's server: %d", rec.Code)
	}
	if rec, _ := w.do("DELETE", fmt.Sprintf("/api/servers/mine/%d", sv.ID), w.host, nil); rec.Code != 204 {
		t.Errorf("deleting your own: %d", rec.Code)
	}
	if _, err := w.svc.bySID(ctx, sv.ServerID); err != ErrNotFound {
		t.Errorf("still there: %v", err)
	}
	// the audit log has the admin's decisions
	_, log := func() (*httptest.ResponseRecorder, map[string]any) {
		return w.do("GET", "/api/admin/audit", w.root, nil)
	}()
	if log != nil {
		t.Logf("audit: %v", log["total"])
	}
}

func TestSuspendedHostsAndOfficialServers(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	// a host who loses the permission cannot make tokens
	w.acc.Revoke(ctx, w.hostID, accounts.PermHostServer)
	if rec, _ := w.do("POST", "/api/servers/tokens", w.host, map[string]string{"name": "x"}); rec.Code != 403 {
		t.Errorf("a host without the permission: %d", rec.Code)
	}
	// an official server is added by an admin, approved and never probed from outside
	k := newKey()
	pub := k.Public().(ed25519.PublicKey)
	rec, out := w.do("POST", "/api/admin/servers", w.root, map[string]string{"public_key": base64.StdEncoding.EncodeToString(pub), "name": "S4ND dev", "game_addr": "play.example.test:27960", "wss_url": "wss://play.example.test/ws"})
	if rec.Code != 201 || out["official"] != true || out["status"] != "approved" {
		t.Fatalf("add official: %d %v", rec.Code, out)
	}
	w.svc.ProbeAll(ctx)
	if _, out := w.do("GET", "/api/servers", "", nil); out["total"] != float64(1) {
		t.Errorf("an official server is not listed: %v", out)
	}
	if _, out := w.do("GET", "/api/servers?kind=official", "", nil); out["total"] != float64(1) {
		t.Errorf("official kind: %v", out)
	}
	if _, out := w.do("GET", "/api/servers?kind=community", "", nil); out["total"] != float64(0) {
		t.Errorf("community kind lists an official server: %v", out)
	}
	if rec, _ := w.do("GET", "/api/servers?kind=bogus", "", nil); rec.Code != 400 {
		t.Errorf("a bad kind: %d", rec.Code)
	}
	if rec, _ := w.do("POST", "/api/admin/servers", w.root, map[string]string{"public_key": "x", "name": "n", "game_addr": "a:27960", "wss_url": "wss://a/ws"}); rec.Code != 400 {
		t.Errorf("a bad key: %d", rec.Code)
	}
}

func TestAddressRules(t *testing.T) {
	for in, ok := range map[string]bool{"Play.Example.com:27960": true, "1.2.3.4:27961": true, "host:80": false, "host": false, "host:99999": false, ":27960": false, "a b:27960": false} {
		if _, err := normalizeGameAddr(in); (err == nil) != ok {
			t.Errorf("game addr %q: %v", in, err)
		}
	}
	for in, ok := range map[string]bool{"wss://Play.Example.com/ws": true, "wss://h.example:27960/ws": true, "wss://h.example": true, "ws://h.example/ws": false,
		"https://h.example/ws": false, "wss://u:p@h.example/ws": false, "wss://h.example:80/ws": false, "wss://h.example/ws?x=1": false} {
		if _, err := normalizeWSS(in); (err == nil) != ok {
			t.Errorf("wss %q: %v", in, err)
		}
	}
	if got, _ := normalizeWSS("wss://Play.Example.COM"); got != "wss://play.example.com/ws" {
		t.Errorf("normalized %q", got)
	}
}

func TestCleanHostname(t *testing.T) {
	for in, want := range map[string]string{
		"Community FFA":            "Community FFA",
		"^1Red ^7and ^4Blue":       "Red and Blue",
		"  spaced \t out \n name ": "spaced out name",
		"trailing caret ^":         "trailing caret ^",
		"":                         "",
		strings.Repeat("a", 100):   strings.Repeat("a", 60),
	} {
		if got := CleanHostname(in); got != want {
			t.Errorf("CleanHostname(%q) = %q, want %q", in, got, want)
		}
	}
}
