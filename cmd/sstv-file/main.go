// sstv-file decodes SSTV recordings and makes them, from the command line.
//
//	sstv-file recording.wav            every picture in the recording -> recording-1.png, ...
//	sstv-file -mode "Scottie 1" picture.png out.wav     a picture -> audio to transmit
package main

import (
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"math"
	"os"
	"strings"

	"github.com/Jnubbz/sstv-console/sstv"
)

func main() {
	mode := flag.String("mode", "", `encode: the mode, e.g. "Scottie 1"`)
	flag.Parse()
	var err error
	switch {
	case *mode != "" && flag.NArg() == 2:
		err = encode(*mode, flag.Arg(0), flag.Arg(1))
	case *mode == "" && flag.NArg() == 1:
		err = decode(flag.Arg(0))
	default:
		fmt.Fprintln(os.Stderr, "usage: sstv-file recording.wav | sstv-file -mode \"Scottie 1\" picture.png out.wav")
		fmt.Fprint(os.Stderr, "modes:")
		for _, m := range sstv.Modes {
			fmt.Fprintf(os.Stderr, " %q", m.Name)
		}
		fmt.Fprintln(os.Stderr)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "sstv-file:", err)
		os.Exit(1)
	}
}

func decode(path string) error {
	audio, rate, err := readWAV(path)
	if err != nil {
		return err
	}
	d := sstv.NewDecoder(rate)
	base := strings.TrimSuffix(path, ".wav")
	n := 0
	for i := 0; i < len(audio); i += 8192 {
		for _, e := range d.Write(audio[i:min(i+8192, len(audio))]) {
			switch e.Kind {
			case sstv.Started:
				fmt.Printf("%s at %.1f s (off-tune %+.0f Hz)\n", e.Mode.Name, float64(i)/float64(rate), e.OffsetHz)
			case sstv.Aborted:
				fmt.Println("  lost it")
			case sstv.Done:
				n++
				out := fmt.Sprintf("%s-%d.png", base, n)
				f, err := os.Create(out)
				if err != nil {
					return err
				}
				if err := png.Encode(f, e.Image); err != nil {
					return err
				}
				f.Close()
				fmt.Printf("  -> %s (%d line syncs found, slant %+.0f ppm)\n", out, e.Synced, e.SlantPPM)
			}
		}
	}
	if n == 0 {
		return errors.New("no picture found (no SSTV header in the recording)")
	}
	return nil
}

func encode(mode, in, out string) error {
	m := sstv.ByName(mode)
	if m == nil {
		return fmt.Errorf("no mode called %q", mode)
	}
	f, err := os.Open(in)
	if err != nil {
		return err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return err
	}
	return writeWAV(out, sstv.Encode(img, m, 48000), 48000)
}

// readWAV: 16-bit PCM or 32-bit float, any channel count (the first channel is used).
func readWAV(path string) ([]float32, int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	if len(b) < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return nil, 0, errors.New("not a WAV file")
	}
	var format, ch, bits, rate int
	for p := 12; p+8 <= len(b); {
		id, n := string(b[p:p+4]), int(binary.LittleEndian.Uint32(b[p+4:]))
		body := b[p+8 : min(p+8+n, len(b))]
		switch id {
		case "fmt ":
			if len(body) < 16 {
				return nil, 0, errors.New("bad WAV header")
			}
			format = int(binary.LittleEndian.Uint16(body))
			ch = int(binary.LittleEndian.Uint16(body[2:]))
			rate = int(binary.LittleEndian.Uint32(body[4:]))
			bits = int(binary.LittleEndian.Uint16(body[14:]))
		case "data":
			if ch == 0 || rate == 0 {
				return nil, 0, errors.New("bad WAV header")
			}
			step := ch * bits / 8
			var out []float32
			switch {
			case (format == 1 || format == 0xFFFE) && bits == 16:
				for i := 0; i+step <= len(body); i += step {
					out = append(out, float32(int16(binary.LittleEndian.Uint16(body[i:])))/32768)
				}
			case format == 3 && bits == 32:
				for i := 0; i+step <= len(body); i += step {
					out = append(out, math.Float32frombits(binary.LittleEndian.Uint32(body[i:])))
				}
			default:
				return nil, 0, fmt.Errorf("WAV format %d with %d bits isn't supported (use 16-bit PCM)", format, bits)
			}
			return out, rate, nil
		}
		p += 8 + n + n%2
	}
	return nil, 0, errors.New("no audio in the WAV file")
}

func writeWAV(path string, audio []float32, rate int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	n := len(audio) * 2
	h := make([]byte, 44)
	copy(h, "RIFF")
	binary.LittleEndian.PutUint32(h[4:], uint32(36+n))
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1)
	binary.LittleEndian.PutUint16(h[22:], 1)
	binary.LittleEndian.PutUint32(h[24:], uint32(rate))
	binary.LittleEndian.PutUint32(h[28:], uint32(rate*2))
	binary.LittleEndian.PutUint16(h[32:], 2)
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], uint32(n))
	body := make([]byte, n)
	for i, s := range audio {
		binary.LittleEndian.PutUint16(body[2*i:], uint16(int16(s*32767)))
	}
	if _, err := f.Write(h); err != nil {
		return err
	}
	_, err = f.Write(body)
	return err
}
