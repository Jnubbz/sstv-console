//go:build !windows

package main

// Mac and Linux without the Mac shell (development builds): open the page in the default
// browser. The installed Mac app runs with -nobrowser and its Swift shell is the window.

import (
	"os/exec"
	"runtime"
)

func showWindow(url string, keep int) bool {
	if runtime.GOOS == "darwin" {
		exec.Command("open", url).Start()
	} else {
		exec.Command("xdg-open", url).Start()
	}
	return false // no window to watch: the page's Quit button stops the app
}
