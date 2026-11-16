//go:build linux || darwin

package serial

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// Port is an open CAT port.
type Port struct{ fd int }

func ioctlInt(fd int, req uintptr, v int32) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, uintptr(unsafe.Pointer(&v)))
	if errno != 0 {
		return errno
	}
	return nil
}

// Open opens port raw at baud, 8N1, with DTR and RTS low.
func Open(port string, baud int) (*Port, error) {
	if !baudOK(baud) {
		return nil, fmt.Errorf("unsupported baud %d", baud)
	}
	fd, err := syscall.Open(port, syscall.O_RDWR|syscall.O_NOCTTY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("can't open %s: %v (is another program, e.g. WSJT-X or Nexus, using it?)", filepath.Base(port), err)
	}
	ioctlInt(fd, tiocmBIC, tiocmDTR|tiocmRTS) // FIRST: both lines down
	if err := setRaw(fd, baud); err != nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("port settings: %v", err)
	}
	syscall.SetNonblock(fd, false)
	flush(fd)
	return &Port{fd: fd}, nil
}

// Write sends a command. The radio needs a moment between commands.
func (p *Port) Write(s string) error {
	_, err := syscall.Write(p.fd, []byte(s))
	time.Sleep(30 * time.Millisecond)
	return err
}

func (p *Port) read(buf []byte) (int, error) { // returns after 0.1 s without data
	n, err := syscall.Read(p.fd, buf)
	if err == syscall.EINTR {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return max(n, 0), nil
}

// SetRTS and SetDTR raise or drop a control line: the push-to-talk on a simple interface.
func (p *Port) SetRTS(on bool) error { return p.line(tiocmRTS, on) }
func (p *Port) SetDTR(on bool) error { return p.line(tiocmDTR, on) }

func (p *Port) line(bit int32, on bool) error {
	req := uintptr(tiocmBIC)
	if on {
		req = uintptr(tiocmBIS)
	}
	return ioctlInt(p.fd, req, bit)
}

// Close drops both lines and closes the port.
func (p *Port) Close() {
	if p.fd >= 0 {
		ioctlInt(p.fd, tiocmBIC, tiocmDTR|tiocmRTS)
		syscall.Close(p.fd)
		p.fd = -1
	}
}

// The FT-710's USB serial chip (a CP2105) has two ports: interface 0, the Enhanced port
// (CAT), and interface 1, the Standard port (keying and PTT lines). Apple's driver names
// them by serial number + interface.
var appleCP2105 = regexp.MustCompile(`^cu\.usbserial-([0-9A-Za-z]+)([01])$`)

// List: the serial ports present, the likely CAT port first.
func List() []Info {
	out := []Info{}
	for _, pat := range portGlobs {
		m, _ := filepath.Glob(pat)
		for _, p := range m {
			base := filepath.Base(p)
			if strings.Contains(base, "Bluetooth") || strings.Contains(base, "debug-console") {
				continue
			}
			out = append(out, Info{Path: p, Label: strings.TrimPrefix(base, "cu.")})
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Path < out[b].Path })
	pairs := map[string]int{}
	for _, p := range out {
		if m := appleCP2105.FindStringSubmatch(filepath.Base(p.Path)); m != nil {
			pairs[m[1]]++
		}
	}
	for i, p := range out {
		m := appleCP2105.FindStringSubmatch(filepath.Base(p.Path))
		switch {
		case m != nil && pairs[m[1]] == 2 && m[2] == "0":
			out[i].Label += "  (Enhanced port: CAT)"
		case m != nil && pairs[m[1]] == 2:
			out[i].Label += "  (Standard port: not CAT)"
		case strings.Contains(p.Path, "SLAB_USBtoUART") && len(pairs) > 0:
			out[i].Label += "  (duplicate: use a usbserial port)"
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		return strings.Contains(out[a].Label, "CAT)") && !strings.Contains(out[a].Label, "not") &&
			!(strings.Contains(out[b].Label, "CAT)") && !strings.Contains(out[b].Label, "not"))
	})
	return out
}
