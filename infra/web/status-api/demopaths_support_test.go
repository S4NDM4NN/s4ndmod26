package main

import (
	"testing"

	"s4ndmod26/status-api/replay"
)

// A client demo has no medpack, ammo or strike events; the feed reads them off the entities.
func TestBuildFeedSupportFromEntities(t *testing.T) {
	rep := &replay.Replay{}
	rep.Header.PlayerNames[2] = "^1Medic"
	ents := &replay.Entities{
		Drops: []replay.Drop{
			{Kind: "medpack", Slot: 2, T0: 1000, T1: 4000, Fate: "taken"},
			{Kind: "ammo", Slot: 2, T0: 1000, T1: 4000, Fate: "taken", Death: true}, // dropped on death: not support
			{Kind: "ammo", Slot: 2, T0: 1000, T1: 9000, Fate: "expired"},
		},
		Projectiles: []replay.Projectile{
			{Kind: "marker", Weapon: 45, Slot: 2, T0: 2000, Pts: []int32{2000, 1, 2, 3}},
			{Kind: "marker", Weapon: 45, Slot: -1, T0: 2500, Pts: []int32{2500, 1, 2, 3}}, // nobody to name
			{Kind: "grenade", Slot: 2, T0: 2600, Pts: []int32{2600, 1, 2, 3}},
		},
	}
	got := map[string]userFeedEvent{}
	n := 0
	for _, e := range buildFeed(rep, 500, nil, nil, ents) {
		got[e.Type] = e
		n++
	}
	if n != 2 {
		t.Fatalf("want a medpack and a strike only, got %d events: %+v", n, got)
	}
	if m := got["medpack"]; m.TMs != 3500 || m.Actor != "Medic" || m.Text != "Medic pack taken" {
		t.Errorf("medpack: %+v", m)
	}
	if s := got["strike"]; s.TMs != 1500 || s.Weapon != "Airstrike" || s.Actor != "Medic" || *s.Pos != [3]int32{1, 2, 3} {
		t.Errorf("strike: %+v", s)
	}
}

// Kills say what they were made with, in the game stats' own words.
func TestBuildFeedKillWeapons(t *testing.T) {
	rep := &replay.Replay{}
	rep.Header.PlayerNames[1], rep.Header.PlayerNames[2] = "A", "B"
	rep.Events = []replay.Event{
		{Type: replay.EventKill, ActorClientNum: 1, TargetClientNum: 2, MeansOfDeath: 17, ServerTime: 1000},
		{Type: replay.EventTeamkill, ActorClientNum: 1, TargetClientNum: 2, MeansOfDeath: 18, ServerTime: 2000},
		{Type: replay.EventSuicide, ActorClientNum: 2, TargetClientNum: 2, MeansOfDeath: 55, ServerTime: 3000},
		{Type: replay.EventRevive, ActorClientNum: 1, TargetClientNum: 2, ServerTime: 4000},
	}
	got := buildFeed(rep, 0, nil, nil, &replay.Entities{})
	want := []string{"MP-40", "Thompson", "Falling", ""}
	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Weapon != w {
			t.Errorf("event %d (%s): weapon %q, want %q", i, got[i].Type, got[i].Weapon, w)
		}
	}
}
