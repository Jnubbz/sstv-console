package main

// The radio's CAT port: frequency and mode for the top bar and for labelling received
// pictures. Yaesu CAT (written for the FT-710; other recent Yaesus answer the same FA and
// MD0 questions). PTT for transmit is here too, for when sending arrives.

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Jnubbz/sstv-console/internal/serial"
	"github.com/Jnubbz/sstv-console/rig/ft710"
	"github.com/Jnubbz/sstv-console/rig/hamlib"
)

type rigStatus struct {
	Connected bool   `json:"connected"`
	Port      string `json:"port,omitempty"`
	Baud      int    `json:"baud,omitempty"`
	Model     string `json:"model,omitempty"`
	Kind      string `json:"kind,omitempty"`   // "yaesu" or "generic"
	Manual    bool   `json:"manual,omitempty"` // no CAT: the frequency is the band last picked, not read from the radio
	FreqHz    int    `json:"freqHz,omitempty"`
	Mode      string `json:"mode,omitempty"`
	Error     string `json:"error,omitempty"`
}

// catPort is what the rig needs of a serial port (a fake one in the tests).
type catPort interface {
	Ask(cmd string) (string, error)
	Write(cmd string) error
	Close()
}

// hamRadio is what the rig needs of a rigctld connection (a fake one in the tests).
type hamRadio interface {
	Freq() (int, error)
	Mode() (string, error)
	SetFreq(hz int) error
	SetMode(mode string) error
	PTT(on bool) error
	Close()
}

// lineSetter: a port that can raise and drop RTS and DTR (push-to-talk for a radio with no CAT).
type lineSetter interface {
	SetRTS(on bool) error
	SetDTR(on bool) error
}

// rigOpts says how to connect. Kind "yaesu" talks Yaesu CAT (frequency, mode, band buttons,
// CAT keying). Kind "generic" is any other radio: no CAT, keyed by RTS or DTR on a serial
// port (or by VOX, with no port at all), and the frequency is whatever band was picked.
type rigOpts struct {
	Kind string
	Port string
	Baud int
	PTT  string // generic: "rts", "dtr" or "none"
	Addr string // hamlib: where rigctld listens, host:port
}

type rigConn struct {
	io   sync.Mutex // one conversation with the radio at a time (never held with mu)
	mu   sync.Mutex
	port catPort
	hl   hamRadio // kind "hamlib": the rigctld connection
	kind string   // "yaesu", "generic" or "hamlib" while connected
	line string   // generic: the line that keys it ("rts", "dtr" or "none")
	st   rigStatus
	gen  int // bumps on every connect/disconnect: stops the old poller
	open func(path string, baud int) (catPort, error)
	dial func(addr string) (hamRadio, error)
}

func newRig() *rigConn {
	return &rigConn{
		open: func(path string, baud int) (catPort, error) { return serial.Open(path, baud) },
		dial: func(addr string) (hamRadio, error) { return hamlib.Dial(addr) },
	}
}

func (r *rigConn) status() rigStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.st
}

// connect opens the port and checks a Yaesu radio answers. It replaces any connection.
func (r *rigConn) connect(path string, baud int) error {
	return r.connectWith(rigOpts{Kind: "yaesu", Port: path, Baud: baud})
}

func (r *rigConn) connectWith(o rigOpts) error {
	if o.Kind == "generic" {
		return r.connectGeneric(o)
	}
	if o.Kind == "hamlib" {
		return r.connectHamlib(o)
	}
	path, baud := o.Port, o.Baud
	r.disconnect()
	fail := func(err error) error {
		r.mu.Lock()
		r.st = rigStatus{Port: path, Baud: baud, Error: err.Error()}
		r.mu.Unlock()
		return err
	}
	p, err := r.open(path, baud)
	if err != nil {
		return fail(err)
	}
	id, err := p.Ask("ID;")
	if err != nil {
		p.Close()
		return fail(fmt.Errorf("no answer from a radio on that port at %d baud. Check it's the radio's CAT port (the Enhanced port on an FT-710, FT-991A or FTDX10) and that the radio's CAT RATE matches", baud))
	}
	model := yaesuModel(id)
	r.mu.Lock()
	r.port = p
	r.kind, r.line = "yaesu", ""
	r.gen++
	gen := r.gen
	r.st = rigStatus{Connected: true, Port: path, Baud: baud, Model: model, Kind: "yaesu"}
	r.mu.Unlock()
	r.poll(gen) // once now, so the frequency is there when connect returns
	go func() {
		for {
			time.Sleep(time.Second)
			if !r.poll(gen) {
				return
			}
		}
	}()
	return nil
}

// connectGeneric "connects" a radio with no CAT: it opens the serial port that keys it
// (RTS or DTR, both held low until a transmission), or none at all for VOX.
func (r *rigConn) connectGeneric(o rigOpts) error {
	r.disconnect()
	if o.PTT != "rts" && o.PTT != "dtr" {
		o.PTT = "none"
	}
	var p catPort
	model := "Other radio (VOX)"
	if o.PTT != "none" {
		if o.Port == "" {
			return fmt.Errorf("pick the serial port that keys the radio")
		}
		var err error
		if p, err = r.open(o.Port, 9600); err != nil {
			r.mu.Lock()
			r.st = rigStatus{Port: o.Port, Error: err.Error()}
			r.mu.Unlock()
			return err
		}
		if _, ok := p.(lineSetter); !ok {
			p.Close()
			return fmt.Errorf("that port can't be used to key a radio")
		}
		model = "Other radio (keyed by " + strings.ToUpper(o.PTT) + ")"
	}
	r.mu.Lock()
	r.port, r.kind, r.line = p, "generic", o.PTT
	r.gen++
	r.st = rigStatus{Connected: true, Port: o.Port, Model: model, Kind: "generic", Manual: true}
	r.mu.Unlock()
	return nil
}

// connectHamlib connects to a running rigctld, which does the talking to the radio.
func (r *rigConn) connectHamlib(o rigOpts) error {
	r.disconnect()
	addr := o.Addr
	if addr == "" {
		addr = hamlib.DefaultAddr
	}
	fail := func(err error) error {
		r.mu.Lock()
		r.st = rigStatus{Kind: "hamlib", Error: err.Error()}
		r.mu.Unlock()
		return err
	}
	if !hamlib.ValidAddr(addr) {
		return fail(fmt.Errorf("%q isn't an address like 127.0.0.1:4532", addr))
	}
	h, err := r.dial(addr)
	if err != nil {
		return fail(fmt.Errorf("can't reach rigctld at %s. Start it first (the Radio panel shows how), then connect", addr))
	}
	if _, err := h.Freq(); err != nil {
		h.Close()
		return fail(fmt.Errorf("rigctld is running but the radio didn't answer: %v", err))
	}
	r.mu.Lock()
	r.hl, r.kind, r.line = h, "hamlib", ""
	r.gen++
	gen := r.gen
	r.st = rigStatus{Connected: true, Port: addr, Model: "Radio via Hamlib (" + addr + ")", Kind: "hamlib"}
	r.mu.Unlock()
	r.poll(gen)
	go func() {
		for {
			time.Sleep(time.Second)
			if !r.poll(gen) {
				return
			}
		}
	}()
	return nil
}

// pollHamlib is poll for a rigctld connection.
func (r *rigConn) pollHamlib(gen int) bool {
	r.io.Lock()
	defer r.io.Unlock()
	r.mu.Lock()
	h := r.hl
	if r.gen != gen || h == nil {
		r.mu.Unlock()
		return false
	}
	r.mu.Unlock()
	hz, err1 := h.Freq()
	md, err2 := h.Mode()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.gen != gen {
		return false
	}
	if err1 != nil || err2 != nil {
		r.st.Error = "The radio stopped answering (is it on, and is rigctld still running?)"
		r.st.FreqHz, r.st.Mode = 0, ""
		return true
	}
	r.st.Error = ""
	r.st.FreqHz, r.st.Mode = hz, hamlib.DisplayMode(md)
	return true
}

// poll asks frequency and mode. false: this connection is over.
func (r *rigConn) poll(gen int) bool {
	r.mu.Lock()
	hl := r.hl != nil
	r.mu.Unlock()
	if hl {
		return r.pollHamlib(gen)
	}
	r.io.Lock()
	defer r.io.Unlock()
	r.mu.Lock()
	p := r.port
	if r.gen != gen || p == nil {
		r.mu.Unlock()
		return false
	}
	r.mu.Unlock()
	fa, err1 := p.Ask("FA;")
	md, err2 := p.Ask("MD0;")
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.gen != gen {
		return false
	}
	if err1 != nil || err2 != nil {
		r.st.Error = "The radio stopped answering (is it on?)"
		r.st.FreqHz, r.st.Mode = 0, ""
		return true // keep trying: it comes back when the radio does
	}
	r.st.Error = ""
	if hz, ok := parseFA(fa); ok {
		r.st.FreqHz = hz
	}
	if strings.HasPrefix(md, "MD0") && len(md) == 5 {
		r.st.Mode = ft710.ModeName(md[3:4])
	}
	return true
}

// parseFA: "FA014230000;" -> 14230000.
func parseFA(s string) (int, bool) {
	if !strings.HasPrefix(s, "FA") || !strings.HasSuffix(s, ";") {
		return 0, false
	}
	hz, err := strconv.Atoi(s[2 : len(s)-1])
	return hz, err == nil && hz > 0
}

func (r *rigConn) disconnect() {
	r.mu.Lock()
	r.gen++ // the poller stops at its next look
	r.mu.Unlock()
	r.io.Lock() // wait out a conversation in progress before closing the port
	defer r.io.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.port != nil {
		r.port.Close() // drops RTS and DTR first
		r.port = nil
	}
	if r.hl != nil {
		r.hl.Close()
		r.hl = nil
	}
	r.kind, r.line = "", ""
	r.st = rigStatus{}
}

// ptt keys or unkeys the radio over CAT ("TX1;" sends the USB audio on a Yaesu).
func (r *rigConn) ptt(on bool) error {
	r.io.Lock()
	defer r.io.Unlock()
	r.mu.Lock()
	p, kind, line, hl := r.port, r.kind, r.line, r.hl
	r.mu.Unlock()
	if kind == "hamlib" {
		if hl == nil {
			return fmt.Errorf("the radio isn't connected")
		}
		return hl.PTT(on)
	}
	if kind == "generic" {
		ls, ok := p.(lineSetter)
		if !ok { // VOX: the audio itself keys the radio
			return nil
		}
		if line == "dtr" {
			return ls.SetDTR(on)
		}
		return ls.SetRTS(on)
	}
	if p == nil {
		return fmt.Errorf("the radio isn't connected")
	}
	if on {
		return p.Write("TX1;")
	}
	return p.Write("TX0;")
}

// sstvBand is a band's usual SSTV frequency. By convention SSTV is on lower sideband on
// 80 and 40 m and upper sideband from 20 m up.
type sstvBand struct {
	Name   string `json:"name"`
	FreqHz int    `json:"freqHz"`
	Lower  bool   `json:"lower"`
}

// The calling frequencies in the ARRL band plan and the long-standing SSTV frequency lists.
var sstvBands = []sstvBand{
	{"80m", 3845000, true},
	{"40m", 7171000, true},
	{"20m", 14230000, false},
	{"15m", 21340000, false},
	{"10m", 28680000, false},
	{"6m", 50680000, false},
}

// tune moves VFO A to a band's SSTV frequency and puts the radio in the mode SSTV needs,
// whatever it was in (CW, FM, AM, voice SSB, RTTY...): DATA-U, or DATA-L on 80 and 40 m.
// The DATA modes take their audio from the USB sound card instead of the microphone.
func (r *rigConn) tune(band string) error {
	var b *sstvBand
	for i := range sstvBands {
		if sstvBands[i].Name == band {
			b = &sstvBands[i]
		}
	}
	if b == nil {
		return fmt.Errorf("no SSTV frequency for %q", band)
	}
	r.mu.Lock()
	if r.kind == "generic" { // no CAT to command: note the band, the operator sets the radio
		r.st.FreqHz = b.FreqHz
		r.mu.Unlock()
		return nil
	}
	r.mu.Unlock()
	r.io.Lock()
	r.mu.Lock()
	p, hl, gen := r.port, r.hl, r.gen
	r.mu.Unlock()
	if p == nil && hl == nil {
		r.io.Unlock()
		return fmt.Errorf("the radio isn't connected")
	}
	var want string
	var err error
	if hl != nil { // rigctld: Hamlib's PKTUSB / PKTLSB are the data modes
		mode := hamlib.DataUSB
		if b.Lower {
			mode = hamlib.DataLSB
		}
		want = hamlib.DisplayMode(mode)
		if err = hl.SetFreq(b.FreqHz); err == nil {
			err = hl.SetMode(mode)
		}
	} else {
		mode := ft710.DATAU
		if b.Lower {
			mode = ft710.DATAL
		}
		want = ft710.ModeName(mode)
		var fa string
		if fa, err = ft710.SetFreqA(b.FreqHz); err == nil {
			err = p.Write(fa)
		}
		if err == nil {
			var md string
			if md, err = ft710.SetMode(mode); err == nil {
				err = p.Write(md)
			}
		}
	}
	r.io.Unlock()
	if err != nil {
		return err
	}
	r.poll(gen)                                                     // read it back: the page shows what the radio says, not what we asked
	if st := r.status(); st.Mode != want && st.FreqHz == b.FreqHz { // give a slow radio one more look
		time.Sleep(150 * time.Millisecond)
		r.poll(gen)
	}
	st := r.status()
	if st.FreqHz != b.FreqHz {
		return fmt.Errorf("the radio didn't move to %s (it may not cover that band, or it's transmitting)", band)
	}
	if st.Mode != want {
		return fmt.Errorf("the radio is on %s but didn't switch to %s (it shows %s)", band, want, st.Mode)
	}
	return nil
}

// yaesuModels are the radios whose "ID;" answer is confirmed in Yaesu's CAT manuals. They all
// speak the same text CAT (FA, MD0, TX1/TX0) with DATA-USB = C and DATA-LSB = 8, so one driver
// covers them. Any other Yaesu that answers "ID;" is tried the same way and named by its code.
var yaesuModels = map[string]string{
	"ID0800;": "FT-710",
	"ID0670;": "FT-991A",
	"ID0570;": "FT-991",
	"ID0650;": "FT-891",
	"ID0761;": "FTDX10",
}

func yaesuModel(id string) string {
	if m, ok := yaesuModels[id]; ok {
		return m
	}
	return "Yaesu radio (" + strings.TrimSuffix(id, ";") + ")"
}
