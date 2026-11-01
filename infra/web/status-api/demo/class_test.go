package demo

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"s4ndmod26/status-api/replay"
)

// The class numbers come from the tinfo readout, and nothing in a demo says
// which number is which class.  The weapons do: only lieutenants call
// artillery and airstrikes, only soldiers fire rockets (a direct rocket hit,
// means of death 6, is too rare in the samples to check) and carry the sniper
// rifle.  So the class of those kills' killers must be 3 and 0.
// (Means-of-death numbers are those of the dm_60 demos; see bg_public.h.)
const (
	modRocketSplash       = 7
	modSniperRifle        = 21
	modAirstrike, modArty = 46, 49
	classSoldier, classLT = 0, 3
)

func TestClassNumbering(t *testing.T) {
	root := sampleRoot(t)
	var files []string
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(p, ".dm_60") {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	// one pass to learn which game each demo is of (a demo is dropped as soon
	// as it is described), then each game is merged one demo at a time
	type file struct {
		path string
		span int
	}
	byServer := map[string][]file{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		d, _ := ParseWith(data, 60, Options{Entities: KeepNone})
		in := Describe(d)
		byServer[in.ServerID] = append(byServer[in.ServerID], file{f, in.EndTime - in.StartTime})
		releaseMemory()
	}
	want := map[int32]int32{modRocketSplash: classSoldier, modSniperRifle: classSoldier, modAirstrike: classLT, modArty: classLT}
	right, wrong := map[int32]int{}, map[int32]int{}
	for _, ds := range byServer {
		if len(ds) < 4 { // a few POVs of one game, or the classes are not all known
			continue
		}
		ref := 0
		for i, f := range ds {
			if f.span > ds[ref].span {
				ref = i
			}
		}
		rep, _, err := MergeStream(len(ds), ref, func(i int) (*Demo, error) {
			data, err := os.ReadFile(ds[i].path)
			if err != nil {
				return nil, err
			}
			d, _ := ParseWith(data, 60, Options{Entities: KeepPlayersEvents})
			return d, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range rep.Events {
			wc, ok := want[e.MeansOfDeath]
			if !ok || (e.Type != replay.EventKill && e.Type != replay.EventTeamkill) {
				continue
			}
			i := sort.Search(len(rep.Frames), func(i int) bool { return rep.Frames[i].ServerTime >= e.ServerTime-300 })
			for j := i; j < len(rep.Frames) && j < i+8; j++ {
				found := false
				for _, s := range rep.Frames[j].Samples {
					if s.ClientNum == e.ActorClientNum && s.PlayerClass >= 0 {
						found = true
						if s.PlayerClass == wc {
							right[e.MeansOfDeath]++
						} else {
							wrong[e.MeansOfDeath]++
						}
					}
				}
				if found {
					break
				}
			}
		}
		rep = nil
		releaseMemory()
	}
	for mod := range want {
		r, w := right[mod], wrong[mod]
		t.Logf("means of death %d: killer has the expected class in %d of %d kills", mod, r, r+w)
		if r+w < 5 {
			t.Errorf("means of death %d: only %d kills with a known class to check", mod, r+w)
		} else if float64(r) < 0.9*float64(r+w) {
			t.Errorf("means of death %d: only %d of %d killers have the class %d", mod, r, r+w, want[mod])
		}
	}
}
