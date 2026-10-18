package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"s4ndmod26/status-api/replay"
	"s4ndmod26/status-api/stats"
)

// liveViewDelay is how far behind the running game the live 3D view is, from LIVE_VIEW_DELAY_SECONDS.  It is never shorter than
// replay.MinLiveDelayMs: the view shows where everybody is, so it has to be late enough that it cannot help anyone playing.
func liveViewDelay(env string) time.Duration {
	min := time.Duration(replay.MinLiveDelayMs) * time.Millisecond
	if env == "" {
		return min
	}
	secs, err := strconv.Atoi(env)
	if err != nil {
		log.Printf("LIVE_VIEW_DELAY_SECONDS=%q is not a number; using %v", env, min)
		return min
	}
	d := time.Duration(secs) * time.Second
	if d < min {
		log.Printf("LIVE_VIEW_DELAY_SECONDS=%d is below the %v minimum; using the minimum", secs, min)
		return min
	}
	return d
}

const (
	liveViewWindowMs = 240_000 // how much of the past the live view sends: the viewer never draws more trail than this
	liveViewFeedMax  = 150     // newest feed lines sent
	liveViewStepMs   = 200
	liveViewMaxAge   = 3 * time.Minute // a recording that has not grown for this long is not a running game
	liveViewMinBuild = 3 * time.Second // the view is built at most this often, however many people watch
)

// liveViewResponse is what the 3D viewer needs to follow a running game, as it was a minute and a half ago or more.
type liveViewResponse struct {
	Live        bool `json:"live"`    // a game is running
	Waiting     bool `json:"waiting"` // ... but not enough of it is old enough to show yet
	DelayS      int  `json:"delay_s"`
	SecondsLeft int  `json:"seconds_left"` // while waiting: roughly how long until there is something to show
	*replay.Paths
	Players     []pathsPlayer     `json:"players"`
	Shots       []replay.Shot     `json:"shots"`
	Carries     []viewCarry       `json:"carries"`
	Feed        []stats.FeedEvent `json:"feed"`
	GameStartMs int32             `json:"game_start_ms"` // feed times are since this
}

type liveViewCache struct {
	mu      sync.Mutex
	key     string
	builtAt time.Time
	body    []byte
}

// liveViewHandler serves GET /api/live/view.  The answer is built once and shared, since everyone watching the same game is
// shown the same thing.
func liveViewHandler(dir string, delay time.Duration) http.HandlerFunc {
	cache := &liveViewCache{}
	delayMs := int32(delay / time.Millisecond)
	build := func() []byte {
		resp := liveViewResponse{DelayS: int(delay / time.Second)}
		rplPath, _, found := replay.FindLiveReplay(dir)
		if found {
			if st, err := os.Stat(rplPath); err != nil || time.Since(st.ModTime()) > liveViewMaxAge {
				found = false
			}
		}
		if !found {
			b, _ := json.Marshal(resp)
			return b
		}
		resp.Live = true
		rep, err := replay.ParseLive(rplPath)
		if err != nil || len(rep.Frames) == 0 {
			resp.Waiting, resp.SecondsLeft = true, resp.DelayS
			b, _ := json.Marshal(resp)
			return b
		}
		newest := rep.Frames[len(rep.Frames)-1].ServerTime
		shown := replay.DelayLive(rep, delayMs)
		if len(shown.Frames) == 0 {
			resp.Waiting = true
			resp.SecondsLeft = int((delayMs-(newest-rep.Frames[0].ServerTime))/1000) + 1
			b, _ := json.Marshal(resp)
			return b
		}
		end := shown.Frames[len(shown.Frames)-1].ServerTime
		opt := replay.PathOptions{StepMs: liveViewStepMs, FromMs: end - liveViewWindowMs, ToMs: end}
		paths := replay.BuildPathsWith(shown, opt)
		paths.StartMs, paths.EndMs = opt.FromMs, end
		a := replay.AnalyzeLive(shown)
		resp.Paths = paths
		resp.Shots = replay.BuildShots(shown, opt)
		resp.Carries = viewCarries(a.FlagCarries)
		resp.GameStartMs = a.Meta.StartTimeMs
		resp.Players = []pathsPlayer{}
		for _, pp := range paths.Players {
			resp.Players = append(resp.Players, pathsPlayer{PlayerPath: pp, Name: nameAtTime(a.Players[itoa(int32(pp.Slot))], end)})
		}
		resp.Feed = stats.FeedFromAnalysis(a)
		if len(resp.Feed) > liveViewFeedMax {
			resp.Feed = resp.Feed[len(resp.Feed)-liveViewFeedMax:]
		}
		if resp.Feed == nil {
			resp.Feed = []stats.FeedEvent{}
		}
		b, err := json.Marshal(resp)
		if err != nil {
			log.Printf("live view: %v", err)
			b, _ = json.Marshal(liveViewResponse{DelayS: resp.DelayS})
		}
		return b
	}
	return func(w http.ResponseWriter, r *http.Request) {
		cache.mu.Lock()
		if cache.body == nil || time.Since(cache.builtAt) >= liveViewMinBuild {
			cache.body = build()
			cache.builtAt = time.Now()
		}
		body := cache.body
		cache.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(body)
	}
}
