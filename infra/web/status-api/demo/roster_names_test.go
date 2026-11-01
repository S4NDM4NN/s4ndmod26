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
