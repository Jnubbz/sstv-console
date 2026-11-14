package main

// Updates, built in from day one (same design as CW Console): a few seconds after starting
// and every 6 hours, look for a new release; show it on the page; on "Install and
// restart" download it, check it against the signed manifest (internal/selfupdate), swap
// the app in place and start the new version. The page reloads itself when the new
// version answers. Settings and received pictures live outside the app and are kept.

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"time"

	"github.com/Jnubbz/sstv-console/internal/selfupdate"
)

type updateStatus struct {
	Current    string `json:"current"`
	Latest     string `json:"latest,omitempty"`
	Available  bool   `json:"available"`
	Notes      string `json:"notes,omitempty"`
	State      string `json:"state"` // "" | "checking" | "downloading" | "installing" | "restarting"
	Error      string `json:"error,omitempty"`
	Done       int64  `json:"done,omitempty"`
	Total      int64  `json:"total,omitempty"`
	CanInstall bool   `json:"canInstall"`
	Checked    string `json:"checked,omitempty"` // RFC 3339
}

type desktopUpdater struct {
	mu      sync.Mutex
	st      updateStatus
	found   *selfupdate.Manifest
	client  *selfupdate.Client
	platKey string                 // "darwin-arm64", "windows-amd64"
	busy    func() error           // refuses while transmitting
	stage   func() (string, error) // folder on the app's own disk for the download
	apply   func(zip, stage string) error
	restart func() // start the new version and quit this one
}

var updater = &desktopUpdater{client: &selfupdate.Client{}}

func (u *desktopUpdater) status() updateStatus {
	u.mu.Lock()
	defer u.mu.Unlock()
	s := u.st
	s.Current = appVersion
	s.CanInstall = u.apply != nil && s.Available
	return s
}

// check asks for the latest release. A failed check never hides an update already found.
func (u *desktopUpdater) check(ctx context.Context) updateStatus {
	u.mu.Lock()
	if u.st.State != "" && u.st.State != "checking" {
		u.mu.Unlock()
		return u.status()
	}
	u.st.State, u.st.Error = "checking", ""
	u.mu.Unlock()
	m, err := u.client.Check(ctx)
	u.mu.Lock()
	u.st.State = ""
	u.st.Checked = time.Now().UTC().Format(time.RFC3339)
	switch {
	case selfupdate.NoRelease(err):
	case err != nil:
		u.st.Error = err.Error()
	case selfupdate.Newer(m.Version, appVersion) && m.Assets[u.platKey].URL != "":
		u.found = m
		u.st.Available, u.st.Latest, u.st.Notes = true, m.Version, m.Notes
	case u.found == nil:
		u.st.Latest = m.Version
	}
	u.mu.Unlock()
	return u.status()
}

// startInstall downloads, verifies and installs in the background, then restarts.
func (u *desktopUpdater) startInstall() error {
	if u.apply == nil {
		return errors.New("this copy can't update itself (a development build, or not the installed app): download the new version from GitHub")
	}
	if u.busy != nil {
		if err := u.busy(); err != nil {
			return err
		}
	}
	u.mu.Lock()
	if u.found == nil {
		u.mu.Unlock()
		return errors.New("you're up to date")
	}
	if u.st.State != "" && u.st.State != "checking" {
		u.mu.Unlock()
		return errors.New("already updating")
	}
	m := u.found
	u.st.State, u.st.Error, u.st.Done, u.st.Total = "downloading", "", 0, m.Assets[u.platKey].Size
	u.mu.Unlock()
	go func() {
		fail := func(err error) {
			u.mu.Lock()
			u.st.State, u.st.Error = "", "Update failed: "+err.Error()
			u.mu.Unlock()
		}
		stage, err := u.stage()
		if err != nil {
			fail(err)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		zip, err := u.client.Download(ctx, m.Assets[u.platKey], stage, func(done, total int64) {
			u.mu.Lock()
			u.st.Done, u.st.Total = done, total
			u.mu.Unlock()
		})
		if err != nil {
			fail(err)
			return
		}
		u.mu.Lock()
		u.st.State = "installing"
		u.mu.Unlock()
		if err := u.apply(zip, stage); err != nil {
			fail(err)
			return
		}
		u.mu.Lock()
		u.st.State = "restarting"
		u.mu.Unlock()
		time.Sleep(700 * time.Millisecond) // let the page see "restarting"
		u.restart()
	}()
	return nil
}

// start: wire the updater to the app and begin checking. Development builds ("dev") never
// update themselves.
func (u *desktopUpdater) start(a *App) {
	u.platKey = runtime.GOOS + "-" + runtime.GOARCH
	u.busy = a.busy
	u.restart = func() { a.setRestarting(); a.quit() }
	setupInstaller(u, a) // per OS: sets stage, apply and restart where updating in place works
	go func() {
		time.Sleep(4 * time.Second)
		for {
			u.check(context.Background())
			time.Sleep(6 * time.Hour)
		}
	}()
}
