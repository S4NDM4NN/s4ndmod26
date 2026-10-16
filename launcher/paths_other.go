//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"syscall"
)

func defaultInstallDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "rtcw")
}

func platformName() string { return "linux" }

func gameBinary() string { return "iowolfmp.x86_64" }

// freeSpace reports the free bytes on the volume holding dir (or its nearest existing parent).
func freeSpace(dir string) (int64, bool) {
	for p := dir; ; p = filepath.Dir(p) {
		var st syscall.Statfs_t
		if syscall.Statfs(p, &st) == nil {
			return int64(st.Bavail) * int64(st.Bsize), true
		}
		if filepath.Dir(p) == p {
			return 0, false
		}
	}
}
