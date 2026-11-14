//go:build windows

package serial

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32            = syscall.NewLazyDLL("kernel32.dll")
	advapi32            = syscall.NewLazyDLL("advapi32.dll")
	setupapi            = syscall.NewLazyDLL("setupapi.dll")
	pCreateFileW        = kernel32.NewProc("CreateFileW")
	pGetCommState       = kernel32.NewProc("GetCommState")
	pSetCommState       = kernel32.NewProc("SetCommState")
	pSetCommTimeouts    = kernel32.NewProc("SetCommTimeouts")
	pEscapeCommFunction = kernel32.NewProc("EscapeCommFunction")
	pPurgeComm          = kernel32.NewProc("PurgeComm")
	pRegOpenKeyExW      = advapi32.NewProc("RegOpenKeyExW")
	pRegEnumValueW      = advapi32.NewProc("RegEnumValueW")
	pRegCloseKey        = advapi32.NewProc("RegCloseKey")

	pSetupDiGetClassDevsW              = setupapi.NewProc("SetupDiGetClassDevsW")
	pSetupDiEnumDeviceInfo             = setupapi.NewProc("SetupDiEnumDeviceInfo")
	pSetupDiGetDeviceRegistryPropertyW = setupapi.NewProc("SetupDiGetDeviceRegistryPropertyW")
	pSetupDiDestroyDeviceInfoList      = setupapi.NewProc("SetupDiDestroyDeviceInfoList")
)

const (
	hkeyLocalMachine = 0x80000002
	keyRead          = 0x20019
	genericRead      = 0x80000000
	genericWrite     = 0x40000000
	openExisting     = 3
	invalidHandle    = ^uintptr(0)
	escCLRRTS        = 4
	escCLRDTR        = 6
)

// DCB (winbase.h). Flags packs the bitfields.
type dcb struct {
	DCBlength  uint32
	BaudRate   uint32
	Flags      uint32
	wReserved  uint16
	XonLim     uint16
	XoffLim    uint16
	ByteSize   byte
	Parity     byte
	StopBits   byte
	XonChar    byte
	XoffChar   byte
	ErrorChar  byte
	EofChar    byte
	EvtChar    byte
	wReserved1 uint16
}

type commTimeouts struct {
	ReadIntervalTimeout, ReadTotalTimeoutMultiplier, ReadTotalTimeoutConstant uint32
	WriteTotalTimeoutMultiplier, WriteTotalTimeoutConstant                    uint32
}

// Port is an open CAT port.
type Port struct{ h syscall.Handle }

func u16(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }

// Open opens port (COM5) raw at baud, 8N1, with DTR and RTS low.
func Open(port string, baud int) (*Port, error) {
	if !baudOK(baud) {
		return nil, fmt.Errorf("unsupported baud %d", baud)
	}
	h, _, err := pCreateFileW.Call(uintptr(unsafe.Pointer(u16(`\\.\`+port))), genericRead|genericWrite, 0, 0, openExisting, 0, 0)
	if h == invalidHandle {
		return nil, fmt.Errorf("can't open %s: %v (is another program, e.g. WSJT-X or a logger, using it?)", port, err)
	}
	idle := func() { // both lines down, in case the open raised them
		pEscapeCommFunction.Call(h, escCLRDTR)
		pEscapeCommFunction.Call(h, escCLRRTS)
	}
	idle()
	var d dcb
	d.DCBlength = uint32(unsafe.Sizeof(d))
	if r, _, e := pGetCommState.Call(h, uintptr(unsafe.Pointer(&d))); r == 0 {
		syscall.CloseHandle(syscall.Handle(h))
		return nil, fmt.Errorf("%s: %v", port, e)
	}
	d.BaudRate = uint32(baud)
	d.Flags |= 1            // fBinary
	d.Flags &^= 1 << 1      // no parity check
	d.Flags &^= 1<<2 | 1<<3 // no CTS/DSR flow control
	d.Flags &^= 3 << 4      // DTR_CONTROL_DISABLE: stays low
	d.Flags &^= 1 << 6      // fDsrSensitivity off
	d.Flags &^= 1<<8 | 1<<9 // no XON/XOFF
	d.Flags &^= 3 << 12     // RTS_CONTROL_DISABLE: stays low
	d.ByteSize, d.Parity, d.StopBits = 8, 0, 0
	if r, _, e := pSetCommState.Call(h, uintptr(unsafe.Pointer(&d))); r == 0 {
		syscall.CloseHandle(syscall.Handle(h))
		return nil, fmt.Errorf("%s won't accept %d baud: %v", port, baud, e)
	}
	idle()
	// a read returns as soon as bytes are there, or after 100 ms with none
	t := commTimeouts{ReadIntervalTimeout: ^uint32(0), ReadTotalTimeoutMultiplier: ^uint32(0), ReadTotalTimeoutConstant: 100,
		WriteTotalTimeoutConstant: 500}
	pSetCommTimeouts.Call(h, uintptr(unsafe.Pointer(&t)))
	pPurgeComm.Call(h, 0x8|0x4) // PURGE_RXCLEAR | PURGE_TXCLEAR
	return &Port{h: syscall.Handle(h)}, nil
}

// Write sends a command. The radio needs a moment between commands.
func (p *Port) Write(s string) error {
	var n uint32
	err := syscall.WriteFile(p.h, []byte(s), &n, nil)
	time.Sleep(30 * time.Millisecond)
	return err
}

func (p *Port) read(buf []byte) (int, error) {
	var n uint32
	if err := syscall.ReadFile(p.h, buf, &n, nil); err != nil {
		return 0, err
	}
	return int(n), nil
}

// Close drops both lines and closes the port.
func (p *Port) Close() {
	if p.h != 0 {
		pEscapeCommFunction.Call(uintptr(p.h), escCLRDTR)
		pEscapeCommFunction.Call(uintptr(p.h), escCLRRTS)
		syscall.CloseHandle(p.h)
		p.h = 0
	}
}

type guid struct {
	D1     uint32
	D2, D3 uint16
	D4     [8]byte
}

type spDevinfoData struct {
	CbSize    uint32
	ClassGuid guid
	DevInst   uint32
	Reserved  uintptr
}

// friendlyNames maps "COM6" -> "Silicon Labs Dual CP2105 USB to UART Bridge: Enhanced COM
// Port" (the names Device Manager shows). Best effort.
func friendlyNames() map[string]string {
	out := map[string]string{}
	ports := guid{0x4D36E978, 0xE325, 0x11CE, [8]byte{0xBF, 0xC1, 0x08, 0x00, 0x2B, 0xE1, 0x03, 0x18}}
	h, _, _ := pSetupDiGetClassDevsW.Call(uintptr(unsafe.Pointer(&ports)), 0, 0, 0x2 /*DIGCF_PRESENT*/)
	if h == 0 || h == invalidHandle {
		return out
	}
	defer pSetupDiDestroyDeviceInfoList.Call(h)
	for i := uintptr(0); i < 256; i++ {
		var d spDevinfoData
		d.CbSize = uint32(unsafe.Sizeof(d))
		if r, _, _ := pSetupDiEnumDeviceInfo.Call(h, i, uintptr(unsafe.Pointer(&d))); r == 0 {
			break
		}
		buf := make([]uint16, 512)
		var typ, need uint32
		r, _, _ := pSetupDiGetDeviceRegistryPropertyW.Call(h, uintptr(unsafe.Pointer(&d)), 0xC, /*SPDRP_FRIENDLYNAME*/
			uintptr(unsafe.Pointer(&typ)), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)*2), uintptr(unsafe.Pointer(&need)))
		if r == 0 {
			continue
		}
		name := syscall.UTF16ToString(buf)
		o, c := strings.LastIndex(name, "(COM"), strings.LastIndex(name, ")")
		if o < 0 || c < o {
			continue
		}
		out[strings.ToUpper(name[o+1:c])] = strings.TrimSpace(name[:o])
	}
	return out
}

// List reads HKLM\HARDWARE\DEVICEMAP\SERIALCOMM (present ports), the likely CAT port first.
func List() []Info {
	out := []Info{}
	var key uintptr
	r, _, _ := pRegOpenKeyExW.Call(hkeyLocalMachine,
		uintptr(unsafe.Pointer(u16(`HARDWARE\DEVICEMAP\SERIALCOMM`))), 0, keyRead, uintptr(unsafe.Pointer(&key)))
	if r != 0 {
		return out
	}
	defer pRegCloseKey.Call(key)
	names := friendlyNames()
	for i := uint32(0); ; i++ {
		name := make([]uint16, 256)
		data := make([]uint16, 256)
		nameLen := uint32(len(name))
		dataLen := uint32(len(data) * 2)
		var typ uint32
		r, _, _ := pRegEnumValueW.Call(key, uintptr(i),
			uintptr(unsafe.Pointer(&name[0])), uintptr(unsafe.Pointer(&nameLen)), 0,
			uintptr(unsafe.Pointer(&typ)), uintptr(unsafe.Pointer(&data[0])), uintptr(unsafe.Pointer(&dataLen)))
		if r != 0 {
			break
		}
		com := syscall.UTF16ToString(data)
		label := com
		if f := names[strings.ToUpper(com)]; f != "" {
			if j := strings.LastIndex(f, ": "); j >= 0 { // the useful tail: "Enhanced COM Port"
				f = f[j+2:]
			}
			label += " - " + f
			if strings.Contains(f, "Enhanced") {
				label += "  (CAT)"
			}
		}
		out = append(out, Info{Path: com, Label: label})
	}
	num := func(s string) int { n, _ := strconv.Atoi(strings.TrimPrefix(strings.ToUpper(s), "COM")); return n }
	sort.Slice(out, func(a, b int) bool {
		ea, eb := strings.Contains(out[a].Label, "(CAT)"), strings.Contains(out[b].Label, "(CAT)")
		if ea != eb {
			return ea
		}
		return num(out[a].Path) < num(out[b].Path)
	})
	return out
}
