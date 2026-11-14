// Package serial opens a radio's CAT port: raw 8N1, with DTR and RTS held low so that
// opening the port can't key the transmitter. Adapted from CW Console (Mac and Linux) and
// written new for Windows.
package serial

import (
	"errors"
	"time"
)

// Info is one serial port on this computer.
type Info struct {
	Path  string `json:"path"`  // what Open takes: /dev/cu.usbserial-..., COM5
	Label string `json:"label"` // for the port list
}

// Bauds the CAT port can be set to (the FT-710's CAT RATE menu).
var Bauds = []int{4800, 9600, 19200, 38400, 115200}

func baudOK(baud int) bool {
	for _, b := range Bauds {
		if b == baud {
			return true
		}
	}
	return false
}

var errRejected = errors.New("the radio rejected the command")
var errNoAnswer = errors.New("no answer")

// Rejected: the radio answered "?;".
func Rejected(err error) bool { return errors.Is(err, errRejected) }

// Ask sends a CAT command and reads its answer (up to the ';').
func (p *Port) Ask(cmd string) (string, error) {
	if err := p.Write(cmd); err != nil {
		return "", err
	}
	return p.ReadReply(600 * time.Millisecond)
}

// ReadReply reads up to the ';' that ends a CAT answer.
func (p *Port) ReadReply(within time.Duration) (string, error) {
	var b []byte
	buf := make([]byte, 64)
	end := time.Now().Add(within)
	for time.Now().Before(end) {
		n, err := p.read(buf)
		if err != nil {
			return "", err
		}
		b = append(b, buf[:n]...)
		for i, ch := range b {
			if ch == ';' {
				if string(b[:i+1]) == "?;" {
					return "", errRejected
				}
				return string(b[:i+1]), nil
			}
		}
		if len(b) > 256 {
			return "", errors.New("the port is sending something that isn't CAT")
		}
	}
	return "", errNoAnswer
}
