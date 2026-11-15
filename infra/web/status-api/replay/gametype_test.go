package replay

import "testing"

// The numbers are the game's gametype_t (mod/src/game/bg_public.h).
func TestGametypeName(t *testing.T) {
	for n, want := range map[int]string{0: "Free for all", 3: "Team deathmatch", 4: "Capture the flag", 5: "Objective", 6: "Stopwatch", 7: "Checkpoint", 8: "Capture & Hold", 99: "99"} {
		if got := GametypeName(n); got != want {
			t.Errorf("GametypeName(%d) = %q, want %q", n, got, want)
		}
	}
	// a summary written with the old table is named from its number
	s := Summarize("x", &Analysis{Meta: AnalysisMeta{Gametype: 5, GametypeName: "Capture & Hold"}})
	if s.GametypeName != "Objective" {
		t.Errorf("summary says %q for gametype 5, want Objective", s.GametypeName)
	}
}
