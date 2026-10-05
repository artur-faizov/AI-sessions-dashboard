package dashboard

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/artur-faizov/token-monitor/src/provider"
)

const Version = "3.0.0"

const defaultPort = 8787

var (
	mainMu  sync.Mutex
	mainSrv *http.Server // the running server, for tests to shut down
	mainLn  net.Listener // its listener, for tests to read the bound port
)

var portTries = 20 // ports tried on "address already in use": PORT, PORT+1, …

// runningServer returns the server started by Run and its listener (nil until serving).
func runningServer() (*http.Server, net.Listener) {
	mainMu.Lock()
	defer mainMu.Unlock()
	return mainSrv, mainLn
}

func sliceContains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// Run starts the dashboard serving the given providers and returns the exit
// code. The first provider is the default; PROVIDER / --provider override it.
func Run(providers []provider.Provider, args []string) int {
	if sliceContains(args, "--version") || sliceContains(args, "-version") {
		fmt.Println(Version)
		return 0
	}
	dev := sliceContains(args, "--dev") || sliceContains(args, "-dev") || sliceContains(args, "-dev=true") || sliceContains(args, "--dev=true")

	active, err := pickProvider(providers, args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := active.Available(); err != nil {
		if active.FatalWhenMissing() {
			fmt.Fprintf(os.Stderr, "%s data not found: %v\n", active.Name(), err)
			return 1
		}
		fmt.Printf("%s data not found: %s — pick a data source in the page header\n", active.Name(), active.DataPath())
	}

	port := defaultPort
	if p := os.Getenv("PORT"); p != "" {
		if v, err := strconv.Atoi(p); err == nil {
			port = v
		}
	}

	var ln net.Listener
	for off := 0; off < portTries; off++ {
		p := port + off
		l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p))
		if err == nil {
			ln = l
			if off > 0 {
				fmt.Printf("Port %d already in use — using %d instead.\n", port, p)
			}
			break
		}
		if off == portTries-1 {
			fmt.Printf("Cannot start server on port %d: %v\n", p, err)
			fmt.Printf("Try a different port:  PORT=%d go run ./cmd/dashboard\n", p+1)
			return 1
		}
	}
	port = ln.Addr().(*net.TCPAddr).Port

	url := fmt.Sprintf("http://localhost:%d", port)
	fmt.Printf("Token usage tree: %s  (%s: %s, read-only)\n", url, active.Name(), active.DataPath())

	if os.Getenv("NO_BROWSER") == "" {
		time.AfterFunc(500*time.Millisecond, func() { openBrowser(url) })
	}

	srv := &http.Server{Handler: newServer(dev, providers, active)}
	mainMu.Lock()
	mainSrv, mainLn = srv, ln
	mainMu.Unlock()
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-done
		srv.Shutdown(context.Background())
	}()
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		return 1
	}
	fmt.Println("\nbye")
	return 0
}
