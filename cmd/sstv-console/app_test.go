package main

import (
	"archive/zip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	a := &App{port: 7380, quit: func() {}}
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
	r := httptest.NewRequest("GET", "/api/status", nil)
	r.Host, r.RemoteAddr = "localhost:7380", "192.168.1.20:5000"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("a request from another machine got %d", w.Code)
	}
}

func TestPageIsEmbedded(t *testing.T) {
	a := &App{port: 7380, quit: func() {}}
	r := httptest.NewRequest("GET", "/", nil)
	r.Host, r.RemoteAddr = "localhost:7380", "127.0.0.1:1"
	w := httptest.NewRecorder()
	a.handler().ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "SSTV Console") {
		t.Fatalf("page: %d", w.Code)
	}
}
