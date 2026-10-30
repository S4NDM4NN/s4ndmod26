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
)

func main() {
	nFrames := flag.Int("frames", 5, "frames to print")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: demoinspect [-frames N] file.dm_NN")
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
	d, perr := demo.Parse(data, proto)
	if d == nil {
		fmt.Fprintln(os.Stderr, perr)
		os.Exit(1)
	}
	fmt.Printf("protocol=%d clientNum=%d frames=%d commands=%d parseErr=%v\n",
		d.Protocol, d.ClientNum, len(d.Frames), len(d.Commands), perr)
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
