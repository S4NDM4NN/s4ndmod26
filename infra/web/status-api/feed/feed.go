// Package feed builds a game's event feed (who killed whom, revives, objective events, joins and leaves) from its replay analysis.  It is a
// package of its own, without a database, so the server agent can use it too.
package feed

import (
	"strconv"

	"s4ndmod26/status-api/replay"
)

// Event is one line of a game's event feed.
type Event struct {
	TMs        int    `json:"t_ms"` // since the recording started
	Type       string `json:"type"` // kill teamkill suicide revive plant defuse capture steal return denial join leave end, and the support events medpack ammo strike tapout
	Actor      string `json:"actor,omitempty"`
	ActorTeam  string `json:"actor_team,omitempty"` // axis | allies
	ActorSlot  int    `json:"actor_slot"`           // client slot, -1 when there is none
	Target     string `json:"target,omitempty"`
	TargetTeam string `json:"target_team,omitempty"`
	TargetSlot int    `json:"target_slot"`
	Weapon     string `json:"weapon,omitempty"`
	Headshot   bool   `json:"headshot,omitempty"`
	// Where it happened, in game units, when the replay recorded a position (a kill is where the victim fell, a strike where it was
	// called to land); absent for events with no place, like joins.
	Pos *[3]int32 `json:"pos,omitempty"`
}

func teamName(t int32) string {
	switch t {
	case 1:
		return "axis"
	case 2:
		return "allies"
	}
	return ""
}

// feedTypes maps the analysis' event types to the feed's.  Anything not listed is left out: damage and pack events would bury the
// rest, and HEADSHOT / EXPLOSIVE_KILL / KNIFE_KILL / MULTIKILL are bonus events for the same kill as a KILL.
var feedTypes = map[string]string{
	"KILL":              "kill",
	"TEAMKILL":          "teamkill",
	"SUICIDE":           "suicide",
	"REVIVE":            "revive",
	"OBJECTIVE_PLANT":   "plant",
	"OBJECTIVE_DEFUSE":  "defuse",
	"OBJECTIVE_CAPTURE": "capture",
	"OBJECTIVE_STEAL":   "steal",
	"OBJECTIVE_RETURN":  "return",
	"OBJECTIVE_DENIAL":  "denial",
	"MEDPACK_PICKUP":    "medpack", // a medic's health pack taken: actor = the medic, target = who took it
	"AMMO_GIVE":         "ammo",    // a lieutenant's ammunition taken: actor = the lieutenant, target = who took it
	"STRIKE_LAUNCH":     "strike",  // an airstrike or artillery called in
	"TAPOUT":            "tapout",  // a player gave up waiting for a medic
	"PLAYER_JOIN":       "join",
	"PLAYER_LEAVE":      "leave",
	"MATCH_END":         "end",
}

// canHeadshotMod is the game's IsHeadShotWeapon: the pistols, SMGs and rifles, which are contiguous in meansOfDeath_t (Luger 15
// through FG42 scope 28).
func canHeadshotMod(mod int32) bool {
	return mod >= 15 && mod <= 28
}

// FromAnalysis builds the event feed of a game from its replay analysis: who killed whom with what, revives, objective
// events, and players joining or leaving after the start.  Names are the ones the slot's player had at the time.
func FromAnalysis(a *replay.Analysis) []Event {
	nameAt := func(slot int32, t int32) string {
		p := a.Players[strconv.Itoa(int(slot))]
		if p == nil {
			return ""
		}
		for _, sp := range p.NameHistory {
			if t >= sp.FromMs && (sp.ToMs < 0 || t < sp.ToMs) && sp.Name != "" {
				return sp.Name
			}
		}
		return p.DisplayName
	}
	teamOf := func(slot int32) string {
		if p := a.Players[strconv.Itoa(int(slot))]; p != nil {
			return teamName(p.Team)
		}
		return ""
	}

	type kill struct{ t, actor, target int32 }
	headshots := map[kill]bool{}
	for _, ev := range a.Events {
		if ev.Type == "HEADSHOT" {
			headshots[kill{ev.ServerTimeMs, ev.Actor, ev.Target}] = true
		}
	}

	start := a.Meta.StartTimeMs
	var out []Event
	for _, ev := range a.Events {
		typ, ok := feedTypes[ev.Type]
		if !ok {
			continue
		}
		// the roster at the start is not news
		if (typ == "join" || typ == "leave") && ev.ServerTimeMs <= start+1000 {
			continue
		}
		// an objective returned because its carrier was killed is already the kill
		if typ == "return" && ev.Target >= 0 {
			continue
		}
		fe := Event{TMs: int(ev.ServerTimeMs - start), Type: typ, ActorSlot: int(ev.Actor), TargetSlot: -1}
		if fe.TMs < 0 {
			fe.TMs = 0
		}
		if ev.Actor >= 0 {
			fe.Actor, fe.ActorTeam = nameAt(ev.Actor, ev.ServerTimeMs), teamOf(ev.Actor)
		}
		switch typ {
		case "kill", "teamkill", "revive", "medpack", "ammo":
			fe.TargetSlot = int(ev.Target)
			if ev.Target >= 0 {
				fe.Target, fe.TargetTeam = nameAt(ev.Target, ev.ServerTimeMs), teamOf(ev.Target)
			}
			if typ == "kill" || typ == "teamkill" {
				fe.Weapon = ModName(ev.MeansOfDeath)
				// the replay marks a kill with a HEADSHOT event whenever the victim carried the head-shot flag, even when something else
				// (a flamethrower, a grenade) finished them, so only weapons that can headshot count
				fe.Headshot = headshots[kill{ev.ServerTimeMs, ev.Actor, ev.Target}] && canHeadshotMod(ev.MeansOfDeath)
			}
		case "suicide":
			fe.Weapon = ModName(ev.MeansOfDeath)
		case "strike":
			fe.Weapon = map[int32]string{replay.StrikeAirstrike: "Airstrike", replay.StrikeArtillery: "Artillery"}[ev.Extra]
		}
		if ev.Origin != [3]float32{} {
			fe.Pos = &[3]int32{int32(ev.Origin[0]), int32(ev.Origin[1]), int32(ev.Origin[2])}
		}
		out = append(out, fe)
	}
	return out
}
