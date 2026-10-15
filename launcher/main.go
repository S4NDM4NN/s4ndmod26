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
	"time"
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
		noSelf   = flag.Bool("no-self-update", false, "do not replace the launcher with the server's copy")
		updated  = flag.Bool("updated", false, "(internal) set on the process a self-update started")
	)
	flag.Parse()

	exe, _ := os.Executable()
	cleanupOldLauncher(exe) // the previous version, moved aside by a self-update
	skipSelf := *noSelf || *updated || version == "dev"

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
		runCLI(cfg, *check, *noLaunch, exe, skipSelf)
		return
	}
	runGUI(cfg, exe, skipSelf)
}

func runGUI(cfg Settings, exe string, skipSelf bool) {
	up := NewUpdater(cfg)
	var app *App
	if !skipSelf {
		up.selfUpdate = func(m *Manifest) bool {
			if !SelfUpdateAvailable(m, exe) {
				return false
			}
			up.set(true, func(s *Snapshot) {
				s.Phase, s.Message = phaseChecking, "Updating the launcher…"
				s.logf("w", "New launcher %s available; updating…", m.LauncherVersion)
			})
			ok, err := SelfUpdate(up.client, cfg.Server, m, exe, os.Args[1:])
			if err != nil {
				// keep going with the current launcher; the game update does not depend on this
				up.set(true, func(s *Snapshot) { s.logf("e", "Launcher update failed: %v", err) })
				return false
			}
			if ok {
				up.set(true, func(s *Snapshot) { s.logf("", "Restarting…") })
				time.Sleep(300 * time.Millisecond)
				app.Quit()
			}
			return ok
		}
	}
	app2, ln, err := NewApp(up)
	app = app2
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

func runCLI(cfg Settings, check, noLaunch bool, self string, skipSelf bool) {
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
		if !skipSelf && !check && SelfUpdateAvailable(m, self) {
			fmt.Println("Updating the launcher…")
			if ok, uerr := SelfUpdate(client, cfg.Server, m, self, os.Args[1:]); uerr != nil {
				fmt.Println("Launcher update failed:", uerr)
			} else if ok {
				fmt.Println("Restarted as the new launcher.")
				return
			}
		} else if check && SelfUpdateAvailable(m, self) {
			fmt.Println("  Would update the launcher")
		}
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
