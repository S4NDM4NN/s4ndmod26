package demo

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"s4ndmod26/status-api/replay"
)

// OSP sends a stats table ("sc" commands) at the end of each round with
// per-player kills, deaths, suicides and teamkills.  Kll and Dth count only
// enemy kills; suicides and teamkills have their own columns.  That is ground
// truth for the obituary events ToReplay reconstructs.
var scoreRow = regexp.MustCompile(`^(?:Axis|Allies)\s+(.+?)\s+(-?\d+)\s+(-?\d+)\s+(-?\d+)\s+(-?\d+)\s+-?\d+\s+-?\d+\s+(-?\d+)\s+(-?\d+)\s+(-?\d+)`)

type tally struct{ kills, deaths, suicides, teamkills int }

// damage is the DG / DR / TD columns: damage given and received (enemies
// only) and team damage.
type damage struct{ given, received, team int }

type board struct {
	time    int
	players map[int]tally // by client number
	damage  map[int]damage
}

func normName(s string) string {
	return strings.ToLower(strings.ReplaceAll(CleanName(s), " ", ""))
}

// scoreboards extracts every complete OSP stats table in a demo.
func scoreboards(d *Demo) []board {
	byName := map[string]int{}
	for n, p := range Describe(d).Players {
		byName[normName(p.Name)] = n
	}
	var out []board
	var cur *board
	for _, c := range d.Commands {
		if !strings.HasPrefix(c.Text, "sc ") {
			continue
		}
		if cur != nil && c.ServerTime != cur.time {
			out, cur = append(out, *cur), nil
		}
		txt := strings.ReplaceAll(strings.Trim(strings.TrimPrefix(c.Text, "sc "), "\""), `\n`, "\n")
		for _, ln := range strings.Split(txt, "\n") {
			m := scoreRow.FindStringSubmatch(strings.TrimSpace(CleanName(ln)))
			if m == nil {
				continue
			}
			n, ok := byName[normName(m[1])]
			if !ok {
				continue // the Totals row, or a player who left
			}
			if cur == nil {
				cur = &board{time: c.ServerTime, players: map[int]tally{}, damage: map[int]damage{}}
			}
			v := func(i int) int { x, _ := strconv.Atoi(m[i]); return x }
			cur.players[n] = tally{v(2), v(3), v(4), v(5)}
			cur.damage[n] = damage{v(6), v(7), v(8)}
		}
	}
	if cur != nil {
		out = append(out, *cur)
	}
	return out
}

// reconstructed tallies the reconstructed events in (from, to].
func reconstructed(rep *replay.Replay, from, to int) map[int]tally {
	out := map[int]tally{}
	for _, e := range rep.Events {
		if int(e.ServerTime) <= from || int(e.ServerTime) > to {
			continue
		}
		a, v := int(e.ActorClientNum), int(e.TargetClientNum)
		switch e.Type {
		case replay.EventKill:
			t, tv := out[a], out[v]
			t.kills++
			tv.deaths++
			out[a], out[v] = t, tv
			out[a], out[v] = out[a], out[v]
		case replay.EventTeamkill:
			t := out[a]
			t.teamkills++
			out[a] = t
		case replay.EventSuicide:
			t := out[v]
			t.suicides++
			out[v] = t
		}
	}
	return out
}

// mapStart is when the map's first live round began.  OSP boards are
// cumulative for the map: the round-two board includes round one.  g_currentRound
// is 0 until round one ends, 1 during round two, and back to 0 after it, so a
// live entry with round 0 that follows a round-1 live entry belongs to the
// same map rather than starting a new one.
func mapStart(ph []phaseChange, t int) int {
	start, lastRound := -1, -1
	for _, p := range ph {
		if p.Time > t || p.State != gsPlaying {
			continue
		}
		if p.Round == 0 && lastRound != 1 {
			start = p.Time
		}
		lastRound = p.Round
	}
	return start
}

// Reconstruction is exact on most boards and 1-2 events off on a few (an
// unexplained over-count of about 1%), so a column may differ by 3% or 2.
func within(want, have int) bool {
	tol := want / 33
	if tol < 2 {
		tol = 2
	}
	d := want - have
	return d <= tol && -d <= tol
}

// minRoundMs: shorter windows are boards sent at a round's start (a
// leftover table), not an end-of-round summary.
const minRoundMs = 30000

func TestKillEventsVsScoreboard(t *testing.T) {
	root := sampleRoot(t)
	var files []string
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.Contains(filepath.Ext(p), "dm_") {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	checked, exact := 0, 0
	for _, path := range files {
		proto, _ := strconv.Atoi(strings.TrimPrefix(filepath.Ext(path), ".dm_"))
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		d, _ := ParseWith(data, proto, Options{Entities: KeepPlayersEvents})
		boards := scoreboards(d)
		if len(boards) == 0 {
			releaseMemory()
			continue
		}
		rep, _ := ToReplay(d)
		ph := phases(d)
		// a demo recorded from mid-round never saw the earlier kills
		sawStart := ph[0].State != gsPlaying && ph[0].State != gsUnknown
		for _, b := range boards {
			start := mapStart(ph, b.time)
			if !sawStart || start < 0 || b.time-start < minRoundMs {
				continue
			}
			got := reconstructed(rep, start, b.time)
			var want, have tally
			for n, w := range b.players {
				g := got[n]
				want.kills, want.deaths, want.suicides, want.teamkills = want.kills+w.kills, want.deaths+w.deaths, want.suicides+w.suicides, want.teamkills+w.teamkills
				have.kills, have.deaths, have.suicides, have.teamkills = have.kills+g.kills, have.deaths+g.deaths, have.suicides+g.suicides, have.teamkills+g.teamkills
			}
			name := filepath.Base(path)
			if len(name) > 40 {
				name = name[len(name)-40:]
			}
			t.Logf("%-40s board@%d: want k/d/s/tk=%d/%d/%d/%d have=%d/%d/%d/%d", name, b.time,
				want.kills, want.deaths, want.suicides, want.teamkills, have.kills, have.deaths, have.suicides, have.teamkills)
			if !within(want.kills, have.kills) || !within(want.deaths, have.deaths) ||
				!within(want.suicides, have.suicides) || !within(want.teamkills, have.teamkills) {
				t.Errorf("%s board@%d: reconstructed %+v, scoreboard says %+v", name, b.time, have, want)
			}
			if want == have {
				exact++
			}
			checked++
		}
		d, rep = nil, nil
		releaseMemory()
	}
	if checked == 0 {
		t.Skip("no sample demo carries a usable OSP scoreboard")
	}
	t.Logf("%d boards checked, %d exact", checked, exact)
}

// Event numbering differs between protocols, so the obituary type is found
// from the data; it must be found for each protocol that has a long demo.
func TestObituaryTypeDetected(t *testing.T) {
	root := sampleRoot(t)
	for _, sub := range []string{"145952--!cF-K", "mTwaTnJoe", "dsvsdrmatch"} {
		path, proto := samplePath(t, root, sub)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		d, _ := ParseWith(data, proto, Options{Entities: KeepPlayersEvents})
		_, r := ToReplay(d)
		t.Logf("dm_%d %s: obituary eType=%d obituaries=%d (+%d in warm-up)", proto, sub, r.ObituaryType, r.Obituaries, r.WarmupObituaries)
		if r.ObituaryType == 0 || r.Obituaries == 0 {
			t.Errorf("dm_%d %s: no obituary events found", proto, sub)
		}
		d = nil
		releaseMemory()
	}
}
