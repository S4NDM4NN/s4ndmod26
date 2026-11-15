package demo

import (
	"sort"
	"strconv"
	"strings"
)

// Teams change during a demo: sides swap at the round restart, players join,
// switch and leave.  Describe's roster is the final state, which is wrong for
// every earlier moment, so the team of a player at a given time comes from
// the configstring history.
type teamChange struct {
	Time int
	Team int
}

// teamTimeline maps a client number to its team changes in time order.
type teamTimeline map[int][]teamChange

func teamOf(cs string) int {
	if cs == "" {
		return 0
	}
	n, _ := strconv.Atoi(InfoValue("\\"+strings.TrimPrefix(cs, "\\"), "t"))
	return n
}

func buildTeamTimeline(d *Demo) teamTimeline {
	tl := teamTimeline{}
	for n := 0; n < maxClients; n++ {
		if t := teamOf(d.InitialConfigstrings[csPlayers+n]); t != 0 {
			tl[n] = []teamChange{{-1, t}}
		}
	}
	for _, c := range d.Commands {
		f := strings.SplitN(c.Text, " ", 3)
		if len(f) != 3 || f[0] != csCommandWord {
			continue
		}
		cs, err := strconv.Atoi(f[1])
		if err != nil || cs < csPlayers || cs >= csPlayers+maxClients {
			continue
		}
		n := cs - csPlayers
		t := teamOf(strings.Trim(strings.TrimSpace(f[2]), "\""))
		if h := tl[n]; len(h) == 0 && t != 0 || len(h) > 0 && h[len(h)-1].Team != t {
			tl[n] = append(tl[n], teamChange{c.ServerTime, t})
		}
	}
	return tl
}

// at returns the client's team at server time t (0 if unknown or not on a
// team yet).
func (tl teamTimeline) at(client, t int) int {
	h := tl[client]
	i := sort.Search(len(h), func(i int) bool { return h[i].Time > t })
	if i == 0 {
		return 0
	}
	return h[i-1].Team
}
