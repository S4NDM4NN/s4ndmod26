package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func waitPhase(t *testing.T, u *Updater, want string) Snapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s := u.Snapshot(); s.Phase == want {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("never reached phase %q; at %+v", want, u.Snapshot())
	return Snapshot{}
}

// deadServer returns the URL of a server that was listening and is now closed: connections are
// refused immediately (unlike an arbitrary port, which may hang until the timeout).
func deadServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	u := srv.URL
	srv.Close()
	return u
}

func logText(s Snapshot) string {
	var b strings.Builder
	for _, l := range s.Log {
		b.WriteString(l.Text + "\n")
	}
	return b.String()
}

func TestUpdaterCheckUpdateReady(t *testing.T) {
	game := gameBinary()
	f := newFake(t,
		map[string]string{"/downloads/g": "GAME", "/downloads/p": "PK3"},
		map[string]string{game: "/downloads/g", "s4ndmod26/s4ndmod26_v1.pk3": "/downloads/p"})
	f.m.Version = "v1"
	dir := t.TempDir()
	write(t, filepath.Join(dir, "s4ndmod26", "s4ndmod26_old.pk3"), "stale")

	u := NewUpdater(Settings{Server: f.srv.URL, Dir: dir})
	u.Check()
	s := waitPhase(t, u, phaseNeedsUpdate)
	if s.Files != 2 || s.BytesTotal != int64(len("GAME")+len("PK3")) || s.Version != "v1" {
		t.Fatalf("needs_update snapshot: %+v", s)
	}
	if _, err := os.Stat(filepath.Join(dir, game)); err == nil {
		t.Fatal("Check must not download anything")
	}

	u.Update()
	s = waitPhase(t, u, phaseReady)
	if s.Message != "You're up to date." || s.Offline {
		t.Fatalf("after update: %+v", s)
	}
	if _, err := os.Stat(filepath.Join(dir, "s4ndmod26", "s4ndmod26_old.pk3")); err == nil {
		t.Error("stale pk3 should have been pruned")
	}
	lt := logText(s)
	for _, want := range []string{"2 file(s)", "s4ndmod26/s4ndmod26_v1.pk3", "Download complete.", "verified. Ready."} {
		if !strings.Contains(lt, want) {
			t.Errorf("log missing %q:\n%s", want, lt)
		}
	}
}

func TestUpdaterOfflineWithInstalledGameStillPlayable(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, gameBinary()), "installed")
	u := NewUpdater(Settings{Server: deadServer(t), Dir: dir})
	u.Check()
	s := waitPhase(t, u, phaseReady)
	if !s.Offline {
		t.Fatalf("expected offline flag: %+v", s)
	}
}

func TestUpdaterOfflineWithoutGameIsAnError(t *testing.T) {
	u := NewUpdater(Settings{Server: deadServer(t), Dir: t.TempDir()})
	u.Check()
	s := waitPhase(t, u, phaseError)
	if s.Error == "" {
		t.Fatal("error phase should carry the error text")
	}
}

func TestConnectHostDefaultsToServerHost(t *testing.T) {
	u := NewUpdater(Settings{Server: "https://s4ndmod26.dev.s4ndmod.com/", Dir: "x"})
	if got := u.connectHost(); got != "s4ndmod26.dev.s4ndmod.com" {
		t.Errorf("connectHost = %q", got)
	}
	u = NewUpdater(Settings{Server: "https://s4ndmod.com", Dir: "x", Connect: "play.example:27961"})
	if got := u.connectHost(); got != "play.example:27961" {
		t.Errorf("explicit connect = %q", got)
	}
}
