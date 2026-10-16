//go:build windows

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

// defaultInstallDir is Documents\RTCW wherever Documents actually lives (OneDrive redirects it), which
// is where the engine already keeps wolfconfig_mp.cfg, so an existing config carries over.
func defaultInstallDir() string {
	// FOLDERID_Documents {FDD39AD0-238F-46AF-ADB4-6C85480369C7}
	id := syscall.GUID{Data1: 0xFDD39AD0, Data2: 0x238F, Data3: 0x46AF, Data4: [8]byte{0xAD, 0xB4, 0x6C, 0x85, 0x48, 0x03, 0x69, 0xC7}}
	proc := syscall.NewLazyDLL("shell32.dll").NewProc("SHGetKnownFolderPath")
	var out *uint16
	hr, _, _ := proc.Call(uintptr(unsafe.Pointer(&id)), 0, 0, uintptr(unsafe.Pointer(&out)))
	if hr == 0 && out != nil {
		defer syscall.NewLazyDLL("ole32.dll").NewProc("CoTaskMemFree").Call(uintptr(unsafe.Pointer(out)))
		return filepath.Join(syscall.UTF16ToString((*[1 << 16]uint16)(unsafe.Pointer(out))[:]), "RTCW")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Documents", "RTCW")
}

func platformName() string { return "windows" }

func gameBinary() string { return "ioWolfMP.x64.exe" }

// freeSpace reports the free bytes on the volume holding dir (or its nearest existing parent).
func freeSpace(dir string) (int64, bool) {
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")
	for p := dir; ; p = filepath.Dir(p) {
		ptr, err := syscall.UTF16PtrFromString(p)
		if err == nil {
			var free uint64
			if r, _, _ := proc.Call(uintptr(unsafe.Pointer(ptr)), uintptr(unsafe.Pointer(&free)), 0, 0); r != 0 {
				return int64(free), true
			}
		}
		if filepath.Dir(p) == p {
			return 0, false
		}
	}
}
