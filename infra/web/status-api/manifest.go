package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// The launcher/updater asks /api/manifest?platform=windows|linux for the list of files that make up a
// current install.  It is built from what is actually on disk under the downloads dir (so it can never
// disagree with what nginx serves, including the pk3 the game server copied onto the shared volume).

type ManifestFile struct {
	Path   string `json:"path"` // install-relative, forward slashes
	URL    string `json:"url"`  // absolute path on this host
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Exec   bool   `json:"exec,omitempty"`
	// Signature (launcher entry only) is the base64 ed25519 signature of the sha256, produced by CI
	// next to the binary as <name>.sig.  Absent for unsigned builds (PR previews, local).
	Signature string `json:"signature,omitempty"`
}

type Manifest struct {
	Version  string         `json:"version"`
	Platform string         `json:"platform"`
	Pk3      string         `json:"pk3"`
	Files    []ManifestFile `json:"files"`
	// Launcher describes the current launcher binary for this platform, if one is published.  The
	// launcher compares its own sha256 with this and replaces itself when they differ.
	Launcher        *ManifestFile `json:"launcher,omitempty"`
	LauncherVersion string        `json:"launcher_version,omitempty"`
	// Prune are install-relative globs the launcher deletes unless the path is in Files: older mod
	// pk3s, and loose cgame/ui modules (an unpure server loads those ahead of the pk3, so a stale one
	// silently wins).
	Prune []string `json:"prune"`
}

type platformSpec struct {
	dir      string   // under downloads/
	files    []string // client binaries, installed at the install root
	exec     map[string]bool
	launcher string // file name under downloads/launcher/
}

var platforms = map[string]platformSpec{
	"windows": {
		dir: "windows",
		files: []string{
			"ioWolfMP.x64.exe", "renderer_mp_opengl1_x64.dll", "SDL264.dll", "OpenAL64.dll",
			"libstdc++-6.dll", "libgcc_s_seh-1.dll", "libwinpthread-1.dll",
		},
		launcher: "s4ndmod-launcher.exe",
	},
	"linux": {
		dir:      "linux",
		files:    []string{"iowolfmp.x86_64", "renderer_mp_opengl1_x86_64.so"},
		exec:     map[string]bool{"iowolfmp.x86_64": true},
		launcher: "s4ndmod-launcher-linux",
	},
}

var manifestPrune = []string{
	"s4ndmod26/s4ndmod26*.pk3",
	"s4ndmod26/cgame_mp_*.dll", "s4ndmod26/ui_mp_*.dll",
	"s4ndmod26/cgame.mp.*.so", "s4ndmod26/ui.mp.*.so",
}

type hashEntry struct {
	size int64
	mod  time.Time
	sum  string
}

type manifestBuilder struct {
	webRoot string

	mu    sync.Mutex
	cache map[string]hashEntry
}

func newManifestBuilder(webRoot string) *manifestBuilder {
	return &manifestBuilder{webRoot: webRoot, cache: map[string]hashEntry{}}
}

func (b *manifestBuilder) hash(path string) (string, int64, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", 0, err
	}
	b.mu.Lock()
	e, ok := b.cache[path]
	b.mu.Unlock()
	if ok && e.size == st.Size() && e.mod.Equal(st.ModTime()) {
		return e.sum, e.size, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", 0, err
	}
	sum := hex.EncodeToString(h.Sum(nil))
	b.mu.Lock()
	b.cache[path] = hashEntry{size: st.Size(), mod: st.ModTime(), sum: sum}
	b.mu.Unlock()
	return sum, st.Size(), nil
}

// currentPk3 names the mod pk3 to install: latest.txt if present and valid, else the newest
// s4ndmod26_*.pk3 in the directory.
func currentPk3(dir string) (string, error) {
	if raw, err := os.ReadFile(filepath.Join(dir, "latest.txt")); err == nil {
		name := strings.TrimSpace(string(raw))
		if name == filepath.Base(name) && strings.HasPrefix(name, "s4ndmod26") && strings.HasSuffix(name, ".pk3") {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				return name, nil
			}
		}
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "s4ndmod26_*.pk3"))
	if len(matches) == 0 {
		return "", fmt.Errorf("no s4ndmod26_*.pk3 in %s", dir)
	}
	sort.Slice(matches, func(i, j int) bool {
		a, _ := os.Stat(matches[i])
		c, _ := os.Stat(matches[j])
		return a.ModTime().After(c.ModTime())
	})
	return filepath.Base(matches[0]), nil
}

func (b *manifestBuilder) build(platform string) (*Manifest, error) {
	spec, ok := platforms[platform]
	if !ok {
		return nil, fmt.Errorf("unknown platform %q", platform)
	}
	dl := filepath.Join(b.webRoot, "downloads")
	m := &Manifest{Platform: platform, Prune: manifestPrune}
	if v, err := os.ReadFile(filepath.Join(b.webRoot, "version.txt")); err == nil {
		m.Version = strings.TrimSpace(string(v))
	}

	add := func(installPath, diskPath, urlPath string, exec bool) error {
		sum, size, err := b.hash(diskPath)
		if err != nil {
			return err
		}
		m.Files = append(m.Files, ManifestFile{Path: installPath, URL: urlPath, SHA256: sum, Size: size, Exec: exec})
		return nil
	}

	for _, name := range spec.files {
		if err := add(name, filepath.Join(dl, spec.dir, name), "/downloads/"+spec.dir+"/"+name, spec.exec[name]); err != nil {
			return nil, err
		}
	}
	pk3, err := currentPk3(filepath.Join(dl, "s4ndmod26"))
	if err != nil {
		return nil, err
	}
	m.Pk3 = pk3
	if err := add("s4ndmod26/"+pk3, filepath.Join(dl, "s4ndmod26", pk3), "/downloads/s4ndmod26/"+pk3, false); err != nil {
		return nil, err
	}
	// The launcher itself (optional: older deployments have none).
	if spec.launcher != "" {
		lp := filepath.Join(dl, "launcher", spec.launcher)
		if sum, size, err := b.hash(lp); err == nil {
			m.Launcher = &ManifestFile{
				Path: spec.launcher, URL: "/downloads/launcher/" + spec.launcher,
				SHA256: sum, Size: size, Exec: platform == "linux",
			}
			if raw, err := os.ReadFile(lp + ".sig"); err == nil {
				m.Launcher.Signature = strings.TrimSpace(string(raw))
			}
			if v, err := os.ReadFile(filepath.Join(dl, "launcher", "version.txt")); err == nil {
				m.LauncherVersion = strings.TrimSpace(string(v))
			}
		}
	}
	// The demo pak is still required with a retail install: the server's maps come from it.
	if err := add("main/demopak0.pk3", filepath.Join(dl, "main", "demopak0.pk3"), "/downloads/main/demopak0.pk3", false); err != nil {
		return nil, err
	}
	return m, nil
}

func manifestHandler(b *manifestBuilder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		platform := r.URL.Query().Get("platform")
		if platform == "" {
			platform = "windows"
		}
		m, err := b.build(platform)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if err != nil {
			code := http.StatusServiceUnavailable
			if _, ok := platforms[platform]; !ok {
				code = http.StatusBadRequest
			}
			http.Error(w, err.Error(), code)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(m)
	}
}
