package demo

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"s4ndmod26/status-api/replay"
)

// A revive is told only to the player revived ("You have been revived by
// ...!"), so for the demos' recorders there is ground truth for the revives
// found from the positions alone.
var reRevivedBy = regexp.MustCompile(`(?s)^cp "You have been revived by (.*?)!?\s*"`)

func TestRevivesVsMessages(t *testing.T) {
	root := sampleRoot(t)
	ds := loadGroup(t, root, []string{"2003-08-15_145952", "2003-08-15_145959", "2003-08-15_150025",
		"2003-08-15_150048", "2003-08-15_150102", "2003-08-15_150139"})
	rep, _, err := MergeDemos(ds)
	if err != nil {
		t.Fatal(err)
	}
	var revives []replay.Event
	for _, e := range rep.Events {
		if e.Type == replay.EventRevive {
			revives = append(revives, e)
		}
	}
	byName := map[string]int32{}
	for n, name := range rep.Header.PlayerNames {
		if name != "" {
			byName[normName(name)] = int32(n)
		}
	}
	messages, found, namedRight := 0, 0, 0
	recorders := map[int32]bool{}
	for _, d := range ds {
		recorders[int32(d.ClientNum)] = true
		for _, c := range d.Commands {
			m := reRevivedBy.FindStringSubmatch(c.Text)
			if m == nil {
				continue
			}
			by, ok := byName[normName(strings.Trim(m[1], "[lof] "))]
			if !ok {
				continue
			}
			messages++
			for _, e := range revives {
				if e.TargetClientNum == int32(d.ClientNum) && int(e.ServerTime) >= c.ServerTime-3000 && int(e.ServerTime) <= c.ServerTime+1500 {
					found++
					if e.ActorClientNum == by {
						namedRight++
					}
					break
				}
			}
		}
	}
	// revives of a recorder with no message are false alarms (only a recorder gets one)
	inRound := func(e replay.Event) bool { return live(phases(ds[0]), int(e.ServerTime)) }
	spurious, ofRecorders := 0, 0
	for _, e := range revives {
		if !recorders[e.TargetClientNum] || !inRound(e) {
			continue
		}
		ofRecorders++
		ok := false
		for _, d := range ds {
			if int32(d.ClientNum) != e.TargetClientNum {
				continue
			}
			for _, c := range d.Commands {
				if reRevivedBy.MatchString(c.Text) && int(e.ServerTime) >= c.ServerTime-3000 && int(e.ServerTime) <= c.ServerTime+1500 {
					ok = true
				}
			}
		}
		if !ok {
			spurious++
		}
	}
	t.Logf("%d revives found in all; recorders were told of %d revives: %d found (%d with the right reviver); of %d revives of recorders, %d had no message",
		len(revives), messages, found, namedRight, ofRecorders, spurious)
	if messages < 5 {
		t.Fatalf("only %d revive messages to check against", messages)
	}
	if float64(found) < 0.8*float64(messages) {
		t.Errorf("only %d of %d revives the game announced were found", found, messages)
	}
	if found > 0 && float64(namedRight) < 0.7*float64(found) {
		t.Errorf("the reviver is right in only %d of %d", namedRight, found)
	}
	if ofRecorders > 0 && float64(spurious) > 0.25*float64(ofRecorders) {
		t.Errorf("%d of %d revives of recorders were not announced to them", spurious, ofRecorders)
	}
}

// A missile's positions come from evaluating its trajectory at each frame; a
// wrong trajectory type would make it jump between frames.
func TestMissileTrajectories(t *testing.T) {
	root := sampleRoot(t)
	for _, sub := range []string{"145952--!cF-K", "mTwaTnJoe", "dsvsdrmatch"} {
		path, proto := samplePath(t, root, sub)
		d := loadOne(t, path, proto)
		rep, _ := ToReplay(d)
		type last struct {
			t   int32
			pos [3]float32
		}
		prev := map[int32]last{}
		var steps []float64
		for _, f := range rep.Frames {
			for _, s := range f.Samples {
				if s.EType != etMissile {
					continue
				}
				if p, ok := prev[s.ClientNum]; ok && f.ServerTime-p.t <= 100 {
					steps = append(steps, dist(s.Origin, p.pos[0], p.pos[1], p.pos[2]))
				}
				prev[s.ClientNum] = last{f.ServerTime, s.Origin}
			}
		}
		sort.Float64s(steps)
		if len(steps) < 100 {
			t.Errorf("dm_%d: only %d missile steps", proto, len(steps))
			continue
		}
		p := func(q float64) float64 { return steps[int(q*float64(len(steps)-1))] }
		t.Logf("dm_%d %s: %d missile steps between frames, median %.0f, p90 %.0f, p99 %.0f units (a frame is 50 ms)", proto, sub, len(steps), p(.5), p(.9), p(.99))
		if p(.99) > 250 {
			t.Errorf("dm_%d: missiles jump %.0f units between frames: the trajectory evaluation is wrong", proto, p(.99))
		}
	}
}

func loadOne(t *testing.T, path string, proto int) *Demo {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	d, perr := ParseWith(data, proto, Options{Entities: KeepPlayersEvents})
	if d == nil {
		t.Fatal(perr)
	}
	return d
}
