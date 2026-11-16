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
		This:  stats.WeekStats{Games: 4, Wins: 3, Losses: 1, Kills: 40, Deaths: 20, Headshots: 8, DamageGiven: 9000, PlayMs: 1_800_000, BestStreak: 6, TopClass: 1, TopClassMs: 900_000, TopWeapon: "MP40", TopWeaponKills: 25, TopWeaponHits: 90, TopWeaponAtts: 300, Revives: 5},
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
	if strings.Count(html, "<img") != 1 || !strings.Contains(html, `<img src="https://example.test/img/s4nd-logo-email.png"`) || !strings.Contains(html, `>S4NDMoD<span style="color:#ffffff;">26</span></div>`) {
		t.Error("the only image should be the logo from the site, with the name as text under it")
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
	d.This.TopWeaponHits, d.This.TopWeaponAtts = 3, 4 // a knife-like weapon: too few shots to show (the least is 5)
	_, html, text := Render(d)
	if strings.Contains(html, "ACCURACY") || strings.Contains(text, "accuracy") {
		t.Error("accuracy shown for a weapon with almost no shots")
	}
}

func TestRecordHasNoDraws(t *testing.T) {
	d := sample()
	d.This.Draws = 2
	_, html, text := Render(d)
	if strings.Contains(html, "drawn") || strings.Contains(text, "drawn") {
		t.Error("the record shows draws")
	}
	if !strings.Contains(html, "3 won · 1 lost") {
		t.Error("the record lacks wins and losses")
	}
}

func TestTotalsWithPerMatchRates(t *testing.T) {
	d := sample() // 4 matches
	d.This.Kills, d.This.Headshots, d.This.Revives, d.This.Medpacks, d.This.AmmoPacks, d.This.FeedGames = 40, 12, 0, 6, 0, 3
	d.Prev.Headshots, d.Prev.Games = 20, 5
	_, html, text := Render(d)
	for _, want := range []string{">KILLS<", "10.0 per match", ">HEADSHOTS<", "3.0 per match", ">MEDPACKS GIVEN<", "2.0 per match"} { // 40/4, 12/4, 6 over the 3 with a feed
		if !strings.Contains(html, want) && !strings.Contains(html, strings.Replace(want, ">", "", 1)) {
			t.Errorf("HTML lacks %q", want)
		}
	}
	if !strings.Contains(html, ">12<") && !strings.Contains(html, "12</div>") && !strings.Contains(html, "12<span") {
		t.Error("the headshot total (12) should be the big number")
	}
	for _, not := range []string{"REVIVES", "AMMO PACKS GIVEN", "HEADSHOT %", "/ GAME"} { // none this week: left out; no old labels
		if strings.Contains(html, ">"+not+"<") || strings.Contains(html, not+" /") || (not == "HEADSHOT %" && strings.Contains(html, not)) || (not == "/ GAME" && strings.Contains(html, not)) {
			t.Errorf("HTML has %q", not)
		}
	}
	if strings.Contains(text, "&#") {
		t.Errorf("markup leaked into the plain text: %q", text)
	}
	if !strings.Contains(text, "Headshots") || !strings.Contains(text, "3.0 per match") || strings.Contains(text, "Revives") {
		t.Errorf("the plain text: %q", text)
	}
}

func TestPerMatchRateArrowComparesWeeksOfDifferentSize(t *testing.T) {
	d := sample()
	// 100 matches and 10 matches at the same rate of 2 head hits a match: no arrow on the rate, whatever the totals
	d.This.Games, d.This.Headshots = 100, 200
	d.Prev.Games, d.Prev.Headshots = 10, 20
	_, html, _ := Render(d)
	i := strings.Index(html, "2.0 per match")
	if i < 0 {
		t.Fatal("no headshot rate")
	}
	rest := html[i:]
	rest = rest[:strings.Index(rest, "</div>")]
	if strings.Contains(rest, "&#9650;") || strings.Contains(rest, "&#9660;") {
		t.Errorf("the same rate in a bigger week should show no arrow: %s", rest)
	}
	d.This.Headshots = 300 // now 3 a match against 2
	_, html, _ = Render(d)
	i = strings.Index(html, "3.0 per match")
	rest = html[i:]
	if !strings.Contains(rest[:strings.Index(rest, "</div>")], "&#9650;") {
		t.Error("a higher rate should show an up arrow, on the rate")
	}
}

func TestObjectivesBlock(t *testing.T) {
	d := sample()
	_, html, _ := Render(d)
	if strings.Contains(html, "OBJECTIVES") {
		t.Error("no objective events: no objectives block")
	}
	d.This.ObjTaken, d.This.Planted = 6, 3
	_, html, text := Render(d)
	for _, want := range []string{"OBJECTIVES</div>", ">TAKEN<", "DYNAMITE PLANTED"} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML lacks %q", want)
		}
	}
	if strings.Contains(html, ">RETURNED<") || strings.Contains(html, "DYNAMITE DEFUSED") {
		t.Error("zero objective counts should be left out")
	}
	if !strings.Contains(text, "Objectives: taken 6, dynamite planted 3") {
		t.Errorf("text: %q", text)
	}
}

func TestGridRowsAreWhole(t *testing.T) {
	// an odd number of cells is padded: every <tr> is closed
	for _, mod := range []func(*Data){func(d *Data) {}, func(d *Data) { d.This.Revives = 0 }, func(d *Data) { d.This.Revives, d.This.Medpacks = 0, 0 }} {
		d := sample()
		mod(&d)
		_, html, _ := Render(d)
		if strings.Count(html, "<tr") != strings.Count(html, "</tr>") {
			t.Errorf("unbalanced rows: %d open, %d closed", strings.Count(html, "<tr"), strings.Count(html, "</tr>"))
		}
	}
}

func TestClosingLine(t *testing.T) {
	_, html, _ := Render(sample())
	if !strings.Contains(html, "a wrap for this week") || !strings.Contains(html, "See you on the server.") || strings.Contains(html, "showed up") {
		t.Error("the closing line is the new one")
	}
}

func TestNoImpactBold(t *testing.T) {
	_, html, _ := Render(sample())
	if strings.Contains(html, "bold 3") && strings.Contains(html, "Impact") && strings.Contains(html, "font:bold 30px Impact") {
		t.Error("Impact must not be bold: mail apps fake an even heavier bold")
	}
	if strings.Contains(html, "Impact") {
		t.Error("no Impact at all now: the logo is an image and the headings are a bold sans")
	}
}

func TestTimePlayedAndKillsPerGame(t *testing.T) {
	d := sample()              // 4 games, 40 kills, 1,800,000 ms (30 min) played; last week 5 games, 20 kills, 30 min
	d.This.PlayMs = 20_400_000 // 5h 40m
	_, html, text := Render(d)
	for _, want := range []string{"TIME PLAYED", "5h 40m", ">KILLS<", "10.0 per match"} { // 40 kills over 4 matches
		if !strings.Contains(html, strings.TrimPrefix(want, ">")) {
			t.Errorf("HTML lacks %q", want)
		}
	}
	if !strings.Contains(text, "Time Played") || !strings.Contains(text, "Kills") {
		t.Error("the plain text lacks time played or kills")
	}
	d.This.Kills = 0
	_, html, _ = Render(d)
	if strings.Contains(html, ">KILLS<") {
		t.Error("no kills this week: left out like the other per-game rates")
	}
	d.This.PlayMs = 30_000 // under a minute: nothing worth saying
	_, html, _ = Render(d)
	if strings.Contains(html, "TIME PLAYED") {
		t.Error("under a minute played is not shown")
	}
}
