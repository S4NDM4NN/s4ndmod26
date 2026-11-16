// digestpreview renders the weekly email for made-up numbers into digest.html and digest.txt in the current folder, to look at the design
// without a database or a mail service.  Usage: digestpreview [username]
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
	subject, html, text := digest.Render(d)
	os.WriteFile("digest.html", []byte(html), 0o644)
	os.WriteFile("digest.txt", []byte(text), 0o644)
	fmt.Println("subject:", subject)
}
