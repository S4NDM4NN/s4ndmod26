// Package stats ingests the per-game stats files the game server writes (g_statsFile) into Postgres and serves them back
// for the games pages.
package stats

import (
	"encoding/json"
	"fmt"
	"regexp"
	"time"
)

// Line is one line of a stats file: a player's numbers for one round, or up to the moment they left.
type Line struct {
	V              int               `json:"v"`
	Event          string            `json:"event"` // round_end | disconnect | map_end
	Match          string            `json:"match"`
	Map            string            `json:"map"`
	Round          int               `json:"round"`
	Slot           int               `json:"slot"`
	Name           string            `json:"name"`
	NameRaw        string            `json:"name_raw"` // with colour codes; empty in older lines
	Bot            bool              `json:"bot"`
	Team           string            `json:"team"` // axis | allies
	Class          int               `json:"class"`
	Kills          int               `json:"kills"`
	Deaths         int               `json:"deaths"`
	Suicides       int               `json:"suicides"`
	TeamKills      int               `json:"teamkills"`
	Gibs           int               `json:"gibs"`
	Revives        int               `json:"revives"`
	DamageGiven    int               `json:"damage_given"`
	DamageReceived int               `json:"damage_received"`
	TeamDamage     int               `json:"team_damage"`
	Score          int               `json:"score"`
	TeamGibs       int               `json:"team_gibs"`
	Goombas        int               `json:"goombas"`
	Poisons        int               `json:"poisons"`
	KnifeKills     int               `json:"knife_kills"`
	KillStreak     int               `json:"kill_streak"`  // best run of kills without dying
	DeathStreak    int               `json:"death_streak"` // worst run of deaths without a kill
	DoubleKills    int               `json:"double_kills"`
	TripleKills    int               `json:"triple_kills"`
	QuadKills      int               `json:"quad_kills"` // a quad kill and every kill after it in the same chain
	FirstBlood     int               `json:"first_blood"`
	FirstDeath     int               `json:"first_death"`
	PlayMs         int               `json:"play_ms"`      // time on a team
	LongestKill    *LongestKill      `json:"longest_kill"` // farthest hitscan kill of the round, null when none
	Victims        []Victim          `json:"victims"`
	Winner         string            `json:"winner"`  // round_end lines: axis | allies, empty when the round had no winner
	Weapons        map[string][5]int `json:"weapons"` // name -> hits, attempts, kills, deaths, headshots
}

// LongestKill is a player's farthest hitscan kill: the distance in game units, the weapon and the victim's name.
type LongestKill struct {
	Dist   int    `json:"dist"`
	Weapon string `json:"weapon"`
	Victim string `json:"victim"`
}

// Metres is the distance in metres (a game unit is an inch).
func (k *LongestKill) Metres() float64 { return float64(k.Dist) * 0.0254 }

// Victim is how many enemy kills a player had of one victim (by slot; the name is the victim's at the time).
type Victim struct {
	Slot int    `json:"slot"`
	Name string `json:"name"`
	N    int    `json:"n"`
}

// Weapons in the order the game prints them (bg_wstats.c); everything else a line carries is ignored.
var WeaponOrder = []string{
	"Knife", "Luger", "Colt", "MP-40", "Thompson", "Sten", "Sniper", "CrewGun", "Panzer", "Venom", "Flame",
	"Grenade", "Dynamite", "Airstrike", "Artillery", "Syringe", "Healthpacks", "Ammopacks", "World",
}

var validEvents = map[string]bool{"round_end": true, "disconnect": true, "map_end": true}

// ParseLine decodes one stats line and checks the few things the rest relies on.
func ParseLine(b []byte) (Line, error) {
	var l Line
	if err := json.Unmarshal(b, &l); err != nil {
		return Line{}, err
	}
	if l.V != 1 {
		return Line{}, fmt.Errorf("unknown stats line version %d", l.V)
	}
	if !validEvents[l.Event] {
		return Line{}, fmt.Errorf("unknown event %q", l.Event)
	}
	if l.Match == "" {
		return Line{}, fmt.Errorf("line has no match")
	}
	if l.Team != "axis" && l.Team != "allies" {
		return Line{}, fmt.Errorf("unknown team %q", l.Team)
	}
	if l.Round < 1 {
		l.Round = 1
	}
	if l.Winner != "axis" && l.Winner != "allies" {
		l.Winner = ""
	}
	return l, nil
}

var matchTime = regexp.MustCompile(`^replays_(\d{4})(\d{2})(\d{2})_(\d{2})(\d{2})(\d{2})$`)

// MatchStart is when the match started according to its name (the replay's file name: the server's clock, taken as UTC);
// ok is false for names that do not carry a time.
func MatchStart(match string) (t time.Time, ok bool) {
	m := matchTime.FindStringSubmatch(match)
	if m == nil {
		return time.Time{}, false
	}
	t, err := time.Parse("20060102150405", m[1]+m[2]+m[3]+m[4]+m[5]+m[6])
	return t, err == nil
}

// Efficiency as the game prints it: kills / (kills + deaths), truncated, 0 when there is nothing.
func Efficiency(kills, deaths int) int {
	if kills+deaths <= 0 {
		return 0
	}
	return 100 * kills / (kills + deaths)
}
