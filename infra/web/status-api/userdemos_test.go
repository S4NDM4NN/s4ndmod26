package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

const testKey = "k3y"

func newUDServer(t *testing.T, mut func(*userDemoConfig)) (*httptest.Server, *userDemos) {
	t.Helper()
	cfg := userDemoConfig{enabled: true, key: testKey, dir: t.TempDir(), maxFileBytes: 1 << 20, maxDemos: 3,
		sessionsPerHour: 100, uploadsPerHour: 100}
	if mut != nil {
		mut(&cfg)
	}
	u := newUserDemos(cfg)
	mux := http.NewServeMux()
	u.routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, u
}

func udDo(t *testing.T, method, url, key string, body []byte) (int, map[string]interface{}) {
	t.Helper()
	req, _ := http.NewRequest(method, url, bytes.NewReader(body))
	if key != "" {
		req.Header.Set("X-Upload-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func newSession(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	code, out := udDo(t, "POST", srv.URL+"/api/userdemos", testKey, nil)
	if code != http.StatusCreated {
		t.Fatalf("create session: %d %v", code, out)
	}
	id, _ := out["id"].(string)
	if !udIDRe.MatchString(id) {
		t.Fatalf("session id %q is not 128 random bits as hex", id)
	}
	return id
}

func TestUserDemosRequireTheKey(t *testing.T) {
	srv, _ := newUDServer(t, nil)
	if code, _ := udDo(t, "POST", srv.URL+"/api/userdemos", "", nil); code != http.StatusForbidden {
		t.Errorf("no key: %d, want 403", code)
	}
	if code, _ := udDo(t, "POST", srv.URL+"/api/userdemos", "wrong", nil); code != http.StatusForbidden {
		t.Errorf("wrong key: %d, want 403", code)
	}
	id := newSession(t, srv)
	if code, _ := udDo(t, "POST", srv.URL+"/api/userdemos/"+id+"/demos?name=a.dm_60", "", []byte("x")); code != http.StatusForbidden {
		t.Errorf("upload without a key: %d, want 403", code)
	}
	if code, _ := udDo(t, "DELETE", srv.URL+"/api/userdemos/"+id+"/demos/0", "", nil); code != http.StatusForbidden {
		t.Errorf("delete without a key: %d, want 403", code)
	}
}

func TestUserDemosOffByDefault(t *testing.T) {
	t.Setenv("USERDEMO_UPLOAD_ENABLED", "")
	t.Setenv("USERDEMO_DIR", t.TempDir())
	mux := http.NewServeMux()
	// startUserDemos registers on the default mux; with the flag off it must register nothing
	before := countRoutes(http.DefaultServeMux)
	startUserDemos(userDemoConfigFromEnv())
	if after := countRoutes(http.DefaultServeMux); after != before {
		t.Errorf("routes were registered with the flag off")
	}
	_ = mux
	t.Setenv("USERDEMO_UPLOAD_ENABLED", "1")
	t.Setenv("USERDEMO_UPLOAD_KEY", "")
	startUserDemos(userDemoConfigFromEnv())
	if after := countRoutes(http.DefaultServeMux); after != before {
		t.Errorf("routes were registered without an upload key")
	}
}

func countRoutes(m *http.ServeMux) int {
	n := 0
	for _, p := range []string{"/api/userdemos", "/api/userdemos/x/demos", "/api/userdemos/x", "/api/userdemos/x/paths"} {
		r := httptest.NewRequest("POST", p, nil)
		if _, pat := m.Handler(r); pat != "" {
			n++
		}
	}
	return n
}

func TestUserDemosRejectBadUploads(t *testing.T) {
	srv, u := newUDServer(t, nil)
	id := newSession(t, srv)
	up := func(name string, body []byte) (int, map[string]interface{}) {
		return udDo(t, "POST", srv.URL+"/api/userdemos/"+id+"/demos?name="+name, testKey, body)
	}
	for _, c := range []struct {
		what string
		name string
		body []byte
		want int
	}{
		{"a zip", "demos.zip", []byte("PK\x03\x04 and some more bytes here"), http.StatusUnsupportedMediaType},
		{"a zip named like a demo", "a.dm_60", []byte("PK\x03\x04 and some more bytes here"), http.StatusUnsupportedMediaType},
		{"another extension", "a.txt", bytes.Repeat([]byte("a"), 64), http.StatusUnsupportedMediaType},
		{"an unknown protocol", "a.dm_99", bytes.Repeat([]byte("a"), 64), http.StatusUnsupportedMediaType},
		{"a path in the name", "../../etc/passwd", bytes.Repeat([]byte("a"), 64), http.StatusUnsupportedMediaType},
		{"too large", "a.dm_60", bytes.Repeat([]byte("a"), int(u.cfg.maxFileBytes)+10), http.StatusRequestEntityTooLarge},
		{"garbage", "a.dm_60", bytes.Repeat([]byte("garbage!"), 64), http.StatusUnprocessableEntity},
		{"empty", "a.dm_60", nil, http.StatusUnsupportedMediaType},
	} {
		if code, out := up(c.name, c.body); code != c.want {
			t.Errorf("%s: %d %v, want %d", c.what, code, out, c.want)
		}
	}
	// nothing may have been stored
	files, _ := os.ReadDir(filepath.Join(u.cfg.dir, id))
	for _, f := range files {
		if strings.Contains(f.Name(), ".dm_") {
			t.Errorf("rejected upload left %s on disk", f.Name())
		}
	}
	if code, _ := udDo(t, "GET", srv.URL+"/api/userdemos/not-an-id", "", nil); code != http.StatusNotFound {
		t.Errorf("bad id: %d, want 404", code)
	}
	if code, _ := udDo(t, "GET", srv.URL+"/api/userdemos/"+strings.Repeat("0", 32), "", nil); code != http.StatusNotFound {
		t.Errorf("unknown id: %d, want 404", code)
	}
	if code, _ := udDo(t, "GET", srv.URL+"/api/userdemos/"+id+"/paths?group=0", "", nil); code != http.StatusNotFound {
		t.Errorf("paths of an empty session: %d, want 404", code)
	}
}

func TestUserDemosRateLimit(t *testing.T) {
	srv, _ := newUDServer(t, func(c *userDemoConfig) { c.sessionsPerHour = 2 })
	newSession(t, srv)
	newSession(t, srv)
	if code, _ := udDo(t, "POST", srv.URL+"/api/userdemos", testKey, nil); code != http.StatusTooManyRequests {
		t.Errorf("third session in an hour: %d, want 429", code)
	}
}

// findSampleFile returns the first sample demo whose name contains sub.
func findSampleFile(t *testing.T, root, sub string) string {
	t.Helper()
	var found string
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && found == "" && strings.Contains(filepath.Base(p), sub) &&
			strings.Contains(filepath.Ext(p), "dm_") {
			found = p
		}
		return nil
	})
	if found == "" {
		t.Fatalf("no sample matching %q", sub)
	}
	return found
}

// The whole flow with real demos.  Run it under a memory cap (see AGENTS.md).
func TestUserDemosFlow(t *testing.T) {
	root := os.Getenv("DEMO_SAMPLES")
	if root == "" || testing.Short() {
		t.Skip("DEMO_SAMPLES not set")
	}
	srv, u := newUDServer(t, func(c *userDemoConfig) { c.maxFileBytes = 40 << 20; c.maxDemos = 10 })
	id := newSession(t, srv)
	upload := func(sub string) map[string]interface{} {
		path := findSampleFile(t, root, sub)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		code, out := udDo(t, "POST", srv.URL+"/api/userdemos/"+id+"/demos?name="+filepath.Base(path), testKey, data)
		if code != http.StatusCreated {
			t.Fatalf("upload %s: %d %v", sub, code, out)
		}
		return out["demo"].(map[string]interface{})
	}
	want := []struct {
		sub, status string
		group       float64
	}{
		{"2003-08-15_145952", "first", 0},
		{"2003-08-15_150102", "synced", 0},   // the other team, same round
		{"2003-08-15_150025", "synced", 0},   // a third POV
		{"2003-08-15_150912", "unsynced", 1}, // another round of the same match
		{"mTwaTnJoe", "unsynced", 2},         // a different map
	}
	for _, w := range want {
		v := upload(w.sub)
		t.Logf("%-20s -> %v group %v map=%v recorder=%v anchors=%v basis=%v reason=%q linked=%v", w.sub, v["status"], v["group"], v["map"], v["recorder"], v["anchors"], v["basis"], v["reason"], v["linked_to"])
		if v["status"] != w.status || v["group"] != w.group {
			t.Errorf("%s: %v group %v, want %s group %v", w.sub, v["status"], v["group"], w.status, w.group)
		}
		if w.status == "unsynced" && v["reason"] == "" {
			t.Errorf("%s: an unsynced demo must say why", w.sub)
		}
	}

	code, state := udDo(t, "GET", srv.URL+"/api/userdemos/"+id, "", nil)
	if code != 200 || len(state["demos"].([]interface{})) != 5 || len(state["groups"].([]interface{})) != 3 {
		t.Fatalf("state: %d %v", code, state)
	}

	// the merged group is viewable, and a second request is served from disk
	get := func() map[string]interface{} {
		code, out := udDo(t, "GET", srv.URL+"/api/userdemos/"+id+"/paths?group=0", "", nil)
		if code != 200 {
			t.Fatalf("paths: %d %v", code, out)
		}
		return out
	}
	p := get()
	players, _ := p["players"].([]interface{})
	src := p["source"].(map[string]interface{})
	stats := src["stats"].(map[string]interface{})
	t.Logf("paths: %d players, %v kills, avg players per frame %.1f, exact health for %v players; %v of %v hp lost explained",
		len(players), stats["kills"], stats["avg_players"], stats["players_with_exact_health"], stats["attributed_hp"], stats["health_lost_hp"])
	if len(players) < 8 {
		t.Errorf("only %d players in the merged paths", len(players))
	}
	if k, _ := stats["kills"].(float64); k < 60 {
		t.Errorf("only %v kills", stats["kills"])
	}
	feed, _ := p["feed"].([]interface{})
	if len(feed) < 60 {
		t.Errorf("the event feed has %d entries, want the kills", len(feed))
	}
	for _, e := range feed {
		if f := e.(map[string]interface{}); f["type"] == "kill" {
			if f["actor"] == "" || f["actor_team"] == "" {
				t.Errorf("a kill in the feed has no player or team: %v", f)
			}
			break
		}
	}

	rounds, _ := p["rounds"].([]interface{})
	if len(rounds) != 2 {
		t.Fatalf("want 2 rounds (a stopwatch match), got %v", rounds)
	}
	r1, r2 := rounds[0].(map[string]interface{}), rounds[1].(map[string]interface{})
	if r1["of"].(float64) != 2 || r2["n"].(float64) != 2 || r2["beat_ms"].(float64) < 188000 {
		t.Errorf("rounds: %v %v", r1, r2)
	}
	// warm-up and intermission are cut: the replay is the two rounds and a short gap
	span := p["end_ms"].(float64) - p["start_ms"].(float64)
	roundsLen := (r1["end_ms"].(float64) - r1["start_ms"].(float64)) + (r2["end_ms"].(float64) - r2["start_ms"].(float64))
	if span > roundsLen+2000 {
		t.Errorf("the replay is %.0f s long but the rounds only add up to %.0f s", span/1000, roundsLen/1000)
	}
	kinds := map[string]int{}
	for _, f := range feed {
		kinds[f.(map[string]interface{})["type"].(string)]++
	}
	if kinds["steal"] == 0 || kinds["plant"] == 0 {
		t.Errorf("the feed has no objective events: %v", kinds)
	}
	if carries, _ := p["carries"].([]interface{}); len(carries) == 0 {
		t.Errorf("no objective carries in the paths")
	}
	names := map[string]bool{}
	for _, pl := range players {
		names[pl.(map[string]interface{})["name"].(string)] = true
	}
	if !names["iN-Summ3L"] {
		t.Errorf("player names missing: %v", names)
	}
	cached, _ := filepath.Glob(filepath.Join(u.cfg.dir, id, "paths_0_*.json"))
	if len(cached) != 1 {
		t.Errorf("expected one cached merge, found %v", cached)
	}
	get()

	// deleting a demo takes it from the session and from disk
	if code, _ := udDo(t, "DELETE", srv.URL+"/api/userdemos/"+id+"/demos/4", testKey, nil); code != 200 {
		t.Errorf("delete: %d", code)
	}
	if _, err := os.Stat(filepath.Join(u.cfg.dir, id, "4.dm_59")); !os.IsNotExist(err) {
		t.Errorf("the deleted demo is still on disk")
	}
	// another session cannot see this one's merge
	other := newSession(t, srv)
	if code, _ := udDo(t, "GET", srv.URL+"/api/userdemos/"+other+"/paths?group=0", "", nil); code != http.StatusNotFound {
		t.Errorf("another session's paths: %d, want 404", code)
	}
}

// A whole match: every dm_60 demo of QCon03 (several rounds, both teams) into one
// session, timed.  Opt-in (DEMO_FULL=1, plus DEMO_SAMPLES) because it takes a while.
func TestUserDemosFullMatch(t *testing.T) {
	root := os.Getenv("DEMO_SAMPLES")
	if root == "" || os.Getenv("DEMO_FULL") == "" || testing.Short() {
		t.Skip("DEMO_SAMPLES and DEMO_FULL not set")
	}
	srv, _ := newUDServer(t, func(c *userDemoConfig) { c.maxFileBytes = 40 << 20; c.maxDemos = 60 })
	id := newSession(t, srv)
	var files []string
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(p, ".dm_60") {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	var slowest time.Duration
	start := time.Now()
	for _, f := range files {
		data, _ := os.ReadFile(f)
		t0 := time.Now()
		code, out := udDo(t, "POST", srv.URL+"/api/userdemos/"+id+"/demos?name="+filepath.Base(f), testKey, data)
		d := time.Since(t0)
		if d > slowest {
			slowest = d
		}
		if code != http.StatusCreated {
			t.Fatalf("%s: %d %v", f, code, out)
		}
		v := out["demo"].(map[string]interface{})
		t.Logf("%5.1fs  %-8v group %v  %v", d.Seconds(), v["status"], v["group"], filepath.Base(f)[:30])
	}
	t.Logf("%d uploads in %.0fs, slowest %.1fs", len(files), time.Since(start).Seconds(), slowest.Seconds())
	_, state := udDo(t, "GET", srv.URL+"/api/userdemos/"+id, "", nil)
	for _, g := range state["groups"].([]interface{}) {
		gm := g.(map[string]interface{})
		t0 := time.Now()
		code, p := udDo(t, "GET", srv.URL+"/api/userdemos/"+id+"/paths?group="+strconv.Itoa(int(gm["group"].(float64))), "", nil)
		if code != 200 {
			t.Logf("group %v: paths %d %v", gm["group"], code, p)
			continue
		}
		st := p["source"].(map[string]interface{})["stats"].(map[string]interface{})
		t.Logf("group %v: %d demos, %d players, %v kills, %.1f players/frame, exact health for %v; merged in %.1fs",
			gm["group"], len(gm["demos"].([]interface{})), len(p["players"].([]interface{})), st["kills"], st["avg_players"], st["players_with_exact_health"], time.Since(t0).Seconds())
	}
}
