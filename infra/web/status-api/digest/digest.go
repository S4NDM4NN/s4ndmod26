// Package digest builds the weekly performance email: what a player did in the last week, set against the week before.  It only renders;
// choosing who gets it and sending it live elsewhere.  The mail is table-based HTML with inline styles (that is all mail clients agree on),
// on a dark card in the site's colours, with a plain-text twin, and uses no images so nothing has to load.
package digest

import (
	"bytes"
	"fmt"
	"html/template"
	"math"
	"net/url"
	"strings"
	"time"

	"s4ndmod26/status-api/stats"
)

// Data is what one email is made from.
type Data struct {
	Username    string
	From, To    time.Time // the week: From is its first day, To the first day after it
	This, Prev  stats.WeekStats
	Plays       int    // the player's plays of the game in the week
	Highlights  int    // and their other highlights
	BaseURL     string // the site, no trailing slash
	UnsubURL    string // one click, no login
	SettingsURL string // defaults to the account page
}

// Eligible says whether the week is worth an email: a quiet week sends nothing.
func (d Data) Eligible() bool { return d.This.Games > 0 }

// Subject is the mail's subject line.
func (d Data) Subject() string {
	return fmt.Sprintf("Your S4NDMoD26 week: %s, K/D %s", plural(d.This.Games, "game"), kd(d.This))
}

func plural(n int, one string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %ss", n, one)
}

func plays(n int) string {
	if n == 1 {
		return "1 play of the game"
	}
	return fmt.Sprintf("%d plays of the game", n)
}

func kd(w stats.WeekStats) string {
	if w.Deaths == 0 {
		return fmt.Sprintf("%d", w.Kills)
	}
	return fmt.Sprintf("%.2f", float64(w.Kills)/float64(w.Deaths))
}

func pct(n, of int) (float64, bool) {
	if of <= 0 {
		return 0, false
	}
	return 100 * float64(n) / float64(of), true
}

func duration(ms int) string {
	m := (ms + 30000) / 60000
	if m >= 60 {
		return fmt.Sprintf("%dh %02dm", m/60, m%60)
	}
	return fmt.Sprintf("%d min", m)
}

func day(t time.Time) string { return t.Format("Jan 2") }

// one cell of the stats grid
type cell struct {
	Value, Label string
	Arrow        template.HTML
}

const (
	cUp   = "#5fcf7a"
	cDown = "#ff7b73"
)

// arrow compares this week with the last: nothing on a first week or when they are the same (differences under 1% are not worth an arrow).
func arrow(this, prev float64, ok bool) template.HTML {
	if !ok || math.Abs(this-prev) < 0.005 { // no earlier week to compare with, or no difference
		return ""
	}
	if this > prev {
		return template.HTML(`<span style="color:` + cUp + `;font-size:11px"> &#9650;</span>`)
	}
	return template.HTML(`<span style="color:` + cDown + `;font-size:11px"> &#9660;</span>`)
}

func (d Data) cells() []cell {
	t, p := d.This, d.Prev
	has := p.Games > 0
	var out []cell
	add := func(value, label string, this, prev float64, ok bool) {
		out = append(out, cell{value, label, arrow(this, prev, has && ok)})
	}
	add(fmt.Sprintf("%d", t.Games), "MATCHES PLAYED", float64(t.Games), float64(p.Games), true)
	wr, wok := pct(t.Wins, t.Wins+t.Losses)
	pwr, pok := pct(p.Wins, p.Wins+p.Losses)
	if wok {
		add(fmt.Sprintf("%.0f%%", wr), "WIN RATE", wr, pwr, pok)
	} else {
		add("&mdash;", "WIN RATE", 0, 0, false)
	}
	tk, pk := float64(t.Kills), float64(p.Kills)
	if t.Deaths > 0 {
		tk = tk / float64(t.Deaths)
	}
	if p.Deaths > 0 {
		pk = pk / float64(p.Deaths)
	}
	add(kd(t), "K/D RATIO", tk, pk, true)
	// the share of hits that landed on the head: head hits are not all kills, so a share of kills could pass 100%
	hs, hok := pct(t.Headshots, t.Hits)
	phs, phok := pct(p.Headshots, p.Hits)
	if hok {
		add(fmt.Sprintf("%.0f%%", hs), "HEADSHOT %", hs, phs, phok)
	} else {
		add("&mdash;", "HEADSHOT %", 0, 0, false)
	}
	dpm := func(w stats.WeekStats) (float64, bool) {
		if w.PlayMs < 60000 {
			return 0, false
		}
		return float64(w.DamageGiven) / (float64(w.PlayMs) / 60000), true
	}
	if v, ok := dpm(t); ok {
		pv, pok := dpm(p)
		add(fmt.Sprintf("%.0f", v), "DAMAGE / MIN", v, pv, pok)
	} else {
		add("&mdash;", "DAMAGE / MIN", 0, 0, false)
	}
	add(fmt.Sprintf("%d", t.Revives), "REVIVES", float64(t.Revives), float64(p.Revives), true)
	add(fmt.Sprintf("%d", t.BestStreak), "BEST KILL STREAK", float64(t.BestStreak), float64(p.BestStreak), true)
	if t.Longest != nil {
		pl := 0.0
		if p.Longest != nil {
			pl = float64(p.Longest.Dist)
		}
		add(fmt.Sprintf("%d m", int(math.Round(float64(t.Longest.Dist)*0.0254))), "LONGEST KILL", float64(t.Longest.Dist), pl, p.Longest != nil)
	} else {
		add("&mdash;", "LONGEST KILL", 0, 0, false)
	}
	return out
}

type view struct {
	Data
	Cells       []cell
	Range       string
	Class       string
	ClassTime   string
	Weapon      string
	WeaponKills int
	Accuracy    string // "29%" with the top weapon, or empty when it fired too few shots
	Record      string
	PlayerURL   string
	PlayURL     string
	Settings    string
	Privacy     string
	Clips       int
	ClipLine    string
}

func (d Data) view() view {
	base := strings.TrimRight(d.BaseURL, "/")
	v := view{Data: d, Cells: d.cells(), Range: day(d.From) + " – " + day(d.To.AddDate(0, 0, -1))}
	v.Class = strings.ToUpper(stats.ClassName(d.This.TopClass))
	if v.Class != "" {
		v.ClassTime = duration(d.This.TopClassMs) + " played"
	}
	v.Weapon, v.WeaponKills = d.This.TopWeapon, d.This.TopWeaponKills
	if acc, ok := d.This.TopWeaponAccuracy(); ok {
		v.Accuracy = fmt.Sprintf("%.0f%%", acc)
	}
	t := d.This
	// Only wins and losses: RTCW games do not end level, so a game with no recorded winner (aborted, restarted) or one left early is
	// counted as played but is in neither.
	v.Record = fmt.Sprintf("%d won · %d lost", t.Wins, t.Losses)
	v.PlayerURL = base + "/player.html?u=" + url.QueryEscape(d.Username)
	v.PlayURL = base + "/"
	v.Settings = d.SettingsURL
	if v.Settings == "" {
		v.Settings = base + "/account/"
	}
	v.Privacy = base + "/privacy.html"
	v.Clips = d.Plays + d.Highlights
	switch {
	case d.Plays > 0 && d.Highlights > 0:
		v.ClipLine = fmt.Sprintf("%s and %s", plays(d.Plays), plural(d.Highlights, "other highlight"))
	case d.Plays > 0:
		v.ClipLine = plays(d.Plays)
	case d.Highlights > 0:
		v.ClipLine = plural(d.Highlights, "highlight")
	}
	return v
}

var htmlTmpl = template.Must(template.New("mail").Funcs(template.FuncMap{"raw": func(s string) template.HTML { return template.HTML(s) }, "mod": func(a, b int) int { return a % b }}).Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><meta name="color-scheme" content="dark light"><meta name="supported-color-schemes" content="dark light"><title>Your S4NDMoD26 week</title></head>
<body style="margin:0;padding:0;background:#0d100e;">
<div style="display:none;max-height:0;overflow:hidden;opacity:0;">{{.Subject}}</div>
<table role="presentation" width="100%" cellspacing="0" cellpadding="0" bgcolor="#0d100e" style="background:#0d100e;"><tr><td align="center" style="padding:18px 10px;">
<table role="presentation" width="600" cellspacing="0" cellpadding="0" bgcolor="#1f2a25" style="width:100%;max-width:600px;background:#1f2a25;font-family:Segoe UI,Helvetica,Arial,sans-serif;color:#e8ece9;">

<tr><td align="center" bgcolor="#141a17" style="padding:22px 20px 18px;background:#141a17;border-bottom:3px solid #cf1b1f;">
<div style="font:normal 30px Impact,'Arial Narrow',Arial,sans-serif;letter-spacing:1px;color:#e0262a;">S4NDMoD<span style="color:#ffffff;">26</span></div>
<div style="font:bold 13px Arial,sans-serif;letter-spacing:3px;color:#d6b04c;margin-top:8px;">WEEKLY PERFORMANCE UPDATE</div>
<div style="font:13px Arial,sans-serif;color:#93a199;margin-top:4px;">{{.Range}}</div>
</td></tr>

<tr><td align="center" bgcolor="#26332d" style="padding:18px 20px;background:#26332d;">
<div style="font:bold 11px Arial,sans-serif;letter-spacing:3px;color:#d6b04c;">PLAYER</div>
<div style="font:700 30px 'Segoe UI',Helvetica,Arial,sans-serif;letter-spacing:1px;color:#ffffff;margin-top:4px;word-break:break-word;">{{.Username}}</div>
</td></tr>
{{if .Class}}
<tr><td bgcolor="#2c3b34" style="padding:22px 24px;background:#2c3b34;">
<div style="font:bold 13px Arial,sans-serif;letter-spacing:2px;color:#d6b04c;">THIS WEEK&rsquo;S TOP CLASS</div>
<div style="font:700 26px 'Segoe UI',Helvetica,Arial,sans-serif;letter-spacing:2px;color:#ffffff;margin-top:8px;">{{.Class}}</div>
<div style="font:12px Arial,sans-serif;color:#93a199;letter-spacing:1px;margin-top:2px;">MOST PLAYED CLASS &middot; {{.ClassTime}}</div>
{{if .Weapon}}<div style="font:700 18px 'Segoe UI',Helvetica,Arial,sans-serif;letter-spacing:0.5px;color:#ffffff;margin-top:14px;">{{.Weapon}}</div>
<div style="font:12px Arial,sans-serif;color:#93a199;letter-spacing:1px;margin-top:2px;">DEADLIEST WEAPON &middot; {{.WeaponKills}} KILLS{{if .Accuracy}} &middot; {{.Accuracy}} ACCURACY{{end}}</div>{{end}}
</td></tr>{{end}}

<tr><td style="padding:22px 18px 10px;">
<div align="center" style="font:700 17px 'Segoe UI',Helvetica,Arial,sans-serif;letter-spacing:3px;color:#ffffff;padding-bottom:14px;">OVERALL STATS</div>
<table role="presentation" width="100%" cellspacing="0" cellpadding="0">
{{range $i, $c := .Cells}}{{if eq (mod $i 2) 0}}<tr>{{end}}
<td width="50%" valign="top" style="padding:8px 10px 14px;"><div style="font:bold 22px Arial,sans-serif;color:#ffffff;">{{raw $c.Value}}{{$c.Arrow}}</div><div style="font:bold 10px Arial,sans-serif;letter-spacing:1px;color:#93a199;margin-top:2px;">{{$c.Label}}</div></td>
{{if eq (mod $i 2) 1}}</tr>{{end}}{{end}}
</table></td></tr>

<tr><td align="center" bgcolor="#1d4f9c" style="padding:20px;background:#1d4f9c;">
<div style="font:bold 13px Arial,sans-serif;letter-spacing:3px;color:#cfe0ff;">THIS WEEK&rsquo;S RECORD</div>
<div style="font:700 22px 'Segoe UI',Helvetica,Arial,sans-serif;letter-spacing:1px;color:#ffffff;margin-top:6px;">{{.Record}}</div>
</td></tr>
{{if .ClipLine}}
<tr><td align="center" style="padding:22px 24px 6px;">
<div style="font:bold 13px Arial,sans-serif;letter-spacing:2px;color:#d6b04c;">&#9733; HIGHLIGHTS</div>
<div style="font:15px Arial,sans-serif;color:#e8ece9;margin-top:8px;">You had {{.ClipLine}} this week.</div>
<table role="presentation" cellspacing="0" cellpadding="0" style="margin-top:12px;"><tr><td bgcolor="#3a2000" style="background:#3a2000;border:1px solid #d6b04c;"><a href="{{.PlayerURL}}" style="display:inline-block;padding:10px 22px;font:bold 14px Arial,sans-serif;color:#d6b04c;text-decoration:none;">Watch them on your stats page</a></td></tr></table>
</td></tr>{{end}}

<tr><td style="padding:22px 28px 8px;font:14px/1.6 Arial,sans-serif;color:#c9d2cd;">
When the server needed a player, you showed up, <strong style="color:#ffffff;">{{.Username}}</strong>. Your full stats, your games and your highlights are on your player page. See you next week.
</td></tr>
<tr><td align="center" style="padding:6px 20px 26px;">
<table role="presentation" cellspacing="0" cellpadding="0"><tr>
<td bgcolor="#2f4a3c" style="background:#2f4a3c;border:1px solid #6f8a7b;"><a href="{{.PlayerURL}}" style="display:inline-block;padding:11px 22px;font:bold 14px Arial,sans-serif;color:#ffffff;text-decoration:none;">My player stats</a></td>
<td width="10"></td>
<td bgcolor="#cf1b1f" style="background:#cf1b1f;border:1px solid #e8393c;"><a href="{{.PlayURL}}" style="display:inline-block;padding:11px 26px;font:bold 14px Arial,sans-serif;color:#ffffff;text-decoration:none;">Play now</a></td>
</tr></table></td></tr>

<tr><td bgcolor="#141a17" style="padding:16px 22px;background:#141a17;font:12px/1.6 Arial,sans-serif;color:#7f8c85;">
You get this because you chose a weekly summary for your S4NDMoD26 account. It is sent only for weeks you played.
<a href="{{.UnsubURL}}" style="color:#8fd0ff;">Unsubscribe</a> &middot; <a href="{{.Settings}}" style="color:#8fd0ff;">Account settings</a> &middot; <a href="{{.Privacy}}" style="color:#8fd0ff;">Privacy</a>
</td></tr>
</table></td></tr></table></body></html>
`))

// Render returns the subject, the HTML and the plain-text version of the email.
func Render(d Data) (subject, html, text string) {
	v := d.view()
	var buf bytes.Buffer
	if err := htmlTmpl.Execute(&buf, v); err != nil {
		return d.Subject(), "", ""
	}
	return d.Subject(), buf.String(), d.text(v)
}

func (d Data) text(v view) string {
	var b strings.Builder
	fmt.Fprintf(&b, "S4NDMoD26 weekly performance update\n%s\n\n%s\n\n", v.Range, d.Username)
	if v.Class != "" {
		fmt.Fprintf(&b, "Top class: %s (%s)\n", strings.Title(strings.ToLower(v.Class)), v.ClassTime)
		if v.Weapon != "" {
			acc := ""
			if v.Accuracy != "" {
				acc = ", " + v.Accuracy + " accuracy"
			}
			fmt.Fprintf(&b, "Deadliest weapon: %s (%d kills%s)\n", v.Weapon, v.WeaponKills, acc)
		}
		b.WriteString("\n")
	}
	for _, c := range v.Cells {
		val := strings.ReplaceAll(c.Value, "&mdash;", "-")
		a := ""
		if strings.Contains(string(c.Arrow), "9650") {
			a = " (up from last week)"
		} else if strings.Contains(string(c.Arrow), "9660") {
			a = " (down from last week)"
		}
		fmt.Fprintf(&b, "%-20s %s%s\n", strings.Title(strings.ToLower(c.Label)), val, a)
	}
	fmt.Fprintf(&b, "\nRecord: %s\n", v.Record)
	if v.ClipLine != "" {
		fmt.Fprintf(&b, "Highlights: you had %s this week.\n", v.ClipLine)
	}
	fmt.Fprintf(&b, "\nYour stats: %s\nPlay now:   %s\n\nYou get this because you chose a weekly summary for your S4NDMoD26 account.\nUnsubscribe: %s\nAccount settings: %s\n", v.PlayerURL, v.PlayURL, d.UnsubURL, v.Settings)
	return b.String()
}
