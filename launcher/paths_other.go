//go:build !windows

package main

import (
	"os"
	"path/filepath"
)

func defaultInstallDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "rtcw")
}

func platformName() string { return "linux" }

func gameBinary() string { return "iowolfmp.x86_64" }
