package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"s4ndmod26/status-api/stats"
)

type fakeSource struct {
	game *stats.GameStats
	err  error
}

func (f fakeSource) Game(ctx context.Context, match string) (*stats.GameStats, error) {
	return f.game, f.err
}

func serve(h http.HandlerFunc, path string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/games/{match}/stats", h)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec
}

func TestGameStatsHandler(t *testing.T) {
	game := &stats.GameStats{Match: "replays_20261017_012254", Map: "mp_beach", Rounds: 2,
		Players: []stats.PlayerStats{{Name: "Ann", Team: "axis", Kills: 3}}}

	rec := serve(gameStatsHandler(fakeSource{game: game}), "/api/games/replays_20261017_012254/stats")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("ok case: %d %v", rec.Code, rec.Header())
	}
	var got stats.GameStats
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Map != "mp_beach" || len(got.Players) != 1 || got.Players[0].Kills != 3 {
		t.Errorf("body: %v %+v", err, got)
	}

	cases := []struct {
		name string
		h    http.HandlerFunc
		path string
		want int
	}{
		{"no database", gameStatsHandler(nil), "/api/games/x/stats", 503},
		{"unknown game", gameStatsHandler(fakeSource{err: stats.ErrNoGame}), "/api/games/x/stats", 404},
		{"database error", gameStatsHandler(fakeSource{err: errors.New("boom")}), "/api/games/x/stats", 500},
		{"bad name", gameStatsHandler(fakeSource{game: game}), "/api/games/a%20b/stats", 400},
		{"path traversal", gameStatsHandler(fakeSource{game: game}), "/api/games/..%2f..%2fetc/stats", 400},
	}
	for _, c := range cases {
		rec := serve(c.h, c.path)
		if rec.Code != c.want {
			t.Errorf("%s: status %d, want %d (%s)", c.name, rec.Code, c.want, rec.Body.String())
		}
		var e map[string]string
		if json.Unmarshal(rec.Body.Bytes(), &e) != nil || e["error"] == "" {
			t.Errorf("%s: want a JSON error body, got %q", c.name, rec.Body.String())
		}
	}
}
