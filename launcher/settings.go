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

func loadSettings() Settings {
	s := Settings{Server: defaultServer, Dir: defaultInstallDir()}
	if p := settingsPath(); p != "" {
		if b, err := os.ReadFile(p); err == nil {
			var in Settings
			if json.Unmarshal(b, &in) == nil {
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
	return s
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
