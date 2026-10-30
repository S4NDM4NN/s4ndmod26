package demo

import (
	"sort"
	"strconv"
	"strings"

	"s4ndmod26/status-api/replay"
)

// A client demo has two sources for other players' health:
//   - the recorder's playerState, every frame and exact (including the
//     negative values of an overkill), for whoever ps.clientNum names: the
//     recorder, or the player they follow after dying;
//   - "tinfo" server commands, about once a second, naming the health and
//     class of every player on the recorder's team.
//
// Merged across demos these cover everybody.
type hpPoint struct {
	t     int32
	hp    int32
	exact bool
}

type classPoint struct {
	t     int32
	class int32
}

type healthData struct {
	hp  map[int32][]hpPoint
	cls map[int32][]classPoint
}

func newHealthData() *healthData {
	return &healthData{hp: map[int32][]hpPoint{}, cls: map[int32][]classPoint{}}
}

func (h *healthData) addTinfo(t int, text string) {
	f := strings.Fields(text)
	if len(f) < 4 || f[0] != "tinfo" {
		return
	}
	cnt, err := strconv.Atoi(f[3])
	if err != nil {
		return
	}
	// tinfo <ident> <identHealth> <count> then 5 fields per teammate:
	// client, location, health, powerups, class
	for i := 0; i < cnt && 4+5*i+4 < len(f); i++ {
		e := f[4+5*i:]
		cl, _ := strconv.Atoi(e[0])
		hp, _ := strconv.Atoi(e[2])
		class, _ := strconv.Atoi(e[4])
		if cl < 0 || cl >= maxClients {
			continue
		}
		h.hp[int32(cl)] = append(h.hp[int32(cl)], hpPoint{int32(t), int32(hp), false})
		h.cls[int32(cl)] = append(h.cls[int32(cl)], classPoint{int32(t), int32(class)})
	}
}

func (h *healthData) addExact(client int, t int, hp int32) {
	h.hp[int32(client)] = append(h.hp[int32(client)], hpPoint{int32(t), hp, true})
}

// merge adds another demo's points, shifted onto this clock.
func (h *healthData) merge(o *healthData, offset int32) {
	for c, ps := range o.hp {
		for _, p := range ps {
			p.t += offset
			h.hp[c] = append(h.hp[c], p)
		}
	}
	for c, ps := range o.cls {
		for _, p := range ps {
			p.t += offset
			h.cls[c] = append(h.cls[c], p)
		}
	}
}

// Drop is a loss of health seen in the timeline.  Until attackers are known
// it is damage received by Victim from an unknown source (or the world).
//
// Amount counts health lost down to zero only: the overkill of a lethal hit
// and shots at a player who is already down are not counted.  Against the
// scoreboard's damage-received column that rule fits best (a floor of -20 or
// lower, or counting overkill, overshoots by 10-40%).
type Drop struct {
	Time   int32 // when the lower reading was taken
	From   int32 // when the higher reading was taken; the damage happened between From and Time
	Victim int32
	Amount int32
	Exact  bool // both ends came from the playerState
	// Before and After are the raw health at both ends; a lethal hit ends
	// below zero by its overkill.
	Before, After int32
}

// HealthReport says how much of the health in a replay is measured.
type HealthReport struct {
	KnownSamples, GuessedSamples int // non-playerState samples with / without a health reading
	Drops                        []Drop
}

const (
	exactStepMs   = 150  // frames this close are one continuous exact run
	tinfoMaxAgeMs = 3000 // how stale a tinfo reading may be and still describe a sample
	classMaxAgeMs = 5000
	maxDropGapMs  = 2000 // a larger gap hides respawns and heals
	minDropAmount = 3    // overheal decays by 1 hp a second; that is not damage
)

// cleaned returns a client's points in time order, one per instant (exact
// wins), with tinfo points inside an exact run removed.
func cleaned(ps []hpPoint) []hpPoint {
	sort.SliceStable(ps, func(i, j int) bool {
		if ps[i].t != ps[j].t {
			return ps[i].t < ps[j].t
		}
		return ps[i].exact && !ps[j].exact
	})
	uniq := ps[:0:0]
	for _, p := range ps {
		if n := len(uniq); n > 0 && uniq[n-1].t == p.t {
			continue
		}
		uniq = append(uniq, p)
	}
	out := uniq[:0:0]
	for i, p := range uniq {
		if !p.exact && i > 0 && i+1 < len(uniq) {
			a, b := uniq[i-1], uniq[i+1]
			if a.exact && b.exact && p.t-a.t <= exactStepMs && b.t-p.t <= exactStepMs {
				continue
			}
		}
		out = append(out, p)
	}
	return out
}

// applyHealth fills Health and PlayerClass in the samples of rep, whose
// frames are in time order.  real[i] has bit n set when frame i's sample of
// client n came from a playerState (its Health is already right).  isLive
// says whether the match is being played at a time; drops outside play
// (warm-up) are not reported.
func applyHealth(rep *replay.Replay, real []uint64, h *healthData, isLive func(int) bool) HealthReport {
	var hr HealthReport
	pts := map[int32][]hpPoint{}
	for c, ps := range h.hp {
		pts[c] = cleaned(append([]hpPoint(nil), ps...))
	}
	cls := map[int32][]classPoint{}
	for c, ps := range h.cls {
		ps = append([]classPoint(nil), ps...)
		sort.SliceStable(ps, func(i, j int) bool { return ps[i].t < ps[j].t })
		cls[c] = ps
	}

	hi, ci := map[int32]int{}, map[int32]int{}
	for fi := range rep.Frames {
		f := &rep.Frames[fi]
		for si := range f.Samples {
			s := &f.Samples[si]
			c := s.ClientNum
			// latest class reading at or before this frame
			cp := cls[c]
			for ci[c]+1 < len(cp) && cp[ci[c]+1].t <= f.ServerTime {
				ci[c]++
			}
			if len(cp) > 0 && cp[ci[c]].t <= f.ServerTime && f.ServerTime-cp[ci[c]].t <= classMaxAgeMs {
				s.PlayerClass = cp[ci[c]].class
			}
			if fi < len(real) && real[fi]&(1<<uint(c)) != 0 {
				continue
			}
			if s.PmType == pmDeadType {
				continue // flagged dead: health 0
			}
			hp := pts[c]
			for hi[c]+1 < len(hp) && hp[hi[c]+1].t <= f.ServerTime {
				hi[c]++
			}
			if len(hp) > 0 && hp[hi[c]].t <= f.ServerTime && f.ServerTime-hp[hi[c]].t <= tinfoMaxAgeMs {
				v := hp[hi[c]].hp
				if v < 0 {
					v = 0
				}
				s.Health = v
				hr.KnownSamples++
			} else {
				hr.GuessedSamples++
			}
		}
	}

	var clients []int
	for c := range pts {
		clients = append(clients, int(c))
	}
	sort.Ints(clients)
	for _, ci := range clients {
		c := int32(ci)
		ps := pts[c]
		for i := 1; i < len(ps); i++ {
			p, q := ps[i-1], ps[i]
			if q.t-p.t > maxDropGapMs || !isLive(int(q.t)) {
				continue
			}
			amount := positive(p.hp) - positive(q.hp)
			if amount < minDropAmount {
				continue
			}
			hr.Drops = append(hr.Drops, Drop{q.t, p.t, c, amount, p.exact && q.exact, p.hp, q.hp})
		}
	}
	sort.SliceStable(hr.Drops, func(i, j int) bool { return hr.Drops[i].Time < hr.Drops[j].Time })
	return hr
}

func positive(x int32) int32 {
	if x < 0 {
		return 0
	}
	return x
}
