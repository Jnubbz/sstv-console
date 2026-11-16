package main

// Putting a downloaded update in place. Plain file work, kept free of OS-specific calls so
// the tests run everywhere; install_darwin.go and install_windows.go wire it up.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

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
// .exe be renamed but not overwritten, so each existing file is renamed to *.old first.
// The names of those files are written to oldList, and the next start (tidyAfterUpdates)
// removes exactly them. If anything fails, every file is put back.
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
	var names []string
	for _, m := range done {
		if rel, err := filepath.Rel(dir, m.old); err == nil {
			names = append(names, rel)
		}
	}
	// If this can't be written the .old files just stay (the exe's is always cleared by name).
	_ = os.WriteFile(filepath.Join(dir, oldList), []byte(strings.Join(names, "\n")+"\n"), 0o644)
	return nil
}

// oldList is the file in the app's folder that names the *.old files the last update left.
const oldList = ".sstv-update-leftovers"

// tidyAfterUpdates runs at every start: it clears what an earlier update left behind.
func tidyAfterUpdates() {
	dir, err := exeDir()
	if err != nil {
		return
	}
	parent := dir // where an update's download folder is made: next to the app
	if app := appBundle(); app != "" {
		parent = filepath.Dir(app)
	}
	if runtime.GOOS == "windows" {
		clearOldIn(dir)
	}
	clearStaleStages(parent, time.Hour)
}

// clearOldIn removes the old files the last update listed in dir, and the old program. It
// never removes anything else, whatever it's called: the list's names must end in .old and
// stay inside dir.
func clearOldIn(dir string) {
	list := filepath.Join(dir, oldList)
	var names []string
	if b, err := os.ReadFile(list); err == nil {
		names = strings.Split(string(b), "\n")
	}
	names = append(names, winExe+".old")
	stuck := false
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" || !strings.HasSuffix(n, ".old") || !filepath.IsLocal(n) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, n)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			stuck = true // still in use: look again at the next start
		}
	}
	if !stuck {
		os.Remove(list)
	}
}

// updateFolder is how an update's working folder is named (os.MkdirTemp(dir, ".sstv-update-")).
var updateFolder = regexp.MustCompile(`^\.sstv-update-[0-9]+$`)

// clearStaleStages removes update working folders in dir that an interrupted update left
// behind, once they are older than age (so one in use right now is never touched).
func clearStaleStages(dir string, age time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !updateFolder.MatchString(e.Name()) {
			continue
		}
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > age {
			os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}
