package main

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// Phases the UI renders.
const (
	phaseSetup          = "setup"           // first run: the install folder has not been confirmed yet
	phaseChecking       = "checking"        // read-only: asking the server, comparing files
	phaseNeedsUpdate    = "needs_update"    // game files to install/update; Play will download them
	phaseLauncherUpdate = "launcher_update" // a newer launcher exists; Play is blocked until it is installed
	phaseUpdating       = "updating"        // downloading, because the user asked
	phaseReady          = "ready"
	phaseLaunching      = "launching"
	phaseError          = "error"
)

// LogLine is one line of the console pane.  The log lives here (not in the page) so a page that
// connects late, or reloads, still shows what has happened.
type LogLine struct {
	Cls  string `json:"cls,omitempty"` // "", "w" (warning), "e" (error), "d" (dim)
	Text string `json:"text"`
}

// Snapshot is the whole UI state; it is sent to the page as JSON.
type Snapshot struct {
	Phase      string    `json:"phase"`
	Message    string    `json:"message"`
	Version    string    `json:"version"`
	Dir        string    `json:"dir"`
	Server     string    `json:"server"`
	Connect    string    `json:"connect"`
	Files      int       `json:"files"`      // files still to download
	Updates    []string  `json:"updates"`    // names of those files
	BytesTotal int64     `json:"bytesTotal"` // to download
	BytesDone  int64     `json:"bytesDone"`
	Current    string    `json:"current"`
	Offline    bool      `json:"offline"` // server unreachable, playing the installed copy
	Error      string    `json:"error,omitempty"`
	Launcher   string    `json:"launcher"`
	Log        []LogLine `json:"log"`

	Installed       bool     `json:"installed"`       // the game binary exists in Dir
	Retail          bool     `json:"retail"`          // Dir holds the retail game files (not the demo)
	CanRevert       bool     `json:"canRevert"`       // an import record exists, so Revert can remove what was copied
	RetailBad       []string `json:"retailBad"`       // retail files that failed the deep check (Verify files)
	Note            string   `json:"note,omitempty"`  // why the install folder was refused, shown during setup
	FirstRun        bool     `json:"firstRun"`        // the install folder is not confirmed yet
	LauncherUpdate  bool     `json:"launcherUpdate"`  // a newer, signed launcher is available
	LauncherVersion string   `json:"launcherVersion"` // that launcher's build
}

// Updater owns the settings, the manifest/plan, and fans state out to connected pages.
type Updater struct {
	client *http.Client

	mu       sync.Mutex
	cfg      Settings
	snap     Snapshot
	plan     *Plan
	manifest *Manifest
	busy     bool
	subs     map[chan Snapshot]struct{}
	lastPush time.Time

	// Set by main after NewUpdater.
	selfPath   string   // this launcher's executable
	skipSelf   bool     // never offer a launcher update (dev builds, --no-self-update, just updated)
	args       []string // this process's arguments, passed on when the launcher relaunches itself
	firstRun   bool     // the install folder has not been confirmed: do nothing until it is
	setupNote  string   // set when the saved install folder was refused (it is an RTCW install)
	deep       bool     // the next Check also re-hashes the retail files (Verify files)
	onLaunched func()   // the game was started: the launcher can close
	onRestart  func()   // the launcher replaced itself and relaunched: this process can close
	startGame  func(dir, exe, host string) error
}

func NewUpdater(cfg Settings) *Updater {
	u := &Updater{client: &http.Client{}, cfg: cfg, subs: map[chan Snapshot]struct{}{},
		startGame: func(dir, exe, host string) error { return startGame(dir, exe, host, false) }}
	u.snap = Snapshot{Phase: phaseChecking, Message: "Starting…", Launcher: launcherVersion()}
	u.snap.logf("", "S4NDMoD26 launcher %s", launcherVersion())
	u.fillCfg()
	return u
}

func (u *Updater) fillCfg() {
	u.snap.Dir, u.snap.Server, u.snap.Connect = u.cfg.Dir, u.cfg.Server, u.connectHost()
}

func (u *Updater) connectHost() string {
	if u.cfg.Connect != "" {
		return u.cfg.Connect
	}
	if p, err := url.Parse(u.cfg.Server); err == nil {
		return p.Hostname()
	}
	return ""
}

func (u *Updater) Snapshot() Snapshot {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.snap
}

func (u *Updater) Subscribe() (chan Snapshot, func()) {
	ch := make(chan Snapshot, 8)
	u.mu.Lock()
	u.subs[ch] = struct{}{}
	ch <- u.snap
	u.mu.Unlock()
	return ch, func() {
		u.mu.Lock()
		delete(u.subs, ch)
		u.mu.Unlock()
	}
}

// logf appends a console line; call it inside a set() callback (the lock is held).
func (s *Snapshot) logf(cls, format string, a ...any) {
	s.Log = append(s.Log, LogLine{Cls: cls, Text: fmt.Sprintf(format, a...)})
	if len(s.Log) > 300 {
		s.Log = s.Log[len(s.Log)-300:]
	}
}

// set mutates the snapshot under the lock and pushes it; force=false throttles progress chatter.
func (u *Updater) set(force bool, f func(s *Snapshot)) {
	u.mu.Lock()
	defer u.mu.Unlock()
	f(&u.snap)
	if !force && time.Since(u.lastPush) < 80*time.Millisecond {
		return
	}
	u.lastPush = time.Now()
	for ch := range u.subs {
		select {
		case ch <- u.snap:
		default: // a slow page just misses an intermediate frame
		}
	}
}

func (u *Updater) exePath() string {
	return filepath.Join(u.cfg.Dir, gameBinary())
}

func (u *Updater) installed() bool {
	_, err := os.Stat(u.exePath())
	return err == nil
}

// Start is what runs when the launcher opens.  It never downloads or installs anything: on the first run
// it waits for the user to confirm where the game goes; otherwise it only checks.
func (u *Updater) Start() {
	u.mu.Lock()
	first := u.firstRun
	u.mu.Unlock()
	if first {
		u.set(true, func(s *Snapshot) {
			s.Phase, s.FirstRun, s.Installed = phaseSetup, true, u.installed()
			s.Message = "Choose an install folder."
			if u.setupNote != "" {
				s.Note = u.setupNote
				s.logf("e", "%s", u.setupNote)
			}
		})
		return
	}
	go u.Check()
}

// Check asks the server what a current install is and compares it with disk.  It is read-only: it never
// downloads, installs or replaces anything.  The result is phaseLauncherUpdate, phaseNeedsUpdate,
// phaseReady or phaseError.
func (u *Updater) Check() {
	u.mu.Lock()
	if u.busy {
		u.mu.Unlock()
		return
	}
	u.busy = true
	cfg := u.cfg
	u.mu.Unlock()
	defer func() { u.mu.Lock(); u.busy = false; u.mu.Unlock() }()

	u.set(true, func(s *Snapshot) {
		s.Phase, s.Message, s.Error, s.Offline = phaseChecking, "Checking for updates…", "", false
		s.Files, s.Updates, s.BytesTotal, s.BytesDone, s.Current = 0, nil, 0, 0, ""
		s.FirstRun, s.LauncherUpdate, s.LauncherVersion, s.Installed = false, false, "", u.installed()
		s.Retail = retailInstalled(cfg.Dir)
		s.CanRevert = canRevert(cfg.Dir)
		s.RetailBad = nil
		s.logf("", "Contacting %s …", cfg.Server)
	})
	u.mu.Lock()
	deep := u.deep
	u.deep = false
	u.mu.Unlock()
	if deep && retailInstalled(cfg.Dir) {
		u.verifyRetail(cfg.Dir)
	}
	m, err := fetchManifest(u.client, cfg.Server, platformName())
	if err != nil {
		if u.installed() {
			u.set(true, func(s *Snapshot) {
				s.Phase, s.Offline = phaseReady, true
				s.Message = "Can't reach the update server, but you can still play."
				s.logf("w", "Server unreachable (%v); using the installed copy.", err)
			})
			return
		}
		u.set(true, func(s *Snapshot) {
			s.Phase, s.Message = phaseError, "Can't reach the update server."
			s.Error = err.Error()
			s.logf("e", "Can't reach %s: %v", cfg.Server, err)
		})
		return
	}
	applyEdition(m, cfg.Dir)
	u.mu.Lock()
	u.manifest = m
	u.mu.Unlock()
	if !u.skipSelf && SelfUpdateAvailable(m, u.selfPath) {
		// Offered, never applied on its own: Play is blocked until the user updates the launcher.
		u.set(true, func(s *Snapshot) {
			s.Phase, s.Version = phaseLauncherUpdate, m.Version
			s.LauncherUpdate, s.LauncherVersion = true, m.LauncherVersion
			s.Message = "A launcher update is available."
			s.logf("w", "New launcher %s is available (this is %s).", m.LauncherVersion, launcherVersion())
		})
		return
	}
	u.set(true, func(s *Snapshot) {
		s.Message, s.Version = "Verifying game files…", m.Version
		s.logf("", "Latest build %s. Verifying files in %s", m.Version, cfg.Dir)
	})
	p := MakePlan(m, cfg.Dir)
	u.mu.Lock()
	u.plan = p
	u.mu.Unlock()
	if p.Empty() {
		u.set(true, func(s *Snapshot) {
			s.Phase, s.Message = phaseReady, "You're up to date."
			s.logf("", "All %d files verified. Ready.", p.UpToDate)
		})
		return
	}
	names := make([]string, len(p.Download))
	for i, f := range p.Download {
		names[i] = f.Path
	}
	u.set(true, func(s *Snapshot) {
		s.Phase, s.Files, s.Updates, s.BytesTotal = phaseNeedsUpdate, len(p.Download), names, p.BytesTotal
		if s.Installed {
			s.Message = "An update is available."
		} else {
			s.Message = "Not installed."
		}
		s.logf("w", "%d file(s), %.1f MB to download.", len(p.Download), float64(p.BytesTotal)/1048576)
		for _, n := range names {
			s.logf("d", "  %s", n)
		}
		for _, n := range p.Prune {
			s.logf("d", "  remove %s", n)
		}
	})
}

// Verify is the "Verify files" action: a normal check that also re-hashes the retail files, which a normal
// check only compares by size.
func (u *Updater) Verify() {
	u.mu.Lock()
	u.deep = true
	u.mu.Unlock()
	u.Check()
}

// verifyRetail hashes every retail pak in the install and reports the damaged ones by name.
func (u *Updater) verifyRetail(dir string) {
	var bad []string
	for _, p := range retailPaks {
		path := filepath.Join(dir, "main", p.Name)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		u.set(false, func(s *Snapshot) { s.Message, s.Current = "Verifying retail files…", p.Name })
		if !fileMatches(path, p) {
			bad = append(bad, p.Name)
			u.set(true, func(s *Snapshot) { s.logf("e", "%s is damaged", p.Name) })
		}
	}
	u.set(true, func(s *Snapshot) {
		s.RetailBad, s.Current = bad, ""
		if len(bad) == 0 {
			s.logf("", "Retail files verified.")
		}
	})
}

// download carries out the plan from the last Check.  It only ever runs because the user pressed a
// button (Play, or "Update only").  It returns an error message-worthy error, leaving the phase to the caller.
func (u *Updater) download(plan *Plan, cfg Settings) error {
	done := map[string]int64{}
	var sum int64
	u.set(true, func(s *Snapshot) {
		s.Phase, s.Message, s.BytesDone = phaseUpdating, "Downloading…", 0
		s.logf("", "Downloading…")
	})
	var announced string
	err := plan.Apply(u.client, cfg.Server, cfg.Dir, func(file string, d, total int64) {
		sum += d - done[file]
		done[file] = d
		first := file != announced
		announced = file
		u.set(first, func(s *Snapshot) {
			s.Current, s.BytesDone = file, sum
			if first {
				s.logf("", "  %s", file)
			}
		})
	})
	if err != nil {
		u.set(true, func(s *Snapshot) {
			s.Phase, s.Message, s.Error = phaseError, "The update failed.", err.Error()
			s.logf("e", "Update failed: %v", err)
		})
		return err
	}
	u.set(true, func(s *Snapshot) { s.logf("", "Download complete.") })
	return nil
}

// begin marks the updater busy for a user-initiated action; it reports false if something is already running.
func (u *Updater) begin() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.busy {
		return false
	}
	u.busy = true
	return true
}

func (u *Updater) end() { u.mu.Lock(); u.busy = false; u.mu.Unlock() }

// CanPlay says whether pressing Play is allowed now, and if not why.  Play is blocked while a launcher
// update is pending, during setup, and while another action runs.
func (u *Updater) CanPlay() (bool, string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	switch {
	case u.busy:
		return false, "busy"
	case u.snap.Phase == phaseLauncherUpdate:
		return false, "a launcher update must be installed first"
	case u.snap.Phase == phaseSetup || u.firstRun:
		return false, "confirm the install folder first"
	case u.snap.Phase == phaseReady:
		return true, ""
	case u.snap.Phase == phaseNeedsUpdate:
		return false, "install or update first"
	}
	return false, "not ready (" + u.snap.Phase + ")"
}

// Update downloads the game files the last Check found.  It is what the "Install" / "Update" button does, and
// nothing else downloads game files.  When it finishes the files are re-verified and the button becomes "Play".
func (u *Updater) Update() {
	u.mu.Lock()
	plan, cfg, phase := u.plan, u.cfg, u.snap.Phase
	u.mu.Unlock()
	if plan == nil || phase != phaseNeedsUpdate || !u.begin() {
		return
	}
	err := u.download(plan, cfg)
	u.end()
	if err == nil {
		u.Check() // confirm the result against the server's hashes
	}
}

// Play starts the game.  It never downloads anything: when the game needs installing or updating, the
// button reads "Install"/"Update" instead (see Update), and Play only appears once that has finished.  It is
// refused (CanPlay) while a launcher update is pending.
func (u *Updater) Play() {
	if ok, _ := u.CanPlay(); !ok || !u.begin() {
		return
	}
	defer u.end()
	u.mu.Lock()
	cfg, start := u.cfg, u.startGame
	u.mu.Unlock()
	if err := start(cfg.Dir, filepath.Join(cfg.Dir, gameBinary()), u.connectHost()); err != nil {
		u.set(true, func(s *Snapshot) {
			s.Phase, s.Message, s.Error = phaseError, "Couldn't start the game.", err.Error()
			s.logf("e", "Couldn't start the game: %v", err)
		})
		return
	}
	u.set(true, func(s *Snapshot) {
		s.Phase, s.Message = phaseLaunching, "Starting the game…"
		s.logf("", "Starting the game → %s", u.connectHost())
	})
	if u.onLaunched != nil {
		u.onLaunched()
	}
}

// UpdateLauncher replaces this launcher with the server's signed build and relaunches.  It only runs when the
// user pressed "Update launcher".
func (u *Updater) UpdateLauncher() {
	u.mu.Lock()
	m, phase := u.manifest, u.snap.Phase
	u.mu.Unlock()
	if m == nil || phase != phaseLauncherUpdate || !u.begin() {
		return
	}
	defer u.end()
	u.set(true, func(s *Snapshot) {
		s.Message = "Updating the launcher…"
		s.logf("", "Downloading the new launcher…")
	})
	ok, err := SelfUpdate(u.client, u.cfg.Server, m, u.selfPath, u.args)
	if err != nil {
		u.set(true, func(s *Snapshot) {
			s.Message = "The launcher update failed. You can try again."
			s.Error = err.Error()
			s.logf("e", "Launcher update failed: %v", err)
		})
		return
	}
	if ok {
		u.set(true, func(s *Snapshot) { s.logf("", "Restarting…") })
		if u.onRestart != nil {
			u.onRestart()
		}
	}
}

// Configure changes server/dir/connect, saves them, and re-checks.
func (u *Updater) Configure(in Settings) error {
	u.mu.Lock()
	dir := in.Dir
	if dir == "" {
		dir = u.cfg.Dir
	}
	u.mu.Unlock()
	if err := checkInstallDir(dir); err != nil {
		return err
	}
	u.mu.Lock()
	if u.busy {
		u.mu.Unlock()
		return fmt.Errorf("busy")
	}
	if in.Server != "" {
		u.cfg.Server = in.Server
	}
	if in.Dir != "" {
		u.cfg.Dir = in.Dir
	}
	u.cfg.Connect = in.Connect
	u.firstRun, u.setupNote = false, ""
	u.fillCfg()
	u.snap.FirstRun = false
	cfg := u.cfg
	u.mu.Unlock()
	if err := cfg.save(); err != nil {
		return err
	}
	go u.Check()
	return nil
}

// startGame launches the engine detached, with base and home path both the install dir.
func startGame(dir, exe, host string, wait bool) error {
	args := []string{"+set", "fs_basepath", dir, "+set", "fs_homepath", dir, "+set", "fs_game", "s4ndmod26"}
	if host != "" {
		args = append(args, "+connect", host)
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		return err
	}
	if wait {
		return cmd.Wait()
	}
	return nil
}

// FindRetail looks for an RTCW install in the usual places.
func (u *Updater) FindRetail() []string { return findRetailFolders() }

// ImportRetail verifies the RTCW folder the user chose, copies its official paks into the install and drops
// the demo pak.  It only runs on the user's click and never downloads anything.
func (u *Updater) ImportRetail(src string) error {
	u.mu.Lock()
	cfg, phase := u.cfg, u.snap.Phase
	u.mu.Unlock()
	if phase == phaseSetup || phase == phaseLauncherUpdate || !u.begin() {
		return fmt.Errorf("busy")
	}
	if err := checkImportSource(src, cfg.Dir); err != nil {
		u.end()
		u.set(true, func(s *Snapshot) { s.logf("e", "%v", err) })
		return err
	}
	u.set(true, func(s *Snapshot) {
		s.Phase, s.Message, s.Error, s.BytesTotal, s.BytesDone, s.Current = phaseUpdating, "Verifying your game files…", "", 0, 0, ""
		s.logf("", "Checking %s", src)
	})
	fail := func(err error) error {
		u.end()
		u.set(true, func(s *Snapshot) {
			s.Phase, s.Message, s.Error = phaseError, "Couldn't use those files.", err.Error()
			s.logf("e", "%v", err)
		})
		return err
	}
	pl, err := planRetailImport(src, func(name string) {
		u.set(false, func(s *Snapshot) { s.Current = name })
	})
	if err != nil {
		return fail(err)
	}
	if free, ok := freeSpace(cfg.Dir); ok && free < pl.Bytes+(64<<20) {
		return fail(fmt.Errorf("Not enough free space: %.0f MB needed, %.0f MB available.", float64(pl.Bytes)/1048576, float64(free)/1048576))
	}
	u.set(true, func(s *Snapshot) {
		s.Message, s.BytesTotal = "Copying…", pl.Bytes
		for _, n := range pl.Skipped {
			s.logf("w", "Skipped %s (not the official file).", n)
		}
	})
	var done int64
	err = copyRetail(pl, cfg.Dir, func(name string, n int64) {
		done += n
		u.set(false, func(s *Snapshot) { s.Current, s.BytesDone = name, done })
	}, func(cls, text string) {
		u.set(true, func(s *Snapshot) { s.logf(cls, "%s", text) })
	})
	if err != nil {
		return fail(err)
	}
	u.set(true, func(s *Snapshot) {
		s.logf("", "Done. Your original RTCW folder was not changed.")
	})
	u.end()
	u.Check()
	return nil
}

// RevertToDemo removes the retail files this launcher imported (and only those, per the import journal),
// naming each one.  The demo pak comes back on the user's next click of Install; nothing downloads here.
func (u *Updater) RevertToDemo() error {
	u.mu.Lock()
	cfg, phase := u.cfg, u.snap.Phase
	u.mu.Unlock()
	if phase == phaseSetup || phase == phaseLauncherUpdate || !u.begin() {
		return fmt.Errorf("busy")
	}
	say := func(cls, text string) { u.set(true, func(s *Snapshot) { s.logf(cls, "%s", text) }) }
	removed, kept, err := revertRetail(cfg.Dir, say)
	if err != nil {
		u.end()
		say("e", err.Error())
		return err
	}
	if kept > 0 {
		say("w", fmt.Sprintf("%d file(s) were kept.", kept))
	}
	say("", fmt.Sprintf("Reverted: %d file(s) removed.", removed))
	u.end()
	u.Check()
	return nil
}

func (u *Updater) canSwitchEdition() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return !u.busy && u.snap.Phase != phaseSetup && u.snap.Phase != phaseLauncherUpdate
}
