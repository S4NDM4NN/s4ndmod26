// digestpreview renders the weekly email for made-up numbers into digest.html and digest.txt in the current folder, to look at the design
// without a database or a mail service.  Usage: digestpreview [username [first|rough]]
package main

import (
	"fmt"
	"os"
	"time"

	"s4ndmod26/status-api/digest"
	"s4ndmod26/status-api/stats"
)

func main() {
	name := "S4NDM4NN"
	if len(os.Args) > 1 {
		name = os.Args[1]
	}
	to := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	d := digest.Data{
		Username: name, From: to.AddDate(0, 0, -7), To: to,
		This: stats.WeekStats{Games: 18, Wins: 10, Losses: 7, Kills: 214, Deaths: 131, Headshots: 36, Revives: 14, DamageGiven: 61400, PlayMs: 5_640_000,
			BestStreak: 11, Longest: &stats.LongestKill{Dist: 3100, Weapon: "Mauser"}, TopClass: 1, TopClassMs: 3_300_000, TopWeapon: "MP40", TopWeaponKills: 96, TopWeaponHits: 1480, TopWeaponAtts: 5100},
		Prev: stats.WeekStats{Games: 15, Wins: 8, Losses: 7, Kills: 160, Deaths: 140, Headshots: 33, Revives: 18, DamageGiven: 52000, PlayMs: 4_900_000,
			BestStreak: 11, Longest: &stats.LongestKill{Dist: 3400, Weapon: "Mauser"}, TopClass: 1},
		Plays: 2, Highlights: 3,
		BaseURL: "https://s4ndmod.com", UnsubURL: "https://s4ndmod.com/api/account/digest/unsubscribe?t=example",
	}
	// a second argument picks another made-up week: "first" (a short first week: nothing to compare with, no highlights, no weapon
	// accuracy worth showing) or "rough" (a worse week than the one before: down arrows, a lieutenant)
	if len(os.Args) > 2 {
		switch os.Args[2] {
		case "first":
			d.Prev = stats.WeekStats{}
			d.Plays, d.Highlights = 0, 0
			d.This = stats.WeekStats{Games: 3, Wins: 1, Losses: 2, Kills: 19, Deaths: 22, Headshots: 2, Revives: 0, DamageGiven: 3900, PlayMs: 1_260_000,
				BestStreak: 4, Longest: &stats.LongestKill{Dist: 1500, Weapon: "Luger"}, TopClass: 2, TopClassMs: 780_000, TopWeapon: "Thompson", TopWeaponKills: 9, TopWeaponHits: 6, TopWeaponAtts: 14}
		case "rough":
			d.Plays, d.Highlights = 0, 1
			d.Prev, d.This = d.This, stats.WeekStats{Games: 6, Wins: 2, Losses: 4, Kills: 61, Deaths: 88, Headshots: 5, Revives: 3, DamageGiven: 16800, PlayMs: 1_980_000,
				BestStreak: 5, Longest: &stats.LongestKill{Dist: 1900, Weapon: "Mauser"}, TopClass: 3, TopClassMs: 1_500_000, TopWeapon: "MP40", TopWeaponKills: 31, TopWeaponHits: 410, TopWeaponAtts: 1900}
			d.Prev.TopClass = 1
		}
	}
	subject, html, text := digest.Render(d)
	os.WriteFile("digest.html", []byte(html), 0o644)
	os.WriteFile("digest.txt", []byte(text), 0o644)
	fmt.Println("subject:", subject)
}
