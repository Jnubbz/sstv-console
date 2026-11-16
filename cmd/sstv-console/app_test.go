package main

import (
	"archive/zip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeZip(t *testing.T, path string, files map[string]string, exec map[string]bool) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, body := range files {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.SetMode(0o644)
		if exec[name] {
			h.SetMode(0o755)
		}
		w, _ := zw.CreateHeader(h)
		w.Write([]byte(body))
	}
	zw.Close()
	f.Close()
}

func TestSwapFilesReplacesAndKeepsOld(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, winExe), []byte("old exe"), 0o755)
	os.WriteFile(filepath.Join(dir, "README.txt"), []byte("old readme"), 0o644)
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte("mine"), 0o644)
	z := filepath.Join(t.TempDir(), "u.zip")
	writeZip(t, z, map[string]string{
		winFolder + "/" + winExe:     "new exe",
		winFolder + "/README.txt":    "new readme",
		winFolder + "/web/extra.txt": "new file",
	}, nil)
	stage, _ := os.MkdirTemp(dir, ".sstv-update-")
	if err := swapFiles(dir, z, stage); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{winExe: "new exe", "README.txt": "new readme", "web/extra.txt": "new file",
		winExe + ".old": "old exe", "settings.json": "mine"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(b) != want {
			t.Errorf("%s = %q, %v; want %q", name, b, err, want)
		}
	}
}

func TestSwapFilesRefusesAnUpdateWithoutTheApp(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, winExe), []byte("old exe"), 0o755)
	z := filepath.Join(t.TempDir(), "u.zip")
	writeZip(t, z, map[string]string{"Something-Else/readme.txt": "x"}, nil)
	stage, _ := os.MkdirTemp(dir, ".sstv-update-")
	if err := swapFiles(dir, z, stage); err == nil {
		t.Fatal("installed an update with no app in it")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, winExe)); string(b) != "old exe" {
		t.Fatalf("the app was touched: %q", b)
	}
}

// After an update only the files it moved aside go: not every *.old file in the folder, which
// could be the user's own (the app may sit in Documents).
func TestClearOldInRemovesOnlyWhatTheUpdateListed(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "app")
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	write := func(rel string) {
		if err := os.WriteFile(filepath.Join(root, rel), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"app/" + winExe + ".old", "app/README.txt.old", "app/notes.old", "app/sub/mine.old", "outside.old", "app/README.txt"} {
		write(f)
	}
	// the list names one real leftover, one that isn't a .old file, and two ways out of the folder
	list := "README.txt.old\nREADME.txt\n../outside.old\n" + filepath.Join(root, "outside.old") + "\n"
	os.WriteFile(filepath.Join(dir, oldList), []byte(list), 0o644)

	clearOldIn(dir)

	gone := []string{"app/" + winExe + ".old", "app/README.txt.old", "app/" + oldList}
	kept := []string{"app/notes.old", "app/sub/mine.old", "outside.old", "app/README.txt"}
	for _, f := range gone {
		if _, err := os.Stat(filepath.Join(root, f)); err == nil {
			t.Errorf("%s should have been removed", f)
		}
	}
	for _, f := range kept {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			t.Errorf("%s must be left alone: %v", f, err)
		}
	}
}

func TestSwapFilesLeavesAListThatTheNextStartClears(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, winExe), []byte("old exe"), 0o755)
	os.WriteFile(filepath.Join(dir, "README.txt"), []byte("old readme"), 0o644)
	os.WriteFile(filepath.Join(dir, "mine.old"), []byte("not ours"), 0o644)
	z := filepath.Join(t.TempDir(), "u.zip")
	writeZip(t, z, map[string]string{winFolder + "/" + winExe: "new exe", winFolder + "/README.txt": "new readme"}, nil)
	stage, _ := os.MkdirTemp(dir, ".sstv-update-")
	if err := swapFiles(dir, z, stage); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, winExe+".old")); err != nil {
		t.Fatal("the old program should be there until the next start")
	}
	clearOldIn(dir)
	for _, f := range []string{winExe + ".old", "README.txt.old", oldList} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Errorf("%s was left behind", f)
		}
	}
	for _, f := range []string{winExe, "README.txt", "mine.old"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s is gone: %v", f, err)
		}
	}
}

func TestClearStaleStagesOnlyTouchesOldUpdateFolders(t *testing.T) {
	dir := t.TempDir()
	long := time.Now().Add(-3 * time.Hour)
	for _, n := range []string{".sstv-update-123", ".sstv-update-456", ".sstv-update-notes", "Documents"} {
		os.Mkdir(filepath.Join(dir, n), 0o755)
		os.WriteFile(filepath.Join(dir, n, "f"), []byte("x"), 0o644)
		os.Chtimes(filepath.Join(dir, n), long, long)
	}
	os.WriteFile(filepath.Join(dir, ".sstv-update-789"), []byte("a file"), 0o644)
	os.Chtimes(filepath.Join(dir, ".sstv-update-789"), long, long)
	now := time.Now()
	os.Chtimes(filepath.Join(dir, ".sstv-update-456"), now, now) // an update in progress

	clearStaleStages(dir, time.Hour)

	for n, want := range map[string]bool{".sstv-update-123": false, ".sstv-update-456": true, ".sstv-update-notes": true,
		"Documents": true, ".sstv-update-789": true} {
		_, err := os.Stat(filepath.Join(dir, n))
		if (err == nil) != want {
			t.Errorf("%s: present=%v, want %v", n, err == nil, want)
		}
	}
}

func TestSwapAppOnTheMac(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "SSTV Console.app")
	os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755)
	os.WriteFile(filepath.Join(app, "Contents", "MacOS", "sstv-console"), []byte("old"), 0o755)
	z := filepath.Join(t.TempDir(), "u.zip")
	prefix := "SSTV-Console-Mac/SSTV Console.app/Contents/"
	writeZip(t, z, map[string]string{
		prefix + "Info.plist":         "<key>CFBundleExecutable</key><string>SSTV Console</string>",
		prefix + "MacOS/SSTV Console": "shell",
		prefix + "MacOS/sstv-console": "new",
	}, map[string]bool{prefix + "MacOS/SSTV Console": true, prefix + "MacOS/sstv-console": true})
	stage, _ := os.MkdirTemp(root, ".sstv-update-")
	to, err := swapApp(app, z, stage)
	if err != nil || to != app {
		t.Fatalf("swapApp = %q, %v", to, err)
	}
	if b, _ := os.ReadFile(filepath.Join(app, "Contents", "MacOS", "sstv-console")); string(b) != "new" {
		t.Fatalf("engine = %q", b)
	}
}

func TestGuard(t *testing.T) {
	a := newApp(7380, func() {}, t.TempDir(), t.TempDir())
	h := a.handler()
	cases := []struct {
		method, host, header string
		want                 int
	}{
		{"GET", "localhost:7380", "", 200},
		{"GET", "127.0.0.1:7380", "", 200},
		{"GET", "evil.example:7380", "", 403}, // DNS rebinding
		{"POST", "localhost:7380", "", 403},   // no custom header: another site's form
		{"POST", "localhost:7380", "1", 200},
	}
	for _, c := range cases {
		path := "/api/status"
		if c.method == "POST" {
			path = "/api/quit"
		}
		r := httptest.NewRequest(c.method, path, nil)
		r.Host = c.host
		r.RemoteAddr = "127.0.0.1:50000"
		if c.header != "" {
			r.Header.Set("X-SSTV-Console", c.header)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Errorf("%s %s host=%s header=%q: %d, want %d", c.method, path, c.host, c.header, w.Code, c.want)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	r.Host, r.RemoteAddr = "localhost:7380", "192.168.1.20:5000"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("a request from another machine got %d", w.Code)
	}
}

func TestGuardRefusesRequestsFromOtherSites(t *testing.T) {
	h := newApp(7380, func() {}, t.TempDir(), t.TempDir()).handler()
	for site, want := range map[string]int{
		"":            200, // curl, older browsers
		"same-origin": 200, // our own page
		"none":        200, // typed the address, or the window opened
		"same-site":   403, // another page on this machine, e.g. another port
		"cross-site":  403, // any other website
	} {
		r := httptest.NewRequest(http.MethodGet, "/api/status", nil)
		r.Host, r.RemoteAddr = "localhost:7380", "127.0.0.1:50000"
		if site != "" {
			r.Header.Set("Sec-Fetch-Site", site)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("Sec-Fetch-Site %q: %d, want %d", site, w.Code, want)
		}
	}
}

func TestPageHeadersLockItDown(t *testing.T) {
	h := newApp(7380, func() {}, t.TempDir(), t.TempDir()).handler()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host, r.RemoteAddr = "localhost:7380", "127.0.0.1:50000"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	csp := w.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "script-src 'self'", "object-src 'none'", "base-uri 'none'", "form-action 'none'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("Content-Security-Policy lacks %q: %s", want, csp)
		}
	}
	if strings.Contains(csp, "unsafe-") || strings.Contains(csp, "http") || strings.Contains(csp, "*") {
		t.Errorf("Content-Security-Policy lets something in from outside: %s", csp)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Cross-Origin-Resource-Policy") != "same-origin" {
		t.Errorf("headers: %v", w.Header())
	}
}

// The radio isn't connected or disconnected under a transmission: that would let go of a keyed radio.
func TestRadioCantBeChangedWhileTransmitting(t *testing.T) {
	a := newApp(7380, func() {}, t.TempDir(), t.TempDir())
	radio := &stickyRadio{}
	a.rig.open = func(string, int) (catPort, error) { return radio, nil }
	a.rig.connect("port", 38400)
	defer a.rig.disconnect()
	id, _, _ := a.tx.prepare(cardPNG(t), "Martin 2", "")
	if err := a.tx.key(id); err != nil {
		t.Fatal(err)
	}
	h := a.handler()
	for _, path := range []string{"/api/rig/disconnect", "/api/rig/connect"} {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"kind":"hamlib"}`))
		r.Host, r.RemoteAddr = "localhost:7380", "127.0.0.1:50000"
		r.Header.Set("X-SSTV-Console", "1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusConflict {
			t.Errorf("%s while transmitting: %d", path, w.Code)
		}
	}
	if !a.rig.status().Connected || !radio.isKeyed() {
		t.Error("the radio was let go of mid-transmission")
	}
	a.tx.unkey(0, true)
}

func TestPageIsEmbedded(t *testing.T) {
	a := newApp(7380, func() {}, t.TempDir(), t.TempDir())
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host, r.RemoteAddr = "localhost:7380", "127.0.0.1:1"
	w := httptest.NewRecorder()
	a.handler().ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "SSTV Console") {
		t.Fatalf("page: %d", w.Code)
	}
}
