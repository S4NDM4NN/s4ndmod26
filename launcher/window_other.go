//go:build !windows

package main

import (
	"os/exec"
	"strings"
)

// There is no native window off Windows; the caller opens the default browser instead.
func runWindow(url string, quit <-chan struct{}) bool { return false }

func openURL(u string) { exec.Command("xdg-open", u).Start() }

func openPath(p string) { exec.Command("xdg-open", p).Start() }

// browseFolder shows a folder picker when zenity or kdialog is installed; otherwise "" (type the path).
func browseFolder() string {
	for _, c := range [][]string{{"zenity", "--file-selection", "--directory"}, {"kdialog", "--getexistingdirectory"}} {
		if _, err := exec.LookPath(c[0]); err != nil {
			continue
		}
		out, _ := exec.Command(c[0], c[1:]...).Output()
		return strings.TrimSpace(string(out))
	}
	return ""
}
