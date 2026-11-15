//go:build !darwin && !windows

package main

import (
	"os/exec"
	"path/filepath"
)

// reveal opens the folder (Linux file managers can't be told to select a file).
func reveal(path string, isDir bool) error {
	if !isDir {
		path = filepath.Dir(path)
	}
	return exec.Command("xdg-open", path).Start()
}
