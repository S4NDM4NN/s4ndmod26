package demo

import (
	"sort"
	"strconv"
	"strings"
)

// A spectator's demo can run for hours across several games, and so can a
// player's across a set.  Each game is better watched (and merged with the
// other players' demos of it) on its own, so a demo like that is cut at the
// games: Games says where, Clip makes the piece.

// gamePadMs is how much of the lead-up to a game and of the time after it a
// piece keeps, so that it still overlaps the demos of the same game that
// start a little later or end a little sooner.
const gamePadMs = 15000

// Game is one game's stretch of a demo, in the demo's own clock.
type Game struct {
	N             int // 1-based, in the order played
	FromMs, ToMs  int
	Rounds        int
	FirstR, LastR int // the first and last round's live play, unpadded
}

// Games lists the games a demo spans, or nil if it holds one game or none.
func Games(d *Demo) []Game {
	if len(d.Frames) == 0 {
		return nil
	}
	var out []Game
	for _, r := range RoundsOf(d) {
		g := max(r.Game, 1)
		if len(out) == 0 || out[len(out)-1].N != g {
			out = append(out, Game{N: g, FirstR: int(r.StartMs)})
		}
		cur := &out[len(out)-1]
		cur.LastR = int(r.EndMs)
		cur.Rounds++
	}
	if len(out) < 2 {
		return nil
	}
	first, last := d.Frames[0].ServerTime, d.Frames[len(d.Frames)-1].ServerTime
	for i := range out {
		g := &out[i]
		g.FromMs, g.ToMs = g.FirstR-gamePadMs, g.LastR+gamePadMs
		// neighbours do not overlap: the gap between two games is split between them
		if i > 0 {
			if mid := (out[i-1].LastR + g.FirstR) / 2; g.FromMs < mid {
				g.FromMs = mid
			}
		}
		if i+1 < len(out) {
			if mid := (g.LastR + out[i+1].FirstR) / 2; g.ToMs > mid {
				g.ToMs = mid
			}
		}
		g.FromMs, g.ToMs = max(g.FromMs, first), min(g.ToMs, last)
	}
	// number them in order, whatever the rounds called their games
	sort.SliceStable(out, func(i, j int) bool { return out[i].FromMs < out[j].FromMs })
	for i := range out {
		out[i].N = i + 1
	}
	return out
}

// Clip is the part of d between from and to (server time, both included).  It
// shares what it can with d, which can then be let go.  The configstrings as
// they stood at from are the piece's initial ones, so the game's phase and
// clock are right from its first frame.
func Clip(d *Demo, from, to int) *Demo {
	out := *d
	out.InitialConfigstrings = make(map[int]string, len(d.InitialConfigstrings))
	for k, v := range d.InitialConfigstrings {
		out.InitialConfigstrings[k] = v
	}
	out.Commands = nil
	for _, c := range d.Commands {
		switch {
		case c.ServerTime < from:
			f := strings.SplitN(c.Text, " ", 3)
			if len(f) == 3 && f[0] == csCommandWord {
				if n, err := strconv.Atoi(f[1]); err == nil {
					out.InitialConfigstrings[n] = strings.Trim(strings.TrimSpace(f[2]), "\"")
				}
			}
		case c.ServerTime <= to:
			out.Commands = append(out.Commands, c)
		}
	}
	out.Frames = nil
	for _, f := range d.Frames {
		if f.ServerTime >= from && f.ServerTime <= to {
			out.Frames = append(out.Frames, f)
		}
	}
	return &out
}
