package replay

import "testing"

func frame(t int32, samples ...Sample) Frame { return Frame{ServerTime: t, Samples: samples} }

func TestBuildPaths(t *testing.T) {
	at := func(slot, hp, team int32, x float32) Sample {
		return Sample{ClientNum: slot, Health: hp, Team: team, Origin: [3]float32{x, 2, 3}}
	}
	rep := &Replay{Header: ArchiveHeader{MapName: "mp_test"}}
	// slot 1 walks for 1 s (one sample per 50 ms frame), dies, then respawns; slot 2 is dead throughout; slot 3 is a spectator (team 3)
	for ms := int32(0); ms <= 2000; ms += 50 {
		s1 := at(1, 100, 1, float32(ms))
		if ms >= 1000 && ms < 1500 {
			s1.Health = 0
		}
		rep.Frames = append(rep.Frames, frame(1000+ms, s1, at(2, 0, 2, 5), at(3, 100, 3, 7)))
	}
	rep.Events = []Event{{Type: EventKill, ServerTime: 1900, ActorClientNum: 1, TargetClientNum: 2, Origin: [3]float32{10, 20, 30}}, {Type: EventSuicide}}

	p := BuildPaths(rep, 250)
	if p.Map != "mp_test" || p.StartMs != 1000 || p.EndMs != 3000 {
		t.Fatalf("header: %+v", p)
	}
	if len(p.Players) != 1 || p.Players[0].Slot != 1 {
		t.Fatalf("only slot 1 was alive on a team: %+v", p.Players)
	}
	segs := p.Players[0].Segments
	if len(segs) != 2 {
		t.Fatalf("a death splits the path into two segments, got %d", len(segs))
	}
	if segs[0].StartMs != 1000 || len(segs[0].Pts)/3 != 4 || segs[0].Pts[3] != 250 {
		t.Errorf("first segment: %+v", segs[0])
	}
	if segs[1].StartMs != 2500 {
		t.Errorf("second segment starts at the respawn: %+v", segs[1])
	}
	if p.Min[0] != 0 || p.Max[0] != 2000 {
		t.Errorf("bounds: %v %v", p.Min, p.Max)
	}
	if len(p.Kills) != 1 || p.Kills[0].Pos != [3]int32{10, 20, 30} || p.Kills[0].Killer != 1 {
		t.Errorf("kills: %+v", p.Kills)
	}
}

func TestBuildPathsEmpty(t *testing.T) {
	p := BuildPaths(&Replay{}, 250)
	if p.Players == nil || p.Kills == nil || len(p.Players) != 0 {
		t.Errorf("empty replay: %+v", p)
	}
}

func TestBuildPathsWithWindowAndSlots(t *testing.T) {
	rep := &Replay{}
	for ms := int32(0); ms <= 4000; ms += 50 {
		rep.Frames = append(rep.Frames, frame(ms,
			Sample{ClientNum: 1, Health: 90, Team: 1, Origin: [3]float32{float32(ms), 0, 0}},
			Sample{ClientNum: 2, Health: 50, Team: 2, Origin: [3]float32{0, float32(ms), 0}}))
	}
	p := BuildPathsWith(rep, PathOptions{StepMs: 250, FromMs: 1000, ToMs: 2000, Slots: map[int32]bool{2: true}})
	if len(p.Players) != 1 || p.Players[0].Slot != 2 {
		t.Fatalf("only slot 2: %+v", p.Players)
	}
	seg := p.Players[0].Segments[0]
	if seg.StartMs != 1000 || len(seg.Pts) != 15 || len(seg.Health) != 5 || seg.Health[0] != 50 || seg.Pts[1] != 1000 {
		t.Errorf("window 1000-2000 at 250 ms: %+v", seg)
	}
}

func TestBuildShots(t *testing.T) {
	// slot 1 (Axis, MP-40) starts firing at 1000 and holds it for 250 ms while aiming along +x, level; slot 2 is firing a panzer
	rep := &Replay{}
	for ms := int32(0); ms <= 2000; ms += 50 {
		a := Sample{ClientNum: 1, Health: 100, Team: 1, Weapon: 3, Origin: [3]float32{10, 20, 30}, ViewAngles: [3]float32{0, 0, 0}}
		b := Sample{ClientNum: 2, Health: 100, Team: 2, Weapon: 7, Weaponstate: 6}
		if ms >= 1000 && ms < 1250 {
			a.Weaponstate = 6
		}
		rep.Frames = append(rep.Frames, frame(ms, a, b))
	}
	// a hit of slot 1's lands at 1060
	rep.Events = []Event{{Type: EventDamage, ServerTime: 1060, ActorClientNum: 1, TargetClientNum: 2, Origin: [3]float32{500, 20, 30}}}
	shots := BuildShots(rep, PathOptions{StepMs: 100})
	if len(shots) != 3 {
		t.Fatalf("one at 1000, then every 100 ms while held (1100, 1200): got %+v", shots)
	}
	if !shots[0].Hit || shots[0].To != [3]int32{500, 20, 30} || shots[0].From != [3]int32{10, 20, 70} {
		t.Errorf("first shot hit the victim: %+v", shots[0])
	}
	if shots[1].Hit || shots[1].To[0] != 410 || shots[1].To[2] != 70 {
		t.Errorf("a miss with nobody in the line of fire is a short tracer: %+v", shots[1])
	}
	for _, s := range shots {
		if s.Slot != 1 || s.Team != 1 {
			t.Errorf("the panzer is not drawn: %+v", s)
		}
	}
}

func TestShotMissStopsAtTheEnemyItWasAimedAt(t *testing.T) {
	// slot 1 fires along +x; an enemy stands 150 units ahead (inside the aim cone), another 600 ahead, a third behind the shooter
	rep := &Replay{}
	rep.Frames = []Frame{frame(1000,
		Sample{ClientNum: 1, Health: 100, Team: 1, Weapon: 3, Weaponstate: 6, Origin: [3]float32{0, 0, 0}},
		Sample{ClientNum: 2, Health: 100, Team: 2, Origin: [3]float32{150, 10, 40}},
		Sample{ClientNum: 3, Health: 100, Team: 2, Origin: [3]float32{600, 0, 40}},
		Sample{ClientNum: 4, Health: 100, Team: 2, Origin: [3]float32{-100, 0, 40}},
		Sample{ClientNum: 5, Health: 0, Team: 2, Origin: [3]float32{100, 0, 40}})}
	shots := BuildShots(rep, PathOptions{StepMs: 100})
	if len(shots) != 1 || shots[0].Hit {
		t.Fatalf("%+v", shots)
	}
	// the tracer ends at the nearest living enemy in front, 150 units out (a corpse and the one behind do not count)
	if d := shots[0].To[0] - shots[0].From[0]; d < 140 || d > 155 {
		t.Errorf("tracer length along x: %d (%+v)", d, shots[0])
	}
}
