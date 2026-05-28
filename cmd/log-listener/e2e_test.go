package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// e2e tests spawn the compiled log-listener binary as a subprocess so they
// exercise the full CLI→config→discover→watch→render→sink path the way a
// user would. The binary is built once per `go test` invocation.

var (
	e2eBinOnce sync.Once
	e2eBin     string
	e2eBinErr  error
)

func e2eBinary(t *testing.T) string {
	t.Helper()
	e2eBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "log-listener-e2e-")
		if err != nil {
			e2eBinErr = err
			return
		}
		bin := filepath.Join(dir, "log-listener")
		cmd := exec.Command("go", "build", "-o", bin, ".")
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			e2eBinErr = fmt.Errorf("build failed: %w", err)
			return
		}
		e2eBin = bin
	})
	if e2eBinErr != nil {
		t.Skipf("e2e build skipped: %v", e2eBinErr)
	}
	return e2eBin
}

// stream is a single persistent reader goroutine over an io.Reader. Multiple
// Await() calls share the same goroutine, which avoids the
// concurrent-scanners race that happens if you spawn a fresh scanner per
// assertion (two scanners on the same pipe steal each other's bytes).
type stream struct {
	ch chan string
}

func newStream(r io.Reader) *stream {
	s := &stream{ch: make(chan string, 4096)}
	go func() {
		defer close(s.ch)
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			s.ch <- sc.Text()
		}
	}()
	return s
}

// Await reads lines until match returns true or the timeout fires. Returns
// the matched line, all lines seen during this call (for diagnostics), and
// whether it timed out.
func (s *stream) Await(timeout time.Duration, match func(string) bool) (matched string, all []string, timedOut bool) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case line, ok := <-s.ch:
			if !ok {
				return "", all, true
			}
			all = append(all, line)
			if match(line) {
				return line, all, false
			}
		case <-timer.C:
			return "", all, true
		}
	}
}

// startListener spawns the binary with the given args and returns a stream
// reader over its stdout. The subprocess is killed on test cleanup.
func startListener(t *testing.T, args ...string) *stream {
	t.Helper()
	bin := e2eBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return newStream(stdout)
}

// TestE2ELiveTailingAppend asserts that lines appended to an existing
// watched file appear on log-listener's stdout in real time.
func TestE2ELiveTailingAppend(t *testing.T) {
	dir := t.TempDir()

	// Seed an existing empty file so the tailer attaches at startup
	// (newly-created files take a different code path — covered by
	// TestE2ELiveTailingNewFile below).
	path := filepath.Join(dir, "app.log")
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	s := startListener(t, "-d", dir, "-r", `name:\.log$`, "--no-tui", "--no-color")
	time.Sleep(300 * time.Millisecond) // let fsnotify register

	// Append a line in the background.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("hello-live-world\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	want := "hello-live-world"
	matched, all, timedOut := s.Await(5*time.Second, func(s string) bool {
		return strings.Contains(s, want)
	})
	if timedOut {
		t.Fatalf("never saw %q in stdout; lines seen:\n  %s",
			want, strings.Join(all, "\n  "))
	}
	if !strings.Contains(matched, "app.log") {
		t.Fatalf("expected line to mention app.log, got: %q", matched)
	}
}

// TestE2ELiveTailingFileGroup is the -f path: a single file given by path,
// not a directory glob. Must behave identically to the -d path for an
// already-existing file.
func TestE2ELiveTailingFileGroup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "single.log")
	if err := os.WriteFile(path, []byte("seed-existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := startListener(t, "-f", path, "--no-tui", "--no-color")
	time.Sleep(300 * time.Millisecond)

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("file-group-live\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	want := "file-group-live"
	matched, all, timedOut := s.Await(5*time.Second, func(line string) bool {
		return strings.Contains(line, want)
	})
	if timedOut {
		t.Fatalf("never saw %q via -f; lines:\n  %s", want, strings.Join(all, "\n  "))
	}
	if !strings.Contains(matched, "single.log") {
		t.Fatalf("expected single.log in line, got: %q", matched)
	}
}

// TestE2ELiveTailingNewFile covers the Create-event branch: a file that
// didn't exist at startup is later created in the watched dir.
func TestE2ELiveTailingNewFile(t *testing.T) {
	dir := t.TempDir()

	s := startListener(t, "-d", dir, "-r", `name:\.log$`, "--no-tui", "--no-color")
	time.Sleep(300 * time.Millisecond)

	path := filepath.Join(dir, "fresh.log")
	if err := os.WriteFile(path, []byte("brand-new-file-line\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	want := "brand-new-file-line"
	matched, all, timedOut := s.Await(5*time.Second, func(s string) bool {
		return strings.Contains(s, want)
	})
	if timedOut {
		t.Fatalf("never saw %q; lines:\n  %s", want, strings.Join(all, "\n  "))
	}
	if !strings.Contains(matched, "fresh.log") {
		t.Fatalf("expected line to mention fresh.log, got: %q", matched)
	}
}

// TestE2ELiveTailingRotation makes sure rename-rotation is handled end-to-end:
// the tail must not lose lines across the rotation boundary.
func TestE2ELiveTailingRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rot.log")
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	s := startListener(t, "-d", dir, "-r", `name:\.log$`, "--no-tui", "--no-color")
	time.Sleep(300 * time.Millisecond)

	appendLine := func(p, line string) {
		f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(line + "\n"); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}

	appendLine(path, "before-rotate")
	if _, _, to := s.Await(5*time.Second, func(line string) bool {
		return strings.Contains(line, "before-rotate")
	}); to {
		t.Fatal("never saw before-rotate")
	}

	// Rotate: rename old, create fresh.
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	appendLine(path, "after-rotate")

	if _, all, to := s.Await(5*time.Second, func(line string) bool {
		return strings.Contains(line, "after-rotate")
	}); to {
		t.Fatalf("never saw after-rotate; lines:\n  %s", strings.Join(all, "\n  "))
	}
}

// TestE2ESSEDeliversEvents asserts that the SSE broadcast carries the same
// rendered events that show up on stdout.
func TestE2ESSEDeliversEvents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sse.log")
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	// Pick an ephemeral port up front. There's a tiny race between
	// listening here and the binary starting; we close the listener and
	// pass the port string. In practice this rarely flakes.
	addr := pickFreeAddr(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := startListener(t,
		"-d", dir, "-r", `name:\.log$`,
		"--no-tui", "--no-color",
		"--sse", addr,
	)
	// Drain stdout in the background so the pipe never fills and blocks emit().
	go func() {
		for range s.ch {
		}
	}()

	// Wait for SSE server to come up.
	sseURL := "http://" + addr + "/stream"
	if err := waitForHTTP(sseURL, 3*time.Second); err != nil {
		t.Fatalf("SSE server didn't come up: %v", err)
	}

	// Subscribe.
	req, _ := http.NewRequestWithContext(ctx, "GET", sseURL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("sse status %d", resp.StatusCode)
	}

	time.Sleep(100 * time.Millisecond)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("sse-payload-marker\n")
	f.Close()

	// Read one SSE 'data:' line and verify the payload.
	r := bufio.NewReader(resp.Body)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("sse read: %v", err)
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(strings.TrimSpace(line), "data: ")
		var ev map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			t.Fatalf("invalid SSE JSON: %v\n%s", err, payload)
		}
		if !strings.Contains(payload, "sse-payload-marker") {
			t.Fatalf("payload missing marker: %s", payload)
		}
		return
	}
	t.Fatal("timed out waiting for SSE event with marker")
}

// pickFreeAddr asks the OS for a free TCP port and returns "127.0.0.1:N".
func pickFreeAddr(t *testing.T) string {
	t.Helper()
	l, err := newListener()
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func waitForHTTP(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for %s", url)
}
