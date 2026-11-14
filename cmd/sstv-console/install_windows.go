//go:build windows

package main

// Installing an update on Windows: download next to SSTV-Console.exe, swap the files in
// (swapFiles), then start the new version with -after-update <this pid>, the same port and
// the same window. It waits for this one to exit, takes over the port, and the open page
// reloads itself: the window stays put.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

func setupInstaller(u *desktopUpdater, a *App) {
	dir, err := exeDir()
	if err != nil {
		return
	}
	u.stage = func() (string, error) {
		d, err := os.MkdirTemp(dir, ".sstv-update-")
		if err != nil {
			return "", fmt.Errorf("can't write to %s (%v): unzip the new version over it by hand this time", dir, err)
		}
		return d, nil
	}
	u.apply = func(zip, stage string) error {
		defer os.RemoveAll(stage)
		return swapFiles(dir, zip, stage)
	}
	u.restart = func() {
		a.setRestarting()
		args := []string{"-after-update", strconv.Itoa(os.Getpid()), "-port", strconv.Itoa(a.port)}
		if pid := windowPID(); pid > 0 {
			args = append(args, "-window", strconv.Itoa(pid))
		}
		cmd := exec.Command(filepath.Join(dir, winExe), args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000200 | 0x00000008} // new process group, detached
		if err := cmd.Start(); err != nil {
			updater.mu.Lock()
			updater.st.State, updater.st.Error = "", "Installed, but couldn't restart: close and reopen SSTV Console"
			updater.mu.Unlock()
			return
		}
		a.quit()
	}
}

const synchronize = 0x00100000

func waitForExit(pid int, max time.Duration) {
	h, err := syscall.OpenProcess(synchronize, false, uint32(pid))
	if err != nil {
		return // already gone
	}
	defer syscall.CloseHandle(h)
	syscall.WaitForSingleObject(h, uint32(max/time.Millisecond))
}
