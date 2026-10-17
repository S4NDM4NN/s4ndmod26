package stats

// TopShotsMinAtts is the number of shots a player needs with a weapon to appear in topshots (the game's g_topshotsMin).
const TopShotsMinAtts = 10

// TopShot is one weapon's best (or worst) accuracy in a game.
type TopShot struct {
	Weapon string `json:"weapon"`
	Player string `json:"player"`
	Hits   int    `json:"hits"`
	Atts   int    `json:"atts"`
	Kills  int    `json:"kills"`
	Deaths int    `json:"deaths"`
}

// shotWeapons are the weapons topshots ranks (Knife through Artillery, bg_wstats.c); packs, the syringe and World have no
// accuracy worth ranking.
var shotWeapons = WeaponOrder[:15]

// minAttsFor is the game's G_WStats_TopMinAtts: the base minimum for most weapons, half for the sniper rifle and 30% for the
// explosives, never below 1.
func minAttsFor(weapon string, base int) int {
	min := base
	switch weapon {
	case "Sniper":
		min = base / 2
	case "Panzer", "Grenade", "Dynamite", "Airstrike", "Artillery":
		min = base * 3 / 10
	}
	if min < 1 {
		return 1
	}
	return min
}

// TopShots is, per weapon, the player with the best (best=true) or worst accuracy among those with enough shots; more shots
// break a tie.  Accuracy can pass 100% for explosives and flames, as in the game.
func TopShots(players []PlayerStats, best bool, base int) []TopShot {
	var out []TopShot
	for _, weapon := range shotWeapons {
		var pick *TopShot
		for _, p := range players {
			for _, w := range p.Weapons {
				if w.Name != weapon || w.Atts <= 0 || w.Atts < minAttsFor(weapon, base) {
					continue
				}
				cand := TopShot{Weapon: weapon, Player: p.Name, Hits: w.Hits, Atts: w.Atts, Kills: w.Kills, Deaths: w.Deaths}
				if pick == nil || better(cand, *pick, best) {
					c := cand
					pick = &c
				}
			}
		}
		if pick != nil {
			out = append(out, *pick)
		}
	}
	return out
}

func better(a, b TopShot, best bool) bool {
	x, y := a.Hits*10000/a.Atts, b.Hits*10000/b.Atts
	if x != y {
		if best {
			return x > y
		}
		return x < y
	}
	return a.Atts > b.Atts
}
