package demo

import "testing"

func TestRoundsOf(t *testing.T) {
	d := &Demo{InitialConfigstrings: map[int]string{}}
	ph := []phaseChange{
		{-1, 1, 0, 0},              // warm-up countdown at the start of the demo
		{1000, gsPlaying, 0, 0},    // round 1 goes live
		{9000, gsPlaying, 1, 0},    // the round counter bumps a moment before ...
		{9100, 3, 1, 0},            // ... intermission
		{12000, 2, 1, 3.0},         // warm-up
		{15000, gsPlaying, 1, 3.0}, // round 2 goes live; 3 minutes to beat
		{40000, gsPlaying, 0, 0},   // the counter resets when it ends ...
		{40100, 3, 0, 0},           // ... and intermission
	}
	got := roundsOf(d, ph, 0, 41000, gametypeStopwatch)
	want := []Round{
		{N: 1, Of: 2, StartMs: 1000, EndMs: 9000, ClockStartMs: 1000},
		{N: 2, Of: 2, StartMs: 15000, EndMs: 40000, BeatMs: 180000, ClockStartMs: 15000},
	}
	if len(got) != len(want) {
		t.Fatalf("rounds = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("round %d = %+v, want %+v", i+1, got[i], want[i])
		}
	}
	// a demo that does not say where play starts has no rounds
	if r := roundsOf(d, []phaseChange{{-1, gsUnknown, 0, 0}}, 0, 100, 0); r != nil {
		t.Errorf("a demo without phases has rounds %+v", r)
	}
	// a demo that starts in the middle of a round, and is not a stopwatch match
	r := roundsOf(d, []phaseChange{{-1, gsPlaying, 0, 0}}, 500, 9000, 5)
	if len(r) != 1 || r[0].StartMs != 500 || r[0].EndMs != 9000 || r[0].Of != 1 {
		t.Errorf("a round in progress at the start: %+v", r)
	}
}

func TestCompressor(t *testing.T) {
	rounds := []Round{{N: 1, Of: 2, StartMs: 1000, EndMs: 9000, ClockStartMs: 900}, {N: 2, Of: 2, StartMs: 15000, EndMs: 40000, ClockStartMs: 14900}}
	c := NewCompressor(rounds)
	for _, tc := range []struct {
		in, out int32
		ok      bool
	}{
		{999, 0, false}, {1000, 1000, true}, {9000, 9000, true}, // round 1 keeps its clock
		{9001, 0, false}, {12000, 0, false}, {14999, 0, false}, // intermission and warm-up are gone
		{15000, 10500, true}, {40000, 35500, true}, // round 2 follows 1.5 s after round 1
		{40001, 0, false},
	} {
		got, ok := c.Map(tc.in)
		if ok != tc.ok || (ok && got != tc.out) {
			t.Errorf("Map(%d) = %d, %v; want %d, %v", tc.in, got, ok, tc.out, tc.ok)
		}
	}
	rs := c.Rounds()
	if rs[1].StartMs != 10500 || rs[1].EndMs != 35500 || rs[1].ClockStartMs != 10400 || rs[0].StartMs != 1000 {
		t.Errorf("rounds on the compressed clock: %+v", rs)
	}
	// a carry that spans the intermission is cut to the rounds; one still held at the end runs to the end of its round
	cs := c.MapCarries([]Carry{{7, 8000, 16000}, {8, 38000, 0}})
	want := []Carry{{7, 8000, 9000}, {7, 10500, 11500}, {8, 33500, 35500}}
	if len(cs) != len(want) {
		t.Fatalf("carries = %+v, want %+v", cs, want)
	}
	for i := range want {
		if cs[i] != want[i] {
			t.Errorf("carry %d = %+v, want %+v", i, cs[i], want[i])
		}
	}
	// the round clock stays right across the shift: 3 minutes at the start of round 2, whichever clock is used
	if got := rs[1].StartMs - rs[1].ClockStartMs; got != rounds[1].StartMs-rounds[1].ClockStartMs {
		t.Errorf("the clock start moved relative to the round start: %d", got)
	}
}

// QCon03 round 1 on dm_60: a stopwatch match, both rounds in one demo.
func TestRoundsFromDemos(t *testing.T) {
	root := sampleRoot(t)
	ds := loadGroup(t, root, []string{"2003-08-15_145952", "2003-08-15_145959", "2003-08-15_150025",
		"2003-08-15_150048", "2003-08-15_150102", "2003-08-15_150139"})
	rep, mr, err := MergeDemos(ds)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("rounds: %+v", mr.Rounds)
	if len(mr.Rounds) != 2 || mr.Rounds[0].N != 1 || mr.Rounds[1].N != 2 || mr.Rounds[0].Of != 2 {
		t.Fatalf("want round 1 and 2 of 2, got %+v", mr.Rounds)
	}
	r1, r2 := mr.Rounds[0], mr.Rounds[1]
	if r1.BeatMs != 0 || r2.BeatMs < 188000 || r2.BeatMs > 189000 { // "Clock set to: 3:08"
		t.Errorf("time to beat: round 1 %d, round 2 %d (want about 188650)", r1.BeatMs, r2.BeatMs)
	}
	if r1.LimitMs <= r1.EndMs-r1.ClockStartMs {
		t.Errorf("round 1 limit %d ms is shorter than the round (%d ms)", r1.LimitMs, r1.EndMs-r1.ClockStartMs)
	}
	if d := r2.LimitMs - r2.BeatMs; d < -50 || d > 50 {
		t.Errorf("round 2 is played against the time to beat: limit %d, beat %d", r2.LimitMs, r2.BeatMs)
	}
	// the first round's attackers finished in 3:08, so the clock that ran in the game ran that long
	if played := r1.EndMs - r1.ClockStartMs; played < r2.BeatMs-1500 || played > r2.BeatMs+1500 {
		t.Errorf("round 1 clock ran %d ms, the time to beat is %d ms", played, r2.BeatMs)
	}

	c := NewCompressor(mr.Rounds)
	live := c.Replay(rep)
	rs := c.Rounds()
	var first, last int32 = 1 << 30, 0
	for _, f := range live.Frames {
		if f.ServerTime < first {
			first = f.ServerTime
		}
		if f.ServerTime > last {
			last = f.ServerTime
		}
		inside := false
		for _, r := range rs {
			if f.ServerTime >= r.StartMs && f.ServerTime <= r.EndMs {
				inside = true
			}
		}
		if !inside {
			t.Fatalf("a frame at %d is outside the rounds %+v", f.ServerTime, rs)
		}
	}
	t.Logf("%d of %d frames kept; compressed clock %d..%d, rounds %+v", len(live.Frames), len(rep.Frames), first, last, rs)
	if gap := rs[1].StartMs - rs[0].EndMs; gap != roundGapMs {
		t.Errorf("the rounds are %d ms apart on the compressed clock, want %d", gap, roundGapMs)
	}
}
