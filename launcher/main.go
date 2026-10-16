// s4ndmod launcher: installs and updates S4NDMoD26 when you ask, and starts the game.
//
//	s4ndmod-launcher               window.  It only checks on start-up; nothing is downloaded or replaced until
//	                               you press a button (Play updates the game first if it needs it).
//	s4ndmod-launcher --cli         the same in the console: like pressing Play
//	  --check                      show what would change, change nothing
//	  --no-launch                  update the game files only
//	  --update-launcher            replace this launcher with the server's signed build, then exit
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
		server      = flag.String("server", "", "server to update from (default: saved setting, then "+defaultServer+")")
		dir         = flag.String("dir", "", "install directory (default: saved setting, otherwise the folder the launcher is in)")
		connect     = flag.String("connect", "", "host to connect to (default: the --server host)")
		cli         = flag.Bool("cli", false, "run in the console instead of opening the window")
		check       = flag.Bool("check", false, "report what would change and exit (implies --cli)")
		noLaunch    = flag.Bool("no-launch", false, "update the game files only, do not start the game (implies --cli)")
		updLauncher = flag.Bool("update-launcher", false, "replace this launcher with the server's signed build and exit (implies --cli)")
		showVer     = flag.Bool("version", false, "print the launcher version")
		noSelf      = flag.Bool("no-self-update", false, "never offer a launcher update")
		updated     = flag.Bool("updated", false, "(internal) set on the process a launcher update started")
	)
	flag.Parse()

	exe, _ := os.Executable()
	cleanupOldLauncher(exe) // the previous version, moved aside by a launcher update
	skipSelf := *noSelf || *updated || version == "dev"

	cfg, found := loadSettings()
	if *server != "" {
		cfg.Server = strings.TrimRight(*server, "/")
	}
	if *dir != "" {
		cfg.Dir = *dir
	}
	if *connect != "" {
		cfg.Connect = *connect
	}
	// first run: no saved settings and no --dir, so the user has to confirm the install folder
	firstRun := !found && *dir == ""

	if *showVer || *cli || *check || *noLaunch || *updLauncher {
		attachConsole()
	}
	if *showVer {
		fmt.Println("s4ndmod launcher", version)
		return
	}
	if *cli || *check || *noLaunch || *updLauncher {
		runCLI(cfg, cliOptions{check: *check, noLaunch: *noLaunch, updateLauncher: *updLauncher, self: exe, skipSelf: skipSelf})
		return
	}
	runGUI(cfg, exe, skipSelf, firstRun)
}

func runGUI(cfg Settings, exe string, skipSelf, firstRun bool) {
	up := NewUpdater(cfg)
	up.selfPath, up.skipSelf, up.args, up.firstRun = exe, skipSelf, os.Args[1:], firstRun
	app, ln, err := NewApp(up)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	go (&http.Server{Handler: app.Handler()}).Serve(ln)

	// Opening the launcher only ever checks.  Installing, updating the game and updating the launcher all wait
	// for the user to press a button.
	up.Start()

	if runWindow(app.URL(), app.quit) {
		return
	}
	// No native window (not Windows, or WebView2 missing): use the default browser.
	fmt.Fprintln(os.Stderr, "Opening the launcher in your browser. If nothing appears, open:", app.URL())
	openURL(app.URL())
	go app.WatchBrowserClose()
	<-app.quit
}

type cliOptions struct {
	check, noLaunch, updateLauncher bool
	self                            string
	skipSelf                        bool
}

func runCLI(cfg Settings, o cliOptions) {
	installDir := cfg.Dir
	client := &http.Client{} // no overall timeout: pak downloads are large
	m, err := fetchManifest(client, cfg.Server, platformName())
	gameExe := filepath.Join(installDir, gameBinary())
	if err != nil {
		if _, serr := os.Stat(gameExe); serr != nil || o.check || o.updateLauncher || o.noLaunch {
			fatal("cannot reach %s: %v", cfg.Server, err)
		}
		fmt.Printf("Could not check for updates (%v); launching the installed version.\n", err)
		m = nil
	}

	if m != nil && !o.skipSelf && SelfUpdateAvailable(m, o.self) {
		switch {
		case o.updateLauncher:
			// explicit request: replace the launcher, but do not start a second copy of it
			relaunch = func(string, []string) error { return nil }
			if _, uerr := SelfUpdate(client, cfg.Server, m, o.self, nil); uerr != nil {
				fatal("launcher update failed: %v", uerr)
			}
			fmt.Println("Launcher updated to", m.LauncherVersion)
			return
		case o.check:
			fmt.Println("A launcher update is available (", m.LauncherVersion, "); run with --update-launcher.")
		default:
			fatal("a launcher update is available (%s). Run with --update-launcher first, then try again.", m.LauncherVersion)
		}
	} else if o.updateLauncher {
		fmt.Println("The launcher is up to date.")
		return
	}

	fmt.Println("Install directory:", installDir)
	if m != nil {
		fmt.Println("Latest version:", m.Version)
		if !o.check {
			if err := os.MkdirAll(installDir, 0o755); err != nil {
				fatal("cannot create %s: %v", installDir, err)
			}
		}
		res, serr := Sync(client, cfg.Server, m, installDir, o.check, progressPrinter())
		fmt.Println()
		if serr != nil {
			fatal("update failed: %v", serr)
		}
		report(res, o.check)
		if o.check {
			return
		}
	}
	if o.noLaunch {
		return
	}
	up := NewUpdater(cfg)
	fmt.Println("Starting:", filepath.Base(gameExe))
	if err := startGame(installDir, gameExe, up.connectHost(), false); err != nil {
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
