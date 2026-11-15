package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"s4ndmod26/launcher/internal/sig"
)

// testKey installs a fresh trusted key for the test and returns its private half.
func testKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	prev := trustedKeys
	trustedKeys = []ed25519.PublicKey{pub}
	t.Cleanup(func() { trustedKeys = prev })
	return priv
}

func signedManifest(priv ed25519.PrivateKey, content, url string) *Manifest {
	m := launcherManifest(content, url)
	m.Launcher.Signature = sig.Sign(priv, m.Launcher.SHA256)
	return m
}

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

	m := signedManifest(testKey(t), "NEW-LAUNCHER", "/downloads/launcher/new")
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

	m := signedManifest(testKey(t), "EXPECTED", "/downloads/launcher/new") // hash of something the server does not serve
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

	ok, err := SelfUpdate(http.DefaultClient, f.srv.URL, signedManifest(testKey(t), "NEW", "/downloads/launcher/new"), exe, nil)
	if ok || err == nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "OLD" {
		t.Errorf("old launcher should be restored, got %q", b)
	}
}

func TestSelfUpdateRejectsUnsignedOrWronglySigned(t *testing.T) {
	good := testKey(t) // trusted
	_, attacker, _ := ed25519.GenerateKey(rand.Reader)

	for name, m := range map[string]*Manifest{
		"unsigned":        launcherManifest("EVIL", "/downloads/launcher/new"),
		"signed by wrong": signedManifest(attacker, "EVIL", "/downloads/launcher/new"),
		"signature for a different file": func() *Manifest {
			m := launcherManifest("EVIL", "/downloads/launcher/new")
			m.Launcher.Signature = signedManifest(good, "SOMETHING-ELSE", "/x").Launcher.Signature
			return m
		}(),
	} {
		f := newFake(t, map[string]string{"/downloads/launcher/new": "EVIL"}, map[string]string{"x": "/downloads/launcher/new"})
		dir := t.TempDir()
		exe := filepath.Join(dir, "launcher.exe")
		write(t, exe, "OLD")
		calls := stubRelaunch(t)

		ok, err := SelfUpdate(http.DefaultClient, f.srv.URL, m, exe, nil)
		if name == "unsigned" {
			if ok || err != nil { // not offered at all
				t.Errorf("%s: ok=%v err=%v", name, ok, err)
			}
		} else if ok || err == nil || !strings.Contains(err.Error(), "rejected") {
			t.Errorf("%s: ok=%v err=%v", name, ok, err)
		}
		if b, _ := os.ReadFile(exe); string(b) != "OLD" {
			t.Errorf("%s: launcher modified: %q", name, b)
		}
		if f.gets.Load() != 0 {
			t.Errorf("%s: must reject before downloading anything (gets=%d)", name, f.gets.Load())
		}
		if len(*calls) != 0 {
			t.Errorf("%s: relaunched", name)
		}
	}
}

func TestSelfUpdateWithNoTrustedKeysFailsClosed(t *testing.T) {
	prev := trustedKeys
	trustedKeys = nil
	t.Cleanup(func() { trustedKeys = prev })
	priv := testKeyNoInstall(t)
	f := newFake(t, map[string]string{"/downloads/launcher/new": "NEW"}, map[string]string{"x": "/downloads/launcher/new"})
	exe := filepath.Join(t.TempDir(), "launcher.exe")
	write(t, exe, "OLD")
	stubRelaunch(t)
	ok, err := SelfUpdate(http.DefaultClient, f.srv.URL, signedManifest(priv, "NEW", "/downloads/launcher/new"), exe, nil)
	if ok || err == nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func testKeyNoInstall(t *testing.T) ed25519.PrivateKey {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	return priv
}

func TestEmbeddedKeysParse(t *testing.T) {
	keys, err := sig.ParseKeys(signingKeysText)
	if err != nil || len(keys) == 0 {
		t.Fatalf("embedded signing_keys.txt: keys=%d err=%v", len(keys), err)
	}
}
