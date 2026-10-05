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
)

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
	t.Setenv("DB_PATH", "/nonexistent/dashboard-version-test.db")
	var code int
	out := captureStdout(t, func() { code = Run([]string{"--version"}) })
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
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("DB_PATH", "")
	var code int
	out := captureStdout(t, func() { code = Run([]string{}) })
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
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("DB_PATH", "")
	oldSrv, oldLn := mainSrv, mainLn
	t.Cleanup(func() { mainSrv, mainLn = oldSrv, oldLn })

	var out string
	var code int
	done := make(chan struct{})
	go func() {
		out = captureStdout(t, func() { code = Run([]string{}) })
		close(done)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if mainSrv != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if mainSrv == nil {
		t.Fatal("Run must start a server even when the port is busy")
	}
	chosen := (*mainLn).Addr().(*net.TCPAddr).Port
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
	mainSrv.Close()
	<-done
	if code != 0 {
		t.Fatalf("exit code %d, want 0", code)
	}
	if !strings.Contains(out, "already in use") || !strings.Contains(out, fmt.Sprintf("%d", chosen)) {
		t.Fatalf("fallback message missing: %q", out)
	}
}
