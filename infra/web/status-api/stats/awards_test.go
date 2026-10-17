package stats

import "testing"

func TestAwards(t *testing.T) {
	ann := PlayerStats{Name: "Ann", Kills: 12, Deaths: 3, Efficiency: 80, KillStreak: 6, DamageGiven: 1500, Revives: 0, Gibs: 4,
		Weapons: []WeaponStats{{Name: "MP-40", Hits: 40, Atts: 80, Headshots: 5}, {Name: "Grenade", Hits: 9, Atts: 3}}}
	bob := PlayerStats{Name: "Bob", Kills: 12, Deaths: 12, Efficiency: 50, KillStreak: 2, DamageGiven: 900, Revives: 7, Suicides: 3, TeamKills: 1, TeamDamage: 60,
		DoubleKills: 2, FirstBlood: 1, Weapons: []WeaponStats{{Name: "Sniper", Hits: 9, Atts: 10, Headshots: 1}}}
	cy := PlayerStats{Name: "Cy", FirstDeath: 1, Objectives: &ObjectiveCounts{Plants: 2, Defuses: 1}, Kills: 1, Deaths: 2, Efficiency: 33, Weapons: []WeaponStats{{Name: "Luger", Hits: 3, Atts: 5}}}
	fame, shame := Awards([]PlayerStats{cy, bob, ann})

	got := map[string]Award{}
	for _, a := range fame {
		got[a.Key] = a
	}
	for _, a := range shame {
		got[a.Key] = a
	}
	check := func(key, player, value string) {
		a, ok := got[key]
		if !ok {
			t.Errorf("%s: no award, want %s %s", key, player, value)
			return
		}
		if a.Player != player || (value != "" && a.Value != value) {
			t.Errorf("%s: %s (%s), want %s (%s)", key, a.Player, a.Value, player, value)
		}
	}
	check("top_fragger", "Ann", "12 kills") // tied with Bob: the first by name wins, so the result never depends on input order
	check("efficiency", "Ann", "80%")       // Cy has too few games to qualify and Bob is lower
	check("kill_streak", "Ann", "6 kills")
	check("most_deadly", "Ann", "1500 damage")
	check("medic", "Bob", "7 revives")
	check("multi_kills", "Bob", "2 multi-kills")
	check("first_blood", "Bob", "")
	check("gibs", "Ann", "4 gibs")
	check("objectives", "Cy", "3 objective events")
	check("headshots", "Ann", "5 headshots") // Bob has 1: under the 3 needed anyway
	// sharpshooter: only bullet weapons count (Ann's grenades do not), 30 shots needed: Ann 80 shots at 50%, Bob 10 shots does not qualify
	check("sharpshooter", "Ann", "50.0%")
	check("deaths", "Bob", "12 deaths")
	check("first_death", "Cy", "1 round")
	check("suicides", "Bob", "3 suicides")
	check("teamkills", "Bob", "1 team kills")
	check("team_damage", "Bob", "60 damage")
	if _, ok := got["death_streak"]; ok {
		t.Error("nobody had a dry spell of 5")
	}

	again, _ := Awards([]PlayerStats{ann, bob, cy})
	for i := range fame {
		if again[i] != fame[i] {
			t.Errorf("input order changed the result: %+v vs %+v", again[i], fame[i])
		}
	}
	if f, s := Awards(nil); len(f) != 0 || len(s) != 0 {
		t.Error("no players, no awards")
	}
}
