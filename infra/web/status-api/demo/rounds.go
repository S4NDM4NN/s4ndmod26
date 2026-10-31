package demo

import (
	"sort"
	"strconv"
	"strings"

	"s4ndmod26/status-api/replay"
)

// A match is played in rounds (a stopwatch match is two: one team defends and
// the other sets a time, then the sides swap and the first team tries to beat
// it), with warm-up, countdown and intermission between them.  Only the live
// part is worth watching, so the viewer is given just that: the rounds are
// found from the OSP phase timeline, and everything outside them is dropped
// and the later rounds slid back to follow the earlier ones.

// configAt is configstring n as it stood at server time t: the gamestate's
// value, changed by the "cs" commands up to t.
func configAt(d *Demo, n, t int) string {
	v := d.InitialConfigstrings[n]
	key := strconv.Itoa(n)
	for _, c := range d.Commands {
		if c.ServerTime > t {
			break
		}
		f := strings.SplitN(c.Text, " ", 3)
		if len(f) == 3 && f[0] == csCommandWord && f[1] == key {
			v = strings.Trim(strings.TrimSpace(f[2]), "\"")
		}
	}
	return v
}

const (
	csLevelStart      = 13 // CS_LEVEL_START_TIME: when the round's clock started
	gametypeStopwatch = 6  // g_gametype GT_WOLF_STOPWATCH
	roundGapMs        = 1500
)

// Round is one stretch of live play.
type Round struct {
	N, Of   int   // "round N of Of"
	StartMs int32 // live play, in the clock of the replay it was found in
	EndMs   int32
	BeatMs  int32 // the time to beat (second round of a stopwatch match); 0 if none
	// LimitMs is the round's time limit and ClockStartMs when its clock
	// started, so the game's clock reads LimitMs - (t - ClockStartMs).  The
	// limit is 0 if the demo does not say.
	LimitMs, ClockStartMs int32
}

// roundsOf lists the live stretches of a demo whose first and last frames are
// at first and last.  A demo from a server that publishes no phase has none
// (the whole of it is play).
func roundsOf(d *Demo, ph []phaseChange, first, last, gametype int) []Round {
	if len(ph) == 1 && ph[0].State == gsUnknown {
		return nil
	}
	of := 1
	if gametype == gametypeStopwatch {
		of = 2
	}
	var out []Round
	var cur *Round
	curRound := 0
	open := func(t int, p phaseChange) {
		cur = &Round{N: p.Round + 1, Of: of, StartMs: int32(t), BeatMs: int32(p.NextLimit*60000 + 0.5)}
		cur.ClockStartMs = int32(atoi(configAt(d, csLevelStart, t)))
		if cur.ClockStartMs == 0 {
			cur.ClockStartMs = cur.StartMs
		}
		lim, _ := strconv.ParseFloat(InfoValue(configAt(d, csServerInfo, t), "timelimit"), 64)
		cur.LimitMs = int32(lim*60000 + 0.5)
		curRound = p.Round
	}
	if ph[0].State == gsPlaying {
		open(first, ph[0])
	}
	for _, p := range ph[1:] {
		playing := p.State == gsPlaying
		switch {
		case cur == nil && playing:
			open(p.Time, p)
		// A round ends when play stops, or a moment before that: OSP bumps
		// g_currentRound while the phase still reads "playing", and clients are
		// already being moved to the intermission spot, so a frame after the bump
		// shows every player in one place.
		case cur != nil && (!playing || p.Round != curRound):
			cur.EndMs = int32(p.Time)
			out = append(out, *cur)
			cur = nil
		}
	}
	if cur != nil {
		cur.EndMs = int32(last)
		out = append(out, *cur)
	}
	return out
}

// Compressor takes the time between live stretches out of a replay.
type Compressor struct {
	rounds []Round
	shift  []int32 // taken off the times inside round k
}

// NewCompressor builds one from rounds in time order.  The stretches stay
// roundGapMs apart (or as far apart as they were, if closer), so a path does
// not run on from one round into the next.
func NewCompressor(rounds []Round) *Compressor {
	c := &Compressor{rounds: rounds, shift: make([]int32, len(rounds))}
	for k := 1; k < len(rounds); k++ {
		gap := rounds[k].StartMs - rounds[k-1].EndMs
		cut := gap - roundGapMs
		if cut < 0 {
			cut = 0
		}
		c.shift[k] = c.shift[k-1] + cut
	}
	return c
}

// Map puts a time on the compressed clock; ok is false for a time outside play.
func (c *Compressor) Map(t int32) (int32, bool) {
	i := sort.Search(len(c.rounds), func(i int) bool { return c.rounds[i].EndMs >= t })
	if i < len(c.rounds) && c.rounds[i].StartMs <= t {
		return t - c.shift[i], true
	}
	return 0, false
}

// atEnd reports whether t is the closing instant of the round it falls in.
func (c *Compressor) atEnd(t int32) bool {
	i := sort.Search(len(c.rounds), func(i int) bool { return c.rounds[i].EndMs >= t })
	return i < len(c.rounds) && c.rounds[i].EndMs == t && c.rounds[i].StartMs < t
}

// Rounds gives the stretches on the compressed clock.
func (c *Compressor) Rounds() []Round {
	out := make([]Round, len(c.rounds))
	for i, r := range c.rounds {
		r.StartMs -= c.shift[i]
		r.EndMs -= c.shift[i]
		r.ClockStartMs -= c.shift[i]
		out[i] = r
	}
	return out
}

// Replay returns the replay with only the frames and events of live play.
// A round's last instant is an event's (a kill on the final tick counts) but
// not a frame's: the frame stamped with the moment play stops is the first one
// after the server has moved every client to the intermission spot, and
// showing it makes the whole field fly to one place.
func (c *Compressor) Replay(rep *replay.Replay) *replay.Replay {
	out := &replay.Replay{Header: rep.Header}
	for _, f := range rep.Frames {
		if t, ok := c.Map(f.ServerTime); ok && !c.atEnd(f.ServerTime) {
			f.ServerTime = t
			out.Frames = append(out.Frames, f)
		}
	}
	for _, e := range rep.Events {
		if t, ok := c.Map(e.ServerTime); ok {
			e.ServerTime = t
			out.Events = append(out.Events, e)
		}
	}
	out.Header.FrameCount = int32(len(out.Frames))
	out.Header.EventCount = int32(len(out.Events))
	return out
}

// MapObjectives keeps the objective events that happened in play.
func (c *Compressor) MapObjectives(os []Objective) []Objective {
	var out []Objective
	for _, o := range os {
		if t, ok := c.Map(o.Time); ok {
			o.Time = t
			out = append(out, o)
		}
	}
	return out
}

// MapCarries cuts carry stretches to the live rounds (a carrier still holding
// the objective when a round ends holds it to the end of that round).
func (c *Compressor) MapCarries(cs []Carry) []Carry {
	var out []Carry
	for _, cr := range cs {
		for i, r := range c.rounds {
			end := cr.EndMs
			if end == 0 || end > r.EndMs {
				end = r.EndMs
			}
			start := cr.StartMs
			if start < r.StartMs {
				start = r.StartMs
			}
			if start < end {
				out = append(out, Carry{cr.Slot, start - c.shift[i], end - c.shift[i]})
			}
		}
	}
	return out
}

// Gametype is the demo's g_gametype (6 is a stopwatch match), 0 if it does not say.
func Gametype(d *Demo) int { return atoi(InfoValue(d.Configstrings[csServerInfo], "g_gametype")) }

// RoundsOf finds the live rounds of a single demo, on its own clock.
func RoundsOf(d *Demo) []Round {
	in := Describe(d)
	return roundsOf(d, phases(d), in.StartTime, in.EndTime, Gametype(d))
}
