package dashboard

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/artur-faizov/token-monitor/src/provider"
)

// captureStderr runs f with stderr captured into a pipe and returns the output.
func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	f()
	os.Stderr = old
	w.Close()
	out := <-done
	r.Close()
	return out
}

func runProviders() []provider.Provider {
	return []provider.Provider{
		newLenient("alpha", "/data/alpha.db"),
		newStrict("beta", "/data/beta", true),
	}
}

// captureStdout runs f with stdout captured into a pipe and returns the output.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	f()
	os.Stdout = old
	w.Close()
	out := <-done
	r.Close()
	return out
}

func TestVersionFlagPrintsVersion(t *testing.T) {
	var code int
	out := captureStdout(t, func() { code = Run(runProviders(), []string{"--version"}) })
	if code != 0 {
		t.Fatalf("exit code %d, want 0", code)
	}
	if out != Version+"\n" {
		t.Fatalf("output %q", out)
	}
}

func occupyPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().(*net.TCPAddr).Port
}

func TestPortInUseGivesClearMessageWithoutPanicking(t *testing.T) {
	port := occupyPort(t)
	oldTries := portTries
	portTries = 1
	t.Cleanup(func() { portTries = oldTries })
	t.Setenv("PORT", fmt.Sprintf("%d", port))
	t.Setenv("NO_BROWSER", "1")
	t.Setenv("PROVIDER", "")
	var code int
	out := captureStdout(t, func() { code = Run(runProviders(), []string{}) })
	if code != 1 {
		t.Fatalf("exit code %d, want 1", code)
	}
	if !strings.Contains(out, "already in use") && !strings.Contains(out, "Cannot start server") {
		t.Fatalf("no clear message in output: %q", out)
	}
	if !strings.Contains(out, fmt.Sprintf("%d", port)) {
		t.Fatalf("port %d missing from output: %q", port, out)
	}
}

func TestMainFallsBackToNextFreePort(t *testing.T) {
	port := occupyPort(t)
	t.Setenv("PORT", fmt.Sprintf("%d", port))
	t.Setenv("NO_BROWSER", "1")
	t.Setenv("PROVIDER", "")
	srv, ln := startRun(t, runProviders(), nil)
	chosen := ln.Addr().(*net.TCPAddr).Port
	if chosen <= port {
		t.Fatalf("must pick a port after the busy one, got %d", chosen)
	}
	r, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/db", chosen))
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	json.NewDecoder(r.Body).Decode(&body)
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatalf("status %d", r.StatusCode)
	}
	out, code := srv.stop()
	if code != 0 {
		t.Fatalf("exit code %d, want 0", code)
	}
	if !strings.Contains(out, "already in use") || !strings.Contains(out, fmt.Sprintf("%d", chosen)) {
		t.Fatalf("fallback message missing: %q", out)
	}
}

type runHandle struct {
	srv  *http.Server
	done chan struct{}
	out  string
	code int
}

// stop shuts the server down and returns Run's stdout and exit code.
func (h *runHandle) stop() (string, int) {
	h.srv.Close()
	<-h.done
	return h.out, h.code
}

// startRun runs Run in the background and waits until it is serving.
func startRun(t *testing.T, ps []provider.Provider, args []string) (*runHandle, net.Listener) {
	t.Helper()
	oldSrv, oldLn := runningServer()
	mainMu.Lock()
	mainSrv, mainLn = nil, nil
	mainMu.Unlock()
	t.Cleanup(func() {
		mainMu.Lock()
		mainSrv, mainLn = oldSrv, oldLn
		mainMu.Unlock()
	})
	h := &runHandle{done: make(chan struct{})}
	go func() {
		h.out = captureStdout(t, func() { h.code = Run(ps, args) })
		close(h.done)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if srv, ln := runningServer(); srv != nil {
			h.srv = srv
			return h, ln
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("Run did not start a server")
	return nil, nil
}

func TestRunLogsActiveProviderDataPath(t *testing.T) {
	t.Setenv("PORT", "0")
	t.Setenv("NO_BROWSER", "1")
	t.Setenv("PROVIDER", "")
	h, _ := startRun(t, runProviders(), []string{"--provider", "beta"})
	out, code := h.stop()
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if !strings.Contains(out, "(beta: /data/beta, read-only)") {
		t.Fatalf("startup line must name the active provider's data path: %q", out)
	}
}

func TestRunLenientMissingDataServesAnyway(t *testing.T) {
	t.Setenv("PORT", "0")
	t.Setenv("NO_BROWSER", "1")
	t.Setenv("PROVIDER", "")
	p := newLenient("alpha", "/no-such/alpha.db")
	p.validPaths = map[string]bool{}
	h, _ := startRun(t, []provider.Provider{p}, nil)
	out, code := h.stop()
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if !strings.Contains(out, "data not found: /no-such/alpha.db") {
		t.Fatalf("missing-data notice must name the path: %q", out)
	}
}

func TestRunStrictMissingDataExits(t *testing.T) {
	t.Setenv("NO_BROWSER", "1")
	t.Setenv("PROVIDER", "")
	ps := []provider.Provider{newStrict("beta", "/no-such/projects", false), newLenient("alpha", "/data/alpha.db")}
	var code int
	errOut := captureStderr(t, func() { code = Run(ps, nil) })
	if code != 1 {
		t.Fatalf("exit code %d, want 1 (no fallback to another provider)", code)
	}
	if !strings.Contains(errOut, "/no-such/projects") {
		t.Fatalf("error must name the missing path: %q", errOut)
	}
}

func TestRunUnknownProviderExits(t *testing.T) {
	t.Setenv("PROVIDER", "")
	var code int
	errOut := captureStderr(t, func() { code = Run(runProviders(), []string{"--provider", "bogus"}) })
	if code != 1 {
		t.Fatalf("exit code %d, want 1", code)
	}
	if !strings.Contains(errOut, `unknown provider "bogus" (available: alpha, beta)`) {
		t.Fatalf("stderr %q", errOut)
	}
}
