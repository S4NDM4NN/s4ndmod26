package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"regexp"
	"time"

	"s4ndmod26/status-api/stats"
)

// gameStatsSource is what the stats endpoint needs from the database (a *stats.Store, or a fake in tests).
type gameStatsSource interface {
	Game(ctx context.Context, match string) (*stats.GameStats, error)
	Feed(ctx context.Context, match string) ([]stats.FeedEvent, error)
}

var matchNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

// gameStatsHandler serves GET /api/games/{match}/stats: one game's players, their weapon numbers and the topshots.  src is
// nil when no database is configured.
func gameStatsHandler(src gameStatsSource) http.HandlerFunc {
	return gameHandler(src, func(ctx context.Context, s gameStatsSource, match string) (interface{}, error) {
		return s.Game(ctx, match)
	})
}

// gameFeedHandler serves GET /api/games/{match}/feed: the game's events in order.
func gameFeedHandler(src gameStatsSource) http.HandlerFunc {
	return gameHandler(src, func(ctx context.Context, s gameStatsSource, match string) (interface{}, error) {
		return s.Feed(ctx, match)
	})
}

func gameHandler(src gameStatsSource, load func(context.Context, gameStatsSource, string) (interface{}, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		fail := func(code int, msg string) {
			w.WriteHeader(code)
			json.NewEncoder(w).Encode(map[string]string{"error": msg})
		}
		if src == nil {
			fail(http.StatusServiceUnavailable, "game stats are not available (no database configured)")
			return
		}
		match := r.PathValue("match")
		if !matchNameRe.MatchString(match) {
			fail(http.StatusBadRequest, "bad game name")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		g, err := load(ctx, src, match)
		if errors.Is(err, stats.ErrNoGame) {
			fail(http.StatusNotFound, "no stats for that game")
			return
		}
		if err != nil {
			log.Printf("game %s: %v", match, err)
			fail(http.StatusInternalServerError, "could not load the stats")
			return
		}
		// a finished game never changes; one still being played changes at round ends, so keep this short
		w.Header().Set("Cache-Control", "public, max-age=15")
		json.NewEncoder(w).Encode(g)
	}
}

// startGameStats connects to Postgres when DATABASE_URL is set and keeps ingesting statsDir into it.  Without a database the
// games pages simply have no stats.  The returned source is nil in that case.
func startGameStats(statsDir, replayDir string) gameStatsSource {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Printf("stats: DATABASE_URL not set, game stats are off")
		return nil
	}
	store, err := stats.Open(context.Background(), dsn)
	if err != nil {
		log.Printf("stats: bad DATABASE_URL: %v (game stats are off)", err)
		return nil
	}
	go store.Run(context.Background(), statsDir, replayDir, 15*time.Second)
	return store
}
