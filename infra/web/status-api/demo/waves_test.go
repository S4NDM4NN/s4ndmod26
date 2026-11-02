package demo

import "testing"

func TestWaveNext(t *testing.T) {
	w := Wave{LimboMs: 20000, PhaseMs: 7000}
	for _, c := range []struct{ t, want int32 }{{1000 + 100000, 6000}, {107000, 20000}, {107001, 19999}, {126999, 1}} {
		if got := w.Next(100000, c.t); got != c.want {
			t.Errorf("t=%d: got %d, want %d", c.t, got, c.want)
		}
	}
	if (Wave{}).Next(0, 5) != 0 {
		t.Error("no schedule means 0")
	}
}

// The strings are the ones the sample demos carry (OSP 0.3 and 0.83).
func TestReinfOffsets(t *testing.T) {
	for _, c := range []struct {
		s            string
		axis, allies int32
		ok           bool
	}{
		{`35 2 77 36 26 7 16 15 14 119`, 7000, 8000, true}, // Allies pick 35>>3=4 (16/2), Axis 2/4=0 (77/11)
		{`"10 3 22 15 195 35 24 10 0 68"`, 2000, 5000, true},
		{"21 21 33 6 91 7 18 20 5 0", 4000, 7000, true},
		{"", 0, 0, false},
		{"0", 0, 0, false},
		{"6", 0, 0, false},
		{"35 2 77 36 26 7 16 15 14 118", 0, 0, false},                    // 118 is no multiple of 17
		{"99 21 33 6 91 7 18 20 5 0", 0, 0, false},                       // a pick outside the table
		{`\g_nextTimeLimit\0\g_currentRound\0\gamestate\3`, 0, 0, false}, // another config string
		{"1 2 3 4 5 6 7 8 9 10 11", 0, 0, false},                         // wrong length
	} {
		ax, al, ok := reinfOffsets(c.s)
		if ok != c.ok || ax != c.axis || al != c.allies {
			t.Errorf("%q: got axis %d allies %d ok=%v, want %d %d %v", c.s, ax, al, ok, c.axis, c.allies, c.ok)
		}
	}
}

// Worked from the sample demos: a round of the OSP 0.3 one starts its clock at
// 18346400 and the Axis wave (35s, offset 7s) was seen 28s in; an old dm_57 one
// starts at 10457700 with no seeds and 40s waves that fall on multiples of 40000.
func TestWavePhase(t *testing.T) {
	if got := wavePhase(7000, 18346400, 18346400, 35000); got != 28000 {
		t.Errorf("seeded: got %d, want 28000", got)
	}
	if got := wavePhase(0, 0, 10457700, 40000); got != 22300 {
		t.Errorf("unseeded: got %d, want 22300", got)
	}
	w := Wave{LimboMs: 40000, PhaseMs: wavePhase(0, 0, 10457700, 40000)}
	if got := w.Next(10457700, 10480000-1000); got != 1000 {
		t.Errorf("a second before the wave at 10480000: %d left", got)
	}
}
