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
)

type rigStatus struct {
	Connected bool   `json:"connected"`
	Port      string `json:"port,omitempty"`
	Baud      int    `json:"baud,omitempty"`
	Model     string `json:"model,omitempty"`
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

type rigConn struct {
	io   sync.Mutex // one conversation with the radio at a time (never held with mu)
	mu   sync.Mutex
	port catPort
	st   rigStatus
	gen  int // bumps on every connect/disconnect: stops the old poller
	open func(path string, baud int) (catPort, error)
}

func newRig() *rigConn {
	return &rigConn{open: func(path string, baud int) (catPort, error) { return serial.Open(path, baud) }}
}

func (r *rigConn) status() rigStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.st
}

// connect opens the port and checks a radio answers. It replaces any connection.
func (r *rigConn) connect(path string, baud int) error {
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
		return fail(fmt.Errorf("no answer from a radio on that port at %d baud. Check it's the radio's CAT port (the Enhanced port on an FT-710) and that the radio's CAT RATE matches", baud))
	}
	model := "Yaesu radio (" + strings.TrimSuffix(id, ";") + ")"
	if id == ft710.ID {
		model = "FT-710"
	}
	r.mu.Lock()
	r.port = p
	r.gen++
	gen := r.gen
	r.st = rigStatus{Connected: true, Port: path, Baud: baud, Model: model}
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

// poll asks frequency and mode. false: this connection is over.
func (r *rigConn) poll(gen int) bool {
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
		r.port.Close()
		r.port = nil
	}
	r.st = rigStatus{}
}

// ptt keys or unkeys the radio over CAT ("TX1;" sends the USB audio on a Yaesu).
func (r *rigConn) ptt(on bool) error {
	r.io.Lock()
	defer r.io.Unlock()
	r.mu.Lock()
	p := r.port
	r.mu.Unlock()
	if p == nil {
		return fmt.Errorf("the radio isn't connected")
	}
	if on {
		return p.Write("TX1;")
	}
	return p.Write("TX0;")
}
