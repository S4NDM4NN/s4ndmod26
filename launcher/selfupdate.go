package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
)

// Self-update.  The manifest carries the sha256 of the current launcher binary; if it differs from the
// running executable, the new one is downloaded next to it, verified, and swapped in.  A running exe
// can be renamed on Windows (just not overwritten or deleted), so the old file is renamed aside to
// <exe>.old and removed by the next run.  There is no version comparison: "the same bytes as the
// server's" is the only question, so a rollback on the server downgrades clients too.

// relaunch starts the (new) executable with the original arguments plus --updated, which makes the new
// process skip its own self-update check (so a hash that can never match cannot loop forever).
var relaunch = func(exe string, args []string) error {
	cmd := exec.Command(exe, append(args, "--updated")...)
	// in --cli mode the new process carries on in the same console
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Start()
}

// SelfUpdateAvailable reports whether the server's launcher differs from the file at exe.
func SelfUpdateAvailable(m *Manifest, exe string) bool {
	if m == nil || m.Launcher == nil {
		return false
	}
	cur, err := hashFile(exe)
	return err != nil || !strings.EqualFold(cur, m.Launcher.SHA256)
}

// cleanupOldLauncher removes the executable a previous self-update moved aside.
func cleanupOldLauncher(exe string) {
	os.Remove(exe + ".old")
}

// SelfUpdate replaces the executable at exe with the manifest's launcher and relaunches it with args.
// It returns true when the new process has been started (the caller must exit).  On any failure the
// original executable is left in place and runnable.
func SelfUpdate(client *http.Client, server string, m *Manifest, exe string, args []string) (bool, error) {
	if !SelfUpdateAvailable(m, exe) {
		return false, nil
	}
	l := *m.Launcher
	l.Exec = true
	next := exe + ".new"
	old := exe + ".old"
	os.Remove(next)

	// download() writes <next>.part, verifies size + sha256, then renames to <next>
	if err := download(client, server, l, next, nil); err != nil {
		return false, fmt.Errorf("download launcher: %w", err)
	}

	os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		os.Remove(next)
		return false, fmt.Errorf("move current launcher aside: %w", err)
	}
	if err := os.Rename(next, exe); err != nil {
		os.Rename(old, exe) // put the working one back
		os.Remove(next)
		return false, fmt.Errorf("install new launcher: %w", err)
	}
	if err := relaunch(exe, args); err != nil {
		// can't start the new one: restore the old so the user is not left with a broken launcher
		os.Remove(exe)
		os.Rename(old, exe)
		return false, fmt.Errorf("start new launcher: %w", err)
	}
	return true, nil
}
