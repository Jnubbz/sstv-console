package main

// Putting a downloaded update in place. Plain file work, kept free of OS-specific calls so
// the tests run everywhere; install_darwin.go and install_windows.go wire it up.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Jnubbz/sstv-console/internal/selfupdate"
)

// Mac -------------------------------------------------------------------------------

// appBundle: the .app the running program is in ("" if it isn't, e.g. go run).
func appBundle() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	macos := filepath.Dir(exe)
	contents := filepath.Dir(macos)
	app := filepath.Dir(contents)
	if filepath.Base(macos) != "MacOS" || filepath.Base(contents) != "Contents" || !strings.HasSuffix(app, ".app") {
		return ""
	}
	return app
}

// swapApp unpacks zip in stage and puts the .app inside it in place of app. The old app is
// moved into stage (thrown away with it); if the new one can't be put in place, the old
// one is put back. Returns where the new app went.
func swapApp(app, zip, stage string) (string, error) {
	x := filepath.Join(stage, "x")
	if err := os.Mkdir(x, 0o755); err != nil {
		return "", err
	}
	if err := selfupdate.Unzip(zip, x); err != nil {
		return "", err
	}
	found, _ := filepath.Glob(filepath.Join(x, "*", "*.app"))
	if len(found) != 1 {
		return "", errors.New("the update doesn't contain the app")
	}
	newApp := found[0]
	if !hasProgram(newApp) {
		return "", errors.New("the update's app has no program in it")
	}
	target := filepath.Join(filepath.Dir(app), filepath.Base(newApp))
	old := filepath.Join(stage, "old.app")
	if err := os.Rename(app, old); err != nil {
		return "", fmt.Errorf("can't replace %s: %v", app, err)
	}
	var older string // a copy already at the new name: set it aside too
	if target != app {
		if _, err := os.Stat(target); err == nil {
			older = filepath.Join(stage, "older.app")
			if err := os.Rename(target, older); err != nil {
				os.Rename(old, app)
				return "", fmt.Errorf("can't replace %s: %v", target, err)
			}
		}
	}
	if err := os.Rename(newApp, target); err != nil {
		if older != "" {
			os.Rename(older, target)
		}
		os.Rename(old, app)
		return "", fmt.Errorf("can't put the new version in place: %v", err)
	}
	return target, nil
}

// hasProgram: the bundle's main program (Info.plist CFBundleExecutable) is there and runnable.
func hasProgram(app string) bool {
	exe := "sstv-console"
	if b, err := os.ReadFile(filepath.Join(app, "Contents", "Info.plist")); err == nil {
		s := string(b)
		if i := strings.Index(s, "<key>CFBundleExecutable</key>"); i >= 0 {
			rest := s[i:]
			if a := strings.Index(rest, "<string>"); a >= 0 {
				if z := strings.Index(rest[a:], "</string>"); z > 0 {
					exe = rest[a+len("<string>") : a+z]
				}
			}
		}
	}
	st, err := os.Stat(filepath.Join(app, "Contents", "MacOS", exe))
	return err == nil && st.Mode()&0o100 != 0
}

// Windows -----------------------------------------------------------------------------

const winFolder = "SSTV-Console-Windows" // the folder inside the Windows zip
const winExe = "SSTV-Console.exe"

func exeDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return filepath.Dir(exe), nil
}

// swapFiles unpacks zip in stage and copies its files over dir. Windows lets a running
// .exe be renamed but not overwritten, so each existing file is renamed to *.old first
// (cleared by the next start, clearOldFiles). If anything fails, every file is put back.
func swapFiles(dir, zip, stage string) error {
	x := filepath.Join(stage, "x")
	if err := os.Mkdir(x, 0o755); err != nil {
		return err
	}
	if err := selfupdate.Unzip(zip, x); err != nil {
		return err
	}
	src := filepath.Join(x, winFolder)
	if st, err := os.Stat(filepath.Join(src, winExe)); err != nil || st.Size() == 0 {
		return errors.New("the update doesn't contain the app")
	}
	type moved struct{ target, old string }
	var done []moved
	var placed []string
	undo := func() {
		for _, p := range placed {
			os.Remove(p)
		}
		for i := len(done) - 1; i >= 0; i-- {
			os.Rename(done[i].old, done[i].target)
		}
	}
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dir, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if _, err := os.Stat(target); err == nil {
			old := target + ".old"
			os.Remove(old)
			if err := os.Rename(target, old); err != nil {
				return fmt.Errorf("can't replace %s: %v", rel, err)
			}
			done = append(done, moved{target, old})
		}
		if err := os.Rename(p, target); err != nil {
			return fmt.Errorf("can't put %s in place: %v", rel, err)
		}
		placed = append(placed, target)
		return nil
	})
	if err != nil {
		undo()
		return err
	}
	return nil
}

// clearOldFiles removes the *.old files a Windows update left next to the app.
func clearOldFiles() {
	dir, err := exeDir()
	if err != nil {
		return
	}
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(p, ".old") {
			os.Remove(p)
		}
		return nil
	})
}
