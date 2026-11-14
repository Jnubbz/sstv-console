// SSTV Console: send and receive slow-scan TV pictures like messages.
//
// This program is the engine: it serves the app's page on localhost and does the work. The
// window around the page is per platform: on the Mac a small Swift shell (packaging/mac),
// on Windows an Edge app-mode window this program opens (window_windows.go), elsewhere the
// default browser.
package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

var appVersion = "dev" // set at build time (-ldflags -X main.appVersion=...)

// defaultPort is fixed so the page can find the app again after it restarts into an update.
// If something else has it, any free port is used.
const defaultPort = 7380

func main() {
	nobrowser := flag.Bool("nobrowser", false, "don't open a window (the Mac app's shell shows the page)")
	afterUpdate := flag.Int("after-update", 0, "wait for this process (the old version) to exit first")
	port := flag.Int("port", defaultPort, "port to serve the page on (localhost only)")
	window := flag.Int("window", 0, "Windows: the app window kept open across an update")
	flag.Parse()

	if *afterUpdate > 0 {
		waitForExit(*afterUpdate, 15*time.Second)
	}
	clearOldFiles()

	ln, err := listen(*port, *afterUpdate > 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, "SSTV Console:", err)
		os.Exit(1)
	}
	p := ln.Addr().(*net.TCPAddr).Port
	url := fmt.Sprintf("http://localhost:%d/", p)

	var once sync.Once
	done := make(chan struct{})
	quit := func() { once.Do(func() { close(done) }) }

	app := &App{port: p, quit: quit}
	updater.start(app)
	go func() {
		if err := http.Serve(ln, app.handler()); err != nil {
			fmt.Fprintln(os.Stderr, "SSTV Console:", err)
			quit()
		}
	}()
	// The Mac shell reads this line to find the page.
	fmt.Printf("SSTV Console %s running at %s\n", appVersion, url)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() { <-sig; quit() }()

	if !*nobrowser {
		go func() {
			if showWindow(url, *window) && !app.restarting() {
				quit() // the window was closed
			}
		}()
	}
	<-done
	app.shutdown()
}

// listen takes the port, or after an update waits for the old version to let go of it.
// Anything else: a free port.
func listen(port int, wait bool) (net.Listener, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(10 * time.Second)
	for {
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			return ln, nil
		}
		if !wait || time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	return net.Listen("tcp", "127.0.0.1:0")
}
