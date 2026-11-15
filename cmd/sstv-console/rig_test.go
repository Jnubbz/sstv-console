package main

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Jnubbz/sstv-console/sstv"
)

type fakeRadio struct {
	answers map[string]string
	sent    []string
	closed  bool
}

func (f *fakeRadio) Ask(cmd string) (string, error) {
	f.sent = append(f.sent, cmd)
	if a, ok := f.answers[cmd]; ok {
		return a, nil
	}
	return "", errors.New("no answer")
}
func (f *fakeRadio) Write(cmd string) error { f.sent = append(f.sent, cmd); return nil }
func (f *fakeRadio) Close()                 { f.closed = true }

func TestRigConnectReadsFrequencyAndMode(t *testing.T) {
	radio := &fakeRadio{answers: map[string]string{"ID;": "ID0800;", "FA;": "FA014230000;", "MD0;": "MD02;"}}
	r := newRig()
	r.open = func(string, int) (catPort, error) { return radio, nil }
	if err := r.connect("port", 38400); err != nil {
		t.Fatal(err)
	}
	st := r.status()
	if !st.Connected || st.Model != "FT-710" || st.FreqHz != 14230000 || st.Mode != "USB" {
		t.Fatalf("status %+v", st)
	}
	r.ptt(true)
	r.ptt(false)
	if got := strings.Join(radio.sent[len(radio.sent)-2:], ""); got != "TX1;TX0;" {
		t.Fatalf("ptt sent %q", got)
	}
	r.disconnect()
	if !radio.closed || r.status().Connected {
		t.Fatal("not disconnected")
	}
}

func TestRigNoAnswerIsAnErrorAndClosesThePort(t *testing.T) {
	radio := &fakeRadio{}
	r := newRig()
	r.open = func(string, int) (catPort, error) { return radio, nil }
	err := r.connect("port", 38400)
	if err == nil || !radio.closed || r.status().Connected || r.status().Error == "" {
		t.Fatalf("err %v, status %+v", err, r.status())
	}
}

// Audio in, a saved picture out, labelled with the radio's frequency.
func TestReceiverSavesAPicture(t *testing.T) {
	dir, pics := t.TempDir(), t.TempDir()
	rx := newReceiver(dir, pics, func() int { return 14230000 })
	rx.now = func() time.Time { return time.Date(2026, 11, 14, 17, 45, 12, 0, time.UTC) }
	m := sstv.ByName("Robot 36")
	img := sstv.TestCard(m.Width, m.Height)
	audio := append(make([]float32, 24000), sstv.Encode(img, m, 48000)...)
	audio = append(audio, make([]float32, 24000)...)
	sawReceiving := false
	for i := 0; i < len(audio); i += 4096 {
		rx.push(audio[i:min(i+4096, len(audio))], 48000)
		sawReceiving = sawReceiving || rx.status().Receiving
	}
	list := rx.pictures()
	if !sawReceiving || len(list) != 1 {
		t.Fatalf("receiving seen: %v, pictures: %+v", sawReceiving, list)
	}
	p := list[0]
	if p.ID != "2026-11-14 174512Z Robot 36 14230 kHz" || p.Mode != "Robot 36" || p.FreqHz != 14230000 {
		t.Fatalf("picture %+v", p)
	}
	// nothing goes to the Pictures folder until the user saves it; the picture waits in the inbox
	if ents, _ := os.ReadDir(pics); len(ents) != 0 {
		t.Fatalf("a picture was saved without being asked: %v", ents)
	}
	if st, err := os.Stat(filepath.Join(dir, "inbox", p.ID+".png")); err != nil || st.Size() < 1000 || p.Saved {
		t.Fatalf("inbox file: %v (saved %v)", err, p.Saved)
	}
	if rx.livePNG() == nil || math.IsNaN(rx.status().LevelDB) {
		t.Fatal("no live picture")
	}
	// the list survives a restart
	if again := newReceiver(dir, pics, func() int { return 0 }).pictures(); len(again) != 1 || again[0].ID != p.ID {
		t.Fatalf("after restart: %+v", again)
	}
}

// A radio that remembers what it was told.
type tunableRadio struct {
	fakeRadio
	freq, mode string
}

func (f *tunableRadio) Ask(cmd string) (string, error) {
	switch cmd {
	case "ID;":
		return "ID0800;", nil
	case "FA;":
		return "FA" + f.freq + ";", nil
	case "MD0;":
		return "MD0" + f.mode + ";", nil
	}
	return "", errors.New("no answer")
}

func (f *tunableRadio) Write(cmd string) error {
	f.sent = append(f.sent, cmd)
	switch {
	case strings.HasPrefix(cmd, "FA"):
		f.freq = cmd[2 : len(cmd)-1]
	case strings.HasPrefix(cmd, "MD0"):
		f.mode = cmd[3:4]
	}
	return nil
}

func TestTuneToSSTVFrequencies(t *testing.T) {
	radio := &tunableRadio{freq: "014074000", mode: "2"} // 20 m, USB
	r := newRig()
	r.open = func(string, int) (catPort, error) { return radio, nil }
	if err := r.connect("port", 38400); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		band, before, freq, mode string
	}{
		{"40m", "2", "007171000", "8"}, // from USB: DATA-L on 40
		{"20m", "1", "014230000", "C"}, // from LSB: DATA-U on 20
		{"80m", "C", "003845000", "8"}, // DATA-U to DATA-L
		{"10m", "8", "028680000", "C"},
		{"15m", "3", "021340000", "C"}, // from CW
		{"6m", "4", "050680000", "C"},  // from FM
		{"20m", "5", "014230000", "C"}, // from AM
		{"40m", "9", "007171000", "8"}, // from RTTY
	} {
		radio.mode = c.before
		r.poll(r.gen)
		if err := r.tune(c.band); err != nil {
			t.Fatalf("%s: %v", c.band, err)
		}
		if radio.freq != c.freq || radio.mode != c.mode {
			t.Errorf("%s from mode %s: radio at %s mode %s, want %s mode %s", c.band, c.before, radio.freq, radio.mode, c.freq, c.mode)
		}
	}
	if st := r.status(); st.FreqHz != 7171000 || st.Mode != "DATA-L" {
		t.Errorf("status %+v", st)
	}
	// a radio that won't change mode is reported, not papered over
	stuck := &stuckModeRadio{tunableRadio{freq: "014074000", mode: "3"}}
	r2 := newRig()
	r2.open = func(string, int) (catPort, error) { return stuck, nil }
	if err := r2.connect("port", 38400); err != nil {
		t.Fatal(err)
	}
	if err := r2.tune("20m"); err == nil || !strings.Contains(err.Error(), "didn't switch") {
		t.Errorf("stuck mode: %v", err)
	}
	if err := r.tune("17m"); err == nil {
		t.Error("tuned to a band with no SSTV frequency")
	}
}

// Tuning in partway: the picture is still saved, at the size that was received, and a test
// recording comes out as a WAV file.
func TestReceiverJoinsAndRecords(t *testing.T) {
	dir, pics := t.TempDir(), t.TempDir()
	rx := newReceiver(dir, pics, func() int { return 0 })
	m := sstv.ByName("Robot 36")
	full := sstv.Encode(sstv.TestCard(m.Width, m.Height), m, 48000)
	audio := append([]float32{}, full[48000*10:]...) // the first 10 s missed
	audio = append(audio, make([]float32, 48000*52)...)
	rx.record()
	joined := false
	for i := 0; i < len(audio); i += 4096 {
		rx.push(audio[i:min(i+4096, len(audio))], 48000)
		joined = joined || rx.status().Joined
	}
	list := rx.pictures()
	if !joined || len(list) != 1 || list[0].H < 150 || list[0].H > 185 { // 26.9 s of 150 ms lines: about 179 rows
		t.Fatalf("joined %v, pictures %+v", joined, list)
	}
	rec := rx.status().Recorded
	if st, err := os.Stat(rec); err != nil || st.Size() != 44+60*48000*2 {
		t.Fatalf("recording %q: %v", rec, err)
	}
}

// A radio that takes a new frequency but ignores mode changes.
type stuckModeRadio struct{ tunableRadio }

func (f *stuckModeRadio) Write(cmd string) error {
	if strings.HasPrefix(cmd, "MD0") {
		f.sent = append(f.sent, cmd)
		return nil
	}
	return f.tunableRadio.Write(cmd)
}

// Save copies to the Pictures folder, Delete only throws away what was never saved, and
// pictures nobody touched expire after a week.
func TestSaveDeleteAndExpire(t *testing.T) {
	dir, pics := t.TempDir(), filepath.Join(t.TempDir(), "SSTV Console")
	rx := newReceiver(dir, pics, func() int { return 14230000 })
	now := time.Date(2026, 11, 14, 17, 0, 0, 0, time.UTC)
	rx.now = func() time.Time { return now }
	m := sstv.ByName("Robot 36")
	card := sstv.TestCard(m.Width, m.Height)
	for i := 0; i < 3; i++ { // three pictures a minute apart
		rx.mu.Lock()
		rx.save(sstv.Event{Mode: m, Image: card})
		rx.mu.Unlock()
		now = now.Add(time.Minute)
	}
	list := rx.pictures()
	if len(list) != 3 {
		t.Fatalf("%d pictures", len(list))
	}
	if _, err := os.Stat(pics); err == nil {
		t.Fatal("the Pictures folder was made before anything was saved")
	}
	keep, bin, old := list[0].ID, list[1].ID, list[2].ID

	path, err := rx.savePicture(keep)
	if err != nil || path != filepath.Join(pics, keep+".png") {
		t.Fatalf("save: %q %v", path, err)
	}
	if st, err := os.Stat(path); err != nil || st.Size() < 100 {
		t.Fatalf("saved file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "inbox", keep+".png")); err == nil {
		t.Fatal("still in the inbox after saving")
	}
	if rx.file(keep) != path || rx.savedPath(keep) != path || rx.savedPath(bin) != "" {
		t.Fatal("wrong file for a saved / unsaved picture")
	}
	if p, err := rx.savePicture(keep); err != nil || p != path { // saving twice is harmless
		t.Fatalf("save again: %q %v", p, err)
	}
	if rx.deletePicture(keep) == nil {
		t.Fatal("deleted a saved picture")
	}
	if err := rx.deletePicture(bin); err != nil || rx.has(bin) {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "inbox", bin+".png")); err == nil {
		t.Fatal("deleted picture's file is still there")
	}
	if _, err := rx.savePicture("nope"); err == nil {
		t.Fatal("saved a picture that isn't listed")
	}
	// the list, saved flags included, survives a restart
	again := newReceiver(dir, pics, func() int { return 0 })
	if l := again.pictures(); len(l) != 2 || !l[0].Saved || l[1].Saved {
		t.Fatalf("after restart: %+v", l)
	}
	// a week later: the unsaved one goes, the saved one stays
	again.now = func() time.Time { return now.Add(unsavedKeep + time.Hour) }
	again.mu.Lock()
	again.expire()
	again.mu.Unlock()
	if l := again.pictures(); len(l) != 1 || l[0].ID != keep || again.has(old) {
		t.Fatalf("after expiry: %+v", l)
	}
	if _, err := os.Stat(filepath.Join(dir, "inbox", old+".png")); err == nil {
		t.Fatal("expired picture's file is still there")
	}
}

// Pictures from before there was an inbox are in the Pictures folder already: they come up saved.
func TestOldPicturesCountAsSaved(t *testing.T) {
	dir, pics := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(pics, "old.png"), []byte("png"), 0o644)
	idx := `[{"id":"old","time":"2026-11-01T10:00:00Z","mode":"Robot 36","w":320,"h":240},{"id":"gone","time":"2026-11-01T11:00:00Z","mode":"Robot 36","w":320,"h":240}]`
	os.WriteFile(filepath.Join(dir, "received.json"), []byte(idx), 0o644)
	rx := newReceiver(dir, pics, func() int { return 0 })
	if l := rx.pictures(); len(l) != 1 || l[0].ID != "old" || !l[0].Saved {
		t.Fatalf("%+v", l)
	}
}
