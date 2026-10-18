package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"s4ndmod26/status-api/replay"
	"s4ndmod26/status-api/stats"
)

// fightSide is one team's players in a fight: what each dealt to and took from the enemy.
type fightSide struct {
	Team    string        `json:"team"` // axis | allies
	Players []fightPlayer `json:"players"`
	Damage  int32         `json:"damage"` // dealt to the other side
	HP      int32         `json:"hp"`     // total HP the side's players had when they entered the fight
	Kills   int           `json:"kills"`
}

type fightPlayer struct {
	Slot  int32  `json:"slot"`
	Name  string `json:"name"`
	HP    int32  `json:"hp"` // HP when they entered the fight
	Dealt int32  `json:"dealt"`
	Taken int32  `json:"taken"`
	Kills int    `json:"kills"`
	Died  bool   `json:"died"`
}

type fightKill struct {
	TimeMs int32  `json:"t"` // ms since the game began
	Killer string `json:"killer"`
	Victim string `json:"victim"`
	Weapon string `json:"weapon"`
	KSlot  int32  `json:"kslot"`
	VSlot  int32  `json:"vslot"`
}

// fight is an engagement as the stats page and the 3D view show it.
type fight struct {
	Index          int         `json:"index"`
	StartMs        int32       `json:"start_ms"` // ms since the game began
	EndMs          int32       `json:"end_ms"`
	Classification string      `json:"classification"`
	Damage         int32       `json:"damage"`
	Hits           int         `json:"hits"`
	Winner         string      `json:"winner"`    // the side with more kills; empty when even
	Advantage      string      `json:"advantage"` // axis | allies when one side had clearly more HP going in, else empty
	Effective      string      `json:"effective"` // the fight as if everyone were at full health: "3v1" for 4v2 at 300 HP to 120
	Sides          []fightSide `json:"sides"`
	Kills          []fightKill `json:"kills"`
}

func teamWord(t int32) string {
	if t == 1 {
		return "axis"
	}
	return "allies"
}

// nameAtTime is who held slot at time t (ms of the recording): the name history span covering it, else the player's last name.
func nameAtTime(p *replay.PlayerInfo, t int32) string {
	if p == nil {
		return "Player"
	}
	for _, h := range p.NameHistory {
		if t >= h.FromMs && (h.ToMs < 0 || t < h.ToMs) {
			return h.Name
		}
	}
	return p.DisplayName
}

// buildFights turns an analysis' engagements into fights.  Analyses made before engagements carried their own hits have no Hits;
// they still list participants, just without kills.
func buildFights(a *replay.Analysis) []fight {
	start := a.Meta.StartTimeMs
	out := []fight{}
	for _, e := range a.Engagements {
		f := fight{Index: e.Index, StartMs: e.StartMs - start, EndMs: e.EndMs - start, Classification: e.Classification,
			Damage: e.TotalDamage, Hits: e.HitCount, Sides: []fightSide{}, Kills: []fightKill{}}
		sides := map[int32]*fightSide{}
		for _, p := range e.Participants {
			if p.Team != 1 && p.Team != 2 {
				continue
			}
			s := sides[p.Team]
			if s == nil {
				s = &fightSide{Team: teamWord(p.Team), Players: []fightPlayer{}}
				sides[p.Team] = s
			}
			s.Players = append(s.Players, fightPlayer{Slot: p.ClientNum, Name: nameAtTime(a.Players[itoa(p.ClientNum)], e.StartMs), Dealt: p.DamageDealt, Taken: p.DamageTaken})
			s.Damage += p.DamageDealt
			hp := p.StartHealth
			if hp <= 0 {
				hp = 100 // unknown: assume a fresh player
			}
			s.HP += hp
			s.Players[len(s.Players)-1].HP = hp
		}
		teamOf := map[int32]int32{}
		for _, p := range e.Participants {
			teamOf[p.ClientNum] = p.Team
		}
		for _, k := range e.Kills {
			f.Kills = append(f.Kills, fightKill{TimeMs: k.TimeMs - start, KSlot: k.Killer, VSlot: k.Victim, Weapon: stats.ModName(k.Mod),
				Killer: nameAtTime(a.Players[itoa(k.Killer)], k.TimeMs), Victim: nameAtTime(a.Players[itoa(k.Victim)], k.TimeMs)})
			if s := sides[teamOf[k.Killer]]; s != nil {
				s.Kills++
			}
		}
		// each player's kills and whether they died in the fight
		for _, s := range sides {
			for i := range s.Players {
				for _, k := range e.Kills {
					if k.Killer == s.Players[i].Slot {
						s.Players[i].Kills++
					}
					if k.Victim == s.Players[i].Slot {
						s.Players[i].Died = true
					}
				}
			}
			sort.SliceStable(s.Players, func(i, j int) bool { return s.Players[i].Dealt > s.Players[j].Dealt })
		}
		for _, t := range []int32{1, 2} {
			if s := sides[t]; s != nil {
				f.Sides = append(f.Sides, *s)
			}
		}
		if len(f.Sides) == 2 {
			switch {
			case f.Sides[0].Kills > f.Sides[1].Kills:
				f.Winner = f.Sides[0].Team
			case f.Sides[1].Kills > f.Sides[0].Kills:
				f.Winner = f.Sides[1].Team
			}
		}
		out = append(out, f)
	}
	return out
}

func itoa(n int32) string { return strconv.Itoa(int(n)) }

// gameFightsHandler serves GET /api/games/{match}/fights from the game's analysis file.
func gameFightsHandler(replayDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		fail := func(code int, msg string) {
			w.WriteHeader(code)
			json.NewEncoder(w).Encode(map[string]string{"error": msg})
		}
		match := r.PathValue("match")
		if !matchNameRe.MatchString(match) {
			fail(http.StatusBadRequest, "bad game name")
			return
		}
		b, err := os.ReadFile(filepath.Join(replayDir, match+".json"))
		if err != nil {
			fail(http.StatusNotFound, "no analysis for that game")
			return
		}
		var a replay.Analysis
		if err := json.Unmarshal(b, &a); err != nil {
			log.Printf("fights %s: %v", match, err)
			fail(http.StatusInternalServerError, "could not read the analysis")
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=300")
		json.NewEncoder(w).Encode(buildFights(&a))
	}
}
