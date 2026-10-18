package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"s4ndmod26/status-api/replay"
)

func TestBuildFights(t *testing.T) {
	a := &replay.Analysis{
		Meta: replay.AnalysisMeta{StartTimeMs: 1000},
		Players: map[string]*replay.PlayerInfo{
			"1": {DisplayName: "Ann", Team: 1},
			"3": {DisplayName: "Cy", Team: 2, NameHistory: []replay.NameSpan{{FromMs: 0, ToMs: 5000, Name: "Old"}, {FromMs: 5000, ToMs: -1, Name: "Cy"}}},
		},
		Engagements: []replay.Engagement{{
			Index: 0, StartMs: 4000, EndMs: 9000, Classification: "1v1", TotalDamage: 160, HitCount: 4,
			Participants: []replay.EngagementParticipant{
				{ClientNum: 1, Team: 1, DamageDealt: 100, DamageTaken: 60},
				{ClientNum: 3, Team: 2, DamageDealt: 60, DamageTaken: 100},
			},
			Kills: []replay.EngagementKill{{TimeMs: 8800, Killer: 1, Victim: 3, Mod: 17}},
		}},
	}
	got := buildFights(a)
	if len(got) != 1 {
		t.Fatalf("%+v", got)
	}
	f := got[0]
	if f.StartMs != 3000 || f.EndMs != 8000 || f.Winner != "axis" || len(f.Sides) != 2 {
		t.Fatalf("fight: %+v", f)
	}
	if f.Sides[0].Team != "axis" || f.Sides[0].Players[0].Kills != 1 || f.Sides[0].Players[0].Died {
		t.Errorf("axis side: %+v", f.Sides[0])
	}
	if p := f.Sides[1].Players[0]; p.Name != "Old" || !p.Died {
		t.Errorf("allies player (named Old when the fight began): %+v", p)
	}
	if len(f.Kills) != 1 || f.Kills[0].Weapon != "MP-40" || f.Kills[0].Killer != "Ann" || f.Kills[0].Victim != "Cy" || f.Kills[0].TimeMs != 7800 {
		t.Errorf("kills: %+v", f.Kills)
	}
}

func TestFightViewHandlerMisses(t *testing.T) {
	h := gameFightViewHandler(t.TempDir())
	get := func(match, n string) int {
		r := httptest.NewRequest("GET", "/x", nil)
		r.SetPathValue("match", match)
		r.SetPathValue("n", n)
		w := httptest.NewRecorder()
		h(w, r)
		return w.Code
	}
	if c := get("replays_1", "0"); c != http.StatusNotFound {
		t.Errorf("no analysis: %d", c)
	}
	if c := get("replays_1", "x"); c != http.StatusBadRequest {
		t.Errorf("bad fight number: %d", c)
	}
	if c := get("../etc", "0"); c != http.StatusBadRequest {
		t.Errorf("bad game name: %d", c)
	}
}
