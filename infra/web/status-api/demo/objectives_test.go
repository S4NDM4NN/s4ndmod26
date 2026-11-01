package demo

import (
	"strings"
	"testing"
)

// The messages are the ones the sample demos carry (dm_57, dm_59, dm_60).
func TestClassifyCP(t *testing.T) {
	for _, c := range []struct {
		cmd  string
		kind string
		team int32
		ok   bool
	}{
		{`cp "Dynamite planted near the Sea Wall door!" 2`, ObjPlant, 0, true},
		{`cp "Dynamite planted near Filtration Door!" 1`, ObjPlant, 0, true},
		{"cp \"Allies have stolen the War Documents!\n\" 2", ObjSteal, 2, true},
		{`cp "Allies transmitted the documents!" 2`, ObjCapture, 2, true},
		{`cp "Allies capture the Forward Bunker!" 2`, ObjCapture, 2, true},
		{`cp "Axis reclaims the Forward Bunker!" 2`, ObjReturn, 1, true},
		{`cp "Axis have returned the War Documents!" 2`, ObjReturn, 1, true},
		{`cp "The Sea Wall has been breached!" 2`, ObjCapture, 0, true},
		{`cp "Allies have breached the Sub Area!" 2`, ObjCapture, 2, true},
		{`cp "Allied team has destoryed the Axis Submarine!" 2`, ObjCapture, 2, true}, // sic; the Allies act
		{`cp "Axis have stolen the gold from the Allies!" 2`, ObjSteal, 1, true},
		{`cp "^7Allies capture the ^3Forward Bunker!" 2`, ObjCapture, 2, true}, // colour codes
		// one player's own progress, or not objectives at all
		{`cp "Arming dynamite..." 1`, "", 0, false},
		{`cp "Dynamite is now armed with a 30 second timer!" 1`, "", 0, false},
		{`cp "Dynamite is set, but NOT armed!"`, "", 0, false},
		{"cp \"\n^4iN^3-^7HoMBre\n^3is ready!\n\"", "", 0, false},
		{`cp "The Axis team is ready!"`, "", 0, false},
		{`cp "You have been revived by [lof]Xp!Re!\n"`, "", 0, false},
		{`print "Dynamite planted near the Sea Wall door!"`, "", 0, false},
		{`chat "Allies capture the Forward Bunker!" 0`, "", 0, false},
	} {
		text, kind, team, ok := classifyCP(c.cmd)
		if ok != c.ok || kind != c.kind || team != c.team {
			t.Errorf("%q: got (%q, %q, team %d, %v), want (%q, team %d, %v)", c.cmd, text, kind, team, ok, c.kind, c.team, c.ok)
		}
		if ok && (strings.Contains(text, "^") || strings.Contains(text, "\n")) {
			t.Errorf("%q: text %q still has colour codes or newlines", c.cmd, text)
		}
	}
}

func TestBuildCarries(t *testing.T) {
	obs := []carryObs{
		{1000, 5, 1 << pwRedFlag}, {2000, 7, 0}, // 7 was never seen carrying
		{9000, 5, 0},                // 5 carried from 1000 to 9000
		{12000, 6, 1 << pwBlueFlag}, // 6 still has it at the end
		{5000, 5, 1 << pwRedFlag},   // seen again while carrying: no new stretch
	}
	got := buildCarries(obs)
	want := []Carry{{5, 1000, 9000}, {6, 12000, 0}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("carries = %+v, want %+v", got, want)
	}
}

func TestResolveObjectives(t *testing.T) {
	carries := []Carry{{11, 23221500, 23255800}, {3, 900000, 900500}}
	msgs := []cpMsg{
		{t: 23221500, text: "Allies have stolen the War Documents!", kind: ObjSteal, team: 2},
		{t: 23245300, text: "Allies transmitted the documents!", kind: ObjCapture, team: 2}, // the flag clears later
		{t: 23245300, text: "Dynamite planted near the Sea Wall door!", kind: ObjPlant},     // never guessed
		{t: 50, text: "Allies have stolen the War Documents!", kind: ObjSteal},              // nobody started carrying near it
	}
	got := resolveObjectives(msgs, carries)
	if got[0].Actor != 11 || got[1].Actor != 11 || got[2].Actor != -1 || got[3].Actor != -1 {
		t.Errorf("actors = %d %d %d %d, want 11 11 -1 -1", got[0].Actor, got[1].Actor, got[2].Actor, got[3].Actor)
	}
}

// Real demos: who stole and who transmitted the documents.  QCon03 round 1,
// six POVs of the same game, on dm_60.
func TestObjectivesFromDemos(t *testing.T) {
	root := sampleRoot(t)
	ds := loadGroup(t, root, []string{"2003-08-15_145952", "2003-08-15_145959", "2003-08-15_150025",
		"2003-08-15_150048", "2003-08-15_150102", "2003-08-15_150139"})
	rep, mr, err := MergeDemos(ds)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	var steals, transmits []Objective
	for _, o := range mr.Objectives {
		counts[o.Kind]++
		if o.Kind == ObjSteal {
			steals = append(steals, o)
		}
		if strings.Contains(o.Text, "transmitted") {
			transmits = append(transmits, o)
		}
		if strings.Contains(strings.ToLower(o.Text), "arming") || strings.Contains(o.Text, "ready") || strings.Contains(o.Text, "revived") {
			t.Errorf("not an objective event: %q", o.Text)
		}
	}
	t.Logf("%d objective events %v, %d carries", len(mr.Objectives), counts, len(mr.Carries))
	for _, k := range []string{ObjPlant, ObjSteal, ObjCapture, ObjReturn} {
		if counts[k] == 0 {
			t.Errorf("no %q event found", k)
		}
	}
	teamAt := func(c int32, tm int32) int32 {
		for _, f := range rep.Frames {
			if f.ServerTime >= tm {
				for _, s := range f.Samples {
					if s.ClientNum == c {
						return s.Team
					}
				}
				break
			}
		}
		return -1
	}
	known := 0
	for _, o := range steals {
		if o.Actor < 0 {
			continue
		}
		known++
		// the thief is on the team that stole
		if tm := teamAt(o.Actor, o.Time); tm != o.Team {
			t.Errorf("the thief at %d (slot %d) is on team %d, the message says team %d", o.Time, o.Actor, tm, o.Team)
		}
	}
	if known < len(steals)-1 {
		t.Errorf("the thief is known for only %d of %d thefts", known, len(steals))
	}
	// whoever stole the documents in round 1 is who transmitted them
	if len(transmits) == 0 || transmits[0].Actor < 0 {
		t.Fatalf("no transmitter found: %+v", transmits)
	}
	if steals[0].Actor != transmits[0].Actor {
		t.Errorf("the first theft is by slot %d but the documents were transmitted by slot %d", steals[0].Actor, transmits[0].Actor)
	}
}
