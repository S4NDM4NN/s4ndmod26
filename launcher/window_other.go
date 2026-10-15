//go:build !windows

package main

import "os/exec"

// There is no native window off Windows; the caller opens the default browser instead.
func runWindow(url string, quit <-chan struct{}) bool { return false }

func openURL(u string) { exec.Command("xdg-open", u).Start() }

func openPath(p string) { exec.Command("xdg-open", p).Start() }
