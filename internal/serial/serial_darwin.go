//go:build darwin

package serial

import (
	"syscall"
	"unsafe"
)

const (
	tiocmBIS = 0x8004746c // _IOW('t', 108, int)
	tiocmBIC = 0x8004746b // _IOW('t', 107, int)
	tiocmDTR = 0x2
	tiocmRTS = 0x4
)

var portGlobs = []string{"/dev/cu.*"}

// macOS termios: speeds are plain numbers in c_ispeed/c_ospeed, set with TIOCSETA.
func setRaw(fd, baud int) error {
	var t syscall.Termios
	t.Cflag = syscall.CS8 | syscall.CREAD | syscall.CLOCAL
	t.Ispeed, t.Ospeed = uint64(baud), uint64(baud)
	t.Cc[syscall.VMIN], t.Cc[syscall.VTIME] = 0, 1
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.TIOCSETA, uintptr(unsafe.Pointer(&t))); e != 0 {
		return e
	}
	return nil
}

func flush(fd int) {
	what := int32(0x3) // FREAD | FWRITE
	syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.TIOCFLUSH, uintptr(unsafe.Pointer(&what)))
}
