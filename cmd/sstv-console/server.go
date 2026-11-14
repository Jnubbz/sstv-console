package main

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

//go:embed web
var webFiles embed.FS

// App is the running engine.
type App struct {
	port int
	quit func()

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
// TODO: transmitting (and the user's choice about mid-receive) once TX exists.
func (a *App) busy() error { return nil }

func (a *App) shutdown() {
	// TODO: unkey the radio and close the audio and serial ports once they exist.
}

// handler: the page and its API. Only this machine's own page may use it: the engine
// listens on 127.0.0.1, refuses other Host names (DNS rebinding), and every POST needs a
// custom header a page from another site can't send without CORS (which we never allow).
func (a *App) handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(webFiles, "web")
	mux.Handle("GET /", http.FileServerFS(static))

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
	mux.HandleFunc("POST /api/quit", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		go func() { time.Sleep(200 * time.Millisecond); a.quit() }()
	})
	return a.guard(mux)
}

type appStatus struct {
	Version string       `json:"version"`
	Update  updateStatus `json:"update"`
}

func (a *App) status() appStatus {
	return appStatus{Version: appVersion, Update: updater.status()}
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
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; style-src 'self'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'")
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
