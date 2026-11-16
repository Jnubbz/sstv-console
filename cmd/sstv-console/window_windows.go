//go:build windows

package main

// The Windows window: Microsoft Edge in app mode (its own window and taskbar button, no
// tabs or address bar), with its own profile so it is a separate process we can watch.
// Edge comes with Windows 10 and 11. If it's missing, the default browser opens instead.

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

var win struct {
	mu  sync.Mutex
	pid int
}

func windowPID() int {
	win.mu.Lock()
	defer win.mu.Unlock()
	return win.pid
}

// showWindow opens the window (or adopts keep, the window from before an update) and
// returns when it closes. It returns false if there's no window to watch.
func showWindow(url string, keep int) bool {
	if keep > 0 {
		if h, err := syscall.OpenProcess(synchronize, false, uint32(keep)); err == nil {
			win.mu.Lock()
			win.pid = keep
			win.mu.Unlock()
			syscall.WaitForSingleObject(h, syscall.INFINITE)
			syscall.CloseHandle(h)
			return true
		}
	}
	edge := findEdge()
	if edge == "" {
		exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
		return false // the page's Quit button stops the app
	}
	base, err := os.UserCacheDir() // %LocalAppData%: the window's browser profile is big and per machine
	if err != nil {
		base = os.TempDir()
	}
	profile := filepath.Join(base, "SSTV Console", "window")
	os.MkdirAll(profile, 0o700)
	cmd := exec.Command(edge, "--app="+url, "--user-data-dir="+profile,
		"--window-size=1440,900", "--autoplay-policy=no-user-gesture-required", "--no-first-run", "--no-default-browser-check")
	if err := cmd.Start(); err != nil {
		exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
		return false
	}
	win.mu.Lock()
	win.pid = cmd.Process.Pid
	win.mu.Unlock()
	start := time.Now()
	cmd.Wait()
	// Edge handed the window to an Edge already running with this profile and exited at
	// once: we can't watch it, so the page's Quit button stops the app.
	return time.Since(start) > 3*time.Second
}

func findEdge() string {
	for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LOCALAPPDATA"} {
		if base := os.Getenv(env); base != "" {
			p := filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe")
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}
