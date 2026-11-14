package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Settings are kept outside the app (so updates keep them), in the user's config folder.
type Settings struct {
	RigPort     string `json:"rigPort"`     // "" = not connected
	RigBaud     int    `json:"rigBaud"`     // CAT RATE on the radio
	AudioDevice string `json:"audioDevice"` // the browser's id for the sound input
	AudioLabel  string `json:"audioLabel"`
}

type settingsStore struct {
	mu   sync.Mutex
	path string
	s    Settings
}

func dataDir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		base = os.TempDir()
	}
	d := filepath.Join(base, "SSTV Console")
	os.MkdirAll(d, 0o755)
	return d
}

func loadSettings(dir string) *settingsStore {
	st := &settingsStore{path: filepath.Join(dir, "settings.json"), s: Settings{RigBaud: 38400}}
	if b, err := os.ReadFile(st.path); err == nil {
		json.Unmarshal(b, &st.s)
	}
	if st.s.RigBaud == 0 {
		st.s.RigBaud = 38400
	}
	return st
}

func (st *settingsStore) get() Settings {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.s
}

func (st *settingsStore) update(f func(*Settings)) {
	st.mu.Lock()
	defer st.mu.Unlock()
	f(&st.s)
	b, _ := json.MarshalIndent(st.s, "", "  ")
	tmp := st.path + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, st.path)
	}
}
