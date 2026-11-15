package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

type fakeServer struct {
	files map[string]string // url path -> content
	m     Manifest
	gets  atomic.Int32 // downloads
	mgets atomic.Int32 // manifest requests
	srv   *httptest.Server
}

func newFake(t *testing.T, files map[string]string, entries map[string]string) *fakeServer {
	// entries: install path -> url path
	f := &fakeServer{files: files}
	for p, u := range entries {
		sum := sha256.Sum256([]byte(files[u]))
		f.m.Files = append(f.m.Files, ManifestFile{Path: p, URL: u, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(files[u]))})
	}
	f.m.Prune = []string{"s4ndmod26/s4ndmod26*.pk3", "s4ndmod26/cgame_mp_*.dll"}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/manifest", func(w http.ResponseWriter, r *http.Request) {
		f.mgets.Add(1)
		json.NewEncoder(w).Encode(f.m)
	})
	mux.HandleFunc("/downloads/", func(w http.ResponseWriter, r *http.Request) {
		f.gets.Add(1)
		c, ok := f.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(c))
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func write(t *testing.T, p, c string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFreshInstallThenNoop(t *testing.T) {
	f := newFake(t,
		map[string]string{"/downloads/windows/game.exe": "EXE", "/downloads/s4ndmod26/s4ndmod26_v1.pk3": "PK3v1"},
		map[string]string{"game.exe": "/downloads/windows/game.exe", "s4ndmod26/s4ndmod26_v1.pk3": "/downloads/s4ndmod26/s4ndmod26_v1.pk3"})
	dir := t.TempDir()

	res, err := Sync(http.DefaultClient, f.srv.URL, &f.m, dir, false, nil)
	if err != nil || len(res.Downloaded) != 2 {
		t.Fatalf("fresh: %+v err=%v", res, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "s4ndmod26", "s4ndmod26_v1.pk3")); string(b) != "PK3v1" {
		t.Fatalf("pk3 content %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "game.exe.part")); err == nil {
		t.Fatal(".part file left behind")
	}

	before := f.gets.Load()
	res, err = Sync(http.DefaultClient, f.srv.URL, &f.m, dir, false, nil)
	if err != nil || len(res.Downloaded) != 0 || res.UpToDate != 2 || f.gets.Load() != before {
		t.Fatalf("second run should download nothing: %+v gets=%d->%d err=%v", res, before, f.gets.Load(), err)
	}
}

func TestRepairsCorruptSameSizeFile(t *testing.T) {
	f := newFake(t, map[string]string{"/downloads/a": "GOOD"}, map[string]string{"a.bin": "/downloads/a"})
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.bin"), "BAD!") // same length, wrong bytes: size check alone would miss it
	res, err := Sync(http.DefaultClient, f.srv.URL, &f.m, dir, false, nil)
	if err != nil || len(res.Downloaded) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.bin")); string(b) != "GOOD" {
		t.Fatalf("not repaired: %q", b)
	}
}

func TestPrunesStaleButKeepsCurrent(t *testing.T) {
	f := newFake(t, map[string]string{"/downloads/s4ndmod26/s4ndmod26_v2.pk3": "v2"},
		map[string]string{"s4ndmod26/s4ndmod26_v2.pk3": "/downloads/s4ndmod26/s4ndmod26_v2.pk3"})
	dir := t.TempDir()
	m := filepath.Join(dir, "s4ndmod26")
	write(t, filepath.Join(m, "s4ndmod26.pk3"), "legacy")
	write(t, filepath.Join(m, "s4ndmod26_v1.pk3"), "old")
	write(t, filepath.Join(m, "s4ndmod26.deadbeef.pk3"), "crc dup")
	write(t, filepath.Join(m, "cgame_mp_x64.dll"), "stale loose module")
	write(t, filepath.Join(m, "wolfconfig_mp.cfg"), "user config")
	write(t, filepath.Join(m, "s4ndmod26_notes.txt"), "not a pk3")

	res, err := Sync(http.DefaultClient, f.srv.URL, &f.m, dir, false, nil)
	if err != nil || len(res.Pruned) != 4 {
		t.Fatalf("pruned %v err=%v", res.Pruned, err)
	}
	for _, gone := range []string{"s4ndmod26.pk3", "s4ndmod26_v1.pk3", "s4ndmod26.deadbeef.pk3", "cgame_mp_x64.dll"} {
		if _, err := os.Stat(filepath.Join(m, gone)); err == nil {
			t.Errorf("%s should have been removed", gone)
		}
	}
	for _, kept := range []string{"s4ndmod26_v2.pk3", "wolfconfig_mp.cfg", "s4ndmod26_notes.txt"} {
		if _, err := os.Stat(filepath.Join(m, kept)); err != nil {
			t.Errorf("%s should still exist", kept)
		}
	}
}

func TestDryRunChangesNothing(t *testing.T) {
	f := newFake(t, map[string]string{"/downloads/a": "A"}, map[string]string{"a.bin": "/downloads/a"})
	dir := t.TempDir()
	write(t, filepath.Join(dir, "s4ndmod26", "s4ndmod26_old.pk3"), "x")
	res, err := Sync(http.DefaultClient, f.srv.URL, &f.m, dir, true, nil)
	if err != nil || len(res.Downloaded) != 1 || len(res.Pruned) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.bin")); err == nil {
		t.Error("dry run downloaded a file")
	}
	if _, err := os.Stat(filepath.Join(dir, "s4ndmod26", "s4ndmod26_old.pk3")); err != nil {
		t.Error("dry run removed a file")
	}
}

func TestRejectsMismatchedDownload(t *testing.T) {
	f := newFake(t, map[string]string{"/downloads/a": "REAL"}, map[string]string{"a.bin": "/downloads/a"})
	f.files["/downloads/a"] = "TAMPERED" // server now serves different bytes than the manifest promised
	dir := t.TempDir()
	if _, err := Sync(http.DefaultClient, f.srv.URL, &f.m, dir, false, nil); err == nil {
		t.Fatal("expected an error for a hash mismatch")
	}
	for _, n := range []string{"a.bin", "a.bin.part"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
			t.Errorf("%s must not exist after a failed download", n)
		}
	}
}

func TestManifestValidation(t *testing.T) {
	good := ManifestFile{Path: "main/demopak0.pk3", URL: "/downloads/main/demopak0.pk3", SHA256: string(make([]byte, 64)), Size: 1}
	for _, bad := range []string{"../evil", "/etc/passwd", `C:\x`, "a/../../b", "a//b", ""} {
		m := Manifest{Files: []ManifestFile{{Path: bad, URL: good.URL, SHA256: good.SHA256, Size: 1}}}
		if m.validate() == nil {
			t.Errorf("path %q should be rejected", bad)
		}
	}
	m := Manifest{Files: []ManifestFile{good}, Prune: []string{"../x*"}}
	if m.validate() == nil {
		t.Error("prune pattern outside the install dir should be rejected")
	}
	m = Manifest{Files: []ManifestFile{good}}
	if err := m.validate(); err != nil {
		t.Errorf("good manifest rejected: %v", err)
	}
}

// The game extracts its libraries into s4ndmod26/ every time it runs; that must not look like an update.
func TestExtractedLibsAreNotAnUpdate(t *testing.T) {
	dir := t.TempDir()
	sum := sha256.Sum256([]byte("pk3"))
	m := &Manifest{
		Files: []ManifestFile{{Path: "s4ndmod26/s4ndmod26_v2.pk3", URL: "/x", SHA256: hex.EncodeToString(sum[:]), Size: 3}},
		Prune: []string{"s4ndmod26/s4ndmod26*.pk3", "s4ndmod26/cgame_mp_*.dll", "s4ndmod26/ui_mp_*.dll"},
	}
	writeTestFile(t, filepath.Join(dir, "s4ndmod26", "s4ndmod26_v2.pk3"), "pk3")
	writeTestFile(t, filepath.Join(dir, "s4ndmod26", "cgame_mp_x64.dll"), "x")
	writeTestFile(t, filepath.Join(dir, "s4ndmod26", "ui_mp_x64.dll"), "x")
	if p := MakePlan(m, dir); !p.Empty() || len(p.Prune) != 2 {
		t.Fatalf("extracted libs reported as an update: %+v", p)
	}
	// a stale mod pk3 still counts
	writeTestFile(t, filepath.Join(dir, "s4ndmod26", "s4ndmod26_v1.pk3"), "x")
	if p := MakePlan(m, dir); p.Empty() {
		t.Fatal("a stale pk3 should count as an update")
	}
}
