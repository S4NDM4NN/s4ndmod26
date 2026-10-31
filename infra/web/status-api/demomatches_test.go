package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"s4ndmod26/status-api/demostore"
	"s4ndmod26/status-api/demostore/storetest"
)

const (
	keyTravis = "travis-key-0123456789"
	keySandy  = "sandy--key-0123456789"
	keyEve    = "eve----key-0123456789"
)

type demoEnv struct {
	srv   *httptest.Server
	svc   *demoService
	blobs string
}

func newDemoEnv(t *testing.T, mut func(*demoConfig)) *demoEnv {
	t.Helper()
	db := storetest.New(t) // skips without a database
	keys, err := demostore.LoadKeys(writeTempFile(t, "travis "+keyTravis+"\nsandy "+keySandy+"\neve "+keyEve+"\n"), "")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	blobs, err := demostore.NewFSBlobs(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := demoConfig{enabled: true, dir: dir, maxFileBytes: 1 << 20, maxDemosPerMatch: 60, uploadsPerHour: 1000}
	if mut != nil {
		mut(&cfg)
	}
	svc := newDemoService(cfg, keys, db, blobs)
	mux := http.NewServeMux()
	svc.routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &demoEnv{srv: srv, svc: svc, blobs: dir}
}

func writeTempFile(t *testing.T, body string) string {
	p := filepath.Join(t.TempDir(), "keys")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func (e *demoEnv) do(t *testing.T, method, path, key string, body []byte, hdr ...string) (int, []byte, http.Header) {
	t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, bytes.NewReader(body))
	if key != "" {
		req.Header.Set("X-Upload-Key", key)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b, resp.Header
}

func (e *demoEnv) json(t *testing.T, method, path, key string, body []byte) (int, map[string]interface{}) {
	t.Helper()
	code, b, _ := e.do(t, method, path, key, body)
	var out map[string]interface{}
	json.Unmarshal(b, &out)
	return code, out
}

// blobCount counts the files kept (raw demos and merged outputs).
func (e *demoEnv) blobCount(t *testing.T, sub string) int {
	n := 0
	filepath.Walk(filepath.Join(e.blobs, sub), func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			n++
		}
		return nil
	})
	return n
}

func TestDemoWritesNeedAKey(t *testing.T) {
	e := newDemoEnv(t, nil)
	for _, c := range []struct {
		method, path, key string
	}{
		{"POST", "/api/demos/upload?name=a.dm_60", ""},
		{"POST", "/api/demos/upload?name=a.dm_60", "wrong"},
		{"POST", "/api/demos/upload?name=a.dm_60", keyTravis + "x"},
		{"GET", "/api/demos/mine", ""},
		{"GET", "/api/demos/mine", "wrong"},
		{"DELETE", "/api/demos/files/1", ""},
		{"DELETE", "/api/demos/files/1", "wrong"},
	} {
		if code, _, _ := e.do(t, c.method, c.path, c.key, []byte("x")); code != http.StatusForbidden {
			t.Errorf("%s %s with key %q: %d, want 403", c.method, c.path, c.key, code)
		}
	}
}

func TestDemoReadsArePublic(t *testing.T) {
	e := newDemoEnv(t, nil)
	if code, out := e.json(t, "GET", "/api/demos/matches", "", nil); code != 200 || out["total"].(float64) != 0 || len(out["matches"].([]interface{})) != 0 {
		t.Errorf("an empty list: %d %v", code, out)
	}
	for _, id := range []string{"nope", "0123456789abcdef", "../etc", strings.Repeat("0", 32)} {
		if code, _, _ := e.do(t, "GET", "/api/demos/matches/"+id, "", nil); code != http.StatusNotFound {
			t.Errorf("match %q: %d, want 404", id, code)
		}
		if code, _, _ := e.do(t, "GET", "/api/demos/matches/"+id+"/paths", "", nil); code != http.StatusNotFound {
			t.Errorf("paths of %q: %d, want 404", id, code)
		}
	}
}

func TestDemoBadUploadsAreRefused(t *testing.T) {
	e := newDemoEnv(t, nil)
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
		{"no name", "", bytes.Repeat([]byte("a"), 64), http.StatusUnsupportedMediaType},
		{"too large", "a.dm_60", bytes.Repeat([]byte("a"), int(e.svc.cfg.maxFileBytes)+10), http.StatusRequestEntityTooLarge},
		{"garbage", "a.dm_60", bytes.Repeat([]byte("garbage!"), 64), http.StatusUnprocessableEntity},
		{"empty", "a.dm_60", nil, http.StatusUnsupportedMediaType},
	} {
		if code, out := e.json(t, "POST", "/api/demos/upload?name="+c.name, keyTravis, c.body); code != c.want {
			t.Errorf("%s: %d %v, want %d", c.what, code, out, c.want)
		}
	}
	if n := e.blobCount(t, "raw"); n != 0 {
		t.Errorf("refused uploads left %d files behind", n)
	}
	if code, out := e.json(t, "GET", "/api/demos/matches", "", nil); out["total"].(float64) != 0 {
		t.Errorf("refused uploads made matches: %d %v", code, out)
	}
}

func TestDemoUploadRateLimit(t *testing.T) {
	e := newDemoEnv(t, func(c *demoConfig) { c.uploadsPerHour = 2 })
	for i := 0; i < 2; i++ {
		e.json(t, "POST", "/api/demos/upload?name=a.dm_60", keyTravis, bytes.Repeat([]byte("g"), 64))
	}
	if code, _ := e.json(t, "POST", "/api/demos/upload?name=a.dm_60", keyTravis, bytes.Repeat([]byte("g"), 64)); code != http.StatusTooManyRequests {
		t.Errorf("third upload in an hour: %d, want 429", code)
	}
	// the limit is per key: another uploader is not held up by it
	if code, _ := e.json(t, "POST", "/api/demos/upload?name=a.dm_60", keySandy, bytes.Repeat([]byte("g"), 64)); code == http.StatusTooManyRequests {
		t.Error("one key's rate limit held up another's")
	}
}

func TestDisplayName(t *testing.T) {
	for in, want := range map[string]string{
		"a.dm_60":                "a.dm_60",
		"../../etc/passwd":       "passwd",
		`C:\demos\x.dm_60`:       "x.dm_60",
		"we\nird\x00.dm_60":      "weird.dm_60",
		strings.Repeat("a", 300): strings.Repeat("a", 120),
	} {
		if got := displayName(in); got != want {
			t.Errorf("displayName(%q) = %q, want %q", in, got, want)
		}
	}
}

// The whole flow with real demos and two uploaders.  Run it under a memory cap (see AGENTS.md).
func TestDemoMatchesFlow(t *testing.T) {
	root := os.Getenv("DEMO_SAMPLES")
	if root == "" || testing.Short() {
		t.Skip("DEMO_SAMPLES not set")
	}
	e := newDemoEnv(t, func(c *demoConfig) { c.maxFileBytes = 40 << 20 })
	read := func(sub string) (string, []byte) {
		var found string
		filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && found == "" && strings.Contains(filepath.Base(p), sub) && strings.Contains(filepath.Ext(p), "dm_") {
				found = p
			}
			return nil
		})
		if found == "" {
			t.Fatalf("no sample matching %q", sub)
		}
		b, err := os.ReadFile(found)
		if err != nil {
			t.Fatal(err)
		}
		return filepath.Base(found), b
	}
	upload := func(key, sub string) map[string]interface{} {
		name, data := read(sub)
		code, out := e.json(t, "POST", "/api/demos/upload?name="+name, key, data)
		if code != http.StatusCreated && code != http.StatusOK {
			t.Fatalf("upload %s: %d %v", sub, code, out)
		}
		return out
	}
	id := func(out map[string]interface{}) string {
		return out["demo"].(map[string]interface{})["match_id"].(string)
	}
	fid := func(out map[string]interface{}) string {
		return strconv.Itoa(int(out["demo"].(map[string]interface{})["id"].(float64)))
	}

	// three POVs of one round join one match; another round and another map each start their own
	a := upload(keyTravis, "2003-08-15_145952")
	b := upload(keyTravis, "2003-08-15_150102")
	c := upload(keySandy, "2003-08-15_150025")
	other := upload(keyTravis, "2003-08-15_150912")
	sub := upload(keySandy, "mTwaTnJoe")
	for i, o := range []map[string]interface{}{a, b, c} {
		t.Logf("%d: %v %v linked=%v", i, o["status"], id(o), o["linked_to"])
	}
	if a["status"] != "new_match" || b["status"] != "joined" || c["status"] != "joined" || id(a) != id(b) || id(b) != id(c) {
		t.Fatalf("round 1: %v %v %v", a["status"], b["status"], c["status"])
	}
	if other["status"] != "new_match" || id(other) == id(a) || other["reason"] == "" {
		t.Errorf("another round: %v %v", other["status"], other["reason"])
	}
	if sub["status"] != "new_match" || id(sub) == id(a) || id(sub) == id(other) {
		t.Errorf("another map: %v", sub["status"])
	}

	// anyone can list and read, with no key
	code, list := e.json(t, "GET", "/api/demos/matches", "", nil)
	ms := list["matches"].([]interface{})
	if code != 200 || len(ms) != 3 || list["total"].(float64) != 3 {
		t.Fatalf("list: %d %v", code, list)
	}
	for _, m := range ms {
		mm := m.(map[string]interface{})
		if mm["id"] == id(a) && (mm["demos"].(float64) != 3 || len(mm["recorders"].([]interface{})) != 3 || mm["duration_ms"].(float64) < 400000) {
			t.Errorf("summary of match 1: %v", mm)
		}
	}
	if code, m := e.json(t, "GET", "/api/demos/matches/"+id(a), "", nil); code != 200 || len(m["demos"].([]interface{})) != 3 {
		t.Errorf("match detail: %d %v", code, m)
	} else if _, leaked := m["demos"].([]interface{})[0].(map[string]interface{})["owner"]; leaked {
		t.Error("the public match detail names the uploader")
	}

	// the merged replay: public, gzipped on disk, kept, revalidated by ETag
	code, body, hdr := e.do(t, "GET", "/api/demos/matches/"+id(a)+"/paths", "", nil, "Accept-Encoding", "gzip")
	if code != 200 || hdr.Get("Content-Encoding") != "gzip" {
		t.Fatalf("paths: %d, encoding %q", code, hdr.Get("Content-Encoding"))
	}
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(zr)
	var paths map[string]interface{}
	if err := json.Unmarshal(raw, &paths); err != nil {
		t.Fatal(err)
	}
	if n := len(paths["players"].([]interface{})); n < 8 {
		t.Errorf("only %d players in the merged replay", n)
	}
	if rs := paths["rounds"].([]interface{}); len(rs) != 2 {
		t.Errorf("rounds: %v", rs)
	}
	for _, k := range []string{"shots", "projectiles", "explosions", "drops", "feed"} {
		if l, _ := paths[k].([]interface{}); len(l) == 0 {
			t.Errorf("the %s layer is empty", k)
		}
	}
	// statistics: public, and they agree with the feed they come from
	code, st := e.json(t, "GET", "/api/demos/matches/"+id(a)+"/stats", "", nil)
	if code != 200 {
		t.Fatalf("stats: %d %v", code, st)
	}
	kills, deaths := 0, 0
	for _, p := range st["players"].([]interface{}) {
		tot := p.(map[string]interface{})["total"].(map[string]interface{})
		kills += int(tot["kills"].(float64))
		deaths += int(tot["deaths"].(float64))
	}
	feedKills := 0
	for _, f := range paths["feed"].([]interface{}) {
		if f.(map[string]interface{})["type"] == "kill" {
			feedKills++
		}
	}
	if kills != feedKills || deaths < kills || len(st["rounds"].([]interface{})) != 2 || len(st["players"].([]interface{})) < 10 {
		t.Errorf("stats: %d kills, %d deaths, feed has %d kills, %d rounds, %d players", kills, deaths, feedKills, len(st["rounds"].([]interface{})), len(st["players"].([]interface{})))
	}
	t.Logf("stats: %d kills, %d deaths", kills, deaths)
	if n := e.blobCount(t, "paths"); n != 1 {
		t.Errorf("%d merged outputs kept, want 1", n)
	}
	if code, _, _ := e.do(t, "GET", "/api/demos/matches/"+id(a)+"/paths", "", nil, "Accept-Encoding", "gzip", "If-None-Match", hdr.Get("ETag")); code != http.StatusNotModified {
		t.Errorf("a repeat request with the ETag: %d, want 304", code)
	}
	// a client that does not take gzip still gets JSON
	if code, plain, h := e.do(t, "GET", "/api/demos/matches/"+id(a)+"/paths", "", nil); code != 200 || h.Get("Content-Encoding") != "" || !json.Valid(plain) {
		t.Errorf("without gzip: %d, encoding %q", code, h.Get("Content-Encoding"))
	}

	// the same file from someone else costs nothing: one demo, two claims
	rawBefore := e.blobCount(t, "raw")
	again := upload(keySandy, "2003-08-15_145952")
	if again["status"] != "already_stored" || fid(again) != fid(a) {
		t.Fatalf("the same file again: %v", again)
	}
	if n := e.blobCount(t, "raw"); n != rawBefore {
		t.Errorf("the same file was stored again: %d -> %d files", rawBefore, n)
	}
	mineOf := func(key string) []interface{} {
		code, out := e.json(t, "GET", "/api/demos/mine", key, nil)
		if code != 200 {
			t.Fatalf("mine: %d", code)
		}
		return out["uploads"].([]interface{})
	}
	if len(mineOf(keyTravis)) != 3 || len(mineOf(keySandy)) != 3 { // travis: a, b, other; sandy: c, sub, a
		t.Errorf("uploads: travis %d, sandy %d, want 3 and 3", len(mineOf(keyTravis)), len(mineOf(keySandy)))
	}

	// naming: a key with a demo in the match; the public sees the name
	nameIt := func(key, matchID, name string) (int, map[string]interface{}) {
		body, _ := json.Marshal(map[string]string{"name": name})
		return e.json(t, "PUT", "/api/demos/matches/"+matchID, key, body)
	}
	if code, _ := nameIt("", id(a), "x"); code != http.StatusForbidden {
		t.Errorf("naming without a key: %d", code)
	}
	if code, _ := nameIt(keyEve, id(a), "x"); code != http.StatusForbidden {
		t.Errorf("naming with no demo in the match: %d", code)
	}
	if code, out := nameIt(keySandy, id(a), "  QCon03 \n final,  game 1 "); code != 200 || out["name"] != "QCon03 final, game 1" {
		t.Errorf("naming: %d %v", code, out)
	}
	if _, m := e.json(t, "GET", "/api/demos/matches/"+id(a), "", nil); m["name"] != "QCon03 final, game 1" {
		t.Errorf("the public match has name %v", m["name"])
	}
	// series: a key links matches it has demos of; anyone can read the result; only its maker changes it
	post := func(method, path, key string, name string, ms ...string) (int, map[string]interface{}) {
		body, _ := json.Marshal(map[string]interface{}{"name": name, "matches": ms})
		return e.json(t, method, path, key, body)
	}
	if code, _ := post("POST", "/api/demos/series", "", "x", id(a)); code != http.StatusForbidden {
		t.Errorf("a series without a key: %d, want 403", code)
	}
	if code, out := post("POST", "/api/demos/series", keySandy, "x", id(a), id(other)); code != http.StatusForbidden {
		t.Errorf("linking a match sandy has no demo of: %d %v, want 403", code, out)
	}
	code, made := post("POST", "/api/demos/series", keyTravis, "  Final \n round 1  ", id(other), id(a))
	if code != 200 {
		t.Fatalf("making a series: %d %v", code, made)
	}
	sid := made["id"].(string)
	code, sr := e.json(t, "GET", "/api/demos/series/"+sid, "", nil)
	sms, _ := sr["matches"].([]interface{})
	if code != 200 || sr["name"] != "Final round 1" || len(sms) != 2 || sms[0].(map[string]interface{})["id"] != id(other) {
		t.Fatalf("reading the series: %d %v", code, sr)
	}
	if _, leaked := sr["owner"]; leaked {
		t.Error("the public series names its maker")
	}
	code, m := e.json(t, "GET", "/api/demos/matches/"+id(a), "", nil)
	if ref, _ := m["series"].(map[string]interface{}); code != 200 || ref == nil || ref["pos"].(float64) != 2 || ref["prev"] != id(other) || ref["next"] != nil {
		t.Errorf("the match does not say where it is in the series: %v", m["series"])
	}
	// a key with no demo in any of its matches cannot change it ...
	if code, out := post("PUT", "/api/demos/series/"+sid, keyEve, "mine now", id(a)); code != http.StatusForbidden {
		t.Errorf("eve changing the series: %d %v", code, out)
	}
	if code, _ := e.json(t, "DELETE", "/api/demos/series/"+sid, keyEve, nil); code != http.StatusForbidden {
		t.Errorf("eve dissolving the series: %d", code)
	}
	// ... but sandy has a demo in one of them, so she can, and keeps travis's other match in it
	if code, out := post("PUT", "/api/demos/series/"+sid, keySandy, "Final (sandy's name)", id(other), id(a)); code != 200 {
		t.Errorf("sandy renaming the series: %d %v", code, out)
	}
	if _, sr := e.json(t, "GET", "/api/demos/series/"+sid, "", nil); sr["name"] != "Final (sandy's name)" || len(sr["matches"].([]interface{})) != 2 {
		t.Errorf("the rename: %v", sr)
	}
	if code, out := e.json(t, "GET", "/api/demos/mine", keySandy, nil); code != 200 || len(out["series"].([]interface{})) != 1 {
		t.Errorf("sandy's editable series: %d %v", code, out["series"])
	}
	if code, out := post("PUT", "/api/demos/series/"+sid, keyTravis, "Final", id(a), id(other)); code != 200 {
		t.Fatalf("reordering: %d %v", code, out)
	}
	if _, m := e.json(t, "GET", "/api/demos/matches/"+id(a), "", nil); m["series"].(map[string]interface{})["pos"].(float64) != 1 {
		t.Errorf("the reorder did not show: %v", m["series"])
	}
	if code, out := e.json(t, "GET", "/api/demos/mine", keyTravis, nil); code != 200 || len(out["series"].([]interface{})) != 1 {
		t.Errorf("the key's own series: %d %v", code, out["series"])
	}
	if code, out := e.json(t, "GET", "/api/demos/series", "", nil); code != 200 || out["total"].(float64) != 1 {
		t.Errorf("series list: %d %v", code, out)
	}
	if code, out := post("POST", "/api/demos/series", keyTravis, "", id(a)); code != http.StatusBadRequest {
		t.Errorf("a nameless series: %d %v", code, out)
	}

	// removal: only by the key that uploaded it
	if code, _ := e.json(t, "DELETE", "/api/demos/files/"+fid(b), keySandy, nil); code != http.StatusNotFound {
		t.Errorf("sandy removing travis's upload: %d, want 404", code)
	}
	if code, _ := e.json(t, "DELETE", "/api/demos/files/"+fid(c), keyTravis, nil); code != http.StatusNotFound {
		t.Errorf("travis removing sandy's upload: %d, want 404", code)
	}
	if code, m := e.json(t, "GET", "/api/demos/matches/"+id(a), "", nil); code != 200 || len(m["demos"].([]interface{})) != 3 {
		t.Fatalf("refused removals changed the match: %d %v", code, m)
	}
	// travis removes the shared file: sandy still has it, so it stays
	if code, out := e.json(t, "DELETE", "/api/demos/files/"+fid(a), keyTravis, nil); code != 200 || out["demo_deleted"] != false {
		t.Fatalf("travis removes the shared file: %d %v", code, out)
	}
	if n := e.blobCount(t, "raw"); n != rawBefore {
		t.Errorf("the shared file was deleted while sandy still had it: %d files", n)
	}
	// the demo is still in the match, so the match is unchanged and its merged output is still good
	if n := e.blobCount(t, "paths"); n != 1 {
		t.Errorf("the merged output was thrown away although the match did not change: %d kept", n)
	}
	// then sandy removes it too: now the file goes
	if code, out := e.json(t, "DELETE", "/api/demos/files/"+fid(a), keySandy, nil); code != 200 || out["demo_deleted"] != true || out["match_deleted"] != false {
		t.Fatalf("last claim: %d %v", code, out)
	}
	if n := e.blobCount(t, "raw"); n != rawBefore-1 {
		t.Errorf("the file is still stored: %d, want %d", n, rawBefore-1)
	}
	// now a demo has left the match: its merged output no longer describes it and is gone, to be built again on demand
	if n := e.blobCount(t, "paths"); n != 0 {
		t.Errorf("the merged output outlived a change to its match: %d kept", n)
	}
	if code, _, _ := e.do(t, "GET", "/api/demos/matches/"+id(a)+"/paths", "", nil, "Accept-Encoding", "gzip"); code != 200 {
		t.Errorf("the match is not viewable after one of its demos was removed: %d", code)
	}
	// and the single-demo match goes when its only demo does
	if code, out := e.json(t, "DELETE", "/api/demos/files/"+fid(other), keyTravis, nil); code != 200 || out["match_deleted"] != true {
		t.Fatalf("removing the only demo of a match: %d %v", code, out)
	}
	if code, _, _ := e.do(t, "GET", "/api/demos/matches/"+id(other), "", nil); code != http.StatusNotFound {
		t.Errorf("a match with no demos is still there: %d", code)
	}
	ids := []string{}
	_, list = e.json(t, "GET", "/api/demos/matches", "", nil)
	for _, m := range list["matches"].([]interface{}) {
		ids = append(ids, m.(map[string]interface{})["id"].(string))
	}
	sort.Strings(ids)
	if len(ids) != 2 {
		t.Errorf("matches left: %v", ids)
	}
}

// A spectator's demo of several games is split, one match per game, and linked as a series; the players'
// demos of each game join the match of their game, whichever came first.
func TestDemoSplitsALongDemo(t *testing.T) {
	root := os.Getenv("DEMO_SAMPLES")
	if root == "" || testing.Short() {
		t.Skip("DEMO_SAMPLES not set")
	}
	read := func(sub string) (string, []byte) {
		var found string
		filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && found == "" && strings.Contains(filepath.Base(p), sub) && strings.Contains(filepath.Ext(p), "dm_") {
				found = p
			}
			return nil
		})
		if found == "" {
			t.Fatalf("no sample matching %q", sub)
		}
		b, err := os.ReadFile(found)
		if err != nil {
			t.Fatal(err)
		}
		return filepath.Base(found), b
	}
	players := []string{"191902-mTwaTnJoe", "192634-mTwaTnJoe", "193307-mTwaTnJoe"}
	for _, spectatorFirst := range []bool{true, false} {
		e := newDemoEnv(t, func(c *demoConfig) { c.maxFileBytes = 40 << 20 })
		upload := func(sub string) map[string]interface{} {
			name, data := read(sub)
			code, out := e.json(t, "POST", "/api/demos/upload?name="+url.QueryEscape(name), keyTravis, data)
			if code != http.StatusCreated && code != http.StatusOK {
				t.Fatalf("upload %s: %d %v", sub, code, out)
			}
			return out
		}
		var spec map[string]interface{}
		if spectatorFirst {
			spec = upload("wsw_mtw")
		}
		for _, p := range players {
			upload(p)
		}
		if !spectatorFirst {
			spec = upload("wsw_mtw")
		}
		games, _ := spec["games"].([]interface{})
		if spec["status"] != "split" || len(games) != 3 {
			t.Fatalf("spectator first=%v: status %v games %v", spectatorFirst, spec["status"], spec["games"])
		}
		code, list := e.json(t, "GET", "/api/demos/matches", "", nil)
		ms := list["matches"].([]interface{})
		if code != 200 || len(ms) != 3 {
			t.Fatalf("spectator first=%v: %d matches, want one per game: %v", spectatorFirst, len(ms), list)
		}
		seen := map[string]bool{}
		total := 0
		for _, g := range games {
			id := g.(map[string]interface{})["match_id"].(string)
			if seen[id] {
				t.Errorf("two games in match %s", id)
			}
			seen[id] = true
			code, m := e.json(t, "GET", "/api/demos/matches/"+id, "", nil)
			spectators := 0
			for _, dd := range m["demos"].([]interface{}) {
				if dd.(map[string]interface{})["spectator"] == true {
					spectators++
				}
			}
			// each game holds the spectator's piece of it; the players' demos of it join (the evening's third
			// game has no player demo among the samples uploaded here)
			if code != 200 || spectators != 1 {
				t.Errorf("spectator first=%v match %s: %d spectator pieces: %v", spectatorFirst, id, spectators, m["demos"])
			}
			total += len(m["demos"].([]interface{}))
		}
		if total != 7 { // 3 pieces of the spectator's demo, 4 of the players' (one demo spans two games)
			t.Errorf("spectator first=%v: %d demo rows in all, want 7", spectatorFirst, total)
		}
		sid, _ := spec["series_id"].(string)
		if sid == "" {
			t.Errorf("spectator first=%v: the games were not linked as a series: %v", spectatorFirst, spec)
		}
		if sid != "" {
			code, ser := e.json(t, "GET", "/api/demos/series/"+sid, "", nil)
			if code != 200 || len(ser["matches"].([]interface{})) != 3 {
				t.Errorf("series: %d %v", code, ser)
			}
		}
		// the same file again is claimed for every game; the stored file goes only with the last piece
		again := upload("wsw_mtw")
		if again["status"] != "already_stored" || len(again["games"].([]interface{})) != 3 {
			t.Errorf("again: %v", again)
		}
		t.Logf("spectator first=%v: series=%q", spectatorFirst, sid)
	}
}
