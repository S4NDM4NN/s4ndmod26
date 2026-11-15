package ticket

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	serverA = "dsdov4sv6poa3pyb" // dev's agent id
	serverB = "tovqaiwztklvijpo"
)

var t0 = time.Unix(1_800_000_000, 0)

func key(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	k, err := KeyFromSeed(base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func pub(k ed25519.PrivateKey) ed25519.PublicKey { return k.Public().(ed25519.PublicKey) }

func TestRoundTrip(t *testing.T) {
	k := key(t)
	tok, err := Issue(k, 42, serverA, t0, 90*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) != 127 || strings.ContainsAny(tok, "+/= \"';") {
		t.Fatalf("wire form %q (%d chars): want 127 characters of base64url", tok, len(tok))
	}
	got, err := Verify(pub(k), tok, serverA, t0.Add(10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if got.Account != 42 || got.Server != serverA || !got.Expires.Equal(t0.Add(90*time.Second)) {
		t.Errorf("ticket %+v", got)
	}
	again, _ := Issue(k, 42, serverA, t0, 90*time.Second)
	other, _ := Verify(pub(k), again, serverA, t0)
	if other.ID == got.ID {
		t.Error("two tickets share an id: a server could not tell them apart")
	}
}

func TestRejects(t *testing.T) {
	k := key(t)
	tok, _ := Issue(k, 7, serverA, t0, time.Minute)
	other := ed25519.NewKeyFromSeed([]byte("fedcba9876543210fedcba9876543210"))

	cases := []struct {
		name   string
		pub    ed25519.PublicKey
		tok    string
		server string
		at     time.Time
		want   error
	}{
		{"another server", pub(k), tok, serverB, t0, ErrServer},
		{"expired", pub(k), tok, serverA, t0.Add(time.Minute), ErrExpired},
		{"long expired", pub(k), tok, serverA, t0.Add(time.Hour), ErrExpired},
		{"another site key", pub(other), tok, serverA, t0, ErrSig},
		{"empty", pub(k), "", serverA, t0, ErrFormat},
		{"not base64", pub(k), strings.Repeat("!", 127), serverA, t0, ErrFormat},
		{"too short", pub(k), tok[:100], serverA, t0, ErrFormat},
		{"too long", pub(k), tok + "AAAA", serverA, t0, ErrFormat},
		{"our server id is malformed", pub(k), tok, "nope", t0, ErrBadID},
	}
	for _, c := range cases {
		if _, err := Verify(c.pub, c.tok, c.server, c.at); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
	if _, err := Verify(pub(k), tok, serverA, t0.Add(59*time.Second)); err != nil {
		t.Errorf("a second before it expires: %v", err)
	}
}

// Changing any single byte of a ticket must make it invalid.
func TestTamper(t *testing.T) {
	k := key(t)
	tok, _ := Issue(k, 7, serverA, t0, time.Minute)
	raw, _ := base64.RawURLEncoding.DecodeString(tok)
	for i := range raw {
		for _, bit := range []byte{1, 0x80} {
			bad := append([]byte(nil), raw...)
			bad[i] ^= bit
			if _, err := Verify(pub(k), base64.RawURLEncoding.EncodeToString(bad), serverA, t0); err == nil {
				t.Fatalf("a ticket with byte %d changed (xor %#x) still verified", i, bit)
			}
		}
	}
}

// The signature covers a fixed prefix, so a signature over the same bytes without it (made for some other use of
// the key) is not a ticket.
func TestSignatureIsDomainSeparated(t *testing.T) {
	k := key(t)
	sid, _ := ParseServerID(serverA)
	payload := make([]byte, payloadSize)
	payload[0] = version
	copy(payload[9:19], sid[:])
	payload[22] = 0xff // far in the future
	forged := append(append([]byte(nil), payload...), ed25519.Sign(k, payload)...)
	if _, err := Verify(pub(k), base64.RawURLEncoding.EncodeToString(forged), serverA, t0); !errors.Is(err, ErrSig) {
		t.Errorf("a signature without the prefix verified: %v", err)
	}
}

func TestLifetimeLimits(t *testing.T) {
	k := key(t)
	for _, ttl := range []time.Duration{0, -time.Second, MaxLifetime + time.Second, 24 * time.Hour} {
		if _, err := Issue(k, 1, serverA, t0, ttl); !errors.Is(err, ErrLifetime) {
			t.Errorf("ttl %v: %v", ttl, err)
		}
	}
	if _, err := Issue(k, 1, serverA, t0, MaxLifetime); err != nil {
		t.Errorf("the longest allowed: %v", err)
	}
}

func TestServerIDs(t *testing.T) {
	b, err := ParseServerID(serverA)
	if err != nil || ServerID(b) != serverA {
		t.Fatalf("%v %q", err, ServerID(b))
	}
	if b2, err := ParseServerID(strings.ToUpper(serverA)); err != nil || b2 != b {
		t.Errorf("upper case: %v", err)
	}
	for _, bad := range []string{"", "short", serverA + "x", "0000000000000000", "dsdov4sv6poa3py!"} { // 0/1/8/9 are not base32
		if _, err := ParseServerID(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestKeyFromSeed(t *testing.T) {
	seed := []byte("0123456789abcdef0123456789abcdef")
	want := ed25519.NewKeyFromSeed(seed)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		got, err := KeyFromSeed(" " + enc.EncodeToString(seed) + "\n")
		if err != nil || !got.Equal(want) {
			t.Errorf("%v", err)
		}
	}
	for _, bad := range []string{"", "AAAA", base64.StdEncoding.EncodeToString(seed[:31]), base64.StdEncoding.EncodeToString(append(seed, 1))} {
		if _, err := KeyFromSeed(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestParseServers(t *testing.T) {
	d, err := ParseServers(serverA + "=192.168.2.233:27960; S4ndmod26.Dev.example.com ," + serverB + "=203.0.113.5:27960")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		serverA: serverA, "192.168.2.233:27960": serverA, "s4ndmod26.dev.example.com": serverA, " S4NDMOD26.DEV.EXAMPLE.COM ": serverA,
		serverB: serverB, "203.0.113.5:27960": serverB,
	} {
		if id, ok := d.Lookup(name); !ok || id != want {
			t.Errorf("%q -> %q %v, want %s", name, id, ok, want)
		}
	}
	if _, ok := d.Lookup("198.51.100.9:27960"); ok {
		t.Error("an unlisted address was found")
	}
	for _, bad := range []string{"", ",", "notanid=1.2.3.4:5", serverA + "=x:1," + serverB + "=x:1"} {
		if _, err := ParseServers(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func issuer(t *testing.T) *Issuer {
	d, err := ParseServers(serverA + "=192.168.2.233:27960;play.example.com")
	if err != nil {
		t.Fatal(err)
	}
	now := t0
	return &Issuer{
		Key: key(t), Servers: d, TTL: 90 * time.Second, Now: func() time.Time { return now },
		Auth: TestAccount{Account: Account{ID: 9, Name: "testplayer"}, Token: "a-long-dev-only-test-token-123"},
	}
}

func post(i *Issuer, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/api/tickets", strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	i.ServeHTTP(rec, req)
	return rec
}

func TestEndpoint(t *testing.T) {
	i := issuer(t)
	const tok = "a-long-dev-only-test-token-123"

	rec := post(i, tok, `{"server":"play.example.com"}`)
	var resp response
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &resp) != nil {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if resp.ServerID != serverA || resp.Account != 9 || resp.Expires != t0.Add(90*time.Second).Unix() {
		t.Errorf("%+v", resp)
	}
	got, err := Verify(pub(i.Key), resp.Ticket, serverA, t0)
	if err != nil || got.Account != 9 {
		t.Errorf("the ticket the endpoint made: %v %+v", err, got)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("tickets must not be cached")
	}

	// by id and by ip:port as well
	for _, name := range []string{serverA, "192.168.2.233:27960"} {
		if rec := post(i, tok, `{"server":"`+name+`"}`); rec.Code != 200 {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}

	for _, c := range []struct {
		name, token, body string
		want              int
	}{
		{"no login", "", `{"server":"play.example.com"}`, 401},
		{"wrong token", "not-the-token-not-the-token-xx", `{"server":"play.example.com"}`, 401},
		{"a prefix of the token", tok[:10], `{"server":"play.example.com"}`, 401},
		{"unknown server", tok, `{"server":"203.0.113.9:27960"}`, 404},
		{"no server", tok, `{}`, 400},
		{"not json", tok, `server=x`, 400},
		{"huge body", tok, `{"server":"` + strings.Repeat("a", 5000) + `"}`, 400},
	} {
		if rec := post(i, c.token, c.body); rec.Code != c.want {
			t.Errorf("%s: %d, want %d (%s)", c.name, rec.Code, c.want, rec.Body.String())
		}
	}
	if rec := post(i, "", `{}`); rec.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Error("401 without WWW-Authenticate")
	}
}

func TestRateLimit(t *testing.T) {
	i := issuer(t)
	i.perMin = 3
	const tok = "a-long-dev-only-test-token-123"
	now := t0
	i.Now = func() time.Time { return now }
	for n := 0; n < 3; n++ {
		if rec := post(i, tok, `{"server":"play.example.com"}`); rec.Code != 200 {
			t.Fatalf("request %d: %d", n, rec.Code)
		}
	}
	if rec := post(i, tok, `{"server":"play.example.com"}`); rec.Code != 429 {
		t.Errorf("fourth in a minute: %d, want 429", rec.Code)
	}
	now = now.Add(61 * time.Second)
	if rec := post(i, tok, `{"server":"play.example.com"}`); rec.Code != 200 {
		t.Errorf("after a minute: %d", rec.Code)
	}
}

func TestKeyEndpoint(t *testing.T) {
	i := issuer(t)
	rec := httptest.NewRecorder()
	i.KeyHandler(rec, httptest.NewRequest("GET", "/api/tickets/key", nil))
	var out map[string]string
	json.Unmarshal(rec.Body.Bytes(), &out)
	got, err := base64.StdEncoding.DecodeString(out["public_key"])
	if err != nil || !ed25519.PublicKey(got).Equal(pub(i.Key)) || out["algorithm"] != "ed25519" {
		t.Errorf("%v %v", err, out)
	}
	if strings.Contains(rec.Body.String(), base64.StdEncoding.EncodeToString(i.Key.Seed())) {
		t.Error("the key endpoint leaked the seed")
	}
}

func TestFromEnv(t *testing.T) {
	good := map[string]string{
		"TICKET_SIGNING_KEY": base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")),
		"TICKET_SERVERS":     serverA + "=play.example.com",
		"TEST_ACCOUNT_TOKEN": "a-long-dev-only-test-token-123",
	}
	set := func(m map[string]string) {
		for _, k := range []string{"TICKET_SIGNING_KEY", "TICKET_SERVERS", "TEST_ACCOUNT_TOKEN", "TEST_ACCOUNT_ID", "TEST_ACCOUNT_NAME", "TICKET_TTL_SECONDS"} {
			t.Setenv(k, m[k])
		}
	}
	set(good)
	i := FromEnv()
	if i == nil || i.TTL != 90*time.Second {
		t.Fatalf("%+v", i)
	}
	if a, ok := i.Auth.Authenticate(func() *http.Request {
		r := httptest.NewRequest("POST", "/", nil)
		r.Header.Set("Authorization", "Bearer "+good["TEST_ACCOUNT_TOKEN"])
		return r
	}()); !ok || a.ID != 1 || a.Name != "testplayer" {
		t.Errorf("test account %+v %v", a, ok)
	}

	// off unless fully set up, and a short token is refused: it is a login
	for name, mod := range map[string]func(map[string]string){
		"no key":          func(m map[string]string) { delete(m, "TICKET_SIGNING_KEY") },
		"bad key":         func(m map[string]string) { m["TICKET_SIGNING_KEY"] = "short" },
		"no servers":      func(m map[string]string) { delete(m, "TICKET_SERVERS") },
		"no token":        func(m map[string]string) { delete(m, "TEST_ACCOUNT_TOKEN") },
		"short token":     func(m map[string]string) { m["TEST_ACCOUNT_TOKEN"] = "dev" },
		"bad server list": func(m map[string]string) { m["TICKET_SERVERS"] = "x=y" },
	} {
		m := map[string]string{}
		for k, v := range good {
			m[k] = v
		}
		mod(m)
		set(m)
		if FromEnv() != nil {
			t.Errorf("%s: tickets switched on", name)
		}
	}

	m := map[string]string{"TICKET_TTL_SECONDS": "600"}
	for k, v := range good {
		m[k] = v
	}
	set(m)
	if i := FromEnv(); i == nil || i.TTL != 90*time.Second {
		t.Errorf("a ticket lifetime over the maximum was accepted: %+v", i)
	}
}
