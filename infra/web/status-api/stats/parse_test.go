package stats

import (
	"testing"
	"time"
)

const sampleLine = `{"v":1,"event":"round_end","match":"replays_20261017_012254","map":"mp_beach","round":2,"slot":3,"name":"Ann","bot":false,"team":"allies","class":1,"kills":5,"deaths":2,"suicides":1,"teamkills":0,"gibs":1,"revives":3,"damage_given":900,"damage_received":400,"team_damage":10,"score":42,"weapons":{"MP-40":[40,100,4,1,2],"Syringe":[3,5,0,0,0]}}`

func TestParseLine(t *testing.T) {
	l, err := ParseLine([]byte(sampleLine))
	if err != nil {
		t.Fatal(err)
	}
	if l.Match != "replays_20261017_012254" || l.Round != 2 || l.Slot != 3 || l.Name != "Ann" || l.Bot || l.Team != "allies" {
		t.Errorf("fields: %+v", l)
	}
	if l.Kills != 5 || l.Deaths != 2 || l.Revives != 3 || l.DamageGiven != 900 || l.Score != 42 {
		t.Errorf("numbers: %+v", l)
	}
	if w := l.Weapons["MP-40"]; w != [5]int{40, 100, 4, 1, 2} {
		t.Errorf("MP-40 = %v", w)
	}
}

func TestParseLineRejects(t *testing.T) {
	for name, line := range map[string]string{
		"not json":      `{"v":1,`,
		"wrong version": `{"v":2,"event":"round_end","match":"m","team":"axis"}`,
		"unknown event": `{"v":1,"event":"boom","match":"m","team":"axis"}`,
		"no match":      `{"v":1,"event":"round_end","team":"axis"}`,
		"unknown team":  `{"v":1,"event":"round_end","match":"m","team":"spectators"}`,
	} {
		if _, err := ParseLine([]byte(line)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// a missing round means the first one
	if l, err := ParseLine([]byte(`{"v":1,"event":"map_end","match":"m","team":"axis"}`)); err != nil || l.Round != 1 {
		t.Errorf("round default: %+v %v", l, err)
	}
}

func TestMatchStart(t *testing.T) {
	got, ok := MatchStart("replays_20261017_012254")
	if !ok || !got.Equal(time.Date(2026, 10, 17, 1, 22, 54, 0, time.UTC)) {
		t.Errorf("got %v %v", got, ok)
	}
	if _, ok := MatchStart("stats_20261017"); ok {
		t.Error("a name without a time must not parse")
	}
	if _, ok := MatchStart("replays_20261399_999999"); ok {
		t.Error("an impossible time must not parse")
	}
}

func TestEfficiency(t *testing.T) {
	if Efficiency(10, 5) != 66 || Efficiency(3, 2) != 60 || Efficiency(2, 11) != 15 || Efficiency(0, 0) != 0 {
		t.Error("efficiency must truncate like the game (OSP dump rows)")
	}
}

func player(name string, ws ...WeaponStats) PlayerStats { return PlayerStats{Name: name, Weapons: ws} }

func TestTopShots(t *testing.T) {
	players := []PlayerStats{
		player("Ann", WeaponStats{Name: "Luger", Hits: 9, Atts: 20}, WeaponStats{Name: "MP-40", Hits: 30, Atts: 100}),
		player("Bob", WeaponStats{Name: "Luger", Hits: 5, Atts: 10}, WeaponStats{Name: "MP-40", Hits: 40, Atts: 100}),
		player("Cy", WeaponStats{Name: "Luger", Hits: 3, Atts: 9}, WeaponStats{Name: "Panzer", Hits: 2, Atts: 2},
			WeaponStats{Name: "Grenade", Hits: 6, Atts: 3}, WeaponStats{Name: "Sniper", Hits: 3, Atts: 5}),
	}
	best := TopShots(players, true, 10)
	got := map[string]string{}
	for _, b := range best {
		got[b.Weapon] = b.Player
	}
	if got["Luger"] != "Bob" || got["MP-40"] != "Bob" {
		t.Errorf("best Luger/MP-40 should be Bob (50%% / 40%%): %v", got)
	}
	if got["Grenade"] != "Cy" || got["Sniper"] != "Cy" {
		t.Errorf("explosives need 3 shots and the sniper 5: Cy qualifies for both: %v", got)
	}
	if _, ok := got["Panzer"]; ok {
		t.Error("2 panzer shots is below the 3 needed")
	}
	if best[0].Weapon != "Luger" {
		t.Errorf("rows follow the weapon order, first is %s", best[0].Weapon)
	}
	worst := TopShots(players, false, 10)
	for _, w := range worst {
		if w.Weapon == "Luger" && w.Player != "Ann" {
			t.Errorf("worst Luger is Ann (45%% vs 50%%), got %s (Cy has too few shots)", w.Player)
		}
	}
	if len(TopShots(players, true, 1000)) != 0 {
		t.Error("nobody qualifies with a high minimum")
	}
}
