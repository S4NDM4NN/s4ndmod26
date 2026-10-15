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
	phaseChecking    = "checking"
	phaseNeedsUpdate = "needs_update"
	phaseUpdating    = "updating"
	phaseReady       = "ready"
	phaseLaunching   = "launching"
	phaseError       = "error"
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

	// selfUpdate, if set, is called with each fetched manifest; it returns true when the launcher
	// replaced itself and relaunched (so the rest of the check must not run).
	selfUpdate func(m *Manifest) bool
}

func NewUpdater(cfg Settings) *Updater {
	u := &Updater{client: &http.Client{}, cfg: cfg, subs: map[chan Snapshot]struct{}{}}
	u.snap = Snapshot{Phase: phaseChecking, Message: "Starting…", Launcher: version}
	u.snap.logf("", "S4NDMoD26 launcher %s", version)
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

// Check asks the server what a current install is and compares it with disk.  It does not change
// anything.  The result is phaseNeedsUpdate, phaseReady or phaseError.
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
		s.logf("", "Contacting %s …", cfg.Server)
	})
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
	if u.selfUpdate != nil && u.selfUpdate(m) {
		return // a new launcher has been started; this process is about to exit
	}
	u.set(true, func(s *Snapshot) {
		s.Message, s.Version = "Verifying game files…", m.Version
		s.logf("", "Latest build %s. Verifying files in %s", m.Version, cfg.Dir)
	})
	p := MakePlan(m, cfg.Dir)
	u.mu.Lock()
	u.manifest, u.plan = m, p
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
		s.Message = "An update is available."
		s.logf("w", "Update needed: %d file(s), %.1f MB", len(p.Download), float64(p.BytesTotal)/1048576)
		for _, n := range names {
			s.logf("d", "  %s", n)
		}
		for _, n := range p.Prune {
			s.logf("d", "  remove %s", n)
		}
	})
}

// Update downloads what Check found, then re-verifies.
func (u *Updater) Update() {
	u.mu.Lock()
	if u.busy || u.plan == nil || u.snap.Phase != phaseNeedsUpdate {
		u.mu.Unlock()
		return
	}
	u.busy = true
	plan, cfg := u.plan, u.cfg
	u.mu.Unlock()

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
	u.mu.Lock()
	u.busy = false
	u.mu.Unlock()
	if err != nil {
		u.set(true, func(s *Snapshot) {
			s.Phase, s.Message, s.Error = phaseError, "The update failed.", err.Error()
			s.logf("e", "Update failed: %v", err)
		})
		return
	}
	u.set(true, func(s *Snapshot) { s.logf("", "Download complete.") })
	u.Check() // confirm the result against the server's hashes
}

// Play starts the game.  The caller quits the launcher afterwards.
func (u *Updater) Play() error {
	u.mu.Lock()
	cfg := u.cfg
	u.mu.Unlock()
	if err := startGame(cfg.Dir, u.exePath(), u.connectHost(), false); err != nil {
		u.set(true, func(s *Snapshot) {
			s.Phase, s.Message, s.Error = phaseError, "Couldn't start the game.", err.Error()
			s.logf("e", "Couldn't start the game: %v", err)
		})
		return err
	}
	u.set(true, func(s *Snapshot) {
		s.Phase, s.Message = phaseLaunching, "Starting the game…"
		s.logf("", "Starting the game → %s", u.connectHost())
	})
	return nil
}

// Configure changes server/dir/connect, saves them, and re-checks.
func (u *Updater) Configure(in Settings) error {
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
	u.fillCfg()
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
