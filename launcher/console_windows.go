//go:build windows

package main

import (
	"os"
	"syscall"
)

// The release exe is a GUI-subsystem program (no console window when double-clicked).  For --cli/--check
// attach to the console of whoever started us so output still appears.
func attachConsole() {
	k := syscall.NewLazyDLL("kernel32.dll")
	if r, _, _ := k.NewProc("AttachConsole").Call(^uintptr(0)); r == 0 { // ATTACH_PARENT_PROCESS
		return
	}
	if f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout, os.Stderr = f, f
	}
}
