package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"s4ndmod26/status-api/replay"
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
	PlayerGameNames(ctx context.Context, keys []string) (map[string][]string, error)
	RecentPlayers(ctx context.Context, games int) ([]stats.RecentPlayer, error)
}

// RecentPlayerEntry is one line of the stats page's "recent players": who, how to open their page, and their newest game.
type RecentPlayerEntry struct {
	Name  string     `json:"name"`
	By    string     `json:"by"` // "u": open the page with ?u=<Ref> (a key linked to an account); "id": ?id=<Ref> (a key)
	Ref   string     `json:"ref"`
	At    *time.Time `json:"at,omitempty"`
	Match string     `json:"match"`
	Map   string     `json:"map"`
	Pool  string     `json:"pool"`
}

// recentPlayersOf turns the keys that played lately into the list the page shows: keys linked to one account become one entry under the
// account's name (the player page does the same), the newest first.
func recentPlayersOf(ctx context.Context, rows []stats.RecentPlayer, dir playerDirectory) []RecentPlayerEntry {
	out := []RecentPlayerEntry{}
	seen := map[uint64]bool{}
	for _, r := range rows {
		e := RecentPlayerEntry{Name: r.Name, By: "id", Ref: r.PID, At: r.At, Match: r.Match, Map: r.Map, Pool: r.Pool}
		if dir != nil {
			if acct, ok := dir.AccountOfKey(r.PID); ok {
				if seen[acct] {
					continue
				}
				seen[acct] = true
				if info, ok := dir.AccountInfo(ctx, acct); ok {
					e.Name, e.By, e.Ref = info.Username, "u", info.Username
				}
			}
		}
		out = append(out, e)
	}
	return out
}

// recentPlayersWindow is how many of the newest games the recent-players list looks through; the list can be paged as far back as that.
const recentPlayersWindow = 2000

// Clip is one watchable play of a player's: the play of the game of a game they were in, or their own highlight there.
type Clip struct {
	Match   string `json:"match"` // the replay (and game) name, as /play/?replay= and /game.html?r= take it
	Map     string `json:"map"`
	At      string `json:"at,omitempty"`     // when the game was recorded
	Kind    string `json:"kind"`             // potg | highlight
	Player  int    `json:"player"`           // a highlight's player number (the replay player's &player=)
	Score   int32  `json:"score,omitempty"`  // a highlight's score
	HasStat bool   `json:"has_stats"`
}

var colourCodeRe = regexp.MustCompile(`\^[0-9A-Za-z]`)

func clipKey(name string) string {
	return strings.ToLower(strings.TrimSpace(colourCodeRe.ReplaceAllString(name, "")))
}

// clipsOf picks the clips that belong to a player out of the games list: a game's play of the game is theirs when its player has a name
// the player was using in that game, and the same for each highlight.  Only games whose recording still exists have clips.  The list is
// newest first and so are the clips.
func clipsOf(list []replay.Summary, names map[string][]string) []Clip {
	out := []Clip{}
	for _, g := range list {
		mine := map[string]bool{}
		for _, n := range names[g.Name] {
			mine[clipKey(n)] = true
		}
		if len(mine) == 0 || !g.HasRPL {
			continue
		}
		if g.HasPOTG && g.PotgPlayer != "" && mine[clipKey(g.PotgPlayer)] {
			out = append(out, Clip{Match: g.Name, Map: g.Map, At: g.GeneratedAt, Kind: "potg", HasStat: g.HasStats})
		}
		for _, h := range g.Highlights {
			if mine[clipKey(h.Name)] {
				out = append(out, Clip{Match: g.Name, Map: g.Map, At: g.GeneratedAt, Kind: "highlight", Player: h.Player, Score: h.Score, HasStat: g.HasStats})
			}
		}
	}
	return out
}

// playersAPI serves a player's aggregate stats and match history.  The player is named by one of their keys, or by their account's username;
// a key that is linked to an account stands for the whole account (all its keys added up), under the account's name and picture.
//
//	GET /api/players/{id}                    totals and records per pool (official, community, ...)
//	GET /api/players/{id}/games?pool=&page=  their games, newest first, 25 a page
//	GET /api/players/recent?page=&per=       the keys that played in the latest games, the newest first, 12 a page (the stats page)
//	GET /api/players/{id}/clips              their play-of-the-game and highlight clips from the games still recorded, newest first
func playersAPI(s playerStore, dir playerDirectory, mux *http.ServeMux, games func() []byte) {
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

	// who played most recently, for the stats page (a literal path, so it wins over {id})
	mux.HandleFunc("GET /api/players/recent", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		per, _ := strconv.Atoi(r.URL.Query().Get("per"))
		if per < 1 || per > 50 {
			per = 12
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		rows, err := s.RecentPlayers(ctx, recentPlayersWindow)
		if err != nil {
			oops(w, err)
			return
		}
		all := recentPlayersOf(ctx, rows, dir)
		from, to := (page-1)*per, page*per
		if from > len(all) {
			from = len(all)
		}
		if to > len(all) {
			to = len(all)
		}
		reply(w, http.StatusOK, map[string]any{"players": all[from:to], "total": len(all), "page": page, "per": per})
	})

	mux.HandleFunc("GET /api/players/{id}/clips", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		who, ok := resolve(ctx, r.PathValue("id"))
		if !ok {
			reply(w, http.StatusNotFound, map[string]string{"error": "no such player"})
			return
		}
		var list []replay.Summary
		if games != nil {
			if body := games(); body != nil {
				json.Unmarshal(body, &list) // a list that does not parse is no list: no clips, not an error
			}
		}
		names, err := s.PlayerGameNames(ctx, who.keys)
		if err != nil {
			oops(w, err)
			return
		}
		reply(w, http.StatusOK, map[string]any{"clips": clipsOf(list, names)})
	})
}

var poolRe = regexp.MustCompile(`^[a-z0-9-]{1,24}$`)
