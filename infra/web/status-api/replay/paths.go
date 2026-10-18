package replay

import "math"

// PathSegment is one stretch of a player's life on one team: x, y, z (game units, whole numbers) for every sample, StepMs apart
// starting at StartMs.  A new segment starts at each respawn, team change or gap in the recording.
type PathSegment struct {
	StartMs int32   `json:"t"`
	Team    int32   `json:"team"` // 1 axis, 2 allies
	Pts     []int32 `json:"p"`
	Health  []int32 `json:"h"` // HP at each sample
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

// PathOptions narrows what BuildPathsWith keeps: a time window (ToMs 0 = the whole recording) and the client slots wanted (nil =
// everyone).
type PathOptions struct {
	StepMs       int32
	FromMs, ToMs int32
	Slots        map[int32]bool
}

func (o PathOptions) wantTime(t int32) bool { return o.ToMs == 0 || (t >= o.FromMs && t <= o.ToMs) }
func (o PathOptions) wantSlot(s int32) bool { return o.Slots == nil || o.Slots[s] }

// BuildPaths thins the replay's frames to one position per player every stepMs while they are alive on a team.
func BuildPaths(rep *Replay, stepMs int32) *Paths {
	return BuildPathsWith(rep, PathOptions{StepMs: stepMs})
}

// BuildPathsWith is BuildPaths for a time window and a set of players.
func BuildPathsWith(rep *Replay, opt PathOptions) *Paths {
	stepMs := opt.StepMs
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
		if !opt.wantTime(f.ServerTime) {
			continue
		}
		for _, s := range f.Samples {
			if !opt.wantSlot(s.ClientNum) || s.Health <= 0 || (s.Team != 1 && s.Team != 2) || s.ClientNum < 0 || s.ClientNum >= 64 {
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
			seg.Health = append(seg.Health, s.Health)
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
		if (ev.Type != EventKill && ev.Type != EventTeamkill) || !opt.wantTime(ev.ServerTime) || !opt.wantSlot(ev.ActorClientNum) || !opt.wantSlot(ev.TargetClientNum) {
			continue
		}
		p.Kills = append(p.Kills, KillMark{
			TimeMs: ev.ServerTime, Killer: ev.ActorClientNum, Victim: ev.TargetClientNum, Team: ev.Type == EventTeamkill,
			Pos: [3]int32{int32(ev.Origin[0]), int32(ev.Origin[1]), int32(ev.Origin[2])},
		})
	}
	return p
}

// Shot is one bullet fired, as a line from the shooter's eye: to the victim when a hit of theirs landed at that moment, otherwise a
// tracer along where they were aiming that stops at the enemy it was aimed at (there is no map in the recording to find where it really
// stopped).  When nobody was in the line of fire it is as long as the distance to the nearest enemy, between shotMissMin and
// shotMissRange, so a fight in a stairwell gets short lines and one across a field gets longer ones.
type Shot struct {
	TimeMs int32    `json:"t"`
	Slot   int32    `json:"slot"`
	Team   int32    `json:"team"`
	From   [3]int32 `json:"a"`
	To     [3]int32 `json:"b"`
	Hit    bool     `json:"hit"`
	Weapon int32    `json:"w"`
}

const (
	shotRepeatMs  = 100 // an automatic weapon held down is one line per this long
	shotMissRange = 400 // the longest a miss's tracer is when nobody is in the line of fire, in game units
	shotMissMin   = 100 // and the shortest
	shotAimCone   = 0.8 // cosine of the cone around the aim within which an enemy is what the shot was at (about 37 degrees)
	shotHitSlopMs = 150 // a hit this close in time after a shot belongs to it
	eyeStanding   = 40  // DEFAULT_VIEWHEIGHT
	eyeCrouched   = 16  // CROUCH_VIEWHEIGHT
	weaponFiring  = 6   // WEAPON_FIRING / WEAPON_FIRINGALT (7)
)

// bulletWeapons are the weapons (WP_* numbers) whose shots are drawn: pistols, SMGs, rifles, the sniper rifle and venom.  Explosives,
// flames, knives, the syringe and ammo packs are projectiles or not shots.
var bulletWeapons = map[int32]bool{2: true, 3: true, 4: true, 5: true, 8: true, 13: true, 14: true, 15: true, 16: true, 19: true, 20: true,
	21: true, 23: true, 24: true, 25: true, 29: true, 30: true}

// BuildShots finds the bullets fired in the window by the wanted players.  The recording says when a weapon was firing (one state per
// 50 ms frame), so a line is drawn when firing starts and again every shotRepeatMs while it continues.
func BuildShots(rep *Replay, opt PathOptions) []Shot {
	shots := []Shot{}
	prevFiring := map[int32]bool{}
	lastShot := map[int32]int32{}
	// damage events by shooter, for the hit end of a line
	type hit struct {
		t    int32
		to   [3]float32
		used bool
	}
	hits := map[int32][]*hit{}
	for _, ev := range rep.Events {
		if ev.Type == EventDamage && opt.wantTime(ev.ServerTime) {
			hits[ev.ActorClientNum] = append(hits[ev.ActorClientNum], &hit{t: ev.ServerTime, to: ev.Origin})
		}
	}
	for _, f := range rep.Frames {
		if !opt.wantTime(f.ServerTime) {
			continue
		}
		for _, s := range f.Samples {
			firing := s.Weaponstate == weaponFiring || s.Weaponstate == weaponFiring+1
			was := prevFiring[s.ClientNum]
			prevFiring[s.ClientNum] = firing && s.Health > 0
			if !firing || s.Health <= 0 || !opt.wantSlot(s.ClientNum) || (s.Team != 1 && s.Team != 2) {
				continue
			}
			if s.Weapon >= 0 && !bulletWeapons[s.Weapon] {
				continue
			}
			if was && f.ServerTime-lastShot[s.ClientNum] < shotRepeatMs {
				continue
			}
			lastShot[s.ClientNum] = f.ServerTime
			eye := float32(eyeStanding)
			if s.PmFlags&1 != 0 {
				eye = eyeCrouched
			}
			from := [3]float32{s.Origin[0], s.Origin[1], s.Origin[2] + eye}
			pitch, yaw := float64(s.ViewAngles[0])*math.Pi/180, float64(s.ViewAngles[1])*math.Pi/180
			dir := [3]float64{math.Cos(pitch) * math.Cos(yaw), math.Cos(pitch) * math.Sin(yaw), -math.Sin(pitch)}
			sh := Shot{TimeMs: f.ServerTime, Slot: s.ClientNum, Team: s.Team, Weapon: s.Weapon}
			sh.From = [3]int32{int32(from[0]), int32(from[1]), int32(from[2])}
			reach, inLine := float64(shotMissRange), false
			nearest := math.MaxFloat64
			for _, e := range f.Samples { // the nearest living enemy inside the aim cone is what a miss was aimed at
				if e.Team == s.Team || (e.Team != 1 && e.Team != 2) || e.Health <= 0 {
					continue
				}
				dx, dy, dz := float64(e.Origin[0]-from[0]), float64(e.Origin[1]-from[1]), float64(e.Origin[2]-from[2])
				dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
				if dist < nearest {
					nearest = dist
				}
				if dist < 1 || (dx*dir[0]+dy*dir[1]+dz*dir[2])/dist < shotAimCone {
					continue
				}
				if !inLine || dist < reach {
					reach, inLine = dist, true
				}
			}
			if !inLine && nearest < shotMissRange {
				reach = math.Max(nearest, shotMissMin)
			}
			sh.To = [3]int32{int32(float64(from[0]) + dir[0]*reach), int32(float64(from[1]) + dir[1]*reach), int32(float64(from[2]) + dir[2]*reach)}
			for _, h := range hits[s.ClientNum] {
				if !h.used && h.t >= f.ServerTime-shotHitSlopMs/3 && h.t <= f.ServerTime+shotHitSlopMs {
					h.used = true
					sh.To = [3]int32{int32(h.to[0]), int32(h.to[1]), int32(h.to[2])}
					sh.Hit = true
					break
				}
			}
			shots = append(shots, sh)
		}
	}
	return shots
}
