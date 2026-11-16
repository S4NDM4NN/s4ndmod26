package digest

import (
	"strings"
	"testing"
	"time"

	"s4ndmod26/status-api/stats"
)

func sample() Data {
	to := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	return Data{
		Username: `Ann <b>&"x"`, From: to.AddDate(0, 0, -7), To: to, BaseURL: "https://example.test/", UnsubURL: "https://example.test/u?t=1",
		This:  stats.WeekStats{Games: 4, Wins: 3, Losses: 1, Kills: 40, Deaths: 20, Headshots: 8, DamageGiven: 9000, PlayMs: 1_800_000, BestStreak: 6, TopClass: 1, TopClassMs: 900_000, TopWeapon: "MP40", TopWeaponKills: 25, TopWeaponHits: 90, TopWeaponAtts: 300},
		Prev:  stats.WeekStats{Games: 5, Wins: 2, Losses: 3, Kills: 20, Deaths: 20, Headshots: 8, DamageGiven: 9000, PlayMs: 1_800_000, BestStreak: 6},
		Plays: 1, Highlights: 2,
	}
}

func TestRender(t *testing.T) {
	d := sample()
	subject, html, text := Render(d)
	if subject != "Your S4NDMoD26 week: 4 games, K/D 2.00" {
		t.Errorf("subject = %q", subject)
	}
	if strings.Contains(html, `Ann <b>`) || !strings.Contains(html, "Ann &lt;b&gt;") {
		t.Error("the username is not escaped in the HTML")
	}
	for _, want := range []string{"MEDIC", "MP40", "30% ACCURACY", "WIN RATE", "75%", "https://example.test/player.html?u=Ann&#43;%3Cb%3E%26%22x%22", "https://example.test/u?t=1", "&#9650;", "&#9660;", "1 play of the game and 2 other highlights"} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML lacks %q", want)
		}
	}
	// the games went down (5 -> 4): a down arrow on the first cell, an up arrow on K/D (1.0 -> 2.0)
	if !strings.Contains(html, "4<span style=\"color:#ff7b73") {
		t.Error("matches played should show a down arrow")
	}
	for _, want := range []string{"Top class: Medic", "MP40 (25 kills, 30% accuracy)", "Unsubscribe: https://example.test/u?t=1", "(up from last week)"} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q", want)
		}
	}
	if strings.Contains(html, "<img") {
		t.Error("the email should load no images")
	}
}

func TestRenderFirstWeekAndQuietWeek(t *testing.T) {
	d := sample()
	d.Prev = stats.WeekStats{}
	d.Plays, d.Highlights = 0, 0
	d.This.TopClass = -1
	_, html, _ := Render(d)
	if strings.Contains(html, "&#9650;") || strings.Contains(html, "&#9660;") {
		t.Error("a first week has nothing to compare with: no arrows")
	}
	if strings.Contains(html, "HIGHLIGHTS") || strings.Contains(html, "TOP CLASS") {
		t.Error("sections without data should be left out")
	}
	if (Data{}).Eligible() {
		t.Error("a week with no games is not worth an email")
	}
	if !d.Eligible() {
		t.Error("a week with games is")
	}
}

func TestAccuracyNeedsShots(t *testing.T) {
	d := sample()
	d.This.TopWeaponHits, d.This.TopWeaponAtts = 3, 5 // a knife-like weapon: too few shots to show
	_, html, text := Render(d)
	if strings.Contains(html, "ACCURACY") || strings.Contains(text, "accuracy") {
		t.Error("accuracy shown for a weapon with almost no shots")
	}
}
