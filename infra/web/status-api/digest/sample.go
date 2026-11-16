package digest

import (
	"time"

	"s4ndmod26/status-api/stats"
)

// Sample is a made-up week for the preview tool and for the admin's test mail when there is no real week to show.  variant "" is a
// good week, "first" a short first week (nothing to compare with), "rough" a worse week than the one before.
func Sample(username, variant string, to time.Time) Data {
	d := Data{
		Username: username, From: to.AddDate(0, 0, -7), To: to,
		This: stats.WeekStats{Games: 18, Wins: 10, Losses: 7, Kills: 214, Deaths: 131, Headshots: 36, Medpacks: 12, AmmoPacks: 0, FeedGames: 16, ObjTaken: 9, ObjReturned: 4, ObjCompleted: 3, Planted: 5, Defused: 2, Revives: 24, DamageGiven: 61400, PlayMs: 15_120_000,
			BestStreak: 11, Longest: &stats.LongestKill{Dist: 3100, Weapon: "Mauser"}, TopClass: 1, TopClassMs: 3_300_000, TopWeapon: "MP40", TopWeaponKills: 96, TopWeaponHits: 1480, TopWeaponAtts: 5100},
		Prev: stats.WeekStats{Games: 15, Wins: 8, Losses: 7, Kills: 160, Deaths: 140, Headshots: 33, Medpacks: 18, FeedGames: 13, Revives: 18, DamageGiven: 52000, PlayMs: 12_600_000,
			BestStreak: 11, Longest: &stats.LongestKill{Dist: 3400, Weapon: "Mauser"}, TopClass: 1},
		Plays: 2, Highlights: 3,
	}
	switch variant {
	case "first":
		d.Prev = stats.WeekStats{}
		d.Plays, d.Highlights = 0, 0
		d.This = stats.WeekStats{Games: 3, Wins: 1, Losses: 2, Kills: 19, Deaths: 22, Headshots: 2, DamageGiven: 3900, PlayMs: 1_260_000,
			BestStreak: 4, Longest: &stats.LongestKill{Dist: 1500, Weapon: "Luger"}, TopClass: 2, TopClassMs: 780_000, TopWeapon: "Thompson", TopWeaponKills: 9, TopWeaponHits: 6, TopWeaponAtts: 14}
	case "rough":
		d.Plays, d.Highlights = 0, 1
		d.Prev, d.This = d.This, stats.WeekStats{Games: 6, Wins: 2, Losses: 4, Kills: 61, Deaths: 88, Headshots: 5, Revives: 3, DamageGiven: 16800, PlayMs: 1_980_000,
			BestStreak: 5, Longest: &stats.LongestKill{Dist: 1900, Weapon: "Mauser"}, TopClass: 3, TopClassMs: 1_500_000, TopWeapon: "MP40", TopWeaponKills: 31, TopWeaponHits: 410, TopWeaponAtts: 1900}
	}
	return d
}
