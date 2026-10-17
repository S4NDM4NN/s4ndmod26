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
