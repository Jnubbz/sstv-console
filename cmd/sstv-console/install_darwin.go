//go:build darwin

package main

// Installing an update on the Mac: the new SSTV Console.app is unpacked next to the
// running one (same disk, so the swap is renames), checked, then swapped in. Then `open`
// starts the new app and this one quits; the Swift shell quits with it.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

func setupInstaller(u *desktopUpdater, a *App) {
	app := appBundle()
	if app == "" {
		return
	}
	u.stage = func() (string, error) {
		d, err := os.MkdirTemp(filepath.Dir(app), ".sstv-update-")
		if err != nil {
			return "", fmt.Errorf("can't write next to %s (%v): drag the new version to Applications by hand this time", app, err)
		}
		return d, nil
	}
	installed := app
	u.apply = func(zip, stage string) error {
		defer os.RemoveAll(stage)
		to, err := swapApp(app, zip, stage)
		if err == nil {
			installed = to
		}
		return err
	}
	u.restart = func() {
		a.setRestarting()
		cmd := exec.Command("/bin/sh", "-c", `sleep 1.5; exec /usr/bin/open -n "$1"`, "sh", installed)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		cmd.Start()
		a.quit()
	}
}

func waitForExit(pid int, limit time.Duration) {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) && syscall.Kill(pid, 0) == nil {
		time.Sleep(100 * time.Millisecond)
	}
}
