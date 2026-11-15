package main

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"s4ndmod26/status-api/replay"
)

func writeSidecar(t *testing.T, dir, base, mapName string, potg bool) {
	t.Helper()
	b, _ := json.Marshal(replay.Summary{Name: base, Map: mapName, HasPOTG: potg})
	if err := os.WriteFile(filepath.Join(dir, base+".summary.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	// the list only shows games that have an analysis file
	if err := os.WriteFile(filepath.Join(dir, base+".json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func listed(t *testing.T, g *gamesList) []replay.Summary {
	t.Helper()
	var out []replay.Summary
	if err := json.Unmarshal(g.get(), &out); err != nil {
		t.Fatalf("list body is not JSON: %v (%q)", err, g.get())
	}
	return out
}

func TestRefreshGamesList(t *testing.T) {
	dir := t.TempDir()
	writeSidecar(t, dir, "replays_20261016_100000", "mp_beach", true)
	writeSidecar(t, dir, "replays_20261017_100000", "mp_base", false)
	os.WriteFile(filepath.Join(dir, "replays_20261016_100000.rpl"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "replays_20261016_100000.txt"), []byte("x"), 0o644)
	var g gamesList
	known := map[string]replay.Summary{}

	refreshGamesList(dir, "", known, &g)
	got := listed(t, &g)
	if len(got) != 2 {
		t.Fatalf("listed %d games, want 2 (sidecars are not games): %+v", len(got), got)
	}
	if got[0].Name != "replays_20261017_100000" || got[1].Name != "replays_20261016_100000" {
		t.Errorf("not newest first: %s, %s", got[0].Name, got[1].Name)
	}
	if got[0].HasRPL || !got[1].HasRPL {
		t.Errorf("HasRPL = %v/%v, want false/true (needs POTG plus .rpl and .txt)", got[0].HasRPL, got[1].HasRPL)
	}

	if got[0].Has3D || !got[1].Has3D {
		t.Errorf("Has3D = %v/%v, want false/true (the .rpl alone)", got[0].Has3D, got[1].Has3D)
	}

	// the replay is pruned before the analysis: the next refresh notices without re-reading anything
	os.Remove(filepath.Join(dir, "replays_20261016_100000.rpl"))
	refreshGamesList(dir, "", known, &g)
	if got = listed(t, &g); got[1].HasRPL || got[1].Has3D {
		t.Error("HasRPL and Has3D must be checked on every refresh")
	}

	// a game pruned from disk leaves the list and the cache
	os.Remove(filepath.Join(dir, "replays_20261016_100000.json"))
	os.Remove(filepath.Join(dir, "replays_20261016_100000.summary.json"))
	refreshGamesList(dir, "", known, &g)
	if got = listed(t, &g); len(got) != 1 || len(known) != 1 {
		t.Errorf("after prune: %d listed, %d cached, want 1 and 1", len(got), len(known))
	}
}

func TestRefreshGamesListEmptyIsAnEmptyArray(t *testing.T) {
	var g gamesList
	refreshGamesList(t.TempDir(), "", map[string]replay.Summary{}, &g)
	if string(g.get()) != "[]" {
		t.Errorf("empty dir gave %q, want []", g.get())
	}
}

func TestReplayListHandler(t *testing.T) {
	var g gamesList
	h := replayListHandler(&g)

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("GET", "/api/replays", nil))
	if rec.Code != 503 {
		t.Errorf("before the first refresh: status %d, want 503 so the page does not show 'no games'", rec.Code)
	}

	g.set([]byte(`[{"name":"a"}]`))
	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest("GET", "/api/replays", nil))
	if rec.Code != 200 || rec.Body.String() != `[{"name":"a"}]` || rec.Header().Get("Content-Encoding") != "" {
		t.Errorf("plain: %d %q enc=%q", rec.Code, rec.Body.String(), rec.Header().Get("Content-Encoding"))
	}
	if rec.Header().Get("Vary") != "Accept-Encoding" || rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("headers: %v", rec.Header())
	}

	gzGet := func() string {
		req := httptest.NewRequest("GET", "/api/replays", nil)
		req.Header.Set("Accept-Encoding", "gzip, deflate")
		rec := httptest.NewRecorder()
		h(rec, req)
		if rec.Header().Get("Content-Encoding") != "gzip" {
			t.Fatalf("not gzipped: %v", rec.Header())
		}
		zr, err := gzip.NewReader(rec.Body)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(zr)
		return string(b)
	}
	if got := gzGet(); got != `[{"name":"a"}]` {
		t.Errorf("gzip body %q", got)
	}
	// a new list replaces the cached compressed copy
	g.set([]byte(`[{"name":"b"},{"name":"c"}]`))
	if got := gzGet(); got != `[{"name":"b"},{"name":"c"}]` {
		t.Errorf("stale gzip after the list changed: %q", got)
	}
}
