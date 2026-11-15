package demo

import (
	"strconv"
	"strings"
)

// Reinforcement waves.  Dead players in limbo come back together, every limbo
// time (g_redlimbotime for the Axis, g_bluelimbotime for the Allies).  Newer
// servers add a random offset per team, which they send to the clients in a
// config string of "seeds" (see reinfOffsets), and count from the level start;
// the older ones have none, and a wave is whenever the server's time is a
// multiple of the limbo time.  Either way the schedule is known outright, as
// the client's own timer knows it.

// Wave is one team's reinforcement schedule in a round.  Waves arrive at
// ClockStartMs + PhaseMs + k*LimboMs.  LimboMs is 0 if it could not be told.
type Wave struct {
	LimboMs, PhaseMs int32
}

// Next is the time left until the wave after t (0 if there is no schedule).
func (w Wave) Next(clockStart, t int32) int32 {
	if w.LimboMs <= 0 {
		return 0
	}
	d := (t - clockStart - w.PhaseMs) % w.LimboMs
	if d < 0 {
		d += w.LimboMs
	}
	return w.LimboMs - d
}

// reinfSeeds are the divisors the game picks its offsets with (aReinfSeeds in
// bg_misc.c).
var reinfSeeds = [8]int64{11, 3, 13, 7, 2, 5, 1, 17}

// reinfOffsets reads how long after the level start each team's waves are
// offset, from the seeds string the way cgame does (CG_ParseReinforcementTimes):
// two numbers that pick which of the following values each team uses (Allies:
// the first >> 3, Axis: the second / 4), each value divided by its seed and
// made milliseconds.  ok is false if s is not a seeds string.
func reinfOffsets(s string) (axis, allies int32, ok bool) {
	f := strings.Fields(strings.Trim(strings.TrimSpace(s), `"`))
	if len(f) != 2+len(reinfSeeds) {
		return 0, 0, false
	}
	n := make([]int64, len(f))
	for i, x := range f {
		v, err := strconv.ParseInt(x, 10, 64)
		if err != nil || v < 0 {
			return 0, 0, false
		}
		n[i] = v
	}
	for j, seed := range reinfSeeds { // the game makes every value a multiple of its seed
		if n[2+j]%seed != 0 {
			return 0, 0, false
		}
	}
	pick := func(j int64) (int32, bool) {
		if j >= int64(len(reinfSeeds)) {
			return 0, false
		}
		return int32(n[2+j] / reinfSeeds[j] * 1000), true
	}
	bl, ok1 := pick(n[0] >> 3)
	rd, ok2 := pick(n[1] / 4)
	if !ok1 || !ok2 {
		return 0, 0, false
	}
	return rd, bl, true
}

// seedsAt finds the seeds string in force at t.  Its config string number
// differs between the game versions (37 in this mod, 41 in the RtCW 1.4 ones
// the OSP demos come from), so the low strings are asked which one looks like
// it: ten numbers, every value a multiple of its seed.
func seedsAt(d *Demo, t int) (axis, allies int32, ok bool) {
	for n := 36; n <= 45; n++ {
		if a, b, ok := reinfOffsets(configAt(d, n, t)); ok {
			return a, b, true
		}
	}
	return 0, 0, false
}

// fillWaves gives each round its teams' wave schedules, from the limbo times
// the server announced and its seeds, if it sent any.
func fillWaves(d *Demo, rounds []Round) {
	for i := range rounds {
		r := &rounds[i]
		info := configAt(d, csServerInfo, int(r.StartMs))
		limbo := func(key string) int32 { n, _ := strconv.Atoi(InfoValue(info, key)); return int32(n) }
		ax, al := limbo("g_redlimbotime"), limbo("g_bluelimbotime")
		if ax <= 0 || al <= 0 {
			continue
		}
		// a wave is at (offset + t - levelStart) mod limbo == 0 with seeds; without them
		// the offset is 0 and the count starts from server time 0
		oa, ol, base := int32(0), int32(0), int32(0)
		if a, b, ok := seedsAt(d, int(r.StartMs)); ok {
			oa, ol, base = a, b, r.ClockStartMs
		}
		r.Axis = Wave{LimboMs: ax, PhaseMs: wavePhase(oa, base, r.ClockStartMs, ax)}
		r.Allies = Wave{LimboMs: al, PhaseMs: wavePhase(ol, base, r.ClockStartMs, al)}
	}
}

// wavePhase is where a team's waves fall relative to the round's clock start.
// They are at the t with (offset + t - base) mod limbo == 0, that is at
// base - offset + k*limbo.
func wavePhase(offset, base, clockStart, limbo int32) int32 {
	p := (base - offset - clockStart) % limbo
	if p < 0 {
		p += limbo
	}
	return p
}
