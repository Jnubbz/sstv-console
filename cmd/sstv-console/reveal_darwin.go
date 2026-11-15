package main

import "os/exec"

// reveal shows a file in Finder (or, for a folder, opens it).
func reveal(path string, isDir bool) error {
	if isDir {
		return exec.Command("open", path).Start()
	}
	return exec.Command("open", "-R", path).Start()
}
