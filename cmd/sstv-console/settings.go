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
	OutDevice   string `json:"outDevice"` // the sound output that feeds the radio
	OutLabel    string `json:"outLabel"`
	TxLevel     int    `json:"txLevel"` // 1..100
	TxMode      string `json:"txMode"`
	MyCall      string `json:"myCall"`
	MyGrid      string `json:"myGrid"`
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
	if st.s.TxLevel < 1 || st.s.TxLevel > 100 {
		st.s.TxLevel = 50
	}
	if st.s.TxMode == "" {
		st.s.TxMode = "Scottie 1"
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
