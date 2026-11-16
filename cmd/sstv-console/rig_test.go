package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// A port with no radio behind it, only control lines: what a simple keying interface is.
type linePort struct {
	rts, dtr bool
	log      []string
	closed   bool
}

func (l *linePort) Ask(string) (string, error) { return "", errors.New("no CAT here") }
func (l *linePort) Write(string) error         { return errors.New("no CAT here") }
func (l *linePort) Close()                     { l.closed = true; l.rts, l.dtr = false, false }
func (l *linePort) SetRTS(on bool) error {
	l.rts = on
	l.log = append(l.log, fmt.Sprint("rts ", on))
	return nil
}
func (l *linePort) SetDTR(on bool) error {
	l.dtr = on
	l.log = append(l.log, fmt.Sprint("dtr ", on))
	return nil
}

// Any radio: no CAT, keyed by RTS or DTR, the frequency is the band that was picked.
func TestGenericRadioKeysWithALine(t *testing.T) {
	for _, line := range []string{"rts", "dtr"} {
		lp := &linePort{}
		r := newRig()
		r.open = func(string, int) (catPort, error) { return lp, nil }
		if err := r.connectWith(rigOpts{Kind: "generic", Port: "COM7", PTT: line}); err != nil {
			t.Fatal(line, err)
		}
		st := r.status()
		if !st.Connected || !st.Manual || st.Kind != "generic" || st.FreqHz != 0 || !strings.Contains(st.Model, strings.ToUpper(line)) {
			t.Fatalf("%s status %+v", line, st)
		}
		if lp.rts || lp.dtr {
			t.Fatal("a line was up before any transmission")
		}
		if err := r.tune("20m"); err != nil || r.status().FreqHz != 14230000 { // noted, not sent: no CAT
			t.Fatalf("tune: %v, %+v", err, r.status())
		}
		if len(lp.log) != 0 {
			t.Fatalf("tuning touched the lines: %v", lp.log)
		}
		if err := r.ptt(true); err != nil || (line == "rts") != lp.rts || (line == "dtr") != lp.dtr {
			t.Fatalf("%s key: rts %v dtr %v, %v", line, lp.rts, lp.dtr, err)
		}
		if err := r.ptt(false); err != nil || lp.rts || lp.dtr {
			t.Fatalf("%s unkey: rts %v dtr %v, %v", line, lp.rts, lp.dtr, err)
		}
		r.ptt(true)
		r.disconnect() // closing the port drops the line, even if a transmission was in progress
		if !lp.closed || lp.rts || lp.dtr || r.status().Connected {
			t.Fatalf("after disconnect: %+v closed %v rts %v", r.status(), lp.closed, lp.rts)
		}
	}
}

// VOX: no port at all. The radio counts as connected (band buttons, Tune), keying does nothing.
func TestGenericRadioVOX(t *testing.T) {
	r := newRig()
	r.open = func(string, int) (catPort, error) { t.Fatal("opened a port for VOX"); return nil, nil }
	if err := r.connectWith(rigOpts{Kind: "generic", PTT: "none"}); err != nil {
		t.Fatal(err)
	}
	if st := r.status(); !st.Connected || st.Port != "" || !strings.Contains(st.Model, "VOX") {
		t.Fatalf("%+v", st)
	}
	if r.ptt(true) != nil || r.ptt(false) != nil {
		t.Fatal("VOX keying should do nothing, quietly")
	}
	if err := r.connectWith(rigOpts{Kind: "generic", PTT: "rts"}); err == nil {
		t.Fatal("RTS keying with no port")
	}
}

// A whole transmission keys the line and lets it go again, through the same path as CAT radios.
func TestGenericRadioKeysDuringATransmission(t *testing.T) {
	a := newApp(7380, func() {}, t.TempDir(), t.TempDir())
	lp := &linePort{}
	a.rig.open = func(string, int) (catPort, error) { return lp, nil }
	if err := a.rig.connectWith(rigOpts{Kind: "generic", Port: "port", PTT: "rts"}); err != nil {
		t.Fatal(err)
	}
	var fire func()
	a.tx.after = func(d time.Duration, f func()) *time.Timer { fire = f; return time.NewTimer(time.Hour) }
	id, secs, err := a.tx.prepareTune()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.tx.key(id); err != nil || !lp.rts {
		t.Fatalf("key: %v, rts %v", err, lp.rts)
	}
	a.tx.since = time.Now().Add(-time.Duration(secs * float64(time.Second)))
	fire()
	if lp.rts || a.tx.isKeyed() {
		t.Fatalf("still keyed after the watchdog: rts %v", lp.rts)
	}
}

func TestYaesuModelsAreNamedAndOthersStillWork(t *testing.T) {
	for id, want := range map[string]string{
		"ID0800;": "FT-710", "ID0670;": "FT-991A", "ID0570;": "FT-991", "ID0650;": "FT-891", "ID0761;": "FTDX10",
		"ID0244;": "Yaesu radio (ID0244)",
	} {
		radio := &fakeRadio{answers: map[string]string{"ID;": id, "FA;": "FA014230000;", "MD0;": "MD0C;"}}
		r := &rigConn{open: func(string, int) (catPort, error) { return radio, nil }}
		if err := r.connect("p", 38400); err != nil {
			t.Fatal(id, err)
		}
		if got := r.status().Model; got != want {
			t.Errorf("%s: model %q, want %q", id, got, want)
		}
		if st := r.status(); st.FreqHz != 14230000 || st.Mode != "DATA-U" {
			t.Errorf("%s: freq %d mode %q", id, st.FreqHz, st.Mode)
		}
		r.disconnect()
	}
}

// fakeHam is a rigctld connection: a radio on a frequency in a mode, that records keying.
type fakeHam struct {
	mu     sync.Mutex
	hz     int
	mode   string
	keyed  bool
	closed bool
	stuck  bool // ignores mode changes, like a radio that can't do DATA
	dead   bool // the link to rigctld dropped: every call fails and Broken says so
}

var errLinkDown = errors.New("connection reset")

func (f *fakeHam) Freq() (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dead {
		return 0, errLinkDown
	}
	return f.hz, nil
}
func (f *fakeHam) Mode() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dead {
		return "", errLinkDown
	}
	return f.mode, nil
}
func (f *fakeHam) SetFreq(hz int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dead {
		return errLinkDown
	}
	f.hz = hz
	return nil
}
func (f *fakeHam) SetMode(m string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dead {
		return errLinkDown
	}
	if !f.stuck {
		f.mode = m
	}
	return nil
}
func (f *fakeHam) PTT(on bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dead {
		return errLinkDown
	}
	f.keyed = on
	return nil
}
func (f *fakeHam) Broken() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.dead }
func (f *fakeHam) Close()       { f.mu.Lock(); defer f.mu.Unlock(); f.closed = true }

func TestHamlibRadioReadsTunesAndKeys(t *testing.T) {
	h := &fakeHam{hz: 7074000, mode: "CW"}
	var asked string
	r := &rigConn{dial: func(addr string) (hamRadio, error) { asked = addr; return h, nil }}
	if err := r.connectWith(rigOpts{Kind: "hamlib"}); err != nil {
		t.Fatal(err)
	}
	if asked != "127.0.0.1:4532" {
		t.Errorf("dialled %q", asked)
	}
	if st := r.status(); !st.Connected || st.Kind != "hamlib" || st.FreqHz != 7074000 || st.Mode != "CW" || st.Manual {
		t.Errorf("status %+v", st)
	}
	if err := r.tune("20m"); err != nil {
		t.Fatal(err)
	}
	if st := r.status(); st.FreqHz != 14230000 || st.Mode != "DATA-U" || h.mode != "PKTUSB" {
		t.Errorf("20m: %+v hamlib mode %s", st, h.mode)
	}
	if err := r.tune("40m"); err != nil || h.mode != "PKTLSB" || h.hz != 7171000 {
		t.Errorf("40m: %v mode %s hz %d", err, h.mode, h.hz)
	}
	if err := r.ptt(true); err != nil || !h.keyed {
		t.Errorf("ptt on: %v keyed %v", err, h.keyed)
	}
	r.ptt(false)
	if h.keyed {
		t.Error("still keyed")
	}
	r.disconnect()
	if !h.closed || r.status().Connected {
		t.Error("disconnect left it connected")
	}
}

func TestHamlibRadioThatWontSwitchModeSaysSo(t *testing.T) {
	h := &fakeHam{hz: 7074000, mode: "USB", stuck: true}
	r := &rigConn{dial: func(string) (hamRadio, error) { return h, nil }}
	if err := r.connectWith(rigOpts{Kind: "hamlib", Addr: "shack.local:4532"}); err != nil {
		t.Fatal(err)
	}
	defer r.disconnect()
	if err := r.tune("20m"); err == nil || !strings.Contains(err.Error(), "didn't switch") {
		t.Errorf("tune error: %v", err)
	}
}

func TestHamlibConnectProblemsAreClear(t *testing.T) {
	r := &rigConn{dial: func(string) (hamRadio, error) { return nil, fmt.Errorf("connection refused") }}
	if err := r.connectWith(rigOpts{Kind: "hamlib"}); err == nil || !strings.Contains(err.Error(), "Start it first") {
		t.Errorf("refused: %v", err)
	}
	if err := r.connectWith(rigOpts{Kind: "hamlib", Addr: "http://evil/x"}); err == nil || !strings.Contains(err.Error(), "isn't an address") {
		t.Errorf("bad address: %v", err)
	}
}

// rigctld restarts (or the link drops): the app dials it again by itself, and keying the radio
// off never waits for the next poll to do it.
func TestHamlibDialsAgainWhenRigctldRestarts(t *testing.T) {
	first := &fakeHam{hz: 14230000, mode: "PKTUSB"}
	second := &fakeHam{hz: 14230000, mode: "PKTUSB"}
	var mu sync.Mutex
	dials, refuse := 0, false
	r := &rigConn{dial: func(string) (hamRadio, error) {
		mu.Lock()
		defer mu.Unlock()
		dials++
		if refuse {
			return nil, errors.New("connection refused")
		}
		if dials == 1 {
			return first, nil
		}
		return second, nil
	}}
	if err := r.connectWith(rigOpts{Kind: "hamlib"}); err != nil {
		t.Fatal(err)
	}
	defer r.disconnect()

	// rigctld goes away and isn't back yet: the status says so, and it keeps trying
	first.mu.Lock()
	first.dead = true
	first.mu.Unlock()
	mu.Lock()
	refuse = true
	mu.Unlock()
	r.poll(r.gen)
	if st := r.status(); !strings.Contains(st.Error, "lost rigctld") || st.FreqHz != 0 {
		t.Fatalf("status while rigctld is down: %+v", st)
	}
	r.poll(r.gen) // inside the 3 seconds: no new dial
	mu.Lock()
	n := dials
	mu.Unlock()
	if n != 2 {
		t.Errorf("dialled %d times, want 2 (the first connect and one try)", n)
	}

	// rigctld is back: the next try (after the pause) reconnects and the radio shows again
	mu.Lock()
	refuse = false
	mu.Unlock()
	r.mu.Lock()
	r.tried = time.Now().Add(-time.Minute)
	r.mu.Unlock()
	r.poll(r.gen)
	if st := r.status(); st.Error != "" || st.FreqHz != 14230000 || st.Mode != "DATA-U" {
		t.Fatalf("status after rigctld came back: %+v", st)
	}
	if !first.closed {
		t.Error("the broken connection wasn't closed")
	}
}

func TestKeyingOffDialsAgainAtOnce(t *testing.T) {
	first := &fakeHam{hz: 14230000, mode: "PKTUSB"}
	second := &fakeHam{hz: 14230000, mode: "PKTUSB", keyed: true} // the radio is still transmitting
	dials := 0
	r := &rigConn{dial: func(string) (hamRadio, error) {
		dials++
		if dials == 1 {
			return first, nil
		}
		return second, nil
	}}
	if err := r.connectWith(rigOpts{Kind: "hamlib"}); err != nil {
		t.Fatal(err)
	}
	defer r.disconnect()
	first.mu.Lock()
	first.dead = true
	first.mu.Unlock()
	r.mu.Lock()
	r.tried = time.Now() // a poll just tried: keying off must not wait out the pause
	r.mu.Unlock()
	if err := r.ptt(false); err != nil {
		t.Fatalf("ptt off: %v", err)
	}
	if second.keyed {
		t.Error("the radio is still keyed")
	}
}

// Two connects at once must not leave a connection open that nothing will ever close.
func TestConnectsOneAtATime(t *testing.T) {
	var mu sync.Mutex
	var made []*fakeHam
	r := &rigConn{dial: func(string) (hamRadio, error) {
		time.Sleep(20 * time.Millisecond) // a slow dial: the other connect gets in
		h := &fakeHam{hz: 14230000, mode: "PKTUSB"}
		mu.Lock()
		made = append(made, h)
		mu.Unlock()
		return h, nil
	}}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.connectWith(rigOpts{Kind: "hamlib"})
		}()
	}
	wg.Wait()
	r.disconnect()
	mu.Lock()
	defer mu.Unlock()
	if len(made) != 4 {
		t.Fatalf("%d connections made", len(made))
	}
	for i, h := range made {
		if !h.closed {
			t.Errorf("connection %d was left open", i)
		}
	}
}
