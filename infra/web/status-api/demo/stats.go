package demo

import (
	"sort"

	"s4ndmod26/status-api/replay"
)

// Match statistics: only what the demos give reliably.  Kills, deaths,
// suicides and team kills come from the obituaries every client receives
// (checked against the OSP scoreboards inside the demos), objectives from the
// game's own centre-prints and the carry flag.  Deliberately not here: damage
// and accuracy (the demos only attribute part of them) and revives (they are
// inferred from a player getting up where they fell, and about one in five of
// those is not a revive).

// RoundInfo describes one round of a match.
type RoundInfo struct {
	Game       int         `json:"game"` // a match that holds several games has several rounds 1 and 2
	N          int         `json:"n"`
	Of         int         `json:"of"`
	DurationMs int32       `json:"duration_ms"`       // how long the round's clock ran
	LimitMs    int32       `json:"limit_ms"`          // its time limit, 0 if the demos do not say
	LeftMs     int32       `json:"left_ms"`           // what was left on the clock when play ended
	BeatMs     int32       `json:"beat_ms,omitempty"` // the time to beat (second round of a stopwatch match)
	Objectives []StatEvent `json:"objectives"`
}

// StatEvent is an announced objective event.
type StatEvent struct {
	TMs   int32  `json:"t_ms"` // from the start of the round
	Kind  string `json:"kind"`
	Team  string `json:"team,omitempty"`
	Text  string `json:"text"`
	Actor string `json:"actor,omitempty"` // who did it, where that could be told
}

// Tally is a set of counts.
type Tally struct {
	Kills     int   `json:"kills"`
	Deaths    int   `json:"deaths"`
	Suicides  int   `json:"suicides"`
	Teamkills int   `json:"teamkills"` // team mates this player killed
	Steals    int   `json:"steals"`    // objective picked up
	Captures  int   `json:"captures"`  // objective delivered
	CarryMs   int32 `json:"carry_ms"`  // time holding the objective
}

func (t *Tally) add(o Tally) {
	t.Kills += o.Kills
	t.Deaths += o.Deaths
	t.Suicides += o.Suicides
	t.Teamkills += o.Teamkills
	t.Steals += o.Steals
	t.Captures += o.Captures
	t.CarryMs += o.CarryMs
}

// PlayerRound is one player's round.
type PlayerRound struct {
	Game  int    `json:"game"`
	Round int    `json:"round"`
	Team  string `json:"team"`  // axis, allies, or "" if never seen on one
	Class string `json:"class"` // the class played for most of the round, "" if not told
	Tally
}

// PlayerStats is one player's match.
type PlayerStats struct {
	Slot   int           `json:"slot"`
	Name   string        `json:"name"`
	Rounds []PlayerRound `json:"rounds"`
	Total  Tally         `json:"total"`
}

// StatTick is one count going up at one moment, so that a viewer can show the
// figures as they stood at any time in the replay.  Kind is one of kills,
// deaths, suicides, teamkills, steals, captures.  TMs is on the replay's clock
// (see BuildStats); carry time is not here, it follows from the carries.
type StatTick struct {
	TMs  int32  `json:"t_ms"`
	Slot int32  `json:"slot"`
	Kind string `json:"kind"`
}

// MatchStats is what the stats page shows.
type MatchStats struct {
	Rounds  []RoundInfo   `json:"rounds"`
	Players []PlayerStats `json:"players"`
	Ticks   []StatTick    `json:"ticks"` // every count, as it happened, in time order
}

var classNames = map[int32]string{0: "soldier", 1: "medic", 2: "engineer", 3: "lieutenant"}

// BuildStats computes a match's statistics from its merged replay, cut to live
// play (see Compressor), with the rounds, objectives and carries on the same
// clock.  With no rounds the whole replay is one.
func BuildStats(rep *replay.Replay, rounds []Round, objectives []Objective, carries []Carry) *MatchStats {
	if len(rounds) == 0 && len(rep.Frames) > 0 {
		rounds = []Round{{N: 1, Of: 1, StartMs: rep.Frames[0].ServerTime, EndMs: rep.Frames[len(rep.Frames)-1].ServerTime}}
	}
	name := func(c int32) string {
		if c < 0 || int(c) >= len(rep.Header.PlayerNames) {
			return ""
		}
		return CleanName(rep.Header.PlayerNames[c])
	}
	teamName := map[int32]string{1: "axis", 2: "allies"}
	roundAt := func(t int32) int {
		for i, r := range rounds {
			if t >= r.StartMs && t <= r.EndMs {
				return i
			}
		}
		return -1
	}

	out := &MatchStats{Rounds: []RoundInfo{}, Players: []PlayerStats{}, Ticks: []StatTick{}}
	for _, r := range rounds {
		ri := RoundInfo{Game: max(r.Game, 1), N: r.N, Of: r.Of, DurationMs: r.EndMs - r.ClockStartMs, LimitMs: r.LimitMs, BeatMs: r.BeatMs, Objectives: []StatEvent{}}
		if r.LimitMs > 0 {
			ri.LeftMs = max(0, r.LimitMs-ri.DurationMs)
		} else {
			ri.DurationMs = r.EndMs - r.StartMs
		}
		out.Rounds = append(out.Rounds, ri)
	}

	// who was in each round, on which team, as which class
	type key struct{ slot, round int }
	type seen struct {
		team, class map[int32]int
	}
	present := map[key]*seen{}
	for _, f := range rep.Frames {
		ri := roundAt(f.ServerTime)
		if ri < 0 {
			continue
		}
		for _, s := range f.Samples {
			if s.ClientNum >= 64 || s.EType != 1 {
				continue
			}
			k := key{int(s.ClientNum), ri}
			p := present[k]
			if p == nil {
				p = &seen{map[int32]int{}, map[int32]int{}}
				present[k] = p
			}
			if s.Team == 1 || s.Team == 2 {
				p.team[s.Team]++
			}
			if s.PlayerClass >= 0 {
				p.class[s.PlayerClass]++
			}
		}
	}
	tally := map[key]*Tally{}
	at := func(slot int32, ri int) *Tally {
		if slot < 0 || ri < 0 {
			return nil
		}
		k := key{int(slot), ri}
		if present[k] == nil {
			present[k] = &seen{map[int32]int{}, map[int32]int{}} // in the round by what they did
		}
		if tally[k] == nil {
			tally[k] = &Tally{}
		}
		return tally[k]
	}
	// count adds one to a player's tally for the round and records when
	count := func(t int32, slot int32, ri int, kind string, add func(*Tally)) {
		if tl := at(slot, ri); tl != nil {
			add(tl)
			out.Ticks = append(out.Ticks, StatTick{t, slot, kind})
		}
	}
	for _, e := range rep.Events {
		ri := roundAt(e.ServerTime)
		switch e.Type {
		case replay.EventKill:
			count(e.ServerTime, e.ActorClientNum, ri, "kills", func(t *Tally) { t.Kills++ })
			count(e.ServerTime, e.TargetClientNum, ri, "deaths", func(t *Tally) { t.Deaths++ })
		case replay.EventTeamkill:
			count(e.ServerTime, e.ActorClientNum, ri, "teamkills", func(t *Tally) { t.Teamkills++ })
			count(e.ServerTime, e.TargetClientNum, ri, "deaths", func(t *Tally) { t.Deaths++ })
		case replay.EventSuicide:
			count(e.ServerTime, e.TargetClientNum, ri, "suicides", func(t *Tally) { t.Suicides++ })
			count(e.ServerTime, e.TargetClientNum, ri, "deaths", func(t *Tally) { t.Deaths++ })
		}
	}
	for _, o := range objectives {
		ri := roundAt(o.Time)
		if ri < 0 {
			continue
		}
		out.Rounds[ri].Objectives = append(out.Rounds[ri].Objectives, StatEvent{
			TMs: o.Time - rounds[ri].StartMs, Kind: o.Kind, Team: teamName[o.Team], Text: o.Text, Actor: name(o.Actor)})
		switch o.Kind {
		case ObjSteal:
			count(o.Time, o.Actor, ri, "steals", func(t *Tally) { t.Steals++ })
		case ObjCapture:
			count(o.Time, o.Actor, ri, "captures", func(t *Tally) { t.Captures++ })
		}
	}
	for _, c := range carries {
		for ri, r := range rounds {
			end := c.EndMs
			if end == 0 || end > r.EndMs {
				end = r.EndMs
			}
			start := max(c.StartMs, r.StartMs)
			if start < end {
				if t := at(c.Slot, ri); t != nil {
					t.CarryMs += end - start
				}
			}
		}
	}

	sort.SliceStable(out.Ticks, func(i, j int) bool { return out.Ticks[i].TMs < out.Ticks[j].TMs })

	top := func(m map[int32]int) int32 {
		best, bestN := int32(-1), 0
		for k, n := range m {
			if n > bestN || (n == bestN && k < best) {
				best, bestN = k, n
			}
		}
		return best
	}
	bySlot := map[int]*PlayerStats{}
	var keys []key
	for k := range present {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].slot != keys[j].slot {
			return keys[i].slot < keys[j].slot
		}
		return keys[i].round < keys[j].round
	})
	for _, k := range keys {
		p := bySlot[k.slot]
		if p == nil {
			p = &PlayerStats{Slot: k.slot, Name: name(int32(k.slot)), Rounds: []PlayerRound{}}
			if p.Name == "" {
				p.Name = "Player"
			}
			bySlot[k.slot] = p
		}
		pr := PlayerRound{Game: max(rounds[k.round].Game, 1), Round: rounds[k.round].N, Team: teamName[top(present[k].team)], Class: classNames[top(present[k].class)]}
		if t := tally[k]; t != nil {
			pr.Tally = *t
		}
		p.Rounds = append(p.Rounds, pr)
		p.Total.add(pr.Tally)
	}
	for _, k := range keys { // in slot order, each player once
		if p := bySlot[k.slot]; p != nil {
			out.Players = append(out.Players, *p)
			delete(bySlot, k.slot)
		}
	}
	sort.SliceStable(out.Players, func(i, j int) bool {
		a, b := out.Players[i].Total, out.Players[j].Total
		if a.Kills != b.Kills {
			return a.Kills > b.Kills
		}
		return a.Deaths < b.Deaths
	})
	return out
}
