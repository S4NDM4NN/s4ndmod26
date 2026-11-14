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

// a username is 3 to 24 letters, digits, _ or - (accounts); a key is 43 characters, so the two can never be confused
var usernameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{3,24}$`)

// playerDirectory says which account a key belongs to and which keys an account has (the link store and the accounts); nil when the site
// has no accounts, and then a player is just a key.
type playerDirectory interface {
	AccountOfKey(key string) (account uint64, ok bool)
	KeysOfAccount(account uint64) []string
	AccountByUsername(ctx context.Context, name string) (account uint64, ok bool)
	AccountInfo(ctx context.Context, account uint64) (stats.AccountInfo, bool)
}

// playerStore is the part of the stats store the player pages use.
type playerStore interface {
	Player(ctx context.Context, key string) (*stats.PlayerSummary, error)
	PlayerHistory(ctx context.Context, keys []string, pool string, limit, offset int) ([]stats.PlayerGame, int, error)
}

// playersAPI serves a player's aggregate stats and match history.  The player is named by one of their keys, or by their account's username;
// a key that is linked to an account stands for the whole account (all its keys added up), under the account's name and picture.
//
//	GET /api/players/{id}                    totals and records per pool (official, community, ...)
//	GET /api/players/{id}/games?pool=&page=  their games, newest first, 25 a page
func playersAPI(s playerStore, dir playerDirectory, mux *http.ServeMux) {
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

	// who is meant: the keys whose games count, and the account when there is one
	type who struct {
		keys    []string
		account *stats.AccountInfo
	}
	resolve := func(ctx context.Context, id string) (who, bool) {
		switch {
		case pidRe.MatchString(id):
			w := who{keys: []string{id}}
			if dir != nil {
				if acct, ok := dir.AccountOfKey(id); ok {
					w.keys = dir.KeysOfAccount(acct)
					if info, ok := dir.AccountInfo(ctx, acct); ok {
						w.account = &info
					}
				}
			}
			return w, true
		case dir != nil && usernameRe.MatchString(id):
			acct, ok := dir.AccountByUsername(ctx, id)
			if !ok {
				return who{}, false
			}
			info, ok := dir.AccountInfo(ctx, acct)
			if !ok {
				return who{}, false
			}
			return who{keys: dir.KeysOfAccount(acct), account: &info}, true
		}
		return who{}, false
	}

	mux.HandleFunc("GET /api/players/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		who, ok := resolve(ctx, id)
		if !ok {
			reply(w, http.StatusNotFound, map[string]string{"error": "no such player"})
			return
		}
		var parts []*stats.PlayerSummary
		for _, k := range who.keys {
			sum, err := s.Player(ctx, k)
			if errors.Is(err, stats.ErrNoPlayer) {
				continue // a key with no games yet
			}
			if err != nil {
				oops(w, err)
				return
			}
			parts = append(parts, sum)
		}
		var out *stats.PlayerSummary
		switch {
		case who.account == nil && len(parts) == 1:
			out = parts[0] // a key that belongs to nobody: exactly its own summary
		case len(parts) > 0:
			out = stats.MergePlayers(parts)
		case who.account != nil:
			out = &stats.PlayerSummary{Pools: []stats.PlayerTotals{}} // an account that has not played anything yet
		default:
			oops(w, stats.ErrNoPlayer)
			return
		}
		out.PID = id
		if who.account != nil {
			out.Account = who.account
			out.Name = who.account.Username
			out.Keys = len(parts)
			// the in-game names are the "also played as" list, and the account's own name is not one of them
			keep := out.Aliases[:0]
			for _, a := range out.Aliases {
				if a.Name != who.account.Username {
					keep = append(keep, a)
				}
			}
			out.Aliases = keep
		}
		reply(w, http.StatusOK, out)
	})

	mux.HandleFunc("GET /api/players/{id}/games", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		pool := r.URL.Query().Get("pool")
		if pool != "" && !poolRe.MatchString(pool) {
			reply(w, http.StatusBadRequest, map[string]string{"error": "bad pool"})
			return
		}
		const per = 25
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		who, ok := resolve(ctx, id)
		if !ok {
			reply(w, http.StatusNotFound, map[string]string{"error": "no such player"})
			return
		}
		games, total, err := s.PlayerHistory(ctx, who.keys, pool, per, (page-1)*per)
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
