//go:build !darwin && !windows

package main

// Linux and others: no self-install yet (no packaged build). Updates are still checked
// and shown, with a link to the release.

import (
	"syscall"
	"time"
)

func setupInstaller(u *desktopUpdater, a *App) {}

func waitForExit(pid int, max time.Duration) {
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) && syscall.Kill(pid, 0) == nil {
		time.Sleep(100 * time.Millisecond)
	}
}
