package dashboard

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

const Version = "3.0.0"

const defaultPort = 8787

var mainSrv *http.Server // the running server, for tests to shut down
var mainLn *net.Listener // its listener, for tests to read the bound port

var portTries = 20 // ports tried on "address already in use": PORT, PORT+1, …

const activeWindowMS = 15 * 60 * 1000 // a session updated within this window is "active"

// cliDBFlag is the --db CLI override captured at startup (used by /api/db candidates).
var cliDBFlag string

func sliceContains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// Run starts the dashboard with the given CLI args and returns the exit code.
func Run(args []string) int {
	if sliceContains(args, "--version") || sliceContains(args, "-version") {
		fmt.Println(Version)
		return 0
	}
	dev := sliceContains(args, "--dev") || sliceContains(args, "-dev") || sliceContains(args, "-dev=true") || sliceContains(args, "--dev=true")

	cliPath, _ := parseDBFlag(args)
	cliDBFlag = cliPath
	path, found := resolveDB(cliPath)
	valid := ""
	if found {
		valid = path
	}
	dbx.reset(path, valid)
	if !found {
		fmt.Printf("DB not found: %s — set the path in the page header\n", path)
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
	fmt.Printf("Token usage tree: %s  (DB: %s, read-only)\n", url, dbx.path)

	if os.Getenv("NO_BROWSER") == "" {
		time.AfterFunc(500*time.Millisecond, func() { openBrowser(url) })
	}

	srv := &http.Server{Handler: newServer(dev)}
	mainSrv = srv
	mainLn = &ln
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
