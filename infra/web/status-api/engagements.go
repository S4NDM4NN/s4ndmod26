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
	Flag  bool   `json:"flag,omitempty"` // had the objective in hand at some point during the fight
	// Health is their HP through the fight as [ms since the game began, HP] points, each holding until the next
	Health [][2]int32 `json:"health"`
}

type fightKill struct {
	TimeMs int32  `json:"t"` // ms since the game began
	Killer string `json:"killer"`
	Victim string `json:"victim"`
	Weapon string `json:"weapon"`
	KSlot  int32  `json:"kslot"`
	VSlot  int32  `json:"vslot"`
}

type fightRevive struct {
	TimeMs  int32  `json:"t"`
	Medic   string `json:"medic"`
	RSlot   int32  `json:"rslot"` // the revived player
	Revived string `json:"revived"`
}

// fight is an engagement as the stats page and the 3D view show it.
type fight struct {
	Index          int           `json:"index"`
	StartMs        int32         `json:"start_ms"` // ms since the game began
	EndMs          int32         `json:"end_ms"`
	Classification string        `json:"classification"`
	Damage         int32         `json:"damage"`
	Hits           int           `json:"hits"`
	Winner         string        `json:"winner"`    // the side with more kills; empty when even
	Advantage      string        `json:"advantage"` // axis | allies when one side had clearly more HP going in, else empty
	Effective      string        `json:"effective"` // the fight as if everyone were at full health: "3v1" for 4v2 at 300 HP to 120
	Sides          []fightSide   `json:"sides"`
	Kills          []fightKill   `json:"kills"`
	Revives        []fightRevive `json:"revives"`
}

func teamWord(t int32) string {
	if t == 1 {
		return "axis"
	}
	return "allies"
}

// carriedDuring is whether slot held the objective at any time in [from, to] (a carry with no end is still going).
func carriedDuring(carries []replay.FlagCarry, slot, from, to int32) bool {
	for _, c := range carries {
		if c.CarrierClientNum == slot && c.StartMs <= to && (c.EndMs == 0 || c.EndMs >= from) {
			return true
		}
	}
	return false
}

// viewCarry is an objective carry for the 3D view.
type viewCarry struct {
	Slot    int32 `json:"slot"`
	StartMs int32 `json:"start_ms"`
	EndMs   int32 `json:"end_ms"` // 0 = until the end of the recording
}

func viewCarries(carries []replay.FlagCarry) []viewCarry {
	out := []viewCarry{}
	for _, c := range carries {
		out = append(out, viewCarry{Slot: c.CarrierClientNum, StartMs: c.StartMs, EndMs: c.EndMs})
	}
	return out
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
			Damage: e.TotalDamage, Hits: e.HitCount, Sides: []fightSide{}, Kills: []fightKill{}, Revives: []fightRevive{}}
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
			series := [][2]int32{}
			if len(p.Health) > 0 {
				for _, h := range p.Health {
					series = append(series, [2]int32{h.TimeMs - start, h.Health})
				}
			} else if pi := a.Players[itoa(p.ClientNum)]; pi != nil { // older analyses: the player's one-second health samples
				for _, h := range pi.HealthSamples {
					if h.TimeMs >= e.StartMs-1000 && h.TimeMs <= e.EndMs+1000 {
						series = append(series, [2]int32{h.TimeMs - start, h.Health})
					}
				}
			}
			s.Players = append(s.Players, fightPlayer{Flag: carriedDuring(a.FlagCarries, p.ClientNum, e.StartMs, e.EndMs), Health: series, Slot: p.ClientNum, Name: nameAtTime(a.Players[itoa(p.ClientNum)], e.StartMs), Dealt: p.DamageDealt, Taken: p.DamageTaken})
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
		for _, rv := range e.Revives {
			f.Revives = append(f.Revives, fightRevive{TimeMs: rv.TimeMs - start, RSlot: rv.Revived, Medic: nameAtTime(a.Players[itoa(rv.Medic)], rv.TimeMs), Revived: nameAtTime(a.Players[itoa(rv.Revived)], rv.TimeMs)})
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

// fightViewResponse is what the 3D viewer needs to replay one fight: the participants' paths for the fight and five seconds either
// side, and the shots they fired.
type fightViewResponse struct {
	*replay.Paths
	*replay.Entities
	Players      []pathsPlayer `json:"players"`
	Shots        []replay.Shot `json:"shots"`
	Carries      []viewCarry   `json:"carries"`
	Index        int           `json:"fight"`
	FightStartMs int32         `json:"fight_start_ms"`
	FightEndMs   int32         `json:"fight_end_ms"`
	GameStartMs  int32         `json:"game_start_ms"` // when the game began, for showing times as m:ss into it
}

const fightViewPadMs = 5000

// gameFightViewHandler serves GET /api/games/{match}/fights/{n}/view.
func gameFightViewHandler(replayDir string) http.HandlerFunc {
	cache := &pathsCache{}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		fail := func(code int, msg string) {
			w.WriteHeader(code)
			json.NewEncoder(w).Encode(map[string]string{"error": msg})
		}
		match := r.PathValue("match")
		n, err := strconv.Atoi(r.PathValue("n"))
		if !matchNameRe.MatchString(match) || err != nil || n < 0 {
			fail(http.StatusBadRequest, "bad game or fight")
			return
		}
		key := match + "/" + r.PathValue("n")
		body, ok := cache.get(key)
		if !ok {
			ab, err := os.ReadFile(filepath.Join(replayDir, match+".json"))
			if err != nil {
				fail(http.StatusNotFound, "no analysis for that game")
				return
			}
			var a replay.Analysis
			if err := json.Unmarshal(ab, &a); err != nil {
				fail(http.StatusInternalServerError, "could not read the analysis")
				return
			}
			if n >= len(a.Engagements) {
				fail(http.StatusNotFound, "no such fight")
				return
			}
			e := a.Engagements[n]
			rep, err := replay.Parse(filepath.Join(replayDir, match+".rpl"))
			if err != nil {
				if os.IsNotExist(err) {
					fail(http.StatusNotFound, "no replay for that game")
				} else {
					log.Printf("fight view %s: %v", key, err)
					fail(http.StatusInternalServerError, "could not read the replay")
				}
				return
			}
			slots := map[int32]bool{}
			for _, p := range e.Participants {
				slots[p.ClientNum] = true
			}
			opt := replay.PathOptions{StepMs: 100, FromMs: e.StartMs - fightViewPadMs, ToMs: e.EndMs + fightViewPadMs, Slots: slots}
			paths := replay.BuildPathsWith(rep, opt)
			paths.StartMs, paths.EndMs = opt.FromMs, opt.ToMs
			resp := fightViewResponse{Paths: paths, Entities: replay.BuildEntities(rep, replay.PathOptions{FromMs: opt.FromMs, ToMs: opt.ToMs}), Shots: replay.BuildShots(rep, opt), Carries: viewCarries(a.FlagCarries), Index: n, FightStartMs: e.StartMs, FightEndMs: e.EndMs, GameStartMs: a.Meta.StartTimeMs}
			for _, pp := range paths.Players {
				resp.Players = append(resp.Players, pathsPlayer{PlayerPath: pp, Name: nameAtTime(a.Players[itoa(int32(pp.Slot))], e.StartMs)})
			}
			if resp.Players == nil {
				resp.Players = []pathsPlayer{}
			}
			body, err = json.Marshal(resp)
			if err != nil {
				fail(http.StatusInternalServerError, "could not build the view")
				return
			}
			cache.put(key, body)
		}
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.Write(body)
	}
}
