package demo

import "testing"

// A player who has left by the end of the file is still on the roster of the
// game they played, and a piece cut from the file gets its own stretch's.
func TestRosterKeepsPlayersWhoLeft(t *testing.T) {
	cs := func(name string, team int) string { return "n\\" + name + "\\t\\" + string(rune('0'+team)) }
	d := &Demo{
		Configstrings: map[int]string{csPlayers + 0: cs("Stayer", 1)}, // the final table: Leaver is gone
		InitialConfigstrings: map[int]string{
			csPlayers + 0: cs("Stayer", 1),
			csPlayers + 1: cs("Leaver", 2),
		},
		Frames: []Snapshot{{ServerTime: 1000}, {ServerTime: 9000}},
		Commands: []Command{
			{ServerTime: 5000, Text: "cs 577 \"\""},
			{ServerTime: 6000, Text: "cs 578 \"" + cs("Joiner", 2) + "\""},
		},
	}
	got := d.roster()
	for slot, name := range map[int]string{0: "Stayer", 1: "Leaver", 2: "Joiner"} {
		if got[slot].Name != name {
			t.Errorf("slot %d: got %q, want %q (roster %v)", slot, got[slot].Name, name, got)
		}
	}
	// a piece that ends before Joiner arrived has no Joiner, but still Leaver
	piece := Clip(d, 1000, 5500)
	r := piece.roster()
	if r[1].Name != "Leaver" || r[0].Name != "Stayer" {
		t.Errorf("piece roster = %v", r)
	}
	if _, ok := r[2]; ok {
		t.Errorf("piece roster has Joiner, who came after it: %v", r)
	}
}

// A demo taken through WolfTV opens on the relay's server info and is sent the
// game's afterwards; the map is the game's.
func TestDescribeMapFromServerInfoUpdates(t *testing.T) {
	d := &Demo{
		Configstrings:        map[int]string{csServerInfo: `\mapname\wtv`},
		InitialConfigstrings: map[int]string{csServerInfo: `\mapname\wtv`},
		Frames:               []Snapshot{{ServerTime: 1000}, {ServerTime: 9000}},
		Commands: []Command{
			{ServerTime: 2000, Text: `cs 0 "\gameversion\OSP v0.9\mapname\mp_assault"`},
		},
	}
	got := Describe(d)
	if got.Map != "mp_assault" || got.Mod != "OSP v0.9" {
		t.Errorf("map %q mod %q, want mp_assault / OSP v0.9", got.Map, got.Mod)
	}
	d.Commands = nil
	if got := Describe(d).Map; got != "wtv" {
		t.Errorf("with no update the opening info stands: got %q", got)
	}
}

// A server may change maps between games.  Each game's piece takes the map in
// force in its middle, whatever the lead-in and tail it keeps.
func TestDescribeMapChangesBetweenGames(t *testing.T) {
	cs := func(m string) string { return `\mapname\` + m }
	d := &Demo{
		Configstrings:        map[int]string{csServerInfo: cs("wtv")},
		InitialConfigstrings: map[int]string{csServerInfo: cs("wtv")},
		Frames:               []Snapshot{{ServerTime: 1000}, {ServerTime: 10000}, {ServerTime: 18000}, {ServerTime: 22000}, {ServerTime: 30000}},
		Commands: []Command{
			{ServerTime: 2000, Text: `cs 0 "` + cs("mp_assault") + `"`},
			{ServerTime: 19000, Text: `cs 0 "` + cs("mp_ice") + `"`}, // in the first game's tail
		},
	}
	first := Clip(d, 1000, 21000)   // game on mp_assault, with the change in its tail
	second := Clip(d, 19500, 30000) // game on mp_ice
	if got := Describe(first).Map; got != "mp_assault" {
		t.Errorf("first game: %q", got)
	}
	if got := Describe(second).Map; got != "mp_ice" {
		t.Errorf("second game: %q", got)
	}
}

// A WolfTV demo opens on the relay's server info; the game's first update
// takes its place for everything that reads the opening info.
func TestSettleRelay(t *testing.T) {
	relay := `\mapname\wtv\timelimit\20160\g_gametype\6`
	d := &Demo{
		Configstrings:        map[int]string{0: relay, 1: `\fs_game\wolftv`},
		InitialConfigstrings: map[int]string{0: relay, 1: `\fs_game\wolftv`},
		Commands:             []Command{{ServerTime: 5, Text: `cs 0 "\mapname\mp_assault\timelimit\10\g_gametype\6"`}},
	}
	settleRelay(d)
	if v := InfoValue(d.InitialConfigstrings[0], "timelimit"); v != "10" {
		t.Errorf("timelimit %q, want 10", v)
	}
	plain := &Demo{
		Configstrings:        map[int]string{0: `\timelimit\20`, 1: `\fs_game\main`},
		InitialConfigstrings: map[int]string{0: `\timelimit\20`, 1: `\fs_game\main`},
		Commands:             []Command{{ServerTime: 5, Text: `cs 0 "\timelimit\5"`}},
	}
	settleRelay(plain)
	if v := InfoValue(plain.InitialConfigstrings[0], "timelimit"); v != "20" {
		t.Errorf("a demo that is not through a relay changed: %q", v)
	}
}
