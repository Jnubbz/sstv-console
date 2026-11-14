package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"math"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Jnubbz/sstv-console/internal/serial"
	"github.com/Jnubbz/sstv-console/sstv"
)

//go:embed web
var webFiles embed.FS

// App is the running engine.
type App struct {
	port int
	quit func()
	set  *settingsStore
	rig  *rigConn
	rx   *receiver
	tx   *transmitter
	dir  string

	mu      sync.Mutex
	restart bool // quitting into an update: don't treat the window closing as Quit
}

func (a *App) restarting() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.restart
}

func (a *App) setRestarting() {
	a.mu.Lock()
	a.restart = true
	a.mu.Unlock()
}

// busy refuses an update while something must not be cut off.
func (a *App) busy() error {
	if a.tx != nil && a.tx.isKeyed() {
		return errors.New("transmitting: update when the picture has gone out")
	}
	return nil
}

func (a *App) shutdown() {
	if a.tx != nil {
		a.tx.unkey(0, true) // never leave the radio transmitting
	}
	if a.rig != nil {
		a.rig.disconnect() // drops the port's lines and closes it
	}
}

// newApp wires the engine together. dir holds settings; picDir the received pictures.
func newApp(port int, quit func(), dir, picDir string) *App {
	a := &App{port: port, quit: quit, set: loadSettings(dir), rig: newRig(), dir: dir}
	a.rx = newReceiver(dir, picDir, func() int { return a.rig.status().FreqHz })
	a.tx = &transmitter{rig: a.rig, onEnd: a.rx.reset, onSent: a.rx.addSent, after: time.AfterFunc}
	a.rx.muted = a.tx.isKeyed
	return a
}

// reconnect: the radio that was connected last time, if it's there.
func (a *App) reconnect() {
	if s := a.set.get(); s.RigPort != "" {
		a.rig.connect(s.RigPort, s.RigBaud)
	}
}

// handler: the page and its API. Only this machine's own page may use it: the engine
// listens on 127.0.0.1, refuses other Host names (DNS rebinding), and every POST needs a
// custom header a page from another site can't send without CORS (which we never allow).
func (a *App) handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(webFiles, "web")
	mux.Handle("GET /", http.FileServerFS(static))

	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, a.status())
	})
	mux.HandleFunc("POST /api/update/check", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
		defer cancel()
		updater.check(ctx)
		writeJSON(w, http.StatusOK, a.status())
	})
	mux.HandleFunc("POST /api/update/install", func(w http.ResponseWriter, r *http.Request) {
		if err := updater.startInstall(); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, a.status())
	})
	mux.HandleFunc("POST /api/audio", func(w http.ResponseWriter, r *http.Request) {
		rate, _ := strconv.Atoi(r.URL.Query().Get("rate"))
		b, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if err != nil || len(b)%4 != 0 {
			http.Error(w, "bad audio", http.StatusBadRequest)
			return
		}
		f := make([]float32, len(b)/4)
		for i := range f {
			v := math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
			if v != v || v > 10 || v < -10 { // NaN or nonsense
				v = 0
			}
			f[i] = v
		}
		a.rx.push(f, rate)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /api/live.png", func(w http.ResponseWriter, r *http.Request) {
		b := a.rx.livePNG()
		if b == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(b)
	})
	mux.HandleFunc("POST /api/rx/reset", func(w http.ResponseWriter, r *http.Request) {
		a.rx.reset()
		writeJSON(w, http.StatusOK, a.status())
	})
	mux.HandleFunc("POST /api/rx/record", func(w http.ResponseWriter, r *http.Request) {
		a.rx.record()
		writeJSON(w, http.StatusOK, a.status())
	})
	mux.HandleFunc("GET /api/pictures", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, a.rx.pictures())
	})
	mux.HandleFunc("GET /api/picture/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSuffix(r.PathValue("id"), ".png")
		if !a.rx.has(id) { // only pictures in the list: never a path from the request
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		http.ServeFile(w, r, a.rx.file(id))
	})
	mux.HandleFunc("GET /api/ports", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ports": serial.List(), "bauds": serial.Bauds})
	})
	mux.HandleFunc("POST /api/rig/connect", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Port string `json:"port"`
			Baud int    `json:"baud"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil || req.Port == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "pick a port"})
			return
		}
		known := false // only a port from the list: never an arbitrary path from the request
		for _, p := range serial.List() {
			known = known || p.Path == req.Port
		}
		if !known {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "that port isn't there any more"})
			return
		}
		if err := a.rig.connect(req.Port, req.Baud); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		a.set.update(func(s *Settings) { s.RigPort, s.RigBaud = req.Port, req.Baud })
		writeJSON(w, http.StatusOK, a.status())
	})
	mux.HandleFunc("POST /api/rig/tune", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Band string `json:"band"`
		}
		json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req)
		if a.tx.isKeyed() {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "transmitting: change band when the picture has gone out"})
			return
		}
		if err := a.rig.tune(req.Band); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, a.status())
	})
	mux.HandleFunc("POST /api/rig/disconnect", func(w http.ResponseWriter, r *http.Request) {
		a.rig.disconnect()
		a.set.update(func(s *Settings) { s.RigPort = "" })
		writeJSON(w, http.StatusOK, a.status())
	})
	mux.HandleFunc("POST /api/settings/audio", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ ID, Label string }
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil || len(req.ID) > 300 || len(req.Label) > 300 {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		a.set.update(func(s *Settings) { s.AudioDevice, s.AudioLabel = req.ID, req.Label })
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/tx/prepare", func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		mode := r.URL.Query().Get("mode")
		id, secs, err := a.tx.prepare(b, mode, cleanCall(r.URL.Query().Get("to")))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		a.set.update(func(s *Settings) { s.TxMode = mode })
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "seconds": secs})
	})
	mux.HandleFunc("GET /api/tx/audio.wav", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(r.URL.Query().Get("id"))
		b := a.tx.audio(id)
		if b == nil {
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, "tx.wav", time.Time{}, bytes.NewReader(b))
	})
	mux.HandleFunc("POST /api/tx/key", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(r.URL.Query().Get("id"))
		if err := a.tx.key(id); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, a.status())
	})
	mux.HandleFunc("POST /api/tx/unkey", func(w http.ResponseWriter, r *http.Request) {
		a.tx.unkey(0, r.URL.Query().Get("stopped") == "1")
		writeJSON(w, http.StatusOK, a.status())
	})
	mux.HandleFunc("POST /api/tx/photo", func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(io.LimitReader(r.Body, 30<<20))
		if err == nil {
			err = savePhoto(a.dir, b)
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /api/tx/photo", func(w http.ResponseWriter, r *http.Request) {
		b, err := os.ReadFile(photoPath(a.dir))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", http.DetectContentType(b))
		w.Write(b)
	})
	mux.HandleFunc("POST /api/settings/station", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			MyCall, MyGrid, OutDevice, OutLabel *string
			TopColor, BottomColor               *string
			TxLevel                             *int
			NoTextBar                           *bool
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		a.set.update(func(s *Settings) {
			if req.MyCall != nil {
				s.MyCall = cleanCall(*req.MyCall)
			}
			if req.MyGrid != nil {
				s.MyGrid = cleanCall(*req.MyGrid)
			}
			if req.OutDevice != nil && len(*req.OutDevice) <= 300 {
				s.OutDevice = *req.OutDevice
			}
			if req.OutLabel != nil && len(*req.OutLabel) <= 300 {
				s.OutLabel = *req.OutLabel
			}
			if req.TopColor != nil && isColor(*req.TopColor) {
				s.TopColor = *req.TopColor
			}
			if req.BottomColor != nil && isColor(*req.BottomColor) {
				s.BottomColor = *req.BottomColor
			}
			if req.NoTextBar != nil {
				s.NoTextBar = *req.NoTextBar
			}
			if req.TxLevel != nil && *req.TxLevel >= 1 && *req.TxLevel <= 100 {
				s.TxLevel = *req.TxLevel
			}
		})
		writeJSON(w, http.StatusOK, a.status())
	})
	mux.HandleFunc("POST /api/picture/call", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ ID, Call string }
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil || !a.rx.setCall(req.ID, cleanCall(req.Call)) {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/quit", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		go func() { time.Sleep(200 * time.Millisecond); a.quit() }()
	})
	return a.guard(mux)
}

type appStatus struct {
	Version  string       `json:"version"`
	Update   updateStatus `json:"update"`
	Rig      rigStatus    `json:"rig"`
	RX       rxStatus     `json:"rx"`
	Settings Settings     `json:"settings"`
	Pictures string       `json:"picturesDir"`
	Bands    []sstvBand   `json:"bands"`
	TX       txStatus     `json:"tx"`
	Modes    []modeInfo   `json:"modes"`
}

type modeInfo struct {
	Name    string  `json:"name"`
	W       int     `json:"w"`
	H       int     `json:"h"`
	Seconds float64 `json:"seconds"`
}

var modeList = func() []modeInfo {
	var out []modeInfo
	for _, m := range sstv.Modes {
		out = append(out, modeInfo{m.Name, m.Width, m.Height, math.Round(m.Duration() + 1)})
	}
	return out
}()

// cleanCall: a callsign or grid as typed, tidied (letters, digits and / only, upper case).
func cleanCall(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	out := make([]rune, 0, len(s))
	for _, c := range s {
		if (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '/' {
			out = append(out, c)
		}
	}
	if len(out) > 12 {
		out = out[:12]
	}
	return string(out)
}

func (a *App) status() appStatus {
	return appStatus{Version: appVersion, Update: updater.status(), Rig: a.rig.status(), RX: a.rx.status(),
		Settings: a.set.get(), Pictures: a.rx.picDir, Bands: sstvBands, TX: a.tx.status(), Modes: modeList}
}

func (a *App) guard(next http.Handler) http.Handler {
	p := strconv.Itoa(a.port)
	okHost := map[string]bool{"localhost:" + p: true, "127.0.0.1:" + p: true}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !okHost[r.Host] || !isLoopback(r.RemoteAddr) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-SSTV-Console") != "1" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; style-src 'self'; script-src 'self'; worker-src 'self'; connect-src 'self'; media-src 'self' blob:; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func isLoopback(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
