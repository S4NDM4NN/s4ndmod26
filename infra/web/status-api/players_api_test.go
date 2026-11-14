package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	playersAPI(fs, fakeDir{}, mux)

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
	playersAPI(&fakeStore{}, nil, mux)
	if code, s := getPlayer(t, mux, "/api/players/"+keyA); code != 200 || s.Account != nil || s.Name != "Ann" {
		t.Errorf("no accounts: %d %+v", code, s)
	}
	if code, _ := getPlayer(t, mux, "/api/players/annsmith"); code != 404 {
		t.Errorf("a username with no accounts: %d", code)
	}
}
