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
// folder is suggested as the per-platform default (Documents\RTCW, where the engine already keeps its
// config, or ~/rtcw), and the caller must make the user confirm or change it before anything is checked or
// written.
func loadSettings() (Settings, bool) {
	s := Settings{Server: defaultServer, Dir: defaultInstallDir()}
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
