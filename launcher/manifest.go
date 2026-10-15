package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"
)

// Manifest mirrors infra/web/status-api/manifest.go (GET /api/manifest?platform=...).
type Manifest struct {
	Version  string         `json:"version"`
	Platform string         `json:"platform"`
	Pk3      string         `json:"pk3"`
	Files    []ManifestFile `json:"files"`
	Prune    []string       `json:"prune"`
	// Launcher is the current launcher binary for this platform (absent on older servers).
	Launcher        *ManifestFile `json:"launcher,omitempty"`
	LauncherVersion string        `json:"launcher_version,omitempty"`
}

type ManifestFile struct {
	Path   string `json:"path"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Exec   bool   `json:"exec,omitempty"`
}

// safeRel reports whether p is a clean, relative, forward-slash path that stays inside the install dir.
// The manifest comes from the network, so nothing in it may name a path outside the install dir.
func safeRel(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, `\`) || strings.Contains(p, ":") {
		return false
	}
	c := path.Clean(p)
	return c == p && c != "." && c != ".." && !strings.HasPrefix(c, "../")
}

func (m *Manifest) validate() error {
	if len(m.Files) == 0 {
		return fmt.Errorf("manifest lists no files")
	}
	for _, f := range m.Files {
		if !safeRel(f.Path) {
			return fmt.Errorf("manifest path %q is not a safe relative path", f.Path)
		}
		if len(f.SHA256) != 64 || f.Size < 0 {
			return fmt.Errorf("manifest entry %q has no valid sha256/size", f.Path)
		}
		if !strings.HasPrefix(f.URL, "/") {
			return fmt.Errorf("manifest url %q for %q is not a host-relative path", f.URL, f.Path)
		}
	}
	if l := m.Launcher; l != nil {
		if len(l.SHA256) != 64 || l.Size <= 0 || !strings.HasPrefix(l.URL, "/") {
			return fmt.Errorf("manifest launcher entry is not valid")
		}
	}
	for _, g := range m.Prune {
		if !safeRel(g) {
			return fmt.Errorf("manifest prune pattern %q is not a safe relative path", g)
		}
	}
	return nil
}

func fetchManifest(client *http.Client, server, platform string) (*Manifest, error) {
	c := *client
	c.Timeout = 15 * time.Second
	resp, err := c.Get(strings.TrimRight(server, "/") + "/api/manifest?platform=" + platform)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("manifest: HTTP %d", resp.StatusCode)
	}
	var m Manifest
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	if err := m.validate(); err != nil {
		return nil, err
	}
	return &m, nil
}
