package main

// Receiving: the page sends the radio's audio here in blocks; the decoder turns it into
// pictures; finished pictures are saved as PNGs in Pictures/SSTV Console and listed.

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Jnubbz/sstv-console/sstv"
)

// Picture is one received picture.
type Picture struct {
	ID     string  `json:"id"`   // also its file name, without .png
	Time   string  `json:"time"` // RFC 3339, UTC
	Mode   string  `json:"mode"`
	FreqHz int     `json:"freqHz,omitempty"`
	Offset float64 `json:"offsetHz"`
	Slant  float64 `json:"slantPpm"`
	Synced int     `json:"synced"`
	W      int     `json:"w"`
	H      int     `json:"h"`
}

type rxStatus struct {
	Audio     bool    `json:"audio"`   // audio is arriving
	LevelDB   float64 `json:"levelDb"` // peak of the last block, dBFS
	Receiving bool    `json:"receiving"`
	Mode      string  `json:"mode,omitempty"`
	Line      int     `json:"line,omitempty"`
	Lines     int     `json:"lines,omitempty"`
	W         int     `json:"w,omitempty"`
	H         int     `json:"h,omitempty"`
	OffsetHz  float64 `json:"offsetHz"`
	SlantPPM  float64 `json:"slantPpm"`
	LiveV     int     `json:"liveV"`     // changes when the live picture does
	PicturesV int     `json:"picturesV"` // changes when the list does
	Note      string  `json:"note,omitempty"`
}

var dumpAudio = func() *os.File {
	if p := os.Getenv("SSTV_DUMP_AUDIO"); p != "" {
		f, _ := os.Create(p)
		return f
	}
	return nil
}()

type receiver struct {
	mu        sync.Mutex
	dec       *sstv.Decoder
	rate      int
	lastAudio time.Time
	st        rxStatus
	live      *image.RGBA // a copy of the picture in progress (or the last one)
	pics      []Picture   // newest first
	picDir    string
	indexPath string
	freq      func() int // the radio's frequency now (0 if unknown)
	now       func() time.Time
}

func newReceiver(dir, picDir string, freq func() int) *receiver {
	r := &receiver{picDir: picDir, indexPath: filepath.Join(dir, "received.json"), freq: freq, now: time.Now}
	os.MkdirAll(picDir, 0o755)
	if b, err := os.ReadFile(r.indexPath); err == nil {
		json.Unmarshal(b, &r.pics)
	}
	kept := r.pics[:0] // drop entries whose file the user deleted
	for _, p := range r.pics {
		if _, err := os.Stat(r.file(p.ID)); err == nil {
			kept = append(kept, p)
		}
	}
	r.pics = kept
	return r
}

func (r *receiver) file(id string) string { return filepath.Join(r.picDir, id+".png") }

func (r *receiver) status() rxStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.st
	s.Audio = time.Since(r.lastAudio) < 1500*time.Millisecond
	if !s.Audio {
		s.LevelDB = -120
	}
	return s
}

func (r *receiver) pictures() []Picture {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Picture{}, r.pics...)
}

func (r *receiver) has(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.pics {
		if p.ID == id {
			return true
		}
	}
	return false
}

// livePNG: the picture in progress.
func (r *receiver) livePNG() []byte {
	r.mu.Lock()
	img := r.live
	r.mu.Unlock()
	if img == nil {
		return nil
	}
	var b bytes.Buffer
	(&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&b, img)
	return b.Bytes()
}

func (r *receiver) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dec != nil {
		r.dec.Reset()
	}
	r.st.Receiving, r.st.Note = false, ""
}

// push takes a block of audio from the page.
func (r *receiver) push(samples []float32, rate int) {
	if rate < 8000 || rate > 192000 || len(samples) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dec == nil || r.rate != rate {
		r.dec, r.rate = sstv.NewDecoder(rate), rate
		r.st.Receiving = false
	}
	r.lastAudio = time.Now()
	if dumpAudio != nil { // SSTV_DUMP_AUDIO=file: raw float32 of exactly what arrived, for debugging
		binary.Write(dumpAudio, binary.LittleEndian, samples)
	}
	peak := 0.0
	for _, s := range samples {
		if a := math.Abs(float64(s)); a > peak {
			peak = a
		}
	}
	r.st.LevelDB = math.Max(-120, 20*math.Log10(peak+1e-9))
	changed := false
	for _, e := range r.dec.Write(samples) {
		switch e.Kind {
		case sstv.Started:
			r.st.Receiving, r.st.Mode, r.st.Line, r.st.Lines = true, e.Mode.Name, 0, e.Mode.Height
			r.st.W, r.st.H = e.Mode.Width, e.Mode.Height
			r.st.OffsetHz, r.st.SlantPPM, r.st.Note = e.OffsetHz, 0, ""
			r.live = clone(e.Image)
			changed = true
		case sstv.Line:
			r.st.Line, r.st.OffsetHz, r.st.SlantPPM = e.Line, e.OffsetHz, e.SlantPPM
			copy(r.live.Pix, e.Image.Pix)
			changed = true
		case sstv.Aborted:
			r.st.Receiving = false
			r.st.Note = "Lost the signal right after the start tones"
		case sstv.Done:
			r.st.Receiving = false
			r.live = clone(e.Image)
			changed = true
			r.save(e)
		}
	}
	if changed {
		r.st.LiveV++
	}
}

func clone(img *image.RGBA) *image.RGBA {
	c := image.NewRGBA(img.Bounds())
	copy(c.Pix, img.Pix)
	return c
}

// save writes a finished picture and lists it. Called with the lock held.
func (r *receiver) save(e sstv.Event) {
	t := r.now().UTC()
	hz := r.freq()
	id := t.Format("2006-01-02 150405") + "Z " + e.Mode.Name
	if hz > 0 {
		id += fmt.Sprintf(" %d kHz", hz/1000)
	}
	id = strings.Map(func(c rune) rune { // a safe file name everywhere
		if c == '/' || c == '\\' || c == ':' {
			return '-'
		}
		return c
	}, id)
	f, err := os.Create(r.file(id))
	if err != nil {
		r.st.Note = "Couldn't save the picture: " + err.Error()
		return
	}
	err = png.Encode(f, e.Image)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		r.st.Note = "Couldn't save the picture: " + err.Error()
		return
	}
	p := Picture{ID: id, Time: t.Format(time.RFC3339), Mode: e.Mode.Name, FreqHz: hz, Offset: math.Round(e.OffsetHz),
		Slant: math.Round(e.SlantPPM), Synced: e.Synced, W: e.Mode.Width, H: e.Mode.Height}
	r.pics = append([]Picture{p}, r.pics...)
	r.st.PicturesV++
	b, _ := json.MarshalIndent(r.pics, "", " ")
	tmp := r.indexPath + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, r.indexPath)
	}
}
