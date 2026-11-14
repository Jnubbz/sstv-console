package main

import (
	"bytes"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/Jnubbz/sstv-console/sstv"
)

func cardPNG(t *testing.T) []byte {
	var b bytes.Buffer
	if err := png.Encode(&b, sstv.TestCard(320, 256)); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// The whole transmit path: the picture becomes audio a decoder reads back; the radio is
// keyed, and unkeyed by the watchdog even if the page never says it finished.
func TestTransmitKeysAndTheWatchdogUnkeys(t *testing.T) {
	a := newApp(7380, func() {}, t.TempDir(), t.TempDir())
	radio := &tunableRadio{freq: "014230000", mode: "2"}
	a.rig.open = func(string, int) (catPort, error) { return radio, nil }
	if err := a.rig.connect("port", 38400); err != nil {
		t.Fatal(err)
	}
	var fire func()
	var wait time.Duration
	a.tx.after = func(d time.Duration, f func()) *time.Timer { wait, fire = d, f; return time.NewTimer(time.Hour) }

	id, secs, err := a.tx.prepare(cardPNG(t), "Robot 36", "W1AW")
	if err != nil || secs < 36 || secs > 38 {
		t.Fatalf("prepare: %v, %.1f s", err, secs)
	}
	wav := a.tx.audio(id)
	if len(wav) != 44+int(secs*txRate+0.5)*2 || string(wav[:4]) != "RIFF" {
		t.Fatalf("audio is %d bytes", len(wav))
	}
	// what goes on the air decodes
	f := make([]float32, (len(wav)-44)/2)
	for i := range f {
		f[i] = float32(int16(uint16(wav[44+2*i])|uint16(wav[45+2*i])<<8)) / 32768
	}
	done := false
	d := sstv.NewDecoder(txRate)
	for _, e := range d.Write(append(f, make([]float32, txRate)...)) {
		done = done || (e.Kind == sstv.Done && e.Mode.Name == "Robot 36" && !e.Joined)
	}
	if !done {
		t.Fatal("the audio to transmit doesn't decode")
	}

	if err := a.tx.key(id); err != nil {
		t.Fatal(err)
	}
	if got := radio.sent[len(radio.sent)-1]; got != "TX1;" || !a.tx.status().PTT {
		t.Fatalf("after key the radio got %q", got)
	}
	if wait < time.Duration(secs*float64(time.Second)) || wait > time.Duration((secs+3)*float64(time.Second)) {
		t.Fatalf("watchdog set for %v, picture is %.1f s", wait, secs)
	}
	if a.tx.key(id) == nil {
		t.Fatal("keyed twice")
	}
	if a.busy() == nil {
		t.Fatal("an update would be allowed mid-transmission")
	}
	// receiving is deaf to my own picture
	a.rx.push(f[:48000*3], txRate)
	if a.rx.status().Receiving {
		t.Fatal("decoding my own transmission")
	}
	a.tx.since = time.Now().Add(-time.Duration(secs * float64(time.Second))) // the picture has played
	fire()                                                                   // the page never called unkey
	if got := radio.sent[len(radio.sent)-1]; got != "TX0;" || a.tx.isKeyed() {
		t.Fatalf("after the watchdog the radio got %q, keyed %v", got, a.tx.isKeyed())
	}
	list := a.rx.pictures()
	if len(list) != 1 || !list[0].Sent || list[0].Call != "W1AW" || !strings.Contains(list[0].ID, "Sent Robot 36 14230 kHz") {
		t.Fatalf("sent picture not listed: %+v", list)
	}
}

func TestStopUnkeysAtOnceAndListsNothing(t *testing.T) {
	a := newApp(7380, func() {}, t.TempDir(), t.TempDir())
	radio := &tunableRadio{freq: "014230000", mode: "2"}
	a.rig.open = func(string, int) (catPort, error) { return radio, nil }
	a.rig.connect("port", 38400)
	id, _, _ := a.tx.prepare(cardPNG(t), "Scottie 1", "")
	if err := a.tx.key(id); err != nil {
		t.Fatal(err)
	}
	a.tx.unkey(0, true)
	if got := radio.sent[len(radio.sent)-1]; got != "TX0;" || a.tx.isKeyed() || len(a.rx.pictures()) != 0 {
		t.Fatalf("radio got %q, keyed %v, pictures %d", got, a.tx.isKeyed(), len(a.rx.pictures()))
	}
	// quitting mid-transmission unkeys too
	id, _, _ = a.tx.prepare(cardPNG(t), "Scottie 1", "")
	a.tx.key(id)
	a.shutdown()
	if n := len(radio.sent); radio.sent[n-1] != "TX0;" {
		t.Fatalf("quit left the radio keyed: %v", radio.sent[n-2:])
	}
}

func TestNoRadioPlaysAudioOnly(t *testing.T) {
	a := newApp(7380, func() {}, t.TempDir(), t.TempDir())
	id, _, _ := a.tx.prepare(cardPNG(t), "Martin 2", "")
	if err := a.tx.key(id); err != nil || a.tx.status().PTT {
		t.Fatalf("key without a radio: %v, ptt %v", err, a.tx.status().PTT)
	}
	a.tx.unkey(0, true)
}
