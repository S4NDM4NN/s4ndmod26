package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func launcherManifest(content string, url string) *Manifest {
	sum := sha256.Sum256([]byte(content))
	return &Manifest{Launcher: &ManifestFile{Path: "l", URL: url, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(content))}}
}

func stubRelaunch(t *testing.T) *[]string {
	t.Helper()
	var calls []string
	prev := relaunch
	relaunch = func(exe string, args []string) error { calls = append(calls, exe+" "+joinArgs(args)); return nil }
	t.Cleanup(func() { relaunch = prev })
	return &calls
}

func joinArgs(a []string) string {
	s := ""
	for _, x := range a {
		s += x + " "
	}
	return s
}

func TestSelfUpdateReplacesAndRelaunches(t *testing.T) {
	f := newFake(t, map[string]string{"/downloads/launcher/new": "NEW-LAUNCHER"}, map[string]string{"x": "/downloads/launcher/new"})
	dir := t.TempDir()
	exe := filepath.Join(dir, "launcher.exe")
	write(t, exe, "OLD-LAUNCHER")
	calls := stubRelaunch(t)

	m := launcherManifest("NEW-LAUNCHER", "/downloads/launcher/new")
	ok, err := SelfUpdate(http.DefaultClient, f.srv.URL, m, exe, []string{"--server", "x"})
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "NEW-LAUNCHER" {
		t.Fatalf("exe = %q", b)
	}
	if b, _ := os.ReadFile(exe + ".old"); string(b) != "OLD-LAUNCHER" {
		t.Fatalf("old = %q", b)
	}
	if len(*calls) != 1 {
		t.Fatalf("relaunch calls: %v", *calls)
	}
	cleanupOldLauncher(exe)
	if _, err := os.Stat(exe + ".old"); err == nil {
		t.Error(".old should be removed by cleanup")
	}
	if SelfUpdateAvailable(m, exe) {
		t.Error("after updating, the launcher must match the manifest (no update loop)")
	}
}

func TestSelfUpdateNoopWhenSameOrAbsent(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "launcher.exe")
	write(t, exe, "SAME")
	calls := stubRelaunch(t)
	if ok, err := SelfUpdate(http.DefaultClient, "http://unused", launcherManifest("SAME", "/x"), exe, nil); ok || err != nil {
		t.Errorf("same hash: ok=%v err=%v", ok, err)
	}
	if ok, err := SelfUpdate(http.DefaultClient, "http://unused", &Manifest{}, exe, nil); ok || err != nil {
		t.Errorf("no launcher entry: ok=%v err=%v", ok, err)
	}
	if len(*calls) != 0 {
		t.Errorf("unexpected relaunch %v", *calls)
	}
}

func TestSelfUpdateBadDownloadLeavesLauncherAlone(t *testing.T) {
	f := newFake(t, map[string]string{"/downloads/launcher/new": "TAMPERED"}, map[string]string{"x": "/downloads/launcher/new"})
	dir := t.TempDir()
	exe := filepath.Join(dir, "launcher.exe")
	write(t, exe, "OLD")
	calls := stubRelaunch(t)

	m := launcherManifest("EXPECTED", "/downloads/launcher/new") // hash of something the server does not serve
	ok, err := SelfUpdate(http.DefaultClient, f.srv.URL, m, exe, nil)
	if ok || err == nil {
		t.Fatalf("expected failure, ok=%v err=%v", ok, err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "OLD" {
		t.Errorf("launcher must be untouched, got %q", b)
	}
	for _, n := range []string{".new", ".new.part", ".old"} {
		if _, err := os.Stat(exe + n); err == nil {
			t.Errorf("leftover %s", n)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("must not relaunch")
	}
}

func TestSelfUpdateRestoresWhenRelaunchFails(t *testing.T) {
	f := newFake(t, map[string]string{"/downloads/launcher/new": "NEW"}, map[string]string{"x": "/downloads/launcher/new"})
	dir := t.TempDir()
	exe := filepath.Join(dir, "launcher.exe")
	write(t, exe, "OLD")
	prev := relaunch
	relaunch = func(string, []string) error { return os.ErrPermission }
	t.Cleanup(func() { relaunch = prev })

	ok, err := SelfUpdate(http.DefaultClient, f.srv.URL, launcherManifest("NEW", "/downloads/launcher/new"), exe, nil)
	if ok || err == nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "OLD" {
		t.Errorf("old launcher should be restored, got %q", b)
	}
}
