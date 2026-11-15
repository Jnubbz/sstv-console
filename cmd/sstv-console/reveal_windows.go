package main

import (
	"os/exec"
	"syscall"
)

// reveal shows a file in Explorer (or, for a folder, opens it). Explorer wants its
// argument quoted by hand: /select, and the path in one quoted piece fails.
func reveal(path string, isDir bool) error {
	cmd := exec.Command("explorer.exe")
	line := `explorer.exe /select,"` + path + `"`
	if isDir {
		line = `explorer.exe "` + path + `"`
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: line, HideWindow: true}
	return cmd.Start()
}
