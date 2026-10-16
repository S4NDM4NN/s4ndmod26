//go:build windows

package main

import (
	"os/exec"
	"syscall"

	"github.com/jchv/go-webview2"
)

// runWindow shows url in a native WebView2 window and blocks until it is closed or quit is closed.
// It reports false if WebView2 is unavailable, so the caller can fall back to the browser.
func runWindow(url string, quit <-chan struct{}) bool {
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  "S4NDMoD26",
			Width:  760,
			Height: 520,
			Center: true,
			IconId: 1, // the icon group embedded by rsrc_windows_amd64.syso (branding/s4nd.ico)
		},
	})
	if w == nil {
		return false
	}
	defer w.Destroy()
	w.SetSize(760, 520, webview2.HintFixed)
	go func() {
		<-quit
		w.Dispatch(func() { w.Terminate() })
	}()
	w.Navigate(url)
	w.Run()
	return true
}

func openURL(u string) {
	exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
}

func openPath(p string) {
	c := exec.Command("explorer", p)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: false}
	c.Start()
}
