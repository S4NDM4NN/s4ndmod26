package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) string {
	root := t.TempDir()
	dl := filepath.Join(root, "downloads")
	for _, n := range platforms["windows"].files {
		writeFile(t, filepath.Join(dl, "windows", n), "win-"+n)
	}
	for _, n := range platforms["linux"].files {
		writeFile(t, filepath.Join(dl, "linux", n), "lin-"+n)
	}
	writeFile(t, filepath.Join(dl, "main", "demopak0.pk3"), "demo")
	writeFile(t, filepath.Join(dl, "s4ndmod26", "s4ndmod26_v1.pk3"), "mod-v1")
	writeFile(t, filepath.Join(root, "version.txt"), "v1\n")
	return root
}

func TestManifestWindows(t *testing.T) {
	root := fixture(t)
	b := newManifestBuilder(root)
	m, err := b.build("windows")
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "v1" || m.Pk3 != "s4ndmod26_v1.pk3" {
		t.Fatalf("version/pk3 = %q/%q", m.Version, m.Pk3)
	}
	want := map[string]string{
		"ioWolfMP.x64.exe":           "win-ioWolfMP.x64.exe",
		"s4ndmod26/s4ndmod26_v1.pk3": "mod-v1",
		"main/demopak0.pk3":          "demo",
	}
	got := map[string]ManifestFile{}
	for _, f := range m.Files {
		got[f.Path] = f
	}
	for p, content := range want {
		f, ok := got[p]
		if !ok {
			t.Fatalf("missing %s", p)
		}
		sum := sha256.Sum256([]byte(content))
		if f.SHA256 != hex.EncodeToString(sum[:]) || f.Size != int64(len(content)) {
			t.Errorf("%s: sha/size mismatch: %+v", p, f)
		}
	}
	if got["s4ndmod26/s4ndmod26_v1.pk3"].URL != "/downloads/s4ndmod26/s4ndmod26_v1.pk3" {
		t.Errorf("url = %s", got["s4ndmod26/s4ndmod26_v1.pk3"].URL)
	}
}

func TestManifestPrefersLatestTxtAndRehashesOnChange(t *testing.T) {
	root := fixture(t)
	dl := filepath.Join(root, "downloads", "s4ndmod26")
	writeFile(t, filepath.Join(dl, "s4ndmod26_v2.pk3"), "mod-v2")
	writeFile(t, filepath.Join(dl, "latest.txt"), "s4ndmod26_v1.pk3\n")
	b := newManifestBuilder(root)
	m, err := b.build("linux")
	if err != nil || m.Pk3 != "s4ndmod26_v1.pk3" {
		t.Fatalf("pk3 = %v err=%v", m, err)
	}
	// without latest.txt the newest pk3 wins; changed content must be re-hashed
	os.Remove(filepath.Join(dl, "latest.txt"))
	writeFile(t, filepath.Join(dl, "s4ndmod26_v2.pk3"), "mod-v2-changed")
	m, err = b.build("linux")
	if err != nil || m.Pk3 != "s4ndmod26_v2.pk3" {
		t.Fatalf("pk3 = %v err=%v", m.Pk3, err)
	}
	sum := sha256.Sum256([]byte("mod-v2-changed"))
	for _, f := range m.Files {
		if f.Path == "s4ndmod26/s4ndmod26_v2.pk3" && f.SHA256 != hex.EncodeToString(sum[:]) {
			t.Fatalf("stale hash for changed file")
		}
	}
}

func TestManifestHandler(t *testing.T) {
	root := fixture(t)
	h := manifestHandler(newManifestBuilder(root))

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("GET", "/api/manifest?platform=linux", nil))
	if rec.Code != 200 {
		t.Fatalf("code %d: %s", rec.Code, rec.Body.String())
	}
	var m Manifest
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil || m.Platform != "linux" || len(m.Files) != 4 {
		t.Fatalf("bad manifest: %v %+v", err, m)
	}
	var exec int
	for _, f := range m.Files {
		if f.Exec {
			exec++
		}
	}
	if exec != 1 {
		t.Errorf("exec files = %d, want 1", exec)
	}

	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest("GET", "/api/manifest?platform=amiga", nil))
	if rec.Code != 400 {
		t.Errorf("unknown platform code = %d", rec.Code)
	}

	os.Remove(filepath.Join(root, "downloads", "main", "demopak0.pk3"))
	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest("GET", "/api/manifest", nil))
	if rec.Code != 503 {
		t.Errorf("missing file code = %d, want 503", rec.Code)
	}
}
