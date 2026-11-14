package main

// Transmitting: the page composes the picture and sends it here as a PNG; the engine
// turns it into audio, keys the radio over CAT, and the page plays the audio into the
// radio's USB sound card. A watchdog here unkeys the radio when the picture's time is up,
// whatever the page does.

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Jnubbz/sstv-console/sstv"
)

const txRate = 48000

type txStatus struct {
	Keyed   bool    `json:"keyed"`
	PTT     bool    `json:"ptt"` // the radio was keyed over CAT (false: audio only, e.g. VOX)
	Mode    string  `json:"mode,omitempty"`
	Seconds float64 `json:"seconds,omitempty"`
	Elapsed float64 `json:"elapsed,omitempty"`
	Error   string  `json:"error,omitempty"`
}

type transmitter struct {
	mu      sync.Mutex
	id      int
	wav     []byte
	png     []byte
	mode    *sstv.Mode
	call    string
	seconds float64
	keyed   bool
	ptt     bool
	since   time.Time
	timer   *time.Timer
	err     string

	rig    *rigConn
	onEnd  func()                                    // receiving starts afresh
	onSent func(png []byte, m *sstv.Mode, to string) // the picture went out
	after  func(d time.Duration, f func()) *time.Timer
}

func (t *transmitter) status() txStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := txStatus{Keyed: t.keyed, PTT: t.ptt, Error: t.err}
	if t.keyed {
		s.Mode, s.Seconds, s.Elapsed = t.mode.Name, t.seconds, time.Since(t.since).Seconds()
	}
	return s
}

func (t *transmitter) isKeyed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.keyed
}

// prepare turns a picture into the audio to send. to: who it's for ("" for a CQ).
func (t *transmitter) prepare(pic []byte, mode, to string) (int, float64, error) {
	m := sstv.ByName(mode)
	if m == nil {
		return 0, 0, fmt.Errorf("no mode called %q", mode)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(pic))
	if err != nil || cfg.Width > 4096 || cfg.Height > 4096 {
		return 0, 0, errors.New("that isn't a picture this can send")
	}
	img, _, err := image.Decode(bytes.NewReader(pic))
	if err != nil {
		return 0, 0, errors.New("that isn't a picture this can send")
	}
	audio := sstv.Encode(img, m, txRate)
	// a short quiet lead-in (the radio's relay settles) and tail
	lead, tail := txRate*3/10, txRate/10
	n := (lead + len(audio) + tail) * 2
	w := make([]byte, 44, 44+n)
	copy(w, "RIFF")
	binary.LittleEndian.PutUint32(w[4:], uint32(36+n))
	copy(w[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(w[16:], 16)
	binary.LittleEndian.PutUint16(w[20:], 1)
	binary.LittleEndian.PutUint16(w[22:], 1)
	binary.LittleEndian.PutUint32(w[24:], txRate)
	binary.LittleEndian.PutUint32(w[28:], txRate*2)
	binary.LittleEndian.PutUint16(w[32:], 2)
	binary.LittleEndian.PutUint16(w[34:], 16)
	copy(w[36:], "data")
	binary.LittleEndian.PutUint32(w[40:], uint32(n))
	w = append(w, make([]byte, lead*2)...)
	for _, s := range audio {
		w = binary.LittleEndian.AppendUint16(w, uint16(int16(math.Round(float64(s)*32767))))
	}
	w = append(w, make([]byte, tail*2)...)
	var sent bytes.Buffer // what goes on the air: the picture at the mode's size
	png.Encode(&sent, sstv.Resize(img, m.Width, m.Height))

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.keyed {
		return 0, 0, errors.New("already transmitting")
	}
	t.id++
	t.wav, t.png, t.mode, t.call = w, sent.Bytes(), m, to
	t.seconds = float64(lead+len(audio)+tail) / txRate
	return t.id, t.seconds, nil
}

func (t *transmitter) audio(id int) []byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	if id != t.id {
		return nil
	}
	return t.wav
}

// key puts the radio into transmit for the prepared picture. With no radio connected it
// keys nothing (the audio still plays: for a radio on VOX).
func (t *transmitter) key(id int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.keyed {
		return errors.New("already transmitting")
	}
	if id != t.id || t.wav == nil {
		return errors.New("that picture isn't ready any more: try again")
	}
	t.ptt, t.err = false, ""
	if t.rig.status().Connected {
		if err := t.rig.ptt(true); err != nil {
			t.rig.ptt(false)
			return fmt.Errorf("couldn't key the radio: %v", err)
		}
		t.ptt = true
	}
	t.keyed, t.since = true, time.Now()
	gen := t.id
	t.timer = t.after(time.Duration((t.seconds+1.5)*float64(time.Second)), func() { t.unkey(gen, false) })
	return nil
}

// unkey returns the radio to receive. stopped: cut short by the user.
func (t *transmitter) unkey(id int, stopped bool) {
	t.mu.Lock()
	if !t.keyed || (id != 0 && id != t.id) {
		t.mu.Unlock()
		return
	}
	if t.timer != nil {
		t.timer.Stop()
	}
	if t.ptt {
		var err error
		for try := 0; try < 4; try++ { // this one matters: don't give up at the first hiccup
			if err = t.rig.ptt(false); err == nil {
				break
			}
			time.Sleep(150 * time.Millisecond)
		}
		if err != nil {
			t.err = "COULDN'T UNKEY THE RADIO over CAT: check it and press its PTT or power button"
		}
	}
	whole := !stopped && time.Since(t.since).Seconds() > t.seconds-1
	pic, m, to := t.png, t.mode, t.call
	t.keyed = false
	t.mu.Unlock()
	if whole && t.onSent != nil {
		t.onSent(pic, m, to)
	}
	if t.onEnd != nil {
		t.onEnd()
	}
}

// My picture: the photo replies are built on, kept between runs.
func photoPath(dir string) string { return filepath.Join(dir, "my-picture") }

func savePhoto(dir string, b []byte) error {
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(b)); err != nil || cfg.Width > 12000 || cfg.Height > 12000 {
		return errors.New("that isn't a JPEG or PNG picture")
	}
	tmp := photoPath(dir) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, photoPath(dir))
}
