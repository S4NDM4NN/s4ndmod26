package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"s4ndmod26/status-api/replay"
)

// pathsPlayer is a path with who it was.
type pathsPlayer struct {
	replay.PlayerPath
	Name string `json:"name"`
}

type pathsResponse struct {
	*replay.Paths
	Players []pathsPlayer `json:"players"`
}

// pathsCache keeps the last few built answers: reading and thinning a replay is the cost, and a finished game never changes.
type pathsCache struct {
	mu    sync.Mutex
	order []string
	items map[string][]byte
}

func (c *pathsCache) get(k string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.items[k]
	return b, ok
}

func (c *pathsCache) put(k string, b []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		c.items = map[string][]byte{}
	}
	if _, ok := c.items[k]; !ok {
		c.order = append(c.order, k)
		for len(c.order) > 8 {
			delete(c.items, c.order[0])
			c.order = c.order[1:]
		}
	}
	c.items[k] = b
}

// playerNames reads who held each slot from the game's analysis file (its display names), when there is one.
func playerNames(replayDir, match string) map[int]string {
	out := map[int]string{}
	b, err := os.ReadFile(filepath.Join(replayDir, match+".json"))
	if err != nil {
		return out
	}
	var a struct {
		Players map[string]struct {
			DisplayName string `json:"display_name"`
		} `json:"players"`
	}
	if json.Unmarshal(b, &a) != nil {
		return out
	}
	for k, p := range a.Players {
		n := 0
		for _, c := range k {
			if c < '0' || c > '9' {
				n = -1
				break
			}
			n = n*10 + int(c-'0')
		}
		if n >= 0 {
			out[n] = p.DisplayName
		}
	}
	return out
}

// gamePathsHandler serves GET /api/games/{match}/paths: where every player was over the game (one position every 250 ms while alive),
// for the 3D movement view.  Straight from the recording, so it works for any game that still has its replay.
func gamePathsHandler(replayDir string) http.HandlerFunc {
	cache := &pathsCache{}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		fail := func(code int, msg string) {
			w.WriteHeader(code)
			json.NewEncoder(w).Encode(map[string]string{"error": msg})
		}
		match := r.PathValue("match")
		if !matchNameRe.MatchString(match) {
			fail(http.StatusBadRequest, "bad game name")
			return
		}
		body, ok := cache.get(match)
		if !ok {
			rep, err := replay.Parse(filepath.Join(replayDir, match+".rpl"))
			if err != nil {
				if os.IsNotExist(err) {
					fail(http.StatusNotFound, "no replay for that game")
				} else {
					log.Printf("paths %s: %v", match, err)
					fail(http.StatusInternalServerError, "could not read the replay")
				}
				return
			}
			p := replay.BuildPaths(rep, 250)
			names := playerNames(replayDir, match)
			resp := pathsResponse{Paths: p}
			for _, pp := range p.Players {
				n := names[pp.Slot]
				if n == "" {
					n = "Player"
				}
				resp.Players = append(resp.Players, pathsPlayer{PlayerPath: pp, Name: n})
			}
			body, err = json.Marshal(resp)
			if err != nil {
				fail(http.StatusInternalServerError, "could not build the paths")
				return
			}
			cache.put(match, body)
		}
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.Write(body)
	}
}
