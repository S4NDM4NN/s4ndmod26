package demo

import (
	"math"
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
	Attributed, Unattributed int // hp (bullets)
	// Explosive is hp traced to a missile's thrower, taken out of Unattributed.
	Explosive int
	Events    int
	// Left are the drops no bullet hit explains (explosives, falls, fire).
	Left []Drop
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
			dr.Left = append(dr.Left, d)
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

// A grenade or rocket in flight is a missile entity.  Its owner is not in the
// snapshot, but its first sighting carries the launch point and time, and the
// thrower is the player standing there then.  The explosion itself is not an
// event a client sees (the entity just ends), so damage is matched by place
// and time: a drop with no bullet hit behind it, near where a missile flight
// ended, was most likely that explosion.
type flight struct {
	num        int
	wp         int
	first      int32 // seen from .. to
	last       int32
	launchT    int32 // trTime of the earliest trajectory seen
	lx, ly, lz float32
	ex, ey, ez float32 // where it was last seen (extrapolated along its trajectory)
}

const (
	throwerRadius = 150 // a muzzle is about 40 units from the thrower
	blastRadius   = 350
	blastBeforeMs = 300
	blastAfterMs  = 300
	flightGapMs   = 300
	// A splash means of death, so the analysis counts it; the weapon is not
	// told apart.
	genericExplosiveMOD = 5
)

// unionFlights unifies the runs of one missile seen by several demos (same
// entity number, overlapping in time) and keeps the earliest sighting for the
// launch and the latest for the end.
func unionFlights(perPart [][]flight) []flight {
	var all []flight
	for _, fs := range perPart {
		all = append(all, fs...)
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].num != all[j].num {
			return all[i].num < all[j].num
		}
		return all[i].first < all[j].first
	})
	var out []flight
	for _, r := range all {
		if n := len(out); n > 0 && out[n-1].num == r.num && r.first <= out[n-1].last+flightGapMs {
			f := &out[n-1]
			if r.first < f.first {
				f.first, f.launchT, f.lx, f.ly, f.lz = r.first, r.launchT, r.lx, r.ly, r.lz
			}
			if r.last >= f.last {
				f.last, f.ex, f.ey, f.ez = r.last, r.ex, r.ey, r.ez
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

func dist(a [3]float32, x, y, z float32) float64 {
	dx, dy, dz := float64(a[0]-x), float64(a[1]-y), float64(a[2]-z)
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

// nearestSample finds a client's sample in the frames at or just after t.
func nearestSample(rep *replay.Replay, client int32, t int32) (replay.Sample, bool) {
	i := sort.Search(len(rep.Frames), func(i int) bool { return rep.Frames[i].ServerTime >= t })
	for j := i; j < len(rep.Frames) && j < i+3; j++ {
		for _, s := range rep.Frames[j].Samples {
			if s.ClientNum == client {
				return s, true
			}
		}
	}
	return replay.Sample{}, false
}

// thrower is the player nearest the launch point when the missile was
// launched, or -1 if nobody was close enough to say.
func thrower(rep *replay.Replay, f flight) int32 {
	i := sort.Search(len(rep.Frames), func(i int) bool { return rep.Frames[i].ServerTime >= f.launchT })
	best, who := float64(throwerRadius), int32(-1)
	for j := i; j < len(rep.Frames) && j < i+2; j++ {
		for _, s := range rep.Frames[j].Samples {
			if d := dist(s.Origin, f.lx, f.ly, f.lz); d <= best {
				best, who = d, s.ClientNum
			}
		}
	}
	return who
}

// attributeExplosions gives drops that no bullet explains to the thrower of a
// missile that ended near the victim just then.
func attributeExplosions(rep *replay.Replay, left []Drop, flights []flight) ([]replay.Event, int) {
	owners := make([]int32, len(flights))
	for i, f := range flights {
		owners[i] = thrower(rep, f)
	}
	var out []replay.Event
	attributed := 0
	for _, d := range left {
		vs, ok := nearestSample(rep, d.Victim, d.Time)
		if !ok {
			continue
		}
		best, bi := float64(blastRadius), -1
		for i, f := range flights {
			if owners[i] < 0 || f.last < d.From-blastBeforeMs || f.last > d.Time+blastAfterMs {
				continue
			}
			if dd := dist(vs.Origin, f.ex, f.ey, f.ez); dd <= best {
				best, bi = dd, i
			}
		}
		if bi < 0 {
			continue
		}
		out = append(out, replay.Event{
			ServerTime:      flights[bi].last,
			ActorClientNum:  owners[bi],
			TargetClientNum: d.Victim,
			Score:           d.Amount,
			Type:            replay.EventDamage,
			MeansOfDeath:    genericExplosiveMOD,
			InflictorEntNum: int32(flights[bi].num),
			LaunchEntNum:    -1,
			Origin:          vs.Origin,
		})
		attributed += int(d.Amount)
	}
	return out, attributed
}
