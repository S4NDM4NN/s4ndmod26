package agent

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func testConfig(t *testing.T) Config {
	base := t.TempDir()
	return Config{
		BaseDir:    base,
		Mod:        "s4ndmod26",
		DistDir:    filepath.Join(base, "s4ndmod26-dist"),
		RetailDir:  filepath.Join(base, "retail", "main"),
		OmnibotDir: filepath.Join(base, "omni-bot"),
		GamePort:   27960,
		StateDir:   filepath.Join(base, "state"),
		CORSOrigin: "*",
	}
}

const template = `seta sv_pure          SV_PURE_PLACEHOLDER
seta sv_dlURL        "SV_DLURL_PLACEHOLDER/downloads"
seta rconpassword     "RCON_PASSWORD_PLACEHOLDER"
  map mp_beach
seta mapname_note "map is not a command here"
`

func TestFillConfig(t *testing.T) {
	c := Config{RconPassword: "secret", DLURL: "https://pr-1.example", Pure: "1", DevMap: true}
	got := FillConfig(template, c)
	for _, want := range []string{`seta sv_pure          1`, `"https://pr-1.example/downloads"`, `"secret"`, "  devmap mp_beach", `"map is not a command here"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	// an unset value keeps its placeholder rather than becoming empty
	if got := FillConfig(template, Config{}); !strings.Contains(got, "RCON_PASSWORD_PLACEHOLDER") || strings.Contains(got, "devmap") {
		t.Errorf("unset values changed the template:\n%s", got)
	}
}

func TestPrepare(t *testing.T) {
	c := testConfig(t)
	mod := c.ModDir()
	write(t, filepath.Join(c.DistDir, "qagame.mp.x86_64.so"), "qagame-new")
	write(t, filepath.Join(c.DistDir, "s4ndmod26_v2.pk3"), "pk3-v2")
	write(t, filepath.Join(mod, "qagame.mp.x86_64.so"), "qagame-old")
	write(t, filepath.Join(mod, "s4ndmod26_v1.pk3"), "pk3-v1")
	write(t, filepath.Join(mod, "s4ndmod26.pk3"), "legacy")
	write(t, filepath.Join(mod, "server.cfg"), "operator's own")
	write(t, filepath.Join(c.RetailDir, "mp_pak0.pk3"), "retail")
	// a link to a retail pak that is no longer mounted, and one the operator made themselves
	os.MkdirAll(c.MainDir(), 0o755)
	os.Symlink(filepath.Join(filepath.Dir(c.RetailDir), "main", "gone.pk3"), filepath.Join(c.MainDir(), "gone.pk3"))
	os.Symlink("/elsewhere/own.pk3", filepath.Join(c.MainDir(), "own.pk3"))

	cfg, err := Prepare(c)
	if err != nil {
		t.Fatal(err)
	}
	if cfg != "server.cfg" {
		t.Errorf("cfg = %q, want server.cfg without a template", cfg)
	}
	if got := read(t, filepath.Join(mod, "qagame.mp.x86_64.so")); got != "qagame-new" {
		t.Errorf("qagame = %q", got)
	}
	if got := read(t, filepath.Join(mod, "latest.txt")); got != "s4ndmod26_v2.pk3\n" {
		t.Errorf("latest.txt = %q", got)
	}
	for _, gone := range []string{"s4ndmod26_v1.pk3", "s4ndmod26.pk3"} {
		if _, err := os.Stat(filepath.Join(mod, gone)); !os.IsNotExist(err) {
			t.Errorf("%s was not removed", gone)
		}
	}
	if got := read(t, filepath.Join(mod, "server.cfg")); got != "operator's own" {
		t.Errorf("server.cfg was touched: %q", got)
	}
	if st, err := os.Stat(c.ReplayDir()); err != nil || !st.IsDir() {
		t.Errorf("replays folder: %v", err)
	}
	if target, err := os.Readlink(filepath.Join(c.MainDir(), "mp_pak0.pk3")); err != nil || target != filepath.Join(c.RetailDir, "mp_pak0.pk3") {
		t.Errorf("retail link = %q, %v", target, err)
	}
	if _, err := os.Lstat(filepath.Join(c.MainDir(), "gone.pk3")); !os.IsNotExist(err) {
		t.Errorf("stale retail link kept")
	}
	if _, err := os.Lstat(filepath.Join(c.MainDir(), "own.pk3")); err != nil {
		t.Errorf("the operator's own link was removed")
	}

	// with a template: the filled config goes to main/, never into the (shared) mod folder
	write(t, filepath.Join(c.BaseDir, "tmpl.cfg"), template)
	c.CfgTemplate = filepath.Join(c.BaseDir, "tmpl.cfg")
	c.RconPassword = "secret"
	cfg, err = Prepare(c)
	if err != nil {
		t.Fatal(err)
	}
	if cfg != "agent_server.cfg" || !strings.Contains(read(t, filepath.Join(c.MainDir(), cfg)), `"secret"`) {
		t.Errorf("generated config %q wrong", cfg)
	}
	if st, _ := os.Stat(filepath.Join(c.MainDir(), cfg)); st.Mode().Perm() != 0o600 {
		t.Errorf("generated config mode %v, want 0600 (it holds the rcon password)", st.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(mod, cfg)); !os.IsNotExist(err) {
		t.Errorf("generated config written into the mod folder")
	}
}

// withDist gives c's dist folder what the image has: qagame, a pk3 and the starter config.
func withDist(t *testing.T, c Config, starter string) {
	t.Helper()
	write(t, filepath.Join(c.DistDir, "qagame.mp.x86_64.so"), "elf")
	write(t, filepath.Join(c.DistDir, "s4ndmod26_v1.pk3"), "pk3")
	write(t, filepath.Join(c.DistDir, StarterConfig), starter)
}

func TestStarterConfig(t *testing.T) {
	c := testConfig(t)
	c.PublicURL = "https://game.example.com"
	withDist(t, c, "seta rconpassword \"RCON_PASSWORD_PLACEHOLDER\"\nseta sv_dlURL \"SV_DLURL_PLACEHOLDER\"\n")
	cfgPath := filepath.Join(c.ModDir(), "server.cfg")

	if _, err := Prepare(c); err != nil {
		t.Fatal(err)
	}
	got := read(t, cfgPath)
	if strings.Contains(got, "PLACEHOLDER") || strings.Contains(got, `rconpassword ""`) ||
		!strings.Contains(got, `sv_dlURL "https://game.example.com/dl"`) {
		t.Fatalf("starter config:\n%s", got)
	}
	if st, _ := os.Stat(cfgPath); st.Mode().Perm() != 0o600 {
		t.Errorf("starter config mode %v, want 0600", st.Mode().Perm())
	}

	// never overwritten, and every server gets its own password
	write(t, cfgPath, "operator edited")
	if _, err := Prepare(c); err != nil {
		t.Fatal(err)
	}
	if read(t, cfgPath) != "operator edited" {
		t.Error("an existing server.cfg was replaced")
	}
	c2 := testConfig(t)
	withDist(t, c2, "seta rconpassword \"RCON_PASSWORD_PLACEHOLDER\"\n")
	Prepare(c2)
	if read(t, filepath.Join(c2.ModDir(), "server.cfg")) == got[:strings.Index(got, "\n")+1] {
		t.Error("two servers got the same rcon password")
	}

	// an empty server.cfg (a Kubernetes mount point) counts as existing
	c3 := testConfig(t)
	withDist(t, c3, "x")
	write(t, filepath.Join(c3.ModDir(), "server.cfg"), "")
	Prepare(c3)
	if read(t, filepath.Join(c3.ModDir(), "server.cfg")) != "" {
		t.Error("an empty server.cfg was replaced")
	}
}

func TestIdentityDefaultsToTheServerFolder(t *testing.T) {
	t.Setenv("AGENT_BASE_DIR", "/srv/rtcw")
	t.Setenv("AGENT_STATE_DIR", "")
	c, err := FromEnv(nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.StateDir != "/srv/rtcw/s4ndmod26/.agent" {
		t.Errorf("state dir %q", c.StateDir)
	}
	t.Setenv("AGENT_STATE_DIR", "/elsewhere")
	if c, _ := FromEnv(nil); c.StateDir != "/elsewhere" {
		t.Errorf("AGENT_STATE_DIR ignored: %q", c.StateDir)
	}
}

func TestServerArgs(t *testing.T) {
	c := testConfig(t)
	c.ExtraArgs = []string{"+set", "g_debug", "1"}
	got := strings.Join(ServerArgs(c, "server.cfg", ""), " ")
	want := "+set dedicated 2 +set fs_basepath " + c.BaseDir + " +set fs_homepath " + c.BaseDir +
		" +set fs_game s4ndmod26 +set omnibot_path " + c.OmnibotDir + " +exec server.cfg +set g_debug 1"
	if got != want {
		t.Errorf("args\n got %s\nwant %s", got, want)
	}
	c.GamePort = 27961
	if got := strings.Join(ServerArgs(c, "x.cfg", ""), " "); !strings.Contains(got, "+set net_port 27961 +exec x.cfg") {
		t.Errorf("port not passed: %s", got)
	}
}

// The game server learns its own id (it only accepts tickets made for it) and the site's key from the agent.
// The challenge secret is new at every start and, like the public names, reaches the game server as a plain argument.
func TestServerArgsCarryAuthSettings(t *testing.T) {
	t.Setenv("AGENT_PUBLIC_NAMES", "play.example.com:27960, 203.0.113.5:27960")
	a, err := FromEnv(nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := FromEnv(nil)
	if len(a.AuthKey) != 32 || a.AuthKey == b.AuthKey {
		t.Errorf("auth keys %q %q: want 32 hex digits, different each time", a.AuthKey, b.AuthKey)
	}
	a.BaseDir = t.TempDir()
	args := ServerArgs(a, "server.cfg", "")
	got := strings.Join(args, " ")
	if !strings.Contains(got, "+set s4nd_auth_key "+a.AuthKey+" +set s4nd_public_names play.example.com:27960,203.0.113.5:27960 +exec server.cfg") {
		t.Errorf("args: %s", got)
	}
	for i, x := range args {
		if x != "+set" && x != "+exec" && strings.ContainsAny(x, "+ \"") {
			t.Errorf("argument %d %q would be split or quoted by the engine's command line parser", i, x)
		}
	}
	t.Setenv("AGENT_PUBLIC_NAMES", "")
	c, _ := FromEnv(nil)
	if strings.Contains(strings.Join(ServerArgs(c, "x.cfg", ""), " "), "s4nd_public_names") {
		t.Error("empty names must not be passed")
	}
}

func TestServerArgsCarryTicketSettings(t *testing.T) {
	// the key has a '+' and a '/' in standard base64: the engine splits its command line at every '+', so the game
	// server must be given the URL-safe form (this is the real dev key, which arrived cut in two)
	t.Setenv("TICKET_PUBLIC_KEY", "INwwC5jddNA4F1U8AO7cmrlGeot3KAgPMKfqicm+FAc=")
	c, err := FromEnv(nil)
	if err != nil {
		t.Fatal(err)
	}
	c.BaseDir = t.TempDir()
	c.AuthKey = ""
	args := ServerArgs(c, "server.cfg", "dsdov4sv6poa3pyb")
	got := strings.Join(args, " ")
	if !strings.Contains(got, "+set s4nd_server_id dsdov4sv6poa3pyb +set s4nd_ticket_key INwwC5jddNA4F1U8AO7cmrlGeot3KAgPMKfqicm-FAc +exec server.cfg") {
		t.Errorf("args: %s", got)
	}
	for i, a := range args {
		if a != "+set" && a != "+exec" && strings.ContainsAny(a, "+ \"") {
			t.Errorf("argument %d %q would be split or quoted by the engine's command line parser", i, a)
		}
	}
	// without a key only the id is passed, and they come before +exec so the cvars exist when the config runs
	c.TicketKey = ""
	got = strings.Join(ServerArgs(c, "server.cfg", "dsdov4sv6poa3pyb"), " ")
	if strings.Contains(got, "s4nd_ticket_key") || !strings.Contains(got, "s4nd_server_id dsdov4sv6poa3pyb +exec") {
		t.Errorf("args: %s", got)
	}
}

func TestTicketKeySetting(t *testing.T) {
	const want = "INwwC5jddNA4F1U8AO7cmrlGeot3KAgPMKfqicm-FAc"
	for _, in := range []string{
		" INwwC5jddNA4F1U8AO7cmrlGeot3KAgPMKfqicm+FAc=\n", // standard, padded (what the site's key endpoint returns)
		"INwwC5jddNA4F1U8AO7cmrlGeot3KAgPMKfqicm+FAc",     // standard, unpadded
		"INwwC5jddNA4F1U8AO7cmrlGeot3KAgPMKfqicm-FAc=",    // URL-safe, padded
		want, // URL-safe, unpadded
	} {
		t.Setenv("TICKET_PUBLIC_KEY", in)
		if c, err := FromEnv(nil); err != nil || c.TicketKey != want {
			t.Errorf("%q: %q, %v", in, c.TicketKey, err)
		}
	}
	for _, bad := range []string{"short", "ndGb+3P06ihPlhT88iyIReneKFR0KlvCTgL3OS+OmX", "not base64 at all!!", "AAAA"} {
		t.Setenv("TICKET_PUBLIC_KEY", bad)
		if _, err := FromEnv(nil); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	t.Setenv("TICKET_PUBLIC_KEY", "")
	if c, err := FromEnv(nil); err != nil || c.TicketKey != "" {
		t.Errorf("unset: %q, %v", c.TicketKey, err)
	}
}

func TestSupervisorRestartsAndStops(t *testing.T) {
	// a server that dies at once is started again, with backoff
	s := &Supervisor{Path: "/bin/sh", Args: []string{"-c", "exit 3"}, MinBackoff: 10 * time.Millisecond, MaxBackoff: 20 * time.Millisecond, StableAfter: time.Hour, StopTimeout: time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for s.State().Starts < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if st := s.State(); st.Starts < 3 || !strings.Contains(st.LastExit, "exit status 3") {
		t.Fatalf("state %+v, want 3+ starts and the exit status", st)
	}

	// a running server is asked to stop when the agent stops, and is killed if it does not
	s = &Supervisor{Path: "/bin/sh", Args: []string{"-c", "trap '' TERM; sleep 30"}, MinBackoff: time.Second, MaxBackoff: time.Second, StableAfter: time.Hour, StopTimeout: 200 * time.Millisecond}
	ctx, cancel = context.WithCancel(context.Background())
	done = make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	for !s.State().Running && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	start := time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor did not stop the server")
	}
	if time.Since(start) > 3*time.Second || s.State().Running {
		t.Errorf("stop took %s, state %+v", time.Since(start), s.State())
	}
}

// fakeGame answers every UDP packet with "echo:" + the packet, from its own port.
func fakeGame(t *testing.T) *net.UDPConn {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	go func() {
		buf := make([]byte, maxPacket)
		for {
			n, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			conn.WriteToUDP(append([]byte("echo:"), buf[:n]...), from)
		}
	}()
	return conn
}

func TestBridge(t *testing.T) {
	game := fakeGame(t)
	port := game.LocalAddr().(*net.UDPAddr).Port
	c := testConfig(t)
	c.GamePort = port
	a := New(c, testIdentity(t), nil)
	srv := httptest.NewServer(a.Handler())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, path := range []string{"/ws", "/"} { // the root also bridges (local ws://host:port mode)
		ws, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+path,
			&websocket.DialOptions{Subprotocols: []string{"binary"}, HTTPHeader: http.Header{"Origin": {"https://s4ndmod.com"}}})
		if err != nil {
			t.Fatalf("%s: dial: %v", path, err)
		}
		if resp.Header.Get("Sec-WebSocket-Protocol") != "binary" {
			t.Errorf("%s: subprotocol %q", path, resp.Header.Get("Sec-WebSocket-Protocol"))
		}
		_, first, err := ws.Read(ctx)
		if err != nil || string(first) != string(portFrame(port)) {
			t.Fatalf("%s: first message %q, %v; want the port frame", path, first, err)
		}
		// the port frame sent back is not forwarded; the next packet is, and its answer comes back
		ws.Write(ctx, websocket.MessageBinary, portFrame(port))
		ws.Write(ctx, websocket.MessageBinary, []byte("\xff\xff\xff\xffgetinfo"))
		_, got, err := ws.Read(ctx)
		if err != nil || string(got) != "echo:\xff\xff\xff\xffgetinfo" {
			t.Fatalf("%s: answer %q, %v", path, got, err)
		}
		if a.Info().BrowserConn != 1 {
			t.Errorf("%s: browser_players = %d, want 1", path, a.Info().BrowserConn)
		}
		ws.Close(websocket.StatusNormalClosure, "")
	}
}

func testIdentity(t *testing.T) *Identity {
	t.Helper()
	id, err := LoadIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func get(t *testing.T, h http.Handler, method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestFilesAndDownloads(t *testing.T) {
	c := testConfig(t)
	write(t, filepath.Join(c.ReplayDir(), "m1.rpl"), "0123456789")
	write(t, filepath.Join(c.ReplayDir(), "m1.txt"), "done")
	write(t, filepath.Join(c.ReplayDir(), "m2.rpl"), "growing")
	write(t, filepath.Join(c.ReplayDir(), "notes.md"), "not a replay")
	write(t, filepath.Join(c.StatsDir(), "stats_20261112.jsonl"), "{}\n")
	write(t, filepath.Join(c.ModDir(), "s4ndmod26_v2.pk3"), "pk3-v2")
	write(t, filepath.Join(c.ModDir(), "latest.txt"), "s4ndmod26_v2.pk3\n")
	write(t, filepath.Join(c.ModDir(), "server.cfg"), "rconpassword secret")
	write(t, filepath.Join(c.ModDir(), "qagame.mp.x86_64.so"), "elf")
	os.Symlink(filepath.Join(c.ModDir(), "server.cfg"), filepath.Join(c.ReplayDir(), "link.txt"))
	h := New(c, testIdentity(t), nil).Handler()

	rec := get(t, h, "GET", "/v1/replays", nil)
	var list []FileInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range list {
		names[f.Name] = f.Live
	}
	if len(list) != 3 || !names["m2.rpl"] || names["m1.rpl"] {
		t.Errorf("listing %+v: want m1.rpl, m1.txt, m2.rpl (live); no .md, no links", list)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("no CORS header on the API")
	}

	rec = get(t, h, "GET", "/v1/replays/m1.rpl", map[string]string{"Range": "bytes=4-"})
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "456789" {
		t.Errorf("range: %d %q", rec.Code, rec.Body.String())
	}
	rec = get(t, h, "GET", "/v1/stats/stats_20261112.jsonl", nil)
	if rec.Code != 200 || rec.Body.String() != "{}\n" {
		t.Errorf("stats file: %d %q", rec.Code, rec.Body.String())
	}

	// nothing outside the folders, nothing of another kind, no links
	for _, p := range []string{
		"/v1/replays/notes.md", "/v1/replays/link.txt", "/v1/replays/..%2Fserver.cfg", "/v1/replays/%2E%2E",
		"/v1/stats/..%2F..%2Fs4ndmod26%2Fserver.cfg", "/dl/s4ndmod26/server.cfg", "/dl/s4ndmod26/qagame.mp.x86_64.so",
		"/dl/main/s4ndmod26_v2.pk3", "/dl/s4ndmod26/..%2Fs4ndmod26%2Fserver.cfg",
	} {
		if rec := get(t, h, "GET", p, nil); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "secret") {
			t.Errorf("%s: %d %q, want 404", p, rec.Code, rec.Body.String())
		}
	}

	rec = get(t, h, "GET", "/dl/s4ndmod26/s4ndmod26_v2.pk3", nil)
	if rec.Code != 200 || rec.Body.String() != "pk3-v2" || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("pk3: %d %q %q", rec.Code, rec.Body.String(), rec.Header().Get("Cache-Control"))
	}
	if rec := get(t, h, "GET", "/dl/s4ndmod26/latest.txt", nil); rec.Body.String() != "s4ndmod26_v2.pk3\n" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("latest.txt: %q %q", rec.Body.String(), rec.Header().Get("Cache-Control"))
	}

	rec = get(t, h, "OPTIONS", "/v1/replays/m1.rpl", map[string]string{"Origin": "https://s4ndmod.com", "Access-Control-Request-Headers": "range"})
	if rec.Code != http.StatusNoContent || !strings.Contains(rec.Header().Get("Access-Control-Allow-Headers"), "Range") {
		t.Errorf("preflight: %d %v", rec.Code, rec.Header())
	}

	var info Info
	json.Unmarshal(get(t, h, "GET", "/v1/info", nil).Body.Bytes(), &info)
	if info.Pk3 == nil || info.Pk3.Name != "s4ndmod26_v2.pk3" || info.Pk3.Size != 6 || len(info.Pk3.SHA256) != 64 ||
		info.Pk3.URL != "/dl/s4ndmod26/s4ndmod26_v2.pk3" || info.EngineAPI != EngineAPI || info.ID == "" {
		t.Errorf("info %+v", info)
	}
}

func TestIdentityPersistsAndSigns(t *testing.T) {
	dir := t.TempDir()
	a, err := LoadIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != b.ID || a.Ephemeral || len(a.ID) != 16 {
		t.Fatalf("ids %q %q (ephemeral %v)", a.ID, b.ID, a.Ephemeral)
	}
	if st, _ := os.Stat(filepath.Join(dir, identityFile)); st.Mode().Perm() != 0o600 {
		t.Errorf("key file mode %v", st.Mode().Perm())
	}
	body := []byte(`{"id":"x"}`)
	sig, _ := base64.StdEncoding.DecodeString(a.Sign(body))
	pub, _ := base64.StdEncoding.DecodeString(b.PublicKeyString())
	if !ed25519.Verify(pub, body, sig) {
		t.Error("signature does not verify")
	}
}

func TestRegistryHeartbeat(t *testing.T) {
	id := testIdentity(t)
	got := make(chan *http.Request, 1)
	bodies := make(chan []byte, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		select {
		case got <- r:
			bodies <- b
		default:
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go RunRegistry(ctx, srv.URL, time.Hour, id, func() Info { return Info{ID: id.ID, Map: "mp_beach"} })
	select {
	case r := <-got:
		body := <-bodies
		sig, _ := base64.StdEncoding.DecodeString(r.Header.Get("X-S4nd-Signature"))
		if r.Header.Get("X-S4nd-Server") != id.ID || !ed25519.Verify(id.PublicKey, body, sig) {
			t.Errorf("heartbeat headers/signature wrong")
		}
		var hb Heartbeat
		if json.Unmarshal(body, &hb) != nil || hb.Info.Map != "mp_beach" {
			t.Errorf("heartbeat body %s", body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no heartbeat")
	}
}
