// s4ndmod launcher: keeps an S4NDMoD26 install current, then starts the game.
//
//	launcher                      update (if needed) and launch, connecting to the server
//	launcher --check              show what would change, change nothing
//	launcher --no-launch          update only
//	launcher --dir D --server URL --connect HOST
package main

import (
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var version = "dev" // set with -ldflags "-X main.version=..."

func main() {
	var (
		server   = flag.String("server", "https://s4ndmod.com", "server to update from")
		dir      = flag.String("dir", "", "install directory (default: "+defaultInstallDir()+")")
		connect  = flag.String("connect", "", "host to connect to (default: the --server host)")
		check    = flag.Bool("check", false, "report what would change and exit")
		noLaunch = flag.Bool("no-launch", false, "update only, do not start the game")
		showVer  = flag.Bool("version", false, "print the launcher version")
	)
	flag.Parse()
	if *showVer {
		fmt.Println("s4ndmod launcher", version)
		return
	}

	installDir := *dir
	if installDir == "" {
		installDir = defaultInstallDir()
	}
	if !*check {
		if err := os.MkdirAll(installDir, 0o755); err != nil {
			fatal("cannot create %s: %v", installDir, err)
		}
	}
	fmt.Println("Install directory:", installDir)

	client := &http.Client{} // no overall timeout: pak downloads are large
	m, err := fetchManifest(client, *server, platformName())
	exe := filepath.Join(installDir, gameBinary())
	if err != nil {
		if _, serr := os.Stat(exe); serr != nil || *check {
			fatal("cannot reach %s: %v", *server, err)
		}
		fmt.Printf("Could not check for updates (%v); launching the installed version.\n", err)
	} else {
		fmt.Println("Latest version:", m.Version)
		res, serr := Sync(client, *server, m, installDir, *check, progressPrinter())
		fmt.Println()
		if serr != nil {
			fatal("update failed: %v", serr)
		}
		report(res, *check)
		if *check {
			return
		}
	}

	if *noLaunch {
		return
	}
	host := *connect
	if host == "" {
		if u, err := url.Parse(*server); err == nil {
			host = u.Hostname()
		}
	}
	launch(installDir, exe, host)
}

func report(r *Result, dry bool) {
	verb := "Updated"
	if dry {
		verb = "Would update"
	}
	for _, p := range r.Downloaded {
		fmt.Printf("  %s %s\n", verb, p)
	}
	rm := "Removed"
	if dry {
		rm = "Would remove"
	}
	for _, p := range r.Pruned {
		fmt.Printf("  %s %s\n", rm, p)
	}
	if len(r.Downloaded) == 0 && len(r.Pruned) == 0 {
		fmt.Printf("Everything is up to date (%d files).\n", r.UpToDate)
	}
}

func progressPrinter() Progress {
	var last string
	var lastPct int64 = -1
	return func(file string, done, total int64) {
		pct := int64(0)
		if total > 0 {
			pct = done * 100 / total
		}
		if file == last && pct == lastPct {
			return
		}
		last, lastPct = file, pct
		fmt.Printf("\r  %-44s %3d%%", trunc(file, 44), pct)
	}
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n+3:]
}

func launch(dir, exe, host string) {
	args := []string{
		"+set", "fs_basepath", dir,
		"+set", "fs_homepath", dir,
		"+set", "fs_game", "s4ndmod26",
	}
	if host != "" {
		args = append(args, "+connect", host)
	}
	fmt.Println("Starting:", filepath.Base(exe), strings.Join(args, " "))
	cmd := exec.Command(exe, args...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		fatal("could not start the game: %v", err)
	}
	// the game runs on its own; the launcher's job is done
	time.Sleep(300 * time.Millisecond)
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", a...)
	os.Exit(1)
}
