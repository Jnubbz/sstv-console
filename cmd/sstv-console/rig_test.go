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
	if st, err := os.Stat(filepath.Join(pics, p.ID+".png")); err != nil || st.Size() < 1000 {
		t.Fatalf("file: %v", err)
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
