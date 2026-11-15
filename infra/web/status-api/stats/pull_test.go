package stats

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAgent serves /v1/stats and /v1/replays like the real agent (a listing, and each file with Range support).
type fakeAgent struct {
	mu     sync.Mutex
	stats  map[string]string
	replay map[string]string
	srv    *httptest.Server
}

func newFakeAgent(t *testing.T) *fakeAgent {
	f := &fakeAgent{stats: map[string]string{}, replay: map[string]string{}}
	mux := http.NewServeMux()
	handle := func(kind string, files func() map[string]string) {
		mux.HandleFunc("GET /v1/"+kind, func(w http.ResponseWriter, r *http.Request) {
			f.mu.Lock()
			defer f.mu.Unlock()
			var out []map[string]any
			for n, c := range files() {
				out = append(out, map[string]any{"name": n, "size": len(c)})
			}
			json.NewEncoder(w).Encode(out)
		})
		mux.HandleFunc("GET /v1/"+kind+"/{name}", func(w http.ResponseWriter, r *http.Request) {
			f.mu.Lock()
			c, ok := files()[r.PathValue("name")]
			f.mu.Unlock()
			if !ok {
				http.NotFound(w, r)
				return
			}
			http.ServeContent(w, r, "x", time.Time{}, strings.NewReader(c))
		})
	}
	handle("stats", func() map[string]string { return f.stats })
	handle("replays", func() map[string]string { return f.replay })
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAgent) set(name, content string) {
	f.mu.Lock()
	f.stats[name] = content
	f.mu.Unlock()
}

func TestPullTagsGamesByServerAndPurges(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	p := NewPuller(s)
	a, b := newFakeAgent(t), newFakeAgent(t)
	ra := Remote{ServerID: "aaaaaaaaaaaaaaaa", BaseURL: a.srv.URL, Private: true}
	rb := Remote{ServerID: "bbbbbbbbbbbbbbbb", BaseURL: b.srv.URL, Private: true}
	const name = "replays_20261017_012254.jsonl"

	// both servers call their game by the same name
	a.set(name, line("round_end", 1, 3, "Ann", "axis", 4, 2, 30, `"Luger":[5,10,1,0,0]`))
	b.set(name, line("round_end", 1, 3, "Bob", "allies", 9, 1, 90, `"Luger":[5,10,1,0,0]`))
	for _, r := range []Remote{ra, rb} {
		if n, _, err := p.Pull(ctx, r); err != nil || n != 1 {
			t.Fatalf("pull %s: %d %v", r.ServerID, n, err)
		}
	}
	ga, err := s.Game(ctx, GameName(ra.ServerID, "replays_20261017_012254"))
	if err != nil || len(ga.Players) != 1 || ga.Players[0].Name != "Ann" {
		t.Fatalf("game of a: %+v %v", ga, err)
	}
	gb, err := s.Game(ctx, GameName(rb.ServerID, "replays_20261017_012254"))
	if err != nil || len(gb.Players) != 1 || gb.Players[0].Name != "Bob" {
		t.Fatalf("game of b: %+v %v", gb, err)
	}
	if ga.StartedAt == nil || ga.StartedAt.Year() != 2026 {
		t.Errorf("the start time should come from the server's own name: %v", ga.StartedAt)
	}

	// nothing new: nothing stored; appended lines are read from where it stopped
	if n, _, err := p.Pull(ctx, ra); err != nil || n != 0 {
		t.Errorf("a second pull: %d %v", n, err)
	}
	a.set(name, a.stats[name]+line("round_end", 2, 3, "Ann", "allies", 6, 1, 55, `"Luger":[5,10,1,0,0]`))
	if n, _, err := p.Pull(ctx, ra); err != nil || n != 1 {
		t.Errorf("after an append: %d %v", n, err)
	}

	// purge a's games only
	n, err := s.CountGames(ctx, ra.ServerID)
	if err != nil || n != 1 {
		t.Fatalf("count: %d %v", n, err)
	}
	if n, err := s.PurgeServer(ctx, ra.ServerID); err != nil || n != 1 {
		t.Fatalf("purge: %d %v", n, err)
	}
	if _, err := s.Game(ctx, GameName(ra.ServerID, "replays_20261017_012254")); err == nil {
		t.Errorf("a purged game is still there")
	}
	if _, err := s.Game(ctx, GameName(rb.ServerID, "replays_20261017_012254")); err != nil {
		t.Errorf("another server's game went too: %v", err)
	}
	if _, err := s.PurgeServer(ctx, ""); err == nil {
		t.Errorf("purging the local folder should be refused")
	}
	// turned back on, it is read again from the start
	p.Forget(ra.ServerID)
	if n, _, err := p.Pull(ctx, ra); err != nil || n != 2 {
		t.Errorf("after the purge: %d %v", n, err)
	}
}

func TestPullIgnoresOddNames(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	p := NewPuller(s)
	a := newFakeAgent(t)
	r := Remote{ServerID: "aaaaaaaaaaaaaaaa", BaseURL: a.srv.URL, Private: true}
	a.set("../evil.jsonl", line("round_end", 1, 3, "Ann", "axis", 1, 1, 1, `"Luger":[1,1,1,0,0]`))
	a.set("ok.jsonl", strings.Replace(line("round_end", 1, 3, "Ann", "axis", 1, 1, 1, `"Luger":[1,1,1,0,0]`), "replays_20261017_012254", "a/b ../c", 1))
	if n, _, err := p.Pull(ctx, r); err != nil || n != 0 {
		t.Errorf("odd names stored %d lines (%v)", n, err)
	}
}

func TestPublicClientRefusesPrivateAddresses(t *testing.T) {
	s := newTestStore(t)
	p := NewPuller(s)
	a := newFakeAgent(t) // on 127.0.0.1
	if _, _, err := p.Pull(context.Background(), Remote{ServerID: "aaaaaaaaaaaaaaaa", BaseURL: a.srv.URL}); err == nil {
		t.Errorf("a community server on a loopback address was reached")
	}
}

func TestBrokenReplayIsNotRetried(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	p := NewPuller(s)
	a := newFakeAgent(t)
	r := Remote{ServerID: "aaaaaaaaaaaaaaaa", BaseURL: a.srv.URL, Private: true}
	a.set("replays_20261017_012254.jsonl", line("round_end", 1, 3, "Ann", "axis", 1, 1, 1, `"Luger":[1,1,1,0,0]`))
	a.replay["replays_20261017_012254.rpl"] = "not a replay"
	a.replay["replays_20261017_012254.txt"] = "x"
	if _, _, err := p.Pull(ctx, r); err != nil {
		t.Fatal(err)
	}
	if done, _ := s.FeedDone(ctx, r.ServerID, "replays_20261017_012254"); !done {
		t.Errorf("a replay that cannot be read should be marked, not retried")
	}
}

func TestPullIndexesReplaysWithoutStoringThem(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	p := NewPuller(s)
	a := newFakeAgent(t)
	r := Remote{ServerID: "aaaaaaaaaaaaaaaa", BaseURL: a.srv.URL, Private: true}
	const m = "replays_20261017_012254"
	a.replay[m+".summary.json"] = `{"name":"` + m + `","map":"mp_beach","gametype":6,"duration_ms":600000,"kill_count":12,"player_count":4,"has_humans":true,"has_potg":true,"potg_player":"Ann"}`
	a.replay[m+".rpl"] = "raw"
	a.replay[m+".txt"] = "meta"
	if _, _, err := p.Pull(ctx, r); err != nil {
		t.Fatal(err)
	}
	list, err := s.IndexedReplays(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("index: %v %v", list, err)
	}
	got := list[0]
	if got.Name != GameName(r.ServerID, m) || got.ServerID != r.ServerID || got.Map != "mp_beach" || !got.Has3D || !got.HasRPL || got.HasStats || got.PotgPlayer != "Ann" {
		t.Errorf("entry: %+v", got)
	}
	base, name, sid, ok, err := s.ReplaySource(ctx, GameName(r.ServerID, m))
	if err != nil || !ok || name != m || sid != r.ServerID || base != a.srv.URL+"/v1/replays" {
		t.Errorf("source: %q %q %q %v %v", base, name, sid, ok, err)
	}
	if _, _, _, ok, _ := s.ReplaySource(ctx, "replays_20261017_012254"); ok {
		t.Errorf("a local name was found in the index")
	}

	// the raw replay is pruned on the server: the entry stays (the analysis is still there) but cannot be watched in 3D
	delete(a.replay, m+".rpl")
	if _, _, err := p.Pull(ctx, r); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.IndexedReplays(ctx); len(list) != 1 || list[0].Has3D {
		t.Errorf("after the rpl went: %+v", list)
	}
	// the whole game is pruned: it leaves the index
	delete(a.replay, m+".summary.json")
	if _, _, err := p.Pull(ctx, r); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.IndexedReplays(ctx); len(list) != 0 {
		t.Errorf("a pruned replay is still indexed: %+v", list)
	}
	// and a purge takes the index too
	a.replay[m+".summary.json"] = `{"name":"x","map":"mp_beach"}`
	p.Pull(ctx, r)
	if list, _ := s.IndexedReplays(ctx); len(list) != 1 {
		t.Fatalf("not indexed again: %v", list)
	}
	if _, err := s.PurgeServer(ctx, r.ServerID); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.IndexedReplays(ctx); len(list) != 0 {
		t.Errorf("the purge left the index: %+v", list)
	}
}
