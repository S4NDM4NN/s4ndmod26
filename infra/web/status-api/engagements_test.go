package main

import (
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

func TestEffectiveFight(t *testing.T) {
	players := func(n int) []fightPlayer { return make([]fightPlayer, n) }
	// four players with 300 HP between them against two with 120: a 3v1 in effect, and the four have the edge
	size, adv := effectiveFight(fightSide{Team: "axis", HP: 300, Players: players(4)}, fightSide{Team: "allies", HP: 120, Players: players(2)})
	if size != "3v1" || adv != "axis" {
		t.Errorf("%s %q", size, adv)
	}
	if size, adv = effectiveFight(fightSide{Team: "axis", HP: 200, Players: players(2)}, fightSide{Team: "allies", HP: 190, Players: players(2)}); size != "2v2" || adv != "" {
		t.Errorf("close fight: %s %q", size, adv)
	}
	if size, adv = effectiveFight(fightSide{Team: "axis", HP: 40, Players: players(1)}, fightSide{Team: "allies", HP: 100, Players: players(1)}); size != "1v1" || adv != "allies" {
		t.Errorf("a nearly dead lone player still counts as one: %s %q", size, adv)
	}
	// extra health on a few players never makes the side bigger than it was: two over-healed players are still two
	if size, _ = effectiveFight(fightSide{Team: "axis", HP: 400, Players: players(2)}, fightSide{Team: "allies", HP: 240, Players: players(2)}); size != "2v2" {
		t.Errorf("a side cannot grow: %s", size)
	}
}
