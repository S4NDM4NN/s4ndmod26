package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// These tests pin the launcher's rule: nothing is downloaded, installed or replaced until the user presses a
// button.  Opening the launcher only checks.

// isolate keeps Configure()'s settings.json inside the test.
func isolate(t *testing.T) {
	t.Helper()
	d := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", d)
	t.Setenv("APPDATA", d)
	t.Setenv("HOME", d)
}

type gameFixture struct {
	f    *fakeServer
	dir  string
	game string
}

func newGame(t *testing.T) gameFixture {
	game := gameBinary()
	f := newFake(t,
		map[string]string{"/downloads/g": "GAME", "/downloads/p": "PK3"},
		map[string]string{game: "/downloads/g", "s4ndmod26/s4ndmod26_v1.pk3": "/downloads/p"})
	f.m.Version = "v1"
	return gameFixture{f: f, dir: t.TempDir(), game: game}
}

func empty(t *testing.T, dir string) bool {
	t.Helper()
	ents, _ := os.ReadDir(dir)
	return len(ents) == 0
}

func TestOpeningTheLauncherOnlyChecks(t *testing.T) {
	g := newGame(t)
	u := NewUpdater(Settings{Server: g.f.srv.URL, Dir: g.dir})
	u.Start()
	s := waitPhase(t, u, phaseNeedsUpdate)
	if s.Installed {
		t.Error("nothing is installed yet")
	}
	if !strings.Contains(s.Message, "when you press Play") {
		t.Errorf("message should say the download waits for Play: %q", s.Message)
	}
	if g.f.gets.Load() != 0 {
		t.Errorf("start-up downloaded %d file(s)", g.f.gets.Load())
	}
	if !empty(t, g.dir) {
		t.Error("start-up wrote to the install folder")
	}
}

func TestFirstRunDoesNothingUntilTheFolderIsConfirmed(t *testing.T) {
	isolate(t)
	g := newGame(t)
	u := NewUpdater(Settings{Server: g.f.srv.URL, Dir: g.dir})
	u.firstRun = true
	u.Start()
	s := waitPhase(t, u, phaseSetup)
	if !s.FirstRun {
		t.Error("snapshot should say first run")
	}
	if ok, _ := u.CanPlay(); ok {
		t.Error("Play must not be allowed before the folder is confirmed")
	}
	time.Sleep(150 * time.Millisecond)
	if g.f.mgets.Load() != 0 || g.f.gets.Load() != 0 {
		t.Errorf("first run contacted the server (manifest %d, downloads %d) before confirmation", g.f.mgets.Load(), g.f.gets.Load())
	}

	// confirming the folder saves it and runs the (read-only) check
	other := t.TempDir()
	if err := u.Configure(Settings{Server: g.f.srv.URL, Dir: other}); err != nil {
		t.Fatal(err)
	}
	s = waitPhase(t, u, phaseNeedsUpdate)
	if s.FirstRun || s.Dir != other {
		t.Errorf("after confirming: %+v", s)
	}
	if g.f.gets.Load() != 0 || !empty(t, other) {
		t.Error("confirming the folder must not download anything")
	}
}

func TestPlayInstallsThenStartsTheGame(t *testing.T) {
	g := newGame(t)
	u := NewUpdater(Settings{Server: g.f.srv.URL, Dir: g.dir})
	var gotDir, gotHost string
	var started, launched atomic.Int32
	u.startGame = func(dir, exe, host string) error { gotDir, gotHost = dir, host; started.Add(1); return nil }
	u.onLaunched = func() { launched.Add(1) }
	u.Start()
	waitPhase(t, u, phaseNeedsUpdate)

	u.Play()
	s := u.Snapshot()
	if s.Phase != phaseLaunching || started.Load() != 1 || launched.Load() != 1 {
		t.Fatalf("phase=%s started=%d launched=%d", s.Phase, started.Load(), launched.Load())
	}
	if g.f.gets.Load() != 2 {
		t.Errorf("Play should have downloaded the two files, got %d", g.f.gets.Load())
	}
	if gotDir != g.dir {
		t.Errorf("game started with install dir %q, want %q (base path = where it was installed)", gotDir, g.dir)
	}
	if gotHost == "" {
		t.Error("no connect host")
	}
	if b, _ := os.ReadFile(filepath.Join(g.dir, g.game)); string(b) != "GAME" {
		t.Errorf("game file = %q", b)
	}
}

func TestPlayWhenUpToDateJustStarts(t *testing.T) {
	g := newGame(t)
	u := NewUpdater(Settings{Server: g.f.srv.URL, Dir: g.dir})
	u.startGame = func(string, string, string) error { return nil }
	u.Start()
	waitPhase(t, u, phaseNeedsUpdate)
	u.Update() // explicit "update only"
	waitPhase(t, u, phaseReady)
	before := g.f.gets.Load()

	u.Play()
	if g.f.gets.Load() != before {
		t.Error("Play on an up-to-date install must not download anything")
	}
	if u.Snapshot().Phase != phaseLaunching {
		t.Errorf("phase = %s", u.Snapshot().Phase)
	}
}

func launcherServer(t *testing.T, g gameFixture, newContent string) *Manifest {
	priv := testKey(t)
	g.f.files["/downloads/launcher/new"] = newContent
	sm := signedManifest(priv, newContent, "/downloads/launcher/new")
	g.f.m.Launcher = sm.Launcher
	g.f.m.LauncherVersion = "v2"
	return &g.f.m
}

func TestLauncherUpdateBlocksPlayAndOnlyInstallsOnRequest(t *testing.T) {
	g := newGame(t)
	launcherServer(t, g, "NEW-LAUNCHER")
	self := filepath.Join(t.TempDir(), "launcher.exe")
	write(t, self, "OLD-LAUNCHER")
	var restarted, started atomic.Int32
	prev := relaunch
	relaunch = func(string, []string) error { return nil }
	t.Cleanup(func() { relaunch = prev })

	u := NewUpdater(Settings{Server: g.f.srv.URL, Dir: g.dir})
	u.selfPath = self
	u.startGame = func(string, string, string) error { started.Add(1); return nil }
	u.onRestart = func() { restarted.Add(1) }
	u.Start()
	s := waitPhase(t, u, phaseLauncherUpdate)

	if !s.LauncherUpdate || s.LauncherVersion != "v2" {
		t.Errorf("snapshot: %+v", s)
	}
	if b, _ := os.ReadFile(self); string(b) != "OLD-LAUNCHER" {
		t.Fatal("the launcher was replaced without being asked")
	}
	if g.f.gets.Load() != 0 {
		t.Fatalf("a launcher update downloaded %d file(s) without being asked", g.f.gets.Load())
	}
	if ok, why := u.CanPlay(); ok || !strings.Contains(why, "launcher update") {
		t.Errorf("Play must be blocked by a pending launcher update (ok=%v why=%q)", ok, why)
	}
	u.Play() // pressing it anyway must do nothing
	if started.Load() != 0 || g.f.gets.Load() != 0 {
		t.Error("Play ran while a launcher update was pending")
	}

	// now the user asks for the update
	u.UpdateLauncher()
	if b, _ := os.ReadFile(self); string(b) != "NEW-LAUNCHER" {
		t.Errorf("launcher not updated after the request: %q", b)
	}
	if restarted.Load() != 1 {
		t.Error("the launcher should close after relaunching itself")
	}
}

func TestUnsignedLauncherOfferIsIgnored(t *testing.T) {
	g := newGame(t)
	g.f.m.Launcher = launcherManifest("SOMETHING", "/downloads/launcher/x").Launcher // no signature
	self := filepath.Join(t.TempDir(), "launcher.exe")
	write(t, self, "OLD")
	testKey(t)
	u := NewUpdater(Settings{Server: g.f.srv.URL, Dir: g.dir})
	u.selfPath = self
	u.Start()
	s := waitPhase(t, u, phaseNeedsUpdate) // game flow continues; the unsigned launcher is not offered
	if s.LauncherUpdate {
		t.Error("an unsigned launcher must not be offered")
	}
}

func TestPlayIsRefusedOverHTTPWhileALauncherUpdateIsPending(t *testing.T) {
	g := newGame(t)
	launcherServer(t, g, "NEW-LAUNCHER")
	self := filepath.Join(t.TempDir(), "launcher.exe")
	write(t, self, "OLD-LAUNCHER")
	u := NewUpdater(Settings{Server: g.f.srv.URL, Dir: g.dir})
	u.selfPath = self
	app, ln, _ := NewApp(u)
	ln.Close()
	u.Start()
	waitPhase(t, u, phaseLauncherUpdate)

	do := func(path string) int {
		r := httptest.NewRequest("POST", path, nil)
		r.Host = "127.0.0.1:1"
		r.Header.Set("X-Token", app.token)
		w := httptest.NewRecorder()
		app.Handler().ServeHTTP(w, r)
		return w.Code
	}
	if c := do("/api/play"); c != http.StatusConflict {
		t.Errorf("/api/play with a launcher update pending = %d, want 409", c)
	}
	if g.f.gets.Load() != 0 {
		t.Error("nothing may be downloaded")
	}

	// and the other direction: updating the launcher is refused when there is nothing to update
	g2 := newGame(t)
	u2 := NewUpdater(Settings{Server: g2.f.srv.URL, Dir: g2.dir})
	app2, ln2, _ := NewApp(u2)
	ln2.Close()
	u2.Start()
	waitPhase(t, u2, phaseNeedsUpdate)
	r := httptest.NewRequest("POST", "/api/update-launcher", nil)
	r.Host = "127.0.0.1:1"
	r.Header.Set("X-Token", app2.token)
	w := httptest.NewRecorder()
	app2.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusConflict {
		t.Errorf("/api/update-launcher with nothing pending = %d, want 409", w.Code)
	}
}

func TestLauncherFolderIsWritableAndIsTheDefaultInstallDir(t *testing.T) {
	exe, _ := os.Executable()
	got := launcherFolder()
	if got == "" {
		t.Fatal("no default install folder")
	}
	if got != filepath.Dir(exe) && got != defaultInstallDir() {
		t.Errorf("default install folder %q is neither the launcher's folder nor the per-platform default", got)
	}
}
