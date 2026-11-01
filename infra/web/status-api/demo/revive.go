package demo

import (
	"s4ndmod26/status-api/replay"
)

// A revive is not announced to everyone (only the revived player gets a
// "You have been revived by ..." message), but it leaves a mark: a player who
// was down is up again where they fell.  A player who is killed and comes back
// from limbo respawns at a spawn point, usually far from where they died, so
// the two are told apart by where the player is when they get up.  The reviver
// is the medic standing next to them.
const (
	reviveMaxMs   = 45000 // longest a player can be down before being revived
	reviveMaxDist = 200   // from where they fell to where they get up
	reviverReach  = 150   // the medic is this close
	classMedic    = 1
)

func detectRevives(rep *replay.Replay) []replay.Event {
	type st struct {
		known, dead bool
		at          int32
		pos         [3]float32
	}
	state := map[int32]*st{}
	var out []replay.Event
	for fi := range rep.Frames {
		f := &rep.Frames[fi]
		for _, s := range f.Samples {
			if s.ClientNum >= maxClients || (s.Team != 1 && s.Team != 2) {
				continue
			}
			alive := s.Health > 0 && s.PmType != pmDeadType
			p := state[s.ClientNum]
			if p == nil {
				p = &st{}
				state[s.ClientNum] = p
			}
			if p.known && p.dead && alive &&
				f.ServerTime-p.at <= reviveMaxMs && dist(s.Origin, p.pos[0], p.pos[1], p.pos[2]) <= reviveMaxDist {
				out = append(out, replay.Event{
					ServerTime: f.ServerTime, ActorClientNum: reviver(f, s), TargetClientNum: s.ClientNum,
					Type: replay.EventRevive, Origin: s.Origin, InflictorEntNum: -1, LaunchEntNum: -1,
				})
			}
			if !alive {
				if !p.dead {
					p.at = f.ServerTime
				}
				p.pos = s.Origin
			}
			p.known, p.dead = true, !alive
		}
	}
	return out
}

// reviver is the nearest living medic of the revived player's team within
// reach, or -1.
func reviver(f *replay.Frame, victim replay.Sample) int32 {
	best, who := float64(reviverReach), int32(-1)
	for _, s := range f.Samples {
		if s.ClientNum >= maxClients || s.ClientNum == victim.ClientNum || s.Team != victim.Team ||
			s.Health <= 0 || s.PlayerClass != classMedic {
			continue
		}
		if d := dist(s.Origin, victim.Origin[0], victim.Origin[1], victim.Origin[2]); d <= best {
			best, who = d, s.ClientNum
		}
	}
	return who
}
