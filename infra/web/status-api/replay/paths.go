package replay

import "math"

// PathSegment is one stretch of a player's life on one team: x, y, z (game units, whole numbers) for every sample, StepMs apart
// starting at StartMs.  A new segment starts at each respawn, team change or gap in the recording.
type PathSegment struct {
	StartMs int32   `json:"t"`
	Team    int32   `json:"team"` // 1 axis, 2 allies
	Pts     []int32 `json:"p"`
}

// PlayerPath is everywhere one client slot was alive in a game.
type PlayerPath struct {
	Slot     int           `json:"slot"`
	Segments []PathSegment `json:"segs"`
}

// KillMark is a kill with where the victim died.
type KillMark struct {
	TimeMs int32    `json:"t"`
	Killer int32    `json:"killer"`
	Victim int32    `json:"victim"`
	Team   bool     `json:"teamkill,omitempty"`
	Pos    [3]int32 `json:"pos"`
}

// Paths is a game's player movement, thinned to one sample every StepMs, for drawing in 3D.
type Paths struct {
	Map     string       `json:"map"`
	StartMs int32        `json:"start_ms"`
	EndMs   int32        `json:"end_ms"`
	StepMs  int32        `json:"step_ms"`
	Min     [3]int32     `json:"min"`
	Max     [3]int32     `json:"max"`
	Players []PlayerPath `json:"players"`
	Kills   []KillMark   `json:"kills"`
}

// BuildPaths thins the replay's frames to one position per player every stepMs while they are alive on a team.
func BuildPaths(rep *Replay, stepMs int32) *Paths {
	if stepMs < 50 {
		stepMs = 50
	}
	p := &Paths{Map: rep.Header.MapName, StepMs: stepMs, Players: []PlayerPath{}, Kills: []KillMark{}}
	if len(rep.Frames) == 0 {
		return p
	}
	p.StartMs = rep.Frames[0].ServerTime
	p.EndMs = rep.Frames[len(rep.Frames)-1].ServerTime

	lo := [3]float32{math.MaxFloat32, math.MaxFloat32, math.MaxFloat32}
	hi := [3]float32{-math.MaxFloat32, -math.MaxFloat32, -math.MaxFloat32}
	bySlot := map[int32]*PlayerPath{}
	last := map[int32]int32{} // slot -> time of the last sample taken (or seen alive)

	for _, f := range rep.Frames {
		for _, s := range f.Samples {
			if s.Health <= 0 || (s.Team != 1 && s.Team != 2) || s.ClientNum < 0 || s.ClientNum >= 64 {
				continue
			}
			pp := bySlot[s.ClientNum]
			if pp == nil {
				pp = &PlayerPath{Slot: int(s.ClientNum), Segments: []PathSegment{}}
				bySlot[s.ClientNum] = pp
			}
			// samples sit on a grid of stepMs from the segment's start; a sample that comes a whole step late (a hole in the recording),
			// a respawn or a team change starts a new segment
			var seg *PathSegment
			if n := len(pp.Segments); n > 0 {
				seg = &pp.Segments[n-1]
				target := seg.StartMs + int32(len(seg.Pts)/3)*stepMs
				if seg.Team != s.Team || f.ServerTime-target >= stepMs || f.ServerTime-last[s.ClientNum] > stepMs*3/2 {
					seg = nil
				} else if f.ServerTime < target {
					continue
				}
			}
			if seg == nil {
				pp.Segments = append(pp.Segments, PathSegment{StartMs: f.ServerTime, Team: s.Team})
				seg = &pp.Segments[len(pp.Segments)-1]
			}
			seg.Pts = append(seg.Pts, int32(math.Round(float64(s.Origin[0]))), int32(math.Round(float64(s.Origin[1]))), int32(math.Round(float64(s.Origin[2]))))
			last[s.ClientNum] = f.ServerTime
			for i := 0; i < 3; i++ {
				lo[i] = float32(math.Min(float64(lo[i]), float64(s.Origin[i])))
				hi[i] = float32(math.Max(float64(hi[i]), float64(s.Origin[i])))
			}
		}
	}
	for slot := int32(0); slot < 64; slot++ {
		if pp := bySlot[slot]; pp != nil && len(pp.Segments) > 0 {
			p.Players = append(p.Players, *pp)
		}
	}
	for i := 0; i < 3; i++ {
		if lo[i] > hi[i] {
			lo[i], hi[i] = 0, 0
		}
		p.Min[i], p.Max[i] = int32(math.Floor(float64(lo[i]))), int32(math.Ceil(float64(hi[i])))
	}
	for _, ev := range rep.Events {
		if ev.Type != EventKill && ev.Type != EventTeamkill {
			continue
		}
		p.Kills = append(p.Kills, KillMark{
			TimeMs: ev.ServerTime, Killer: ev.ActorClientNum, Victim: ev.TargetClientNum, Team: ev.Type == EventTeamkill,
			Pos: [3]int32{int32(ev.Origin[0]), int32(ev.Origin[1]), int32(ev.Origin[2])},
		})
	}
	return p
}
