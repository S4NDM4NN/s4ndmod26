// demoinspect prints what the demo parser can read from a client demo.
// Usage: demoinspect [-frames N] file.dm_NN
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"s4ndmod26/status-api/demo"
	"s4ndmod26/status-api/replay"
)

func main() {
	nFrames := flag.Int("frames", 5, "frames to print")
	toReplay := flag.Bool("replay", false, "convert to a replay and print the analysis summary")
	merge := flag.Bool("merge", false, "treat every argument as a demo of one game, merge them and print the analysis summary")
	flag.Parse()
	if *merge {
		mergeFiles(flag.Args())
		return
	}
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: demoinspect [-frames N] [-replay] file.dm_NN | demoinspect -merge file.dm_NN ...")
		os.Exit(2)
	}
	path := flag.Arg(0)
	ext := strings.TrimPrefix(filepath.Ext(path), ".dm_")
	proto, err := strconv.Atoi(ext)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot infer protocol from extension:", ext)
		os.Exit(2)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	opt := demo.Options{}
	if *toReplay {
		opt.Entities = demo.KeepPlayersEvents // all ToReplay reads; full retention costs gigabytes
	}
	d, perr := demo.ParseWith(data, proto, opt)
	if d == nil {
		fmt.Fprintln(os.Stderr, perr)
		os.Exit(1)
	}
	fmt.Printf("protocol=%d clientNum=%d frames=%d commands=%d parseErr=%v\n",
		d.Protocol, d.ClientNum, len(d.Frames), len(d.Commands), perr)
	if *toReplay {
		summarize(d)
		return
	}
	fmt.Printf("serverinfo: %s\n", d.Configstrings[0])
	var keys []int
	for k := range d.Configstrings {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	for _, k := range keys {
		if s := d.Configstrings[k]; strings.HasPrefix(s, "n\\") {
			fmt.Printf("cs[%d] %s\n", k, s)
		}
	}
	ox, oy, oz := d.Layout.PlayerIdx("origin[0]"), d.Layout.PlayerIdx("origin[1]"), d.Layout.PlayerIdx("origin[2]")
	for i, f := range d.Frames {
		if i >= *nFrames {
			break
		}
		fmt.Printf("frame %d msg=%d t=%d ents=%d origin=(%.0f %.0f %.0f)\n", i, f.MessageNum, f.ServerTime,
			len(f.Entities), demo.Float(f.PS.F[ox]), demo.Float(f.PS.F[oy]), demo.Float(f.PS.F[oz]))
	}
	if n := len(d.Frames); n > 0 {
		f := d.Frames[n-1]
		fmt.Printf("last  %d msg=%d t=%d ents=%d origin=(%.0f %.0f %.0f)\n", n-1, f.MessageNum, f.ServerTime,
			len(f.Entities), demo.Float(f.PS.F[ox]), demo.Float(f.PS.F[oy]), demo.Float(f.PS.F[oz]))
	}
	for i, c := range d.Commands {
		if i >= 8 {
			break
		}
		fmt.Printf("cmd t=%d #%d %q\n", c.ServerTime, c.Seq, c.Text)
	}
}

func summarize(d *demo.Demo) {
	rep, r := demo.ToReplay(d)
	fmt.Printf("replay: map=%s gametype=%d recordMsec=%d frames=%d playerSamples=%d own=%d dead=%d rosterPlayers=%d obituaries=%d (+%d in warm-up, dropped)\n",
		rep.Header.MapName, rep.Header.Gametype, rep.Header.RecordMsec, r.Frames, r.PlayerSamples, r.OwnSamples, r.DeadSamples, r.Players, r.Obituaries, r.WarmupObituaries)
	surveyCommands(d)
	analyze(rep)
}

// mergeFiles parses each file (players and events only, to bound memory),
// merges them and prints what the merge achieved.
func mergeFiles(paths []string) {
	var ds []*demo.Demo
	for _, path := range paths {
		proto, err := strconv.Atoi(strings.TrimPrefix(filepath.Ext(path), ".dm_"))
		if err != nil {
			fmt.Fprintln(os.Stderr, "cannot infer protocol from", path)
			os.Exit(2)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		d, perr := demo.ParseWith(data, proto, demo.Options{Entities: demo.KeepPlayersEvents})
		if d == nil {
			fmt.Fprintln(os.Stderr, path, perr)
			os.Exit(1)
		}
		ds = append(ds, d)
	}
	rep, mr, err := demo.MergeDemos(ds)
	for _, p := range mr.Parts {
		state := "merged"
		if !p.Used {
			state = "LEFT OUT: " + p.Reason
		}
		fmt.Printf("  rec=%-2d %-20s spectator=%-5v %.1f -> %.1f players/frame  %s\n", p.Recorder, p.Name, p.Spectator, p.AvgPlayers, p.MergedAvgPlayers, state)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("merged: frames=%d avgPlayers=%.1f playersWithRealHealth=%d events %d -> %d\n", mr.Frames, mr.AvgPlayers, mr.OwnHealthPlayers, mr.RawEvents, mr.Events)
	analyze(rep)
}

func analyze(rep *replay.Replay) {
	a := replay.Analyze(rep, "")
	b, err := replay.MarshalJSON(a)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	fmt.Printf("analysis: %d players, %d events, %d engagements, json=%d bytes\n",
		len(a.Players), len(a.Events), len(a.Engagements), len(b))
	byType := map[string]int{}
	for _, e := range rep.Events {
		byType[e.Type.String()]++
	}
	fmt.Println("events by type:", byType)
	var ids []string
	for k := range a.Players {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	for _, k := range ids {
		p := a.Players[k]
		fmt.Printf("  %-3s %-22s team=%d class=%d maxHP=%d kills=%d deaths=%d dmgDealt=%d dmgTaken=%d\n", k, p.DisplayName, p.Team, p.PlayerClass, p.MaxHealth, p.Kills, p.Deaths, p.TotalDamageDealt, p.TotalDamageReceived)
	}
}

// surveyCommands shows which server commands a demo carries, to judge what
// events can be reconstructed from them.
func surveyCommands(d *demo.Demo) {
	counts := map[string]int{}
	shown := 0
	for _, c := range d.Commands {
		f := strings.SplitN(c.Text, " ", 2)[0]
		counts[f]++
		if f == "print" && shown < 25 && (strings.Contains(c.Text, "kill") || strings.Contains(c.Text, "gib") || strings.Contains(c.Text, "died") || strings.Contains(c.Text, "ELIM")) {
			fmt.Printf("  t=%d %q\n", c.ServerTime, c.Text)
			shown++
		}
	}
	var ks []string
	for k := range counts {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool { return counts[ks[i]] > counts[ks[j]] })
	fmt.Print("command kinds:")
	for _, k := range ks {
		fmt.Printf(" %s=%d", k, counts[k])
	}
	fmt.Println()
}
