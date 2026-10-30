package demo

import (
	"sort"

	"s4ndmod26/status-api/replay"
)

// A bullet that hits a player spawns a temp entity (EV_BULLET_HIT_FLESH) with
// the victim in eventParm and the shooter in otherEntityNum.  It carries no
// damage, but health drops do, so each drop is attributed to the hits on
// that victim around it.  The event number differs between protocols, so the
// type is found from the data: it is the one whose (victim, shooter) pairs
// match the obituaries (victim, killer) just before each kill.
type hit struct {
	t        int32
	victim   int32
	attacker int32
}

const (
	hitBeforeKillMs = 400 // a hit precedes the obituary it caused
	hitAfterKillMs  = 100
	hitMinMatches   = 5
	hitMinShare     = 0.25 // of the kills seen; a recorder only sees what is in view
	// A health drop shows up in the snapshot at or just after the hit, and a
	// drop between two readings (tinfo, a second apart) happened anywhere
	// between them; slack covers the temp entity's snapshot delay.
	dropHitBeforeMs = 100
	dropHitAfterMs  = 100
	hitDedupMs      = 60
	// Flesh hits do not say what was fired, so attributed damage is tagged
	// with a generic bullet means of death (a pistol/SMG/rifle value, which
	// the replay analysis counts as shooting) rather than a guessed weapon.
	genericBulletMOD = 16
)

func detectFlesh(d *Demo, obitType int) int {
	if obitType == 0 {
		return 0
	}
	lay := d.Layout
	it, io, io2, ip := lay.EntityIdx("eType"), lay.EntityIdx("otherEntityNum"), lay.EntityIdx("otherEntityNum2"), lay.EntityIdx("eventParm")
	if it < 0 || io < 0 || io2 < 0 || ip < 0 {
		return 0
	}
	type kill struct{ t, victim, killer int }
	var kills []kill
	byType := map[int]map[int][]hit{} // type -> victim -> candidate hits
	prev := map[int]int{}
	for fi := range d.Frames {
		fr := &d.Frames[fi]
		cur := map[int]int{}
		for ei := range fr.Entities {
			e := &fr.Entities[ei]
			et := int(e.F[it])
			if e.Number < maxClients || et < etEvents {
				continue
			}
			cur[e.Number] = et
			if prev[e.Number] == et {
				continue
			}
			if et == obitType {
				kills = append(kills, kill{fr.ServerTime, int(e.F[io]), int(e.F[io2])})
				continue
			}
			if v, a := int(e.F[ip]), int(e.F[io]); v < maxClients && a < maxClients {
				if byType[et] == nil {
					byType[et] = map[int][]hit{}
				}
				byType[et][v] = append(byType[et][v], hit{int32(fr.ServerTime), int32(v), int32(a)})
			}
		}
		prev = cur
	}
	best, bestN := 0, 0
	for et, byVictim := range byType {
		n := 0
		for _, k := range kills {
			for _, h := range byVictim[k.victim] {
				if dt := k.t - int(h.t); int(h.attacker) == k.killer && dt >= -hitAfterKillMs && dt <= hitBeforeKillMs {
					n++
					break
				}
			}
		}
		if n > bestN {
			best, bestN = et, n
		}
	}
	if bestN < hitMinMatches || float64(bestN) < hitMinShare*float64(len(kills)) {
		return 0
	}
	return best
}

// unionHits combines the hits seen by several demos (already on one clock).
// The same shot seen by two demos is one hit: a hit matches at most one
// earlier hit from each other demo.
func unionHits(perPart [][]hit) []hit {
	type tagged struct {
		hit
		seen uint64
	}
	var all []tagged
	for p, hs := range perPart {
		for _, h := range hs {
			all = append(all, tagged{h, 1 << uint(p)})
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].t < all[j].t })
	var kept []tagged
	for _, h := range all {
		matched := false
		for k := len(kept) - 1; k >= 0 && h.t-kept[k].t <= hitDedupMs; k-- {
			o := &kept[k]
			if o.victim == h.victim && o.attacker == h.attacker && o.seen&h.seen == 0 {
				o.seen |= h.seen
				matched = true
				break
			}
		}
		if !matched {
			kept = append(kept, h)
		}
	}
	out := make([]hit, len(kept))
	for i, k := range kept {
		out[i] = k.hit
	}
	return out
}

// DamageReport says how much of the health lost was traced to a shooter.
type DamageReport struct {
	Attributed, Unattributed int // hp
	Events                   int
}

// attributeDamage turns health drops into EventDamage events for the drops
// that line up with a bullet hit.  Each hit explains at most one drop; a drop
// with several hits is shared between them.  Drops with no hit (explosives,
// falls, fire, melee) stay unattributed.
func attributeDamage(drops []Drop, hits []hit) ([]replay.Event, DamageReport) {
	var dr DamageReport
	byVictim := map[int32][]int{}
	for i, h := range hits {
		byVictim[h.victim] = append(byVictim[h.victim], i)
	}
	used := make([]bool, len(hits))
	var out []replay.Event
	for _, d := range drops {
		var hs []int
		for _, i := range byVictim[d.Victim] {
			if h := hits[i].t; !used[i] && h >= d.From-dropHitBeforeMs && h <= d.Time+dropHitAfterMs {
				hs = append(hs, i)
			}
		}
		if len(hs) == 0 {
			dr.Unattributed += int(d.Amount)
			continue
		}
		share, rest := d.Amount/int32(len(hs)), d.Amount%int32(len(hs))
		for k, i := range hs {
			used[i] = true
			amount := share
			if k == 0 {
				amount += rest
			}
			h := hits[i]
			out = append(out, replay.Event{
				ServerTime:      h.t,
				ActorClientNum:  h.attacker,
				TargetClientNum: h.victim,
				Score:           amount,
				Type:            replay.EventDamage,
				MeansOfDeath:    genericBulletMOD,
				InflictorEntNum: -1,
				LaunchEntNum:    -1,
			})
		}
		dr.Attributed += int(d.Amount)
	}
	dr.Events = len(out)
	return out, dr
}
