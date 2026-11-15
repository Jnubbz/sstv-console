package main

// Receiving: the page sends the radio's audio here in blocks; the decoder turns it into
// pictures; finished pictures are saved as PNGs in Pictures/SSTV Console and listed.

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
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
	Sent   bool    `json:"sent,omitempty"`  // one of mine
	Saved  bool    `json:"saved,omitempty"` // copied to the Pictures folder by the user
	Call   string  `json:"call,omitempty"`  // the other station
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
	Joined    bool    `json:"joined"`              // locked on partway: no start tones heard
	Recording int     `json:"recording,omitempty"` // seconds left of a test recording
	Recorded  string  `json:"recorded,omitempty"`  // the last test recording's file
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
	picDir    string      // the user's Pictures folder: only what they save goes here
	inboxDir  string      // everything received (and sent) waits here until saved, deleted or expired
	indexPath string
	rec       []int16 // a test recording in progress
	recLeft   int     // samples still to record
	recRate   int
	muted     func() bool // transmitting: don't decode my own picture
	freq      func() int  // the radio's frequency now (0 if unknown)
	now       func() time.Time
}

// unsavedKeep: how long a picture that was never saved or deleted stays in the list.
const unsavedKeep = 7 * 24 * time.Hour

func newReceiver(dir, picDir string, freq func() int) *receiver {
	r := &receiver{picDir: picDir, inboxDir: filepath.Join(dir, "inbox"), indexPath: filepath.Join(dir, "received.json"), freq: freq, now: time.Now}
	os.MkdirAll(r.inboxDir, 0o755)
	if b, err := os.ReadFile(r.indexPath); err == nil {
		json.Unmarshal(b, &r.pics)
	}
	kept := r.pics[:0]
	for _, p := range r.pics {
		if _, err := os.Stat(filepath.Join(r.inboxDir, p.ID+".png")); err == nil {
			kept = append(kept, p) // waiting in the inbox
		} else if _, err := os.Stat(filepath.Join(r.picDir, p.ID+".png")); err == nil {
			p.Saved = true // from before there was an inbox, or saved: it's in the Pictures folder
			kept = append(kept, p)
		} // else: the file is gone (the user deleted it)
	}
	r.pics = kept
	r.expire()
	return r
}

// file: where a picture is now (the inbox, or the Pictures folder once saved).
func (r *receiver) file(id string) string {
	if p := filepath.Join(r.inboxDir, id+".png"); fileExists(p) {
		return p
	}
	return filepath.Join(r.picDir, id+".png")
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// expire drops unsaved pictures older than unsavedKeep. Called with the lock held (or at start).
func (r *receiver) expire() {
	kept, changed := r.pics[:0], false
	for _, p := range r.pics {
		if t, err := time.Parse(time.RFC3339, p.Time); err == nil && !p.Saved && r.now().Sub(t) > unsavedKeep {
			os.Remove(filepath.Join(r.inboxDir, p.ID+".png"))
			changed = true
			continue
		}
		kept = append(kept, p)
	}
	r.pics = kept
	if changed {
		r.st.PicturesV++
		r.writeIndex()
	}
}

// savePicture copies a picture into the Pictures folder. It returns the file's path.
func (r *receiver) savePicture(id string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.pics {
		if r.pics[i].ID != id {
			continue
		}
		dst := filepath.Join(r.picDir, id+".png")
		if r.pics[i].Saved {
			return dst, nil
		}
		b, err := os.ReadFile(filepath.Join(r.inboxDir, id+".png"))
		if err == nil {
			if err = os.MkdirAll(r.picDir, 0o755); err == nil {
				err = os.WriteFile(dst, b, 0o644)
			}
		}
		if err != nil {
			return "", fmt.Errorf("couldn't save it: %v", err)
		}
		os.Remove(filepath.Join(r.inboxDir, id+".png"))
		r.pics[i].Saved = true
		r.st.PicturesV++
		r.writeIndex()
		return dst, nil
	}
	return "", errors.New("that picture isn't in the list")
}

// deletePicture throws away a picture that wasn't saved. Saved ones are the user's files:
// they delete those themselves.
func (r *receiver) deletePicture(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, p := range r.pics {
		if p.ID != id {
			continue
		}
		if p.Saved {
			return errors.New("that picture is saved in your Pictures folder: delete the file there")
		}
		os.Remove(filepath.Join(r.inboxDir, id+".png"))
		r.pics = append(r.pics[:i], r.pics[i+1:]...)
		r.st.PicturesV++
		r.writeIndex()
		return nil
	}
	return errors.New("that picture isn't in the list")
}

// savedPath: where a saved picture is ("" if it isn't saved).
func (r *receiver) savedPath(id string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.pics {
		if p.ID == id && p.Saved {
			return filepath.Join(r.picDir, id+".png")
		}
	}
	return ""
}

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
	if r.muted != nil && r.muted() {
		return
	}
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
	if r.recLeft > 0 {
		if r.recRate != rate { // first block: now the rate is known
			r.recRate, r.recLeft = rate, recordSeconds*rate
		}
		for _, v := range samples {
			r.rec = append(r.rec, int16(math.Max(-1, math.Min(1, float64(v)))*32767))
		}
		if r.recLeft -= len(samples); r.recLeft <= 0 {
			r.finishRecording()
		}
		r.st.Recording = max(r.recLeft, 0) / rate
	}
	changed := false
	for _, e := range r.dec.Write(samples) {
		switch e.Kind {
		case sstv.Started:
			r.st.Receiving, r.st.Mode, r.st.Line, r.st.Lines = true, e.Mode.Name, 0, e.Mode.Height
			r.st.W, r.st.H = e.Mode.Width, e.Mode.Height
			r.st.OffsetHz, r.st.SlantPPM, r.st.Note, r.st.Joined = e.OffsetHz, 0, "", e.Joined
			if e.Joined {
				r.st.Note = "Joined partway through: the start of this picture was missed."
			}
			r.live = clone(e.Image)
			changed = true
		case sstv.Line:
			r.st.Line, r.st.OffsetHz, r.st.SlantPPM = e.Line, e.OffsetHz, e.SlantPPM
			copy(r.live.Pix, e.Image.Pix)
			changed = true
		case sstv.Aborted:
			r.st.Receiving = false
			r.st.Note = "Lost the signal right after it started"
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

// setCall tags a picture with the other station's callsign.
func (r *receiver) setCall(id, call string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.pics {
		if r.pics[i].ID == id {
			r.pics[i].Call = call
			r.st.PicturesV++
			r.writeIndex()
			return true
		}
	}
	return false
}

// addSent saves and lists a picture I sent.
func (r *receiver) addSent(pic []byte, m *sstv.Mode, to string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t := r.now().UTC()
	hz := r.freq()
	id := t.Format("2006-01-02 150405") + "Z Sent " + m.Name
	if hz > 0 {
		id += fmt.Sprintf(" %d kHz", hz/1000)
	}
	if err := os.WriteFile(filepath.Join(r.inboxDir, id+".png"), pic, 0o644); err != nil {
		r.st.Note = "Couldn't save the sent picture: " + err.Error()
		return
	}
	r.pics = append([]Picture{{ID: id, Time: t.Format(time.RFC3339), Mode: m.Name, FreqHz: hz, W: m.Width, H: m.Height,
		Sent: true, Call: to}}, r.pics...)
	r.st.PicturesV++
	r.expire()
	r.writeIndex()
}

func (r *receiver) writeIndex() {
	b, _ := json.MarshalIndent(r.pics, "", " ")
	tmp := r.indexPath + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, r.indexPath)
	}
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
	f, err := os.Create(filepath.Join(r.inboxDir, id+".png"))
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
		Slant: math.Round(e.SlantPPM), Synced: e.Synced, W: e.Image.Bounds().Dx(), H: e.Image.Bounds().Dy()}
	r.pics = append([]Picture{p}, r.pics...)
	r.st.PicturesV++
	r.expire()
	r.writeIndex()
}

// A test recording: a minute of exactly the audio the decoder is given, as a WAV file next
// to the pictures. For working out why something didn't decode.
const recordSeconds = 60

func (r *receiver) record() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.recLeft > 0 {
		return
	}
	r.rec, r.recRate, r.recLeft = nil, 0, 1 // sized at the first block
	r.st.Recording, r.st.Recorded = recordSeconds, ""
}

// finishRecording writes the WAV. Called with the lock held.
func (r *receiver) finishRecording() {
	name := "Recording " + r.now().UTC().Format("2006-01-02 150405") + "Z"
	if hz := r.freq(); hz > 0 {
		name += fmt.Sprintf(" %d kHz", hz/1000)
	}
	os.MkdirAll(r.picDir, 0o755)
	path := filepath.Join(r.picDir, name+".wav")
	if want := recordSeconds * r.recRate; len(r.rec) > want {
		r.rec = r.rec[:want]
	}
	n := len(r.rec) * 2
	h := make([]byte, 44, 44+n)
	copy(h, "RIFF")
	binary.LittleEndian.PutUint32(h[4:], uint32(36+n))
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1)
	binary.LittleEndian.PutUint16(h[22:], 1)
	binary.LittleEndian.PutUint32(h[24:], uint32(r.recRate))
	binary.LittleEndian.PutUint32(h[28:], uint32(r.recRate*2))
	binary.LittleEndian.PutUint16(h[32:], 2)
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], uint32(n))
	for _, v := range r.rec {
		h = binary.LittleEndian.AppendUint16(h, uint16(v))
	}
	r.rec, r.recLeft = nil, 0
	if err := os.WriteFile(path, h, 0o644); err != nil {
		r.st.Note = "Couldn't save the recording: " + err.Error()
		return
	}
	r.st.Recorded = path
}
