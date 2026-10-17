package stats

import (
	"fmt"
	"sort"
)

// Award is a one-line honour (or, in the shame list, dishonour) for a game: the title, who got it and the number behind it.
type Award struct {
	Key    string `json:"key"`
	Title  string `json:"title"`
	Player string `json:"player"`
	Value  string `json:"value"`
}

type awardDef struct {
	key, title string
	// score is the number that ranks players for this award and whether the player qualifies at all; format shows it.
	score  func(p PlayerStats) (float64, bool)
	format func(v float64) string
}

func count(unit string) func(float64) string {
	return func(v float64) string { return fmt.Sprintf("%d %s", int(v), unit) }
}

func percent(v float64) string { return fmt.Sprintf("%.1f%%", v) }

// bulletWeapons are the ones whose accuracy means "how well did they shoot" (no explosives, flames, packs or World).
var bulletWeapons = map[string]bool{"Luger": true, "Colt": true, "MP-40": true, "Thompson": true, "Sten": true, "Sniper": true, "CrewGun": true, "Venom": true}

func totalHeadshots(p PlayerStats) int {
	n := 0
	for _, w := range p.Weapons {
		n += w.Headshots
	}
	return n
}

func bulletAccuracy(p PlayerStats) (acc float64, shots int) {
	hits := 0
	for _, w := range p.Weapons {
		if bulletWeapons[w.Name] {
			hits += w.Hits
			shots += w.Atts
		}
	}
	if shots == 0 {
		return 0, 0
	}
	return float64(hits) * 100 / float64(shots), shots
}

var fameAwards = []awardDef{
	{"top_fragger", "Top fragger", func(p PlayerStats) (float64, bool) { return float64(p.Kills), p.Kills > 0 }, count("kills")},
	{"efficiency", "Best efficiency", func(p PlayerStats) (float64, bool) { return float64(p.Efficiency), p.Kills+p.Deaths >= 10 }, func(v float64) string { return fmt.Sprintf("%d%%", int(v)) }},
	{"kill_streak", "Longest killing streak", func(p PlayerStats) (float64, bool) { return float64(p.KillStreak), p.KillStreak >= 3 }, count("kills")},
	{"most_deadly", "Most damage", func(p PlayerStats) (float64, bool) { return float64(p.DamageGiven), p.DamageGiven > 0 }, count("damage")},
	{"sharpshooter", "Sharpshooter", func(p PlayerStats) (float64, bool) { a, n := bulletAccuracy(p); return a, n >= 30 }, percent},
	{"headshots", "Headshot hunter", func(p PlayerStats) (float64, bool) { n := totalHeadshots(p); return float64(n), n >= 3 }, count("headshots")},
	{"multi_kills", "Multi-kill master", func(p PlayerStats) (float64, bool) {
		n := p.DoubleKills + p.TripleKills + p.QuadKills
		return float64(n), n >= 1
	}, count("multi-kills")},
	{"medic", "Best medic", func(p PlayerStats) (float64, bool) { return float64(p.Revives), p.Revives >= 1 }, count("revives")},
	{"gibs", "Gib master", func(p PlayerStats) (float64, bool) { return float64(p.Gibs), p.Gibs >= 3 }, count("gibs")},
	{"objectives", "Objective hero", func(p PlayerStats) (float64, bool) { n := p.Objectives.Total(); return float64(n), n >= 1 }, count("objective events")},
	{"first_blood", "First blood", func(p PlayerStats) (float64, bool) { return float64(p.FirstBlood), p.FirstBlood >= 1 }, func(v float64) string {
		if v == 1 {
			return "1 round"
		}
		return fmt.Sprintf("%d rounds", int(v))
	}},
}

var shameAwards = []awardDef{
	{"deaths", "Most deaths", func(p PlayerStats) (float64, bool) { return float64(p.Deaths), p.Deaths >= 5 }, count("deaths")},
	{"suicides", "Most suicides", func(p PlayerStats) (float64, bool) { return float64(p.Suicides), p.Suicides >= 2 }, count("suicides")},
	{"teamkills", "Most team kills", func(p PlayerStats) (float64, bool) { return float64(p.TeamKills), p.TeamKills >= 1 }, count("team kills")},
	{"team_damage", "Most team damage", func(p PlayerStats) (float64, bool) { return float64(p.TeamDamage), p.TeamDamage >= 50 }, count("damage")},
	{"first_death", "First to die", func(p PlayerStats) (float64, bool) { return float64(p.FirstDeath), p.FirstDeath >= 1 }, func(v float64) string {
		if v == 1 {
			return "1 round"
		}
		return fmt.Sprintf("%d rounds", int(v))
	}},
	{"death_streak", "Longest dry spell", func(p PlayerStats) (float64, bool) { return float64(p.DeathStreak), p.DeathStreak >= 5 }, count("deaths in a row")},
}

// Awards picks, for each honour, the player with the highest number (those who qualify; ties go to the first by name so the
// result is stable), and the same for the shame list.  An award nobody qualifies for is left out.
func Awards(players []PlayerStats) (fame, shame []Award) {
	sorted := append([]PlayerStats(nil), players...)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a].Name < sorted[b].Name })
	pick := func(defs []awardDef) []Award {
		var out []Award
		for _, d := range defs {
			best, found := 0.0, -1
			for i, p := range sorted {
				v, ok := d.score(p)
				if ok && (found < 0 || v > best) {
					best, found = v, i
				}
			}
			if found >= 0 {
				out = append(out, Award{Key: d.key, Title: d.title, Player: sorted[found].Name, Value: d.format(best)})
			}
		}
		return out
	}
	return pick(fameAwards), pick(shameAwards)
}
