//go:build linux

package serial

import (
	"syscall"
	"unsafe"
)

const (
	tiocmBIS = 0x5416
	tiocmBIC = 0x5417
	tiocmDTR = 0x2
	tiocmRTS = 0x4
)

var portGlobs = []string{"/dev/ttyUSB*", "/dev/ttyACM*"}

var speeds = map[int]uint32{4800: syscall.B4800, 9600: syscall.B9600, 19200: syscall.B19200,
	38400: syscall.B38400, 115200: syscall.B115200}

func setRaw(fd, baud int) error {
	sp := speeds[baud]
	var t syscall.Termios
	t.Cflag = syscall.CS8 | syscall.CREAD | syscall.CLOCAL | sp
	t.Ispeed, t.Ospeed = sp, sp
	t.Cc[syscall.VMIN], t.Cc[syscall.VTIME] = 0, 1
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.TCSETS, uintptr(unsafe.Pointer(&t))); e != 0 {
		return e
	}
	return nil
}

func flush(fd int) { syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), 0x540B /* TCFLSH */, 2) }
