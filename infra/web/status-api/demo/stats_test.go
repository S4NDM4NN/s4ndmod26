package demo

import (
	"testing"

	"s4ndmod26/status-api/replay"
)

func TestBuildStats(t *testing.T) {
	rep := &replay.Replay{}
	rep.Header.PlayerNames[0], rep.Header.PlayerNames[1], rep.Header.PlayerNames[2] = "^1Ann", "Bob", "^4Cy"
	smp := func(c, team, class int32) replay.Sample {
		return replay.Sample{ClientNum: c, EType: 1, Team: team, PlayerClass: class}
	}
	for _, ms := range []int32{1000, 2000, 3000, 6000, 7000} { // round 1: 1000-3000, round 2: 6000-7000
		rep.Frames = append(rep.Frames, replay.Frame{ServerTime: ms, Samples: []replay.Sample{smp(0, 1, 0), smp(1, 2, 1), smp(2, 2, 3)}})
	}
	ev := func(t replay.EventType, ms, actor, target int32) replay.Event {
		return replay.Event{ServerTime: ms, Type: t, ActorClientNum: actor, TargetClientNum: target}
	}
	rep.Events = []replay.Event{
		ev(replay.EventKill, 1500, 0, 1),
		ev(replay.EventKill, 1600, 0, 2),
		ev(replay.EventTeamkill, 1700, 1, 2),
		ev(replay.EventSuicide, 2500, 1, 1),
		ev(replay.EventRevive, 2600, 2, 1), // revives are not counted
		ev(replay.EventKill, 4000, 1, 0),   // between rounds: not play
		ev(replay.EventKill, 6500, 1, 0),
	}
	rounds := []Round{
		{N: 1, Of: 2, StartMs: 1000, EndMs: 3000, ClockStartMs: 900, LimitMs: 5000},
		{N: 2, Of: 2, StartMs: 6000, EndMs: 7000, ClockStartMs: 5900, LimitMs: 2100, BeatMs: 2100},
	}
	objs := []Objective{
		{Time: 2000, Kind: ObjSteal, Team: 2, Text: "Allies have stolen the War Documents!", Actor: 2},
		{Time: 2800, Kind: ObjCapture, Team: 2, Text: "Allies transmitted the documents!", Actor: 2},
		{Time: 4500, Kind: ObjReturn, Text: "between rounds"},
	}
	carries := []Carry{{Slot: 2, StartMs: 2000, EndMs: 2900}}

	st := BuildStats(rep, rounds, objs, carries)
	if len(st.Rounds) != 2 || st.Rounds[0].DurationMs != 2100 || st.Rounds[0].LeftMs != 2900 || st.Rounds[1].BeatMs != 2100 || st.Rounds[1].LeftMs != 1000 {
		t.Errorf("rounds: %+v", st.Rounds)
	}
	if len(st.Rounds[0].Objectives) != 2 || st.Rounds[0].Objectives[0].Actor != "Cy" || st.Rounds[0].Objectives[0].TMs != 1000 || len(st.Rounds[1].Objectives) != 0 {
		t.Errorf("objectives by round: %+v / %+v", st.Rounds[0].Objectives, st.Rounds[1].Objectives)
	}
	byName := map[string]PlayerStats{}
	for _, p := range st.Players {
		byName[p.Name] = p
	}
	ann, bob, cy := byName["Ann"], byName["Bob"], byName["Cy"]
	if ann.Total.Kills != 2 || ann.Total.Deaths != 1 { // killed twice in round 1; Bob killed her in round 2; the kill between rounds is ignored
		t.Errorf("Ann: %+v", ann.Total)
	}
	if bob.Total.Kills != 1 || bob.Total.Deaths != 2 || bob.Total.Suicides != 1 || bob.Total.Teamkills != 1 {
		t.Errorf("Bob: %+v", bob.Total)
	}
	if cy.Total.Deaths != 2 || cy.Total.Steals != 1 || cy.Total.Captures != 1 || cy.Total.CarryMs != 900 {
		t.Errorf("Cy: %+v", cy.Total)
	}
	if len(ann.Rounds) != 2 || ann.Rounds[0].Team != "axis" || ann.Rounds[0].Class != "soldier" || bob.Rounds[1].Class != "medic" || cy.Rounds[0].Class != "lieutenant" {
		t.Errorf("teams and classes: %+v %+v %+v", ann.Rounds, bob.Rounds, cy.Rounds)
	}
	if st.Players[0].Name != "Ann" { // most kills first
		t.Errorf("order: %v", st.Players[0].Name)
	}
	// the ticks add up to the totals, in time order
	sums := map[int32]Tally{}
	last := int32(-1)
	for _, k := range st.Ticks {
		if k.TMs < last {
			t.Errorf("ticks out of order at %d", k.TMs)
		}
		last = k.TMs
		tl := sums[k.Slot]
		switch k.Kind {
		case "kills":
			tl.Kills++
		case "deaths":
			tl.Deaths++
		case "suicides":
			tl.Suicides++
		case "teamkills":
			tl.Teamkills++
		case "steals":
			tl.Steals++
		case "captures":
			tl.Captures++
		default:
			t.Errorf("unknown tick %q", k.Kind)
		}
		sums[k.Slot] = tl
	}
	for slot, p := range []PlayerStats{ann, bob, cy} {
		got, want := sums[int32(slot)], p.Total
		want.CarryMs = 0 // carry time is not a tick
		if got != want {
			t.Errorf("ticks of %s add up to %+v, want %+v", p.Name, got, want)
		}
	}
	// a replay with no rounds is one round
	if one := BuildStats(rep, nil, nil, nil); len(one.Rounds) != 1 || one.Rounds[0].N != 1 {
		t.Errorf("no rounds: %+v", one.Rounds)
	}
}
