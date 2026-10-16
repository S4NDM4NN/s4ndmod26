package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Settings persist between runs in <user config dir>/s4ndmod-launcher/settings.json.
type Settings struct {
	Server  string `json:"server"`
	Dir     string `json:"dir"`
	Connect string `json:"connect,omitempty"`
}

const defaultServer = "https://s4ndmod.com"

func settingsPath() string {
	d, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(d, "s4ndmod-launcher", "settings.json")
}

// loadSettings returns the saved settings and whether any were saved.  With none (first run) the install
// folder defaults to the folder the launcher itself is in, and the caller must make the user confirm it
// before anything is checked or written.
func loadSettings() (Settings, bool) {
	s := Settings{Server: defaultServer, Dir: launcherFolder()}
	found := false
	if p := settingsPath(); p != "" {
		if b, err := os.ReadFile(p); err == nil {
			var in Settings
			if json.Unmarshal(b, &in) == nil {
				found = true
				if in.Server != "" {
					s.Server = in.Server
				}
				if in.Dir != "" {
					s.Dir = in.Dir
				}
				s.Connect = in.Connect
			}
		}
	}
	return s, found
}

// launcherFolder is where the game is installed unless the user says otherwise: the folder the launcher is
// in (so the game ends up next to it), falling back to the per-platform default if that is not writable.
func launcherFolder() string {
	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exe)
		if f, err := os.CreateTemp(dir, ".s4nd-write-test-*"); err == nil {
			f.Close()
			os.Remove(f.Name())
			return dir
		}
	}
	return defaultInstallDir()
}

func (s Settings) save() error {
	p := settingsPath()
	if p == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(p, b, 0o644)
}
