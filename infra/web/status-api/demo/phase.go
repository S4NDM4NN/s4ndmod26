package demo

import (
	"strconv"
	"strings"
)

// OSP publishes the match phase as a "gamestate" key in configstring 36:
// 0 playing, 1 warm-up countdown, 2 warm-up, 3 intermission.  Deaths during
// warm-up (teammates shooting each other while waiting for ready) do not
// count towards the match, so event reconstruction must know the phase.
const (
	csOSPState    = 36
	gsPlaying     = 0
	gsUnknown     = -1
	gamestateKey  = "gamestate"
	csCommandWord = "cs"
)

type phaseChange struct {
	Time  int
	State int
	Round int // g_currentRound: 0 first round of a map, 1 second
	// NextLimit is g_nextTimeLimit in minutes: in the second round of a
	// stopwatch match, the time the first round's attackers set (the "clock
	// set to" time to beat); 0 otherwise.
	NextLimit float64
}

func nextLimitOf(cs string) float64 {
	f, _ := strconv.ParseFloat(InfoValue(cs, "g_nextTimeLimit"), 64)
	return f
}

func roundOf(cs string) int {
	n, _ := strconv.Atoi(InfoValue(cs, "g_currentRound"))
	return n
}

func gamestateOf(cs string) int {
	v := InfoValue(cs, gamestateKey)
	if v == "" {
		return gsUnknown
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return gsUnknown
	}
	return n
}

// phases lists the match-phase changes of a demo in time order.  The first
// entry is the state in the initial gamestate (Time -1).  A mod that does not
// publish a phase gives a single gsUnknown entry.
func phases(d *Demo) []phaseChange {
	init := d.InitialConfigstrings[csOSPState]
	out := []phaseChange{{-1, gamestateOf(init), roundOf(init), nextLimitOf(init)}}
	for _, c := range d.Commands {
		f := strings.SplitN(c.Text, " ", 3)
		if len(f) != 3 || f[0] != csCommandWord || f[1] != strconv.Itoa(csOSPState) {
			continue
		}
		cs := strings.Trim(strings.TrimSpace(f[2]), "\"")
		st, rd := gamestateOf(cs), roundOf(cs)
		if last := out[len(out)-1]; st != last.State || rd != last.Round {
			out = append(out, phaseChange{c.ServerTime, st, rd, nextLimitOf(cs)})
		}
	}
	return out
}

// live reports whether the match is being played at server time t.  Demos
// from servers that publish no phase are treated as always live.
func live(ph []phaseChange, t int) bool {
	st := ph[0].State
	for _, p := range ph[1:] {
		if p.Time > t {
			break
		}
		st = p.State
	}
	return st == gsPlaying || st == gsUnknown
}
