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
