package demo

import (
	"regexp"
	"sort"
	"strings"
)

// Objective events.  The game announces them with a "cp" (centre print)
// server command and a global sound, but neither names a player.  The text
// gives the kind (and often the team) and the sound's path gives the team
// ("sound/multiplayer/allies/a-objective_taken.wav").  The player is read
// from the objective flag in the entity state: the thief of the documents is
// whoever starts carrying it, and the one who transmits them whoever is
// carrying it then.  Who planted dynamite is not told: the dynamite entity
// cannot be told apart from the map's own stationary entities, and a wrong
// name is worse than none.

const (
	pwRedFlag, pwBlueFlag = 12, 13 // powerup_t PW_REDFLAG / PW_BLUEFLAG
	carryMask             = 1<<pwRedFlag | 1<<pwBlueFlag
	csSounds              = 320 // CS_SOUNDS: CS_MODELS 64 + MAX_MODELS 256

	cpSoundWindowMs = 300 // a sound belongs to a message this close in time
	cpDedupMs       = 1500
	stealBeforeMs   = 1500 // the carry flag shows up around the message,
	stealAfterMs    = 4000 // later if the carrier was out of view
	handoffMs       = 4000
)

// Objective kinds, as the event feed groups them (feed.js).
const (
	ObjPlant   = "plant"
	ObjSteal   = "steal"
	ObjCapture = "capture"
	ObjReturn  = "return"
	ObjDefuse  = "defuse"
)

// Objective is one announced objective event.
type Objective struct {
	Time  int32
	Kind  string
	Team  int32 // 1 axis, 2 allies, 0 unknown
	Text  string
	Actor int32 // client slot, -1 if it could not be told
}

// Carry is a stretch of one player carrying the objective (the documents,
// the gold).  EndMs 0 means they still had it when the recording ended.
type Carry struct {
	Slot, StartMs, EndMs int32
}

// cpMsg is a classified centre-print message.
type cpMsg struct {
	t    int32
	text string
	kind string
	team int32
}

type carryObs struct {
	t      int32
	client int32
	mask   uint32
}

var (
	reCPCommand = regexp.MustCompile(`(?s)^cp "(.*)"(?: \d+)?\s*$`)
	reTeamAxis  = regexp.MustCompile(`(?i)\baxis\b`)
	reTeamAllie = regexp.MustCompile(`(?i)\ballie[sd]?\b`)
	// one player's own progress messages are not events
	reCPSkip = regexp.MustCompile(`(?i)arming|defusing|not armed|armed with|is ready|ready!|revived|waiting|timelimit|round`)
	reObj    = regexp.MustCompile(`(?i)planted|stolen|transmitted|captur|reclaim|breach|destroy|destory|defused|escaped|returned|secured`)
)

// classifyCP turns the text of a "cp" command into an objective message.
func classifyCP(command string) (text, kind string, team int32, ok bool) {
	m := reCPCommand.FindStringSubmatch(strings.TrimRight(command, "\n"))
	if m == nil {
		return "", "", 0, false
	}
	text = strings.Join(strings.Fields(strings.ReplaceAll(CleanName(m[1]), "\n", " ")), " ")
	if text == "" || reCPSkip.MatchString(text) || !reObj.MatchString(text) {
		return "", "", 0, false
	}
	l := strings.ToLower(text)
	switch {
	case strings.Contains(l, "planted"):
		kind = ObjPlant
	case strings.Contains(l, "defused"):
		kind = ObjDefuse
	case strings.Contains(l, "stolen"):
		kind = ObjSteal
	case strings.Contains(l, "reclaim") || strings.Contains(l, "returned"):
		kind = ObjReturn
	default:
		kind = ObjCapture
	}
	// the team that acts is named first ("Allied team has destoryed the Axis
	// Submarine!")
	ax, al := reTeamAxis.FindStringIndex(text), reTeamAllie.FindStringIndex(text)
	switch {
	case ax != nil && (al == nil || ax[0] < al[0]):
		team = 1
	case al != nil:
		team = 2
	}
	return text, kind, team, true
}

// objectiveMessages reads a demo's objective messages, with the team of any
// sound event that goes with them.
func objectiveMessages(d *Demo) []cpMsg {
	var out []cpMsg
	lay := d.Layout
	it, ip := lay.EntityIdx("eType"), lay.EntityIdx("eventParm")
	for _, c := range d.Commands {
		if !strings.HasPrefix(c.Text, "cp ") || c.ServerTime < 0 {
			continue
		}
		text, kind, team, ok := classifyCP(c.Text)
		if !ok {
			continue
		}
		if team == 0 && it >= 0 && ip >= 0 {
			team = soundTeam(d, c.ServerTime, it, ip)
		}
		out = append(out, cpMsg{int32(c.ServerTime), text, kind, team})
	}
	return out
}

// soundTeam finds a team-specific sound event around t: the sound's name
// (from the sound configstrings) says allies or axis.
func soundTeam(d *Demo, t, it, ip int) int32 {
	i := sort.Search(len(d.Frames), func(i int) bool { return d.Frames[i].ServerTime >= t-cpSoundWindowMs })
	for ; i < len(d.Frames) && d.Frames[i].ServerTime <= t+cpSoundWindowMs; i++ {
		for _, e := range d.Frames[i].Entities {
			if e.Number < maxClients || int(e.F[it]) < etEvents {
				continue
			}
			name := d.Configstrings[csSounds+int(e.F[ip])]
			switch {
			case strings.Contains(name, "/allies/"):
				return 2
			case strings.Contains(name, "/axis/"):
				return 1
			}
		}
	}
	return 0
}

// unionCP joins several demos' messages: the same message seen by two demos
// is one event.
func unionCP(perPart [][]cpMsg) []cpMsg {
	var all []cpMsg
	for _, p := range perPart {
		all = append(all, p...)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].t < all[j].t })
	var out []cpMsg
	for _, m := range all {
		dup := false
		for k := len(out) - 1; k >= 0 && m.t-out[k].t <= cpDedupMs; k-- {
			if out[k].text == m.text {
				if out[k].team == 0 {
					out[k].team = m.team
				}
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, m)
		}
	}
	return out
}

// carries builds the carry stretches from observations of who held the flag
// bits when (from any demo).  A bit seen set starts a stretch, seen clear
// ends it.
func buildCarries(obs []carryObs) []Carry {
	sort.SliceStable(obs, func(i, j int) bool { return obs[i].t < obs[j].t })
	state := map[int32]uint32{}
	start := map[int32]int32{}
	var out []Carry
	for _, o := range obs {
		was, now := state[o.client] != 0, o.mask != 0
		state[o.client] = o.mask
		switch {
		case !was && now:
			start[o.client] = o.t
		case was && !now:
			out = append(out, Carry{o.client, start[o.client], o.t})
		}
	}
	for c, m := range state {
		if m != 0 {
			out = append(out, Carry{c, start[c], 0})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartMs < out[j].StartMs })
	return out
}

// resolveObjectives gives each message its actor where the carry flag says.
func resolveObjectives(msgs []cpMsg, carries []Carry) []Objective {
	abs := func(x int32) int32 {
		if x < 0 {
			return -x
		}
		return x
	}
	out := make([]Objective, 0, len(msgs))
	for _, m := range msgs {
		o := Objective{Time: m.t, Kind: m.kind, Team: m.team, Text: m.text, Actor: -1}
		switch {
		case m.kind == ObjSteal:
			best := int32(1 << 30)
			for _, c := range carries {
				if d := c.StartMs - m.t; d >= -stealBeforeMs && d <= stealAfterMs && abs(d) < best {
					best, o.Actor = abs(d), c.Slot
				}
			}
		case strings.Contains(strings.ToLower(m.text), "transmitted"):
			// the flag is only seen to clear later (at the round restart), so
			// the transmitter is whoever was carrying it then
			var latest int32 = -1 << 30
			for _, c := range carries {
				if c.StartMs <= m.t && (c.EndMs == 0 || c.EndMs >= m.t) && c.StartMs > latest {
					latest, o.Actor = c.StartMs, c.Slot
				}
			}
		}
		out = append(out, o)
	}
	return out
}
