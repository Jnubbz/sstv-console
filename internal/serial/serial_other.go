//go:build !linux && !darwin && !windows

package serial

import "errors"

type Port struct{}

func Open(port string, baud int) (*Port, error) { return nil, errors.New("no serial ports here") }
func (p *Port) Write(s string) error            { return nil }
func (p *Port) read(buf []byte) (int, error)    { return 0, errors.New("closed") }
func (p *Port) Close()                          {}
func (p *Port) SetRTS(on bool) error            { return nil }
func (p *Port) SetDTR(on bool) error            { return nil }
func List() []Info                              { return []Info{} }
