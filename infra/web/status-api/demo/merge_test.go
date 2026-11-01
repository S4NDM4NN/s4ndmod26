package demo

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// loadGroup parses every sample whose file name contains one of subs.  The
// whole group is held in memory at once, so groups must stay small.
func loadGroup(t *testing.T, root string, subs []string) []*Demo {
	t.Helper()
	var paths []string
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.Contains(filepath.Ext(p), "dm_") {
			return nil
		}
		for _, s := range subs {
			if strings.Contains(filepath.Base(p), s) {
				paths = append(paths, p)
				break
			}
		}
		return nil
	})
	sort.Strings(paths)
	var out []*Demo
	for _, p := range paths {
		proto, _ := strconv.Atoi(strings.TrimPrefix(filepath.Ext(p), ".dm_"))
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		d, perr := ParseWith(data, proto, Options{Entities: KeepPlayersEvents})
		if perr != nil {
			t.Fatalf("%s: %v", p, perr)
		}
		out = append(out, d)
	}
	return out
}

func TestMergeSamples(t *testing.T) {
	root := sampleRoot(t)
	groups := []struct {
		name string
		subs []string
	}{
		{"dm_60 round 1, six POVs", []string{"2003-08-15_145952", "2003-08-15_145959", "2003-08-15_150025",
			"2003-08-15_150048", "2003-08-15_150102", "2003-08-15_150139"}},
		{"dm_59 set 1, twelve POVs and the shoutcaster overview", []string{"-191902-", "-191904-", "-191906-",
			"-191911-", "-191924-", "wsw_mtw"}},
	}
	for _, g := range groups {
		ds := loadGroup(t, root, g.subs)
		if len(ds) < 2 {
			t.Fatalf("%s: only %d demos found", g.name, len(ds))
		}
		rep, mr, err := MergeDemos(ds)
		if err != nil {
			t.Fatalf("%s: %v", g.name, err)
		}
		t.Logf("== %s: %d demos, merged frames=%d avgPlayers/frame=%.1f ownHealthPlayers=%d events %d -> %d",
			g.name, len(ds), mr.Frames, mr.AvgPlayers, mr.OwnHealthPlayers, mr.RawEvents, mr.Events)
		recorders := map[int]bool{}
		for _, p := range mr.Parts {
			t.Logf("   rec=%-2d %-18s spec=%-5v used=%-5v off=%d frames=%d avgPlayers=%.1f -> merged %.1f %s",
				p.Recorder, p.Name, p.Spectator, p.Used, p.OffsetMs, p.Frames, p.AvgPlayers, p.MergedAvgPlayers, p.Reason)
			if !p.Used {
				t.Errorf("%s: %s was not merged: %s", g.name, p.Name, p.Reason)
				continue
			}
			if !p.Spectator {
				recorders[p.Recorder] = true
			}
			// over its own frames, the merge can only add to what a demo saw
			if p.MergedAvgPlayers < p.AvgPlayers {
				t.Errorf("%s: %s sees %.1f players per frame alone but %.1f merged", g.name, p.Name, p.AvgPlayers, p.MergedAvgPlayers)
			}
			if !p.Spectator && p.MergedAvgPlayers <= p.AvgPlayers {
				t.Errorf("%s: merging added no players to %s's view", g.name, p.Name)
			}
		}
		// every recorder has exact health, and so does anyone a recorder
		// followed after dying
		if mr.OwnHealthPlayers < len(recorders) {
			t.Errorf("%s: %d players with real health, want at least %d (the recorders)", g.name, mr.OwnHealthPlayers, len(recorders))
		}

		// the merged kills must still match the game's own scoreboard
		checked := false
		for _, d := range ds {
			ph := phases(d)
			if ph[0].State == gsPlaying || ph[0].State == gsUnknown {
				continue
			}
			for _, b := range scoreboards(d) {
				start := mapStart(ph, b.time)
				if start < 0 || b.time-start < minRoundMs || checked {
					continue
				}
				got := reconstructed(rep, start, b.time)
				var want, have tally
				for n, w := range b.players {
					g := got[n]
					want.kills, want.deaths, want.suicides, want.teamkills = want.kills+w.kills, want.deaths+w.deaths, want.suicides+w.suicides, want.teamkills+w.teamkills
					have.kills, have.deaths, have.suicides, have.teamkills = have.kills+g.kills, have.deaths+g.deaths, have.suicides+g.suicides, have.teamkills+g.teamkills
				}
				t.Logf("   scoreboard@%d: want k/d/s/tk=%+v merged=%+v", b.time, want, have)
				if !within(want.kills, have.kills) || !within(want.deaths, have.deaths) ||
					!within(want.suicides, have.suicides) || !within(want.teamkills, have.teamkills) {
					t.Errorf("%s: merged kills %+v vs scoreboard %+v", g.name, have, want)
				}
				checked = true
			}
		}
		if !checked {
			t.Errorf("%s: no scoreboard to check the merged events against", g.name)
		}
		ds, rep = nil, nil
		releaseMemory()
	}
}
