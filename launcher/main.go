// s4ndmod launcher: keeps an S4NDMoD26 install current, then starts the game.
//
//	s4ndmod-launcher               window: updates if needed, then PLAY
//	s4ndmod-launcher --cli         same thing in the console (also --check / --no-launch)
//	  --check                      show what would change, change nothing
//	  --no-launch                  update only
//	  --dir D --server URL --connect HOST
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var version = "dev" // set with -ldflags "-X main.version=..."

func init() {
	// the native window must be created and run on the main thread
	runtime.LockOSThread()
}

func main() {
	var (
		server   = flag.String("server", "", "server to update from (default: saved setting, then "+defaultServer+")")
		dir      = flag.String("dir", "", "install directory (default: saved setting, then "+defaultInstallDir()+")")
		connect  = flag.String("connect", "", "host to connect to (default: the --server host)")
		cli      = flag.Bool("cli", false, "run in the console instead of opening the window")
		check    = flag.Bool("check", false, "report what would change and exit (implies --cli)")
		noLaunch = flag.Bool("no-launch", false, "update only, do not start the game (implies --cli)")
		showVer  = flag.Bool("version", false, "print the launcher version")
	)
	flag.Parse()

	cfg := loadSettings()
	if *server != "" {
		cfg.Server = strings.TrimRight(*server, "/")
	}
	if *dir != "" {
		cfg.Dir = *dir
	}
	if *connect != "" {
		cfg.Connect = *connect
	}

	if *showVer || *cli || *check || *noLaunch {
		attachConsole()
	}
	if *showVer {
		fmt.Println("s4ndmod launcher", version)
		return
	}
	if *cli || *check || *noLaunch {
		runCLI(cfg, *check, *noLaunch)
		return
	}
	runGUI(cfg)
}

func runGUI(cfg Settings) {
	up := NewUpdater(cfg)
	app, ln, err := NewApp(up)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	go (&http.Server{Handler: app.Handler()}).Serve(ln)

	// Check on start-up, and install what's needed without asking: that is the point of a launcher.
	go func() {
		up.Check()
		if up.Snapshot().Phase == phaseNeedsUpdate {
			up.Update()
		}
	}()

	if runWindow(app.URL(), app.quit) {
		return
	}
	// No native window (not Windows, or WebView2 missing): use the default browser.
	fmt.Fprintln(os.Stderr, "Opening the launcher in your browser. If nothing appears, open:", app.URL())
	openURL(app.URL())
	go app.WatchBrowserClose()
	<-app.quit
}

func runCLI(cfg Settings, check, noLaunch bool) {
	installDir := cfg.Dir
	if !check {
		if err := os.MkdirAll(installDir, 0o755); err != nil {
			fatal("cannot create %s: %v", installDir, err)
		}
	}
	fmt.Println("Install directory:", installDir)

	client := &http.Client{} // no overall timeout: pak downloads are large
	m, err := fetchManifest(client, cfg.Server, platformName())
	exe := filepath.Join(installDir, gameBinary())
	if err != nil {
		if _, serr := os.Stat(exe); serr != nil || check {
			fatal("cannot reach %s: %v", cfg.Server, err)
		}
		fmt.Printf("Could not check for updates (%v); launching the installed version.\n", err)
	} else {
		fmt.Println("Latest version:", m.Version)
		res, serr := Sync(client, cfg.Server, m, installDir, check, progressPrinter())
		fmt.Println()
		if serr != nil {
			fatal("update failed: %v", serr)
		}
		report(res, check)
		if check {
			return
		}
	}
	if noLaunch {
		return
	}
	up := NewUpdater(cfg)
	fmt.Println("Starting:", filepath.Base(exe))
	if err := startGame(installDir, exe, up.connectHost(), false); err != nil {
		fatal("could not start the game: %v", err)
	}
}

func report(r *Result, dry bool) {
	verb, rm := "Updated", "Removed"
	if dry {
		verb, rm = "Would update", "Would remove"
	}
	for _, p := range r.Downloaded {
		fmt.Printf("  %s %s\n", verb, p)
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
		name := file
		if len(name) > 44 {
			name = "..." + name[len(name)-41:]
		}
		fmt.Printf("\r  %-44s %3d%%", name, pct)
	}
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", a...)
	os.Exit(1)
}
