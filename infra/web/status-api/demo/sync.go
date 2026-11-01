package demo

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Sync decides whether two client demos recorded the same game, and if so
// how their server clocks relate.  It does not trust identifiers such as the
// server id on their own: the verdict rests on server commands that both
// demos received (chat, prints, configstring changes) and on the positions
// of players that both demos can see.

// Tunables.  Positions are compared between snapshots up to posTimeSlackMs
// apart, so the tolerance has to absorb a little player movement.
const (
	anchorBinMs     = 100  // histogram bin for command-time differences
	anchorWindowMs  = 250  // a command counts as consistent within this of the offset
	minAnchors      = 3    // command anchors needed for an event-based sync
	minPosSamples   = 50   // shared player samples needed to trust positions
	posTimeSlackMs  = 60   // max gap between the two snapshots compared
	posToleranceU   = 150  // world units
	posAgreeRatio   = 0.90 // fraction of samples within tolerance
	minOverlapMs    = 5000
	maxPosSamples   = 20000
	etPlayer        = 1
	maxPlayerEntNum = maxClients
)

// anchorPrefixes are the server commands that are broadcast to everyone, so
// two clients of one game receive the same text.  Per-client or per-frame
// traffic (tinfo, scores, shake, cp ...) is ignored.
var anchorPrefixes = map[string]bool{
	"chat": true, "tchat": true, "vtchat": true, "print": true,
	"cs": true, "sc": true, "ws": true,
}

// SyncResult is the outcome of comparing two demos.
type SyncResult struct {
	Synced bool
	// OffsetMs maps B's server clock onto A's: timeA = timeB + OffsetMs.
	OffsetMs int
	// Overlap is the time span (in A's clock) both demos cover.
	OverlapStart, OverlapEnd int
	// CmdAnchors is the number of shared unique commands that agree with
	// the offset, out of CmdCandidates that were shared at all.
	CmdAnchors, CmdCandidates int
	// PosSamples and PosAgree count player-position comparisons.
	PosSamples, PosAgree int
	// Basis is "events", "events+positions" or "positions".
	Basis string
	// Reason explains a negative verdict (or a caveat on a positive one).
	Reason string
}

// Sync compares demo b against demo a.
func Sync(a, b *Demo) SyncResult {
	ia, ib := Describe(a), Describe(b)
	if len(a.Frames) == 0 || len(b.Frames) == 0 {
		return SyncResult{Reason: "demo contains no frames"}
	}
	if ia.Map != ib.Map {
		return SyncResult{Reason: fmt.Sprintf("different maps (%s vs %s)", ia.Map, ib.Map)}
	}

	// Two players' own demos of one round share sv_serverid, so a mismatch
	// means different round or map loads; stop before estimating an offset
	// from coincidental commands.  A spectator (e.g. a WolfTV shoutcaster)
	// stays connected across round restarts, so one demo spans several
	// serverids and Describe only sees the last; such demos are judged on
	// events and positions instead.
	if !ia.Spectator && !ib.Spectator && ia.ServerID != "" && ib.ServerID != "" && ia.ServerID != ib.ServerID {
		return SyncResult{Reason: "recorded in a different round or map load (serverid differs)"}
	}

	off, cands, anchors := estimateOffset(a, b)
	res := SyncResult{OffsetMs: off, CmdAnchors: anchors, CmdCandidates: cands}

	res.OverlapStart = max(ia.StartTime, ib.StartTime+off)
	res.OverlapEnd = min(ia.EndTime, ib.EndTime+off)
	if res.OverlapEnd-res.OverlapStart < minOverlapMs {
		res.OverlapStart, res.OverlapEnd = 0, 0
		if ia.ServerID != "" && ib.ServerID != "" && ia.ServerID != ib.ServerID {
			res.Reason = "recorded in a different round or map load (no time overlap)"
		} else {
			res.Reason = "the demos do not overlap in time"
		}
		return res
	}

	res.PosSamples, res.PosAgree = comparePositions(a, b, off)
	posOK := res.PosSamples >= minPosSamples &&
		float64(res.PosAgree) >= posAgreeRatio*float64(res.PosSamples)
	posBad := res.PosSamples >= minPosSamples && !posOK
	eventsOK := anchors >= minAnchors

	switch {
	case eventsOK && posOK:
		res.Synced, res.Basis = true, "events+positions"
	case eventsOK && res.PosSamples < minPosSamples:
		res.Synced, res.Basis = true, "events"
		res.Reason = "synced on shared events only; the players never shared a view"
	case posOK && !eventsOK:
		res.Synced, res.Basis = true, "positions"
		res.Reason = "synced on player positions only; few shared events"
	case posBad:
		res.Reason = "the demos overlap in time but player positions disagree (different game?)"
	default:
		res.Reason = "the demos overlap in time but share no matching events or visible players"
	}
	return res
}

// estimateOffset votes on timeA-timeB over server commands that appear
// exactly once in each demo.  It returns the offset, how many shared unique
// commands there were, and how many of them agree with the offset.
func estimateOffset(a, b *Demo) (offset, candidates, agreeing int) {
	idx := func(d *Demo) map[string][]int {
		m := map[string][]int{}
		for _, c := range d.Commands {
			if c.ServerTime < 0 || !anchorPrefixes[strings.SplitN(c.Text, " ", 2)[0]] {
				continue
			}
			m[c.Text] = append(m[c.Text], c.ServerTime)
		}
		return m
	}
	ma, mb := idx(a), idx(b)
	var deltas []int
	for text, ta := range ma {
		tb, ok := mb[text]
		if ok && len(ta) == 1 && len(tb) == 1 {
			deltas = append(deltas, ta[0]-tb[0])
		}
	}
	candidates = len(deltas)
	if candidates == 0 {
		return 0, 0, 0
	}
	bins := map[int]int{}
	for _, d := range deltas {
		bins[floorDiv(d, anchorBinMs)]++
	}
	bestBin, bestN := 0, -1
	for k, n := range bins {
		// prefer the bin (then its neighbours) with most votes; ties go to
		// the smaller |offset| so identical clocks win over coincidences
		n += bins[k-1] + bins[k+1]
		if n > bestN || (n == bestN && abs(k) < abs(bestBin)) {
			bestBin, bestN = k, n
		}
	}
	center := bestBin*anchorBinMs + anchorBinMs/2
	var near []int
	for _, d := range deltas {
		if abs(d-center) <= anchorWindowMs {
			near = append(near, d)
		}
	}
	if len(near) == 0 {
		return 0, candidates, 0
	}
	sort.Ints(near)
	return near[len(near)/2], candidates, len(near)
}

// comparePositions compares players that both demos can see at (almost) the
// same server time.  Frames of b are mapped into a's clock with off.
func comparePositions(a, b *Demo, off int) (samples, agree int) {
	ix := a.Layout.EntityIdx("pos.trBase[0]")
	it := a.Layout.EntityIdx("eType")
	if ix < 0 || it < 0 || b.Layout.EntityIdx("pos.trBase[0]") != ix {
		return 0, 0
	}
	j := 0
	for fi := range a.Frames {
		fa := &a.Frames[fi]
		target := fa.ServerTime - off // a's time expressed in b's clock
		for j+1 < len(b.Frames) && b.Frames[j+1].ServerTime <= target {
			j++
		}
		best := j
		if j+1 < len(b.Frames) &&
			abs(b.Frames[j+1].ServerTime-target) < abs(b.Frames[j].ServerTime-target) {
			best = j + 1
		}
		fb := &b.Frames[best]
		if abs(fb.ServerTime-target) > posTimeSlackMs {
			continue
		}
		for ei := range fa.Entities {
			ea := &fa.Entities[ei]
			if ea.Number >= maxPlayerEntNum || ea.F[it] != etPlayer {
				continue
			}
			eb := findEntity(fb.Entities, ea.Number)
			if eb == nil || eb.F[it] != etPlayer {
				continue
			}
			dx := Float(ea.F[ix]) - Float(eb.F[ix])
			dy := Float(ea.F[ix+1]) - Float(eb.F[ix+1])
			dz := Float(ea.F[ix+2]) - Float(eb.F[ix+2])
			samples++
			if math.Sqrt(dx*dx+dy*dy+dz*dz) <= posToleranceU {
				agree++
			}
			if samples >= maxPosSamples {
				return samples, agree
			}
		}
	}
	return samples, agree
}

// findEntity binary-searches a snapshot's entity list (sorted by Number).
func findEntity(es []EntityState, num int) *EntityState {
	i := sort.Search(len(es), func(i int) bool { return es[i].Number >= num })
	if i < len(es) && es[i].Number == num {
		return &es[i]
	}
	return nil
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}
