package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"s4ndmod26/status-api/stats"
)

var pidRe = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// playersAPI serves a player's aggregate stats and match history (the player is their key; an account links keys later).
//
//	GET /api/players/{pid}                    totals and records per pool (official, community, ...)
//	GET /api/players/{pid}/games?pool=&page=  their games, newest first, 25 a page
func playersAPI(s *stats.Store, mux *http.ServeMux) {
	reply := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if code == http.StatusOK {
			w.Header().Set("Cache-Control", "public, max-age=30")
		}
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(v)
	}
	oops := func(w http.ResponseWriter, err error) {
		if errors.Is(err, stats.ErrNoPlayer) {
			reply(w, http.StatusNotFound, map[string]string{"error": "no games for that player"})
			return
		}
		log.Printf("players: %v", err)
		reply(w, http.StatusInternalServerError, map[string]string{"error": "could not load the stats"})
	}
	mux.HandleFunc("GET /api/players/{pid}", func(w http.ResponseWriter, r *http.Request) {
		pid := r.PathValue("pid")
		if !pidRe.MatchString(pid) {
			reply(w, http.StatusBadRequest, map[string]string{"error": "bad player id"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		sum, err := s.Player(ctx, pid)
		if err != nil {
			oops(w, err)
			return
		}
		reply(w, http.StatusOK, sum)
	})
	mux.HandleFunc("GET /api/players/{pid}/games", func(w http.ResponseWriter, r *http.Request) {
		pid := r.PathValue("pid")
		pool := r.URL.Query().Get("pool")
		if !pidRe.MatchString(pid) || (pool != "" && !poolRe.MatchString(pool)) {
			reply(w, http.StatusBadRequest, map[string]string{"error": "bad player id or pool"})
			return
		}
		const per = 25
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		games, total, err := s.PlayerHistory(ctx, pid, pool, per, (page-1)*per)
		if err != nil {
			oops(w, err)
			return
		}
		if games == nil {
			games = []stats.PlayerGame{}
		}
		reply(w, http.StatusOK, map[string]any{"games": games, "total": total, "page": page, "per": per})
	})
}

var poolRe = regexp.MustCompile(`^[a-z0-9-]{1,24}$`)
