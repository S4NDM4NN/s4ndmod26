package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// The launcher keeps, updates, prunes and reverts files in its install folder, so that folder must never be
// (or sit inside) somebody's RTCW installation.  This is a hard rule: there is no override.

// retailExes are the game's own executables; a folder holding one is an RTCW install.
var retailExes = []string{"WolfMP.exe", "WolfSP.exe", "WolfMP", "WolfSP"}

// errRetailInstall is why a folder was refused.
func errRetailInstall(where string) error {
	return fmt.Errorf("%s is part of an RTCW install. Choose a separate folder for S4NDMoD26.", where)
}

func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// within reports whether p is dir itself or somewhere inside it.
func within(p, dir string) bool {
	p, dir = filepath.Clean(p), filepath.Clean(dir)
	if strings.EqualFold(p, dir) {
		return true
	}
	if !strings.HasSuffix(dir, string(filepath.Separator)) {
		dir += string(filepath.Separator)
	}
	return len(p) > len(dir) && strings.EqualFold(p[:len(dir)], dir)
}

// retailInstallAt returns the RTCW install that dir is (or is inside), or "" when it is not one.  It walks up
// from dir looking for the game's executable, and also refuses the folders findRetailFolders knows about
// (Steam, GOG, the original installer) even if the executable is missing.
func retailInstallAt(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	for p := abs; ; {
		for _, e := range retailExes {
			if hasFileFold(p, e) {
				return p
			}
		}
		up := filepath.Dir(p)
		if up == p {
			break
		}
		p = up
	}
	for _, main := range findRetailFolders() {
		for _, root := range []string{main, filepath.Dir(main)} {
			if within(abs, root) {
				return root
			}
		}
	}
	return ""
}

// checkInstallDir is the one test every way of choosing the install folder goes through.
func checkInstallDir(dir string) error {
	if where := retailInstallAt(dir); where != "" {
		return errRetailInstall(where)
	}
	return nil
}

// checkImportSource refuses a source folder that overlaps the install folder (copying a folder onto itself,
// or out of the launcher's own files, makes no sense and would blur what the launcher owns).
func checkImportSource(src, installDir string) error {
	a, err1 := filepath.Abs(src)
	b, err2 := filepath.Abs(installDir)
	if err1 != nil || err2 != nil {
		return nil
	}
	if within(a, b) || within(b, a) {
		return fmt.Errorf("The RTCW folder can't be the S4NDMoD26 install folder, or be inside it.")
	}
	return nil
}
