package demo

import (
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
)

// sampleRoot skips the test unless the (large) sample demos are in play.
// Run these under a memory cap, e.g.
//
//	DEMO_SAMPLES=~/dev/sample_demos GOMEMLIMIT=4GiB \
//	  systemd-run --user --scope -p MemoryMax=6G go test -run TestSyncSamples -count=1 .
func sampleRoot(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("sample demos skipped in -short mode")
	}
	root := os.Getenv("DEMO_SAMPLES")
	if root == "" {
		t.Skip("DEMO_SAMPLES not set")
	}
	return root
}

// releaseMemory returns freed demo memory to the OS between cases.
func releaseMemory() {
	runtime.GC()
	debug.FreeOSMemory()
}

// samplePath locates a sample demo by file-name substring under root.
func samplePath(t *testing.T, root, sub string) (string, int) {
	t.Helper()
	var found string
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && found == "" && strings.Contains(filepath.Base(p), sub) &&
			strings.Contains(filepath.Ext(p), "dm_") {
			found = p
		}
		return nil
	})
	if found == "" {
		t.Fatalf("no sample matching %q", sub)
	}
	proto, _ := strconv.Atoi(strings.TrimPrefix(filepath.Ext(found), ".dm_"))
	return found, proto
}

// findSample parses a sample demo, keeping only what Sync reads (player
// entities); retaining every entity of every frame costs gigabytes.
func findSample(t *testing.T, sub string) *Demo {
	t.Helper()
	path, proto := samplePath(t, sampleRoot(t), sub)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	d, perr := ParseWith(data, proto, Options{Entities: KeepPlayers})
	if perr != nil {
		t.Fatalf("%s: %v", path, perr)
	}
	return d
}

// TestMeasureSample parses ONE demo (DEMO_MEASURE=<name substring>) and
// reports what keeping its frames costs, per retention mode.
func TestMeasureSample(t *testing.T) {
	sub := os.Getenv("DEMO_MEASURE")
	if sub == "" {
		t.Skip("DEMO_MEASURE not set")
	}
	path, proto := samplePath(t, sampleRoot(t), sub)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct {
		name string
		keep EntityKeep
	}{{"KeepNone", KeepNone}, {"KeepPlayers", KeepPlayers}, {"KeepAll", KeepAll}} {
		releaseMemory()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		d, perr := ParseWith(data, proto, Options{Entities: m.keep})
		if perr != nil {
			t.Fatalf("%s: %v", path, perr)
		}
		releaseMemory()
		runtime.ReadMemStats(&after)
		ents := 0
		for i := range d.Frames {
			ents += len(d.Frames[i].Entities)
		}
		avg := 0.0
		if len(d.Frames) > 0 {
			avg = float64(ents) / float64(len(d.Frames))
		}
		t.Logf("%s %-11s file=%dMB frames=%d avgEntities=%.1f retained=%dMB totalAlloc=%dMB",
			filepath.Base(path), m.name, len(data)>>20, len(d.Frames), avg,
			(int64(after.HeapAlloc)-int64(before.HeapAlloc))>>20, int64(after.TotalAlloc-before.TotalAlloc)>>20)
		runtime.KeepAlive(d)
	}
}

func TestSyncSamples(t *testing.T) {
	cases := []struct {
		name, a, b string
		synced     bool
		reason     string // substring expected in Reason when not synced
	}{
		{"same round, two teams (dm_60)", "145952--!cF-K!llaS!n", "150102-iN-Summ3L", true, ""},
		{"same round, same team (dm_60)", "145959--!cF-Tekoda", "150025--!cF-Xp!Re", true, ""},
		{"different rounds of one match", "145952--!cF-K!llaS!n", "150912--!cF-K!llaS!n", false, "different round"},
		{"round 2 vs round 3", "150912--!cF-K!llaS!n", "152311--!cF-K!llaS!n", false, "different round"},
		{"different maps", "145952--!cF-K!llaS!n", "mTwaTnJoe", false, "different maps"},
		{"POV vs overview (dm_59)", "mTwaTnJoe", "wsw_mtw", true, ""},
		{"two POVs (dm_59)", "mTwaTnJoe", "mTwaTnVirus", true, ""},
		{"two demos (dm_57)", "dsvsdrmatch", "dsvsdrshaft", true, ""},
	}
	sampleRoot(t)
	// hold only the two demos the current case needs; anything else is
	// dropped and its memory returned before the next parse
	cache := map[string]*Demo{}
	get := func(c ...string) []*Demo {
		for k := range cache {
			if k != c[0] && k != c[1] {
				delete(cache, k)
			}
		}
		releaseMemory()
		out := make([]*Demo, len(c))
		for i, s := range c {
			if cache[s] == nil {
				cache[s] = findSample(t, s)
			}
			out[i] = cache[s]
		}
		return out
	}
	for _, c := range cases {
		ds := get(c.a, c.b)
		r := Sync(ds[0], ds[1])
		t.Logf("%-34s synced=%-5v off=%4d basis=%-16s anchors=%d/%d pos=%d/%d overlap=%ds reason=%q",
			c.name, r.Synced, r.OffsetMs, r.Basis, r.CmdAnchors, r.CmdCandidates,
			r.PosAgree, r.PosSamples, (r.OverlapEnd-r.OverlapStart)/1000, r.Reason)
		if r.Synced != c.synced {
			t.Errorf("%s: synced=%v want %v (%s)", c.name, r.Synced, c.synced, r.Reason)
		}
		if !c.synced && !strings.Contains(r.Reason, c.reason) {
			t.Errorf("%s: reason %q lacks %q", c.name, r.Reason, c.reason)
		}
	}
}

// A spectator's demo of a whole evening is cut at its games, and each piece reads as one game.
func TestGamesSplit(t *testing.T) {
	sampleRoot(t)
	d := findSample(t, "wsw_mtw")
	games := Games(d)
	if len(games) != 3 {
		t.Fatalf("games = %+v, want 3", games)
	}
	for i, g := range games {
		t.Logf("game %d: %d-%d (%ds) rounds=%d", g.N, g.FromMs, g.ToMs, (g.ToMs-g.FromMs)/1000, g.Rounds)
		if g.N != i+1 || g.FromMs >= g.ToMs || g.Rounds < 1 {
			t.Errorf("game %d: %+v", i+1, g)
		}
		if i > 0 && g.FromMs < games[i-1].ToMs {
			t.Errorf("game %d starts at %d, before game %d ends at %d", g.N, g.FromMs, games[i-1].N, games[i-1].ToMs)
		}
		piece := Clip(d, g.FromMs, g.ToMs)
		rs := RoundsOf(piece)
		if len(rs) != g.Rounds {
			t.Errorf("game %d: the piece has %d rounds, want %d", g.N, len(rs), g.Rounds)
		}
		for _, r := range rs {
			if r.Game != 1 {
				t.Errorf("game %d: a round of the piece is numbered game %d", g.N, r.Game)
			}
		}
		if f := piece.Frames; f[0].ServerTime < g.FromMs || f[len(f)-1].ServerTime > g.ToMs {
			t.Errorf("game %d: frames %d-%d outside the window", g.N, f[0].ServerTime, f[len(f)-1].ServerTime)
		}
	}
}
