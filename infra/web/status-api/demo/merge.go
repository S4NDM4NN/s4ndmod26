package demo

import (
	"fmt"
	"sort"

	"s4ndmod26/status-api/replay"
)

// MergeDemos overlays several client demos of one game into a single
// replay.  Each player's demo sees only what is in that player's view, so the
// merged replay has more players per frame than any one demo, real health for
// every player who recorded, and the union of the kill events.
//
// Demos are put on the clock of the longest one using Sync; a demo that does
// not sync with it is left out and reported.  The demos must have been parsed
// with KeepPlayersEvents.

// The same event seen by two demos is reported at the same server time, give
// or take a snapshot (eventDedupMs).  A kill gets more slack (killDedupMs): a
// victim cannot die again to the same player within half a second, so a second
// obituary that close is the same one seen again, for instance after a demo
// lost a snapshot and the temp entity reappeared.
const (
	eventDedupMs = 150
	killDedupMs  = 500
)

// PartReport says what became of one input demo.
type PartReport struct {
	Recorder   int
	Name       string
	Spectator  bool
	OffsetMs   int
	Used       bool
	Reason     string
	Frames     int
	AvgPlayers float64 // player samples per frame in this demo alone
	// MergedAvgPlayers is the same measure in the merged replay, over this
	// demo's own frames; compare with AvgPlayers for the gain from merging.
	MergedAvgPlayers float64
}

// MergeReport summarises a merge.
type MergeReport struct {
	Parts  []PartReport
	Frames int
	// AvgPlayers is player samples per merged frame; compare with the parts.
	AvgPlayers float64
	// OwnHealthPlayers is how many players have real (playerState) health in
	// the merged replay, i.e. recorded one of the merged demos.
	OwnHealthPlayers int
	RawEvents        int // events from all parts before de-duplication
	Events           int
	// Health says how much of the merged health and class was read from the
	// demos; Health.Drops are the losses of health seen (attacker unknown).
	Health HealthReport
	Damage DamageReport
}

type mergePart struct {
	c         *conv
	rep       *replay.Replay
	recorder  int
	spectator bool
	offset    int
}

// pick is the sample chosen for one client at one time; own marks one that
// came from a playerState (exact health).
type pick struct {
	s   replay.Sample
	own bool
}

// MergeDemos merges demos held in memory; it returns an error if none can be
// used.  The longest demo is the clock reference.
func MergeDemos(ds []*Demo) (*replay.Replay, MergeReport, error) {
	if len(ds) == 0 {
		return nil, MergeReport{}, fmt.Errorf("demo: nothing to merge")
	}
	ref := 0
	span := func(d *Demo) int { in := Describe(d); return in.EndTime - in.StartTime }
	for i, d := range ds {
		if span(d) > span(ds[ref]) {
			ref = i
		}
	}
	return MergeStream(len(ds), ref, func(i int) (*Demo, error) { return ds[i], nil })
}

// MergeStream merges n demos that load hands over one at a time, so only the
// reference demo (index ref, whose clock the others are put on) and the demo
// being converted are parsed at once.  A demo that cannot be loaded or does
// not sync with the reference is left out and reported.
func MergeStream(n, ref int, load func(i int) (*Demo, error)) (*replay.Replay, MergeReport, error) {
	var mr MergeReport
	if n == 0 || ref < 0 || ref >= n {
		return nil, mr, fmt.Errorf("demo: nothing to merge")
	}
	refDemo, err := load(ref)
	if err != nil {
		return nil, mr, fmt.Errorf("demo: reference demo: %w", err)
	}

	var parts []mergePart
	refPart := 0
	for i := 0; i < n; i++ {
		d := refDemo
		if i != ref {
			if d, err = load(i); err != nil {
				mr.Parts = append(mr.Parts, PartReport{Reason: "could not be read: " + err.Error()})
				continue
			}
		}
		info := Describe(d)
		pr := PartReport{Recorder: d.ClientNum, Name: info.Recorder, Spectator: info.Spectator}
		if i != ref {
			r := Sync(refDemo, d)
			if !r.Synced {
				pr.Reason = r.Reason
				mr.Parts = append(mr.Parts, pr)
				continue
			}
			pr.OffsetMs = r.OffsetMs
		}
		c := convert(d)
		pr.Used, pr.Frames = true, c.report.Frames
		if c.report.Frames > 0 {
			pr.AvgPlayers = float64(c.report.PlayerSamples) / float64(c.report.Frames)
		}
		mr.Parts = append(mr.Parts, pr)
		if i == ref {
			refPart = len(parts)
		}
		parts = append(parts, mergePart{c, c.rep, d.ClientNum, info.Spectator, pr.OffsetMs})
	}
	if len(parts) == 0 {
		return nil, mr, fmt.Errorf("demo: no demo could be used")
	}

	// time -> client -> best sample.  An own sample (from the recorder's
	// playerState: real health, angles) beats an entityState sample.
	byTime := map[int32]map[int32]pick{}
	for _, p := range parts {
		for fi := range p.rep.Frames {
			f := &p.rep.Frames[fi]
			t := f.ServerTime + int32(p.offset)
			m := byTime[t]
			if m == nil {
				m = map[int32]pick{}
				byTime[t] = m
			}
			for _, s := range f.Samples {
				own := p.c.real[fi]&(1<<uint(s.ClientNum)) != 0
				if cur, ok := m[s.ClientNum]; ok && (cur.own || !own) {
					continue
				}
				m[s.ClientNum] = pick{s, own}
			}
		}
	}

	// the header comes from the part with the most frames; player names are
	// the union over all parts
	best := 0
	for i, p := range parts {
		if len(p.rep.Frames) > len(parts[best].rep.Frames) {
			best = i
		}
	}
	out := &replay.Replay{Header: parts[best].rep.Header}
	for _, p := range parts {
		for n, name := range p.rep.Header.PlayerNames {
			if out.Header.PlayerNames[n] == "" {
				out.Header.PlayerNames[n] = name
			}
		}
	}
	times := make([]int32, 0, len(byTime))
	for t := range byTime {
		times = append(times, t)
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	samples, ownHealth := 0, map[int32]bool{}
	var real []uint64
	for _, t := range times {
		m := byTime[t]
		ids := make([]int, 0, len(m))
		for n := range m {
			ids = append(ids, int(n))
		}
		sort.Ints(ids)
		f := replay.Frame{ServerTime: t, Samples: make([]replay.Sample, 0, len(ids))}
		var mask uint64
		for _, n := range ids {
			pk := m[int32(n)]
			f.Samples = append(f.Samples, pk.s)
			if pk.own {
				ownHealth[pk.s.ClientNum] = true
				mask |= 1 << uint(n)
			}
		}
		real = append(real, mask)
		samples += len(f.Samples)
		out.Frames = append(out.Frames, f)
	}
	out.Header.FrameCount = int32(len(out.Frames))

	// union of events, the same one seen by several demos kept once
	var all []replay.Event
	for _, p := range parts {
		for _, e := range p.rep.Events {
			e.ServerTime += int32(p.offset)
			all = append(all, e)
		}
	}
	out.Events = dedupeEvents(all)
	out.Header.EventCount = int32(len(out.Events))

	pi := 0
	for i := range mr.Parts {
		pr := &mr.Parts[i]
		if !pr.Used {
			continue
		}
		p := parts[pi]
		pi++
		n := 0
		for fi := range p.rep.Frames {
			n += len(byTime[p.rep.Frames[fi].ServerTime+int32(p.offset)])
		}
		if len(p.rep.Frames) > 0 {
			pr.MergedAvgPlayers = float64(n) / float64(len(p.rep.Frames))
		}
	}

	// health and class from the playerStates and tinfo of every demo
	hd := newHealthData()
	for _, p := range parts {
		hd.merge(p.c.hp, int32(p.offset))
	}
	refPh := parts[refPart].c.ph
	mr.Health = applyHealth(out, real, hd, func(t int) bool { return live(refPh, t-parts[refPart].offset) })
	nKills := len(out.Events)
	perPart := make([][]hit, len(parts))
	for i, p := range parts {
		for _, h := range p.c.hits {
			h.t += int32(p.offset)
			perPart[i] = append(perPart[i], h)
		}
	}
	dmgEvents, dr := attributeDamage(mr.Health.Drops, unionHits(perPart))
	fl := make([][]flight, len(parts))
	for i, p := range parts {
		for _, f := range p.c.flight {
			f.first, f.last, f.launchT = f.first+int32(p.offset), f.last+int32(p.offset), f.launchT+int32(p.offset)
			fl[i] = append(fl[i], f)
		}
	}
	xev, xhp := attributeExplosions(out, dr.Left, unionFlights(fl))
	dr.Explosive, dr.Unattributed = xhp, dr.Unattributed-xhp
	mr.Damage = dr
	out.Events = append(append(out.Events, dmgEvents...), xev...)
	sort.SliceStable(out.Events, func(i, j int) bool { return out.Events[i].ServerTime < out.Events[j].ServerTime })
	out.Header.EventCount = int32(len(out.Events))

	mr.Frames, mr.RawEvents, mr.Events = len(out.Frames), len(all), nKills
	mr.OwnHealthPlayers = len(ownHealth)
	if mr.Frames > 0 {
		mr.AvgPlayers = float64(samples) / float64(mr.Frames)
	}
	return out, mr, nil
}

// dedupeEvents sorts events by time and keeps one of each occurrence.
func dedupeEvents(evs []replay.Event) []replay.Event {
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].ServerTime < evs[j].ServerTime })
	var out []replay.Event
	for _, e := range evs {
		dup := false
		for k := len(out) - 1; k >= 0 && e.ServerTime-out[k].ServerTime <= killDedupMs; k-- {
			o := out[k]
			window := int32(eventDedupMs)
			if obituaryKind(e.Type) && obituaryKind(o.Type) {
				window = killDedupMs
			}
			if e.ServerTime-o.ServerTime <= window && sameEvent(o, e) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, e)
		}
	}
	return out
}

// sameEvent reports whether two events are one occurrence seen by two demos.
// A kill, a teamkill and a suicide are the same obituary read with different
// team knowledge, so they match on who died and who did it.
func sameEvent(a, b replay.Event) bool {
	if a.ActorClientNum != b.ActorClientNum || a.TargetClientNum != b.TargetClientNum {
		return false
	}
	return a.Type == b.Type || (obituaryKind(a.Type) && obituaryKind(b.Type))
}

func obituaryKind(t replay.EventType) bool {
	return t == replay.EventKill || t == replay.EventTeamkill || t == replay.EventSuicide
}
