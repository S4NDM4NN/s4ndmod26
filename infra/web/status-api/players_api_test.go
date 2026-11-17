package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"s4ndmod26/status-api/replay"
	"s4ndmod26/status-api/stats"
)

const (
	keyA = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" // Ann's browser
	keyB = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB" // Ann's launcher
	keyC = "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC" // nobody's
	keyD = "DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD" // nobody's, never played
)

type fakeStore struct{ asked [][]string }

func (f *fakeStore) Player(_ context.Context, key string) (*stats.PlayerSummary, error) {
	switch key {
	case keyA:
		return &stats.PlayerSummary{PID: key, Name: "Ann", Pools: []stats.PlayerTotals{{Pool: stats.PoolOfficial, Games: 2, Kills: 20, Deaths: 10}}}, nil
	case keyB:
		return &stats.PlayerSummary{PID: key, Name: "AnnL", Aliases: []stats.Alias{{Name: "ann", Games: 1}}, Pools: []stats.PlayerTotals{{Pool: stats.PoolOfficial, Games: 1, Kills: 5, Deaths: 5}}}, nil
	case keyC:
		return &stats.PlayerSummary{PID: key, Name: "Carl", Pools: []stats.PlayerTotals{{Pool: stats.PoolCommunity, Games: 1, Kills: 3}}}, nil
	}
	return nil, stats.ErrNoPlayer
}

func (f *fakeStore) PlayerHistory(_ context.Context, keys []string, _ string, _, _ int) ([]stats.PlayerGame, int, error) {
	f.asked = append(f.asked, keys)
	return []stats.PlayerGame{{Match: "m"}}, 1, nil
}

func (f *fakeStore) PlayerGameNames(_ context.Context, keys []string) (map[string][]string, error) {
	f.asked = append(f.asked, keys)
	return map[string][]string{
		"replays_1": {"Ann", "annl"}, // POTG is Ann's; one highlight is hers by another name's case, one is Carl's
		"replays_2": {"Ann"},         // no recording any more
		"replays_4": {"Ann"},         // nothing of hers
	}, nil
}

func (f *fakeStore) RecentPlayers(_ context.Context, _ int) ([]stats.RecentPlayer, error) {
	t := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	return []stats.RecentPlayer{
		{PID: keyC, Name: "Carl", At: &t, Match: "m3", Map: "mp_ice", Pool: stats.PoolCommunity},
		{PID: keyB, Name: "AnnL", At: &t, Match: "m2", Map: "mp_base", Pool: stats.PoolOfficial}, // Ann's launcher key: shows as the account
		{PID: keyA, Name: "Ann", At: &t, Match: "m1", Map: "mp_beach", Pool: stats.PoolOfficial}, // Ann's other key: folded into the same entry
	}, nil
}

type fakeDir struct{}

func (fakeDir) AccountOfKey(k string) (uint64, bool) {
	if k == keyA || k == keyB {
		return 1, true
	}
	return 0, false
}
func (fakeDir) KeysOfAccount(a uint64) []string {
	if a == 1 {
		return []string{keyA, keyB}
	}
	if a == 2 {
		return nil
	}
	return nil
}
func (fakeDir) AccountByUsername(_ context.Context, n string) (uint64, bool) {
	switch strings.ToLower(n) {
	case "annsmith":
		return 1, true
	case "newbie":
		return 2, true
	}
	return 0, false
}
func (fakeDir) AccountInfo(_ context.Context, a uint64) (stats.AccountInfo, bool) {
	switch a {
	case 1:
		return stats.AccountInfo{Username: "annsmith", Avatar: "medic_axis"}, true
	case 2:
		return stats.AccountInfo{Username: "newbie"}, true
	}
	return stats.AccountInfo{}, false
}

func getPlayer(t *testing.T, mux *http.ServeMux, path string) (int, stats.PlayerSummary) {
	t.Helper()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	var s stats.PlayerSummary
	json.Unmarshal(w.Body.Bytes(), &s)
	return w.Code, s
}

func TestPlayerPagesResolveAccounts(t *testing.T) {
	fs := &fakeStore{}
	mux := http.NewServeMux()
	playersAPI(fs, fakeDir{}, mux, nil)

	// a key that belongs to nobody is just itself
	if code, s := getPlayer(t, mux, "/api/players/"+keyC); code != 200 || s.Name != "Carl" || s.Account != nil || s.Pools[0].Kills != 3 {
		t.Errorf("an unlinked key: %d %+v", code, s)
	}
	// either of an account's keys, and the username, give the whole account: one page, the account's name and picture
	for _, id := range []string{keyA, keyB, "annsmith", "AnnSmith"} {
		code, s := getPlayer(t, mux, "/api/players/"+id)
		if code != 200 || s.Account == nil || s.Account.Username != "annsmith" || s.Account.Avatar != "medic_axis" || s.Name != "annsmith" || s.Keys != 2 {
			t.Fatalf("%s: %d %+v", id, code, s)
		}
		if p := s.Pools[0]; p.Games != 3 || p.Kills != 25 || p.Deaths != 15 {
			t.Errorf("%s: the keys are not added up: %+v", id, p)
		}
		for _, a := range s.Aliases {
			if a.Name == "annsmith" {
				t.Errorf("%s: the account's own name is listed as an alias", id)
			}
		}
		if len(s.Aliases) == 0 {
			t.Errorf("%s: the in-game names should be the aliases", id)
		}
	}
	// an account that has played nothing yet still has a page; a key that has played nothing does not; nor does a stranger
	if code, s := getPlayer(t, mux, "/api/players/newbie"); code != 200 || s.Account == nil || len(s.Pools) != 0 {
		t.Errorf("a new account: %d %+v", code, s)
	}
	if code, _ := getPlayer(t, mux, "/api/players/"+keyD); code != 404 {
		t.Errorf("a key with no games: %d", code)
	}
	for _, id := range []string{"nobodyhere", "x", "bad%20id", strings.Repeat("z", 30)} {
		if code, _ := getPlayer(t, mux, "/api/players/"+id); code != 404 {
			t.Errorf("%q: %d", id, code)
		}
	}
	// history covers every key of the account
	fs.asked = nil
	getPlayer(t, mux, "/api/players/annsmith/games")
	getPlayer(t, mux, "/api/players/"+keyB+"/games?pool=official")
	getPlayer(t, mux, "/api/players/"+keyC+"/games")
	if len(fs.asked) != 3 || len(fs.asked[0]) != 2 || len(fs.asked[1]) != 2 || len(fs.asked[2]) != 1 || fs.asked[2][0] != keyC {
		t.Errorf("history asked for %v", fs.asked)
	}
	if code, _ := getPlayer(t, mux, "/api/players/"+keyA+"/games?pool=BAD!"); code != 400 {
		t.Errorf("a bad pool: %d", code)
	}
}

func TestPlayerPagesWithoutAccounts(t *testing.T) {
	mux := http.NewServeMux()
	playersAPI(&fakeStore{}, nil, mux, nil)
	if code, s := getPlayer(t, mux, "/api/players/"+keyA); code != 200 || s.Account != nil || s.Name != "Ann" {
		t.Errorf("no accounts: %d %+v", code, s)
	}
	if code, _ := getPlayer(t, mux, "/api/players/annsmith"); code != 404 {
		t.Errorf("a username with no accounts: %d", code)
	}
}

func TestPlayerClips(t *testing.T) {
	list := []replay.Summary{
		{Name: "replays_1", Map: "mp_beach", GeneratedAt: "2026-11-15T10:00:00Z", HasRPL: true, HasPOTG: true, PotgPlayer: "^1ANN", HasStats: true,
			Highlights: []replay.HighlightSummary{{Player: 3, Name: "Carl", Score: 90}, {Player: 5, Name: "ANNL", Score: 70}}},
		{Name: "replays_2", Map: "mp_base", HasRPL: false, HasPOTG: true, PotgPlayer: "Ann"},
		{Name: "replays_3", Map: "mp_ice", HasRPL: true, HasPOTG: true, PotgPlayer: "Ann"}, // a game she has no row in
		{Name: "replays_4", Map: "mp_fuel", HasRPL: true, HasPOTG: true, PotgPlayer: "Dave"},
	}
	body, _ := json.Marshal(list)
	fs := &fakeStore{}
	mux := http.NewServeMux()
	playersAPI(fs, fakeDir{}, mux, func() []byte { return body })

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/players/"+keyA+"/clips", nil))
	var got struct{ Clips []Clip }
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 {
		t.Fatalf("clips: %d %v %s", rec.Code, err, rec.Body.String())
	}
	if len(got.Clips) != 2 || got.Clips[0].Kind != "potg" || got.Clips[0].Match != "replays_1" ||
		got.Clips[1].Kind != "highlight" || got.Clips[1].Player != 5 || got.Clips[1].Score != 70 {
		t.Errorf("clips = %+v: want her play of the game and her highlight in replays_1 only", got.Clips)
	}
	// the account's keys were all asked for
	if last := fs.asked[len(fs.asked)-1]; len(last) != 2 {
		t.Errorf("asked for %v, want both of Ann's keys", last)
	}

	// no list yet (warming up) or an unknown player: no clips, or a 404
	rec = httptest.NewRecorder()
	mux2 := http.NewServeMux()
	playersAPI(&fakeStore{}, fakeDir{}, mux2, nil)
	mux2.ServeHTTP(rec, httptest.NewRequest("GET", "/api/players/"+keyA+"/clips", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"clips":[]`) {
		t.Errorf("no list: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	mux2.ServeHTTP(rec, httptest.NewRequest("GET", "/api/players/nope!/clips", nil))
	if rec.Code != 404 {
		t.Errorf("bad id: %d", rec.Code)
	}
}

func TestRecentPlayers(t *testing.T) {
	mux := http.NewServeMux()
	playersAPI(&fakeStore{}, fakeDir{}, mux, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/players/recent", nil))
	var got struct{ Players []RecentPlayerEntry }
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 {
		t.Fatalf("recent: %d %v %s", rec.Code, err, rec.Body.String())
	}
	if len(got.Players) != 2 || got.Players[0].By != "id" || got.Players[0].Ref != keyC || got.Players[0].Name != "Carl" {
		t.Errorf("players = %+v: want Carl by key, then Ann once", got.Players)
	}
	if got.Players[1].By != "u" || got.Players[1].Map != "mp_base" {
		t.Errorf("Ann's entry = %+v: want the account, with her newest game (the first of her keys in the list)", got.Players[1])
	}

	// no accounts: every key is its own entry; limit cuts the list
	mux = http.NewServeMux()
	playersAPI(&fakeStore{}, nil, mux, nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/players/recent?per=2&page=1", nil))
	got.Players = nil
	json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.Players) != 2 || got.Players[1].Ref != keyB {
		t.Errorf("without accounts: %+v", got.Players)
	}
	// the second page has the rest, and a page past the end is empty, not an error
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/players/recent?per=2&page=2", nil))
	var pg struct {
		Players []RecentPlayerEntry
		Total   int
	}
	json.Unmarshal(rec.Body.Bytes(), &pg)
	if len(pg.Players) != 1 || pg.Players[0].Ref != keyA || pg.Total != 3 {
		t.Errorf("page 2: %+v total %d", pg.Players, pg.Total)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/players/recent?per=2&page=9", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"players":[]`) {
		t.Errorf("past the end: %d %s", rec.Code, rec.Body.String())
	}
}

// Slot 0 is a real player number.  A highlight for it must still say "player":0, or the page cannot tell it from a
// play of the game and opens the wrong clip.
func TestClipKeepsPlayerZero(t *testing.T) {
	b, _ := json.Marshal(Clip{Match: "m", Kind: "highlight", Player: 0, Score: 351})
	if !strings.Contains(string(b), `"player":0`) {
		t.Errorf("clip json %s has no player number", b)
	}
}
