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
	goruntime "runtime"
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
		if goruntime.GOOS == "windows" {
			bin += ".exe"
		}
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
	// Isolate from ambient config discovery: the binary auto-loads
	// ./log-listener.yml and ~/.log-listener.yml. Run from a throwaway dir
	// with HOME/USERPROFILE pointed there so a developer's local config in the
	// repo root (gitignored) can't perturb e2e assertions. All e2e args use
	// absolute paths, so the working directory is otherwise irrelevant.
	isoHome := t.TempDir()
	cmd.Dir = isoHome
	cmd.Env = append(os.Environ(), "HOME="+isoHome, "USERPROFILE="+isoHome)
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

// TestE2EStaticDirGlobAtStartup asserts that -d with a glob pattern
// expands at startup to all matching directories and picks up files in
// each of them.
func TestE2EStaticDirGlobAtStartup(t *testing.T) {
	base := t.TempDir()
	// Two sibling dirs matching the pattern.
	for _, sub := range []string{"acp-A", "acp-B"} {
		d := filepath.Join(base, sub, "log")
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "x.log"), []byte{}, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	pattern := filepath.Join(base, "acp-*", "log")
	s := startListener(t, "-d", pattern, "-r", `name:\.log$`, "--no-tui", "--no-color")
	time.Sleep(400 * time.Millisecond)

	// Append a line to each — both must be visible.
	for _, sub := range []string{"acp-A", "acp-B"} {
		f, err := os.OpenFile(filepath.Join(base, sub, "log", "x.log"), os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString("hi-from-" + sub + "\n")
		f.Close()
	}

	want := map[string]bool{"hi-from-acp-A": false, "hi-from-acp-B": false}
	deadline := time.Now().Add(5 * time.Second)
	for !allSeen(want) && time.Now().Before(deadline) {
		_, all, to := s.Await(500*time.Millisecond, func(line string) bool {
			for marker := range want {
				if !want[marker] && strings.Contains(line, marker) {
					want[marker] = true
					return true
				}
			}
			return false
		})
		_ = all
		_ = to
	}
	for marker, seen := range want {
		if !seen {
			t.Fatalf("never saw %q (pattern: %s)", marker, pattern)
		}
	}
}

func allSeen(m map[string]bool) bool {
	for _, v := range m {
		if !v {
			return false
		}
	}
	return true
}

// TestE2ENewDirMatchingPattern starts with one matching dir, then creates
// a second one at runtime. Files in the runtime-created dir must be
// tailed.
func TestE2ENewDirMatchingPattern(t *testing.T) {
	base := t.TempDir()
	// One existing matching dir.
	existing := filepath.Join(base, "acp-old", "log")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(existing, "x.log"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	pattern := filepath.Join(base, "acp-*", "log")
	s := startListener(t, "-d", pattern, "-r", `name:\.log$`, "--no-tui", "--no-color")
	time.Sleep(400 * time.Millisecond)

	// Create a NEW matching dir + file at runtime.
	fresh := filepath.Join(base, "acp-new", "log")
	if err := os.MkdirAll(fresh, 0o755); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond) // let the new-dir cascade settle
	if err := os.WriteFile(filepath.Join(fresh, "fresh.log"), []byte("hello-from-new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	matched, all, to := s.Await(5*time.Second, func(l string) bool {
		return strings.Contains(l, "hello-from-new")
	})
	if to {
		t.Fatalf("runtime-created dir's file never tailed; lines:\n  %s",
			strings.Join(all, "\n  "))
	}
	if !strings.Contains(matched, "fresh.log") {
		t.Fatalf("got %q", matched)
	}
}

// TestE2ENewDirWithDelayedSubdir covers the multi-hop case where the
// pattern has segments AFTER the wildcard: the matching parent appears
// first, then the sub-directory inside it, then the file.
func TestE2ENewDirWithDelayedSubdir(t *testing.T) {
	base := t.TempDir()
	pattern := filepath.Join(base, "run-*", "logs")
	s := startListener(t, "-d", pattern, "-r", `name:\.log$`, "--no-tui", "--no-color")
	time.Sleep(400 * time.Millisecond)

	// Step 1: the wildcard-matching parent appears.
	mid := filepath.Join(base, "run-42")
	if err := os.Mkdir(mid, 0o755); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)

	// Step 2: the suffix sub-dir appears.
	deep := filepath.Join(mid, "logs")
	if err := os.Mkdir(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)

	// Step 3: the file appears.
	if err := os.WriteFile(filepath.Join(deep, "app.log"), []byte("multi-hop-marker\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, all, to := s.Await(5*time.Second, func(l string) bool {
		return strings.Contains(l, "multi-hop-marker")
	})
	if to {
		t.Fatalf("multi-hop create chain not picked up; lines:\n  %s",
			strings.Join(all, "\n  "))
	}
}

// TestE2EFileGlobPicksUpNewDirs verifies the -f / files-group runtime
// glob behaviour: a brand-new directory containing a file that matches
// the file-group glob gets tailed.
func TestE2EFileGlobPicksUpNewDirs(t *testing.T) {
	base := t.TempDir()
	pattern := filepath.Join(base, "session-*", "out.log")
	s := startListener(t, "-f", pattern, "--no-tui", "--no-color")
	time.Sleep(400 * time.Millisecond)

	// Create new session dir + matching file.
	d := filepath.Join(base, "session-new")
	if err := os.Mkdir(d, 0o755); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(d, "out.log"), []byte("file-glob-marker\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, all, to := s.Await(5*time.Second, func(l string) bool {
		return strings.Contains(l, "file-glob-marker")
	})
	if to {
		t.Fatalf("file-group glob did not pick up new dir; lines:\n  %s",
			strings.Join(all, "\n  "))
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

// TestE2EConfigReloadSwapsRenderer verifies that rewriting the YAML config
// file causes the renderer pipeline to be swapped live (stdout mode).
//
// The debounce is 300ms and the watcher tick is 2s, so we use generous
// timeouts throughout. Line 2 is appended in a loop after the config rewrite
// because the rebuilt watcher starts at EOF — an append landing before the new
// tailer attaches would be silently missed; retrying until the deadline ensures
// at least one append lands after the swap.
func TestE2EConfigReloadSwapsRenderer(t *testing.T) {
	dir := t.TempDir()

	// Create the log file up front (empty, so the tailer attaches at EOF).
	logPath := filepath.Join(dir, "reload.log")
	if err := os.WriteFile(logPath, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	// Write the initial YAML config: renderer template "V1: $1".
	cfgPath := filepath.Join(dir, "test.yml")
	v1cfg := fmt.Sprintf(`files:
  - id: testfiles
    paths: [%q]
renderers:
  - name: v1renderer
    line_regex: "^(.*)$"
    template: "V1: $1"
`, logPath)
	if err := os.WriteFile(cfgPath, []byte(v1cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	s := startListener(t, "--config", cfgPath, "--no-tui", "--no-color")
	time.Sleep(400 * time.Millisecond) // let fsnotify register and tailer attach

	// Append line 1 and wait for V1 output to confirm the pipeline is running.
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("line-one\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if _, all, timedOut := s.Await(8*time.Second, func(line string) bool {
		return strings.Contains(line, "V1: line-one")
	}); timedOut {
		t.Fatalf("never saw V1 output for line-one; lines:\n  %s", strings.Join(all, "\n  "))
	}

	// Rewrite the config with a different renderer template "V2: $1".
	v2cfg := fmt.Sprintf(`files:
  - id: testfiles
    paths: [%q]
renderers:
  - name: v2renderer
    line_regex: "^(.*)$"
    template: "V2: $1"
`, logPath)
	if err := os.WriteFile(cfgPath, []byte(v2cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	// The rebuilt watcher starts tailing at EOF. Append line 2 repeatedly
	// until we see V2 output or the deadline passes. Early appends that land
	// before the new tailer attaches are silently skipped by design; this
	// loop ensures at least one lands after the reload completes.
	deadline := time.Now().Add(15 * time.Second)
	appendTicker := time.NewTicker(500 * time.Millisecond)
	defer appendTicker.Stop()

	// Start the append loop in the background. stopAppend is closed when we
	// no longer need more appends (after Await returns or the deadline passes).
	stopAppend := make(chan struct{})
	go func() {
		for {
			select {
			case <-appendTicker.C:
				if time.Now().After(deadline) {
					return
				}
				af, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644)
				if err != nil {
					return
				}
				af.WriteString("line-two\n")
				af.Close()
			case <-stopAppend:
				return
			}
		}
	}()

	_, all, timedOut := s.Await(15*time.Second, func(line string) bool {
		return strings.Contains(line, "V2: line-two")
	})
	// Stop the append loop before asserting.
	close(stopAppend)

	if timedOut {
		t.Fatalf("never saw V2 output after config reload; lines seen:\n  %s", strings.Join(all, "\n  "))
	}
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

// TestE2EMuteDropsLine verifies that a mute rule suppresses matching lines
// while passing through non-matching lines.
func TestE2EMuteDropsLine(t *testing.T) {
	dir := t.TempDir()

	// Write a log file with three lines: two to keep, one to mute.
	logPath := filepath.Join(dir, "app.log")
	if err := os.WriteFile(logPath, []byte("KEEP one\nGET /health 200\nKEEP two\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write a YAML config pointing at the log file with a mute rule.
	cfgPath := filepath.Join(dir, "test.yml")
	cfg := fmt.Sprintf(`files:
  - id: app
    paths: [%q]
mute:
  - id: h
    line_regex: 'GET /health'
`, logPath)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	// Run in one-shot mode so the process exits after draining the file.
	s := startListener(t, "--config", cfgPath, "--once", "--no-tui", "--no-color")

	// Collect all output until the stream closes (process exits).
	var allLines []string
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
collect:
	for {
		select {
		case line, ok := <-s.ch:
			if !ok {
				break collect
			}
			allLines = append(allLines, line)
		case <-timer.C:
			t.Fatal("timed out waiting for log-listener to exit")
		}
	}

	output := strings.Join(allLines, "\n")

	if strings.Contains(output, "/health") {
		t.Errorf("muted line leaked into output; got:\n  %s", strings.Join(allLines, "\n  "))
	}
	if !strings.Contains(output, "KEEP one") {
		t.Errorf("expected 'KEEP one' in output; got:\n  %s", strings.Join(allLines, "\n  "))
	}
	if !strings.Contains(output, "KEEP two") {
		t.Errorf("expected 'KEEP two' in output; got:\n  %s", strings.Join(allLines, "\n  "))
	}
}

// TestE2ENonJSONBracesRenderRaw verifies that a line whose braces contain
// non-JSON content (e.g. IntelliJ-style path macros) falls through the
// json()/xml() render function and is emitted intact on one line, while a
// valid trailing-JSON line on the same run still pretty-prints.
func TestE2ENonJSONBracesRenderRaw(t *testing.T) {
	dir := t.TempDir()

	// Write a log file with two lines: one with non-JSON braces, one with valid JSON.
	logPath := filepath.Join(dir, "app.log")
	// Use a raw string literal so backslashes are literal (C:\x\artifacts).
	logContent := "2026 INFO Saved path macros: {DB_ARTIFACTS_BUNDLE=C:\\x\\artifacts}\n" +
		`2026 INFO payload: {"a":1}` + "\n"
	if err := os.WriteFile(logPath, []byte(logContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write a YAML config with two renderers:
	//   1. json-line: matches a whole-line brace expression and json()-renders it.
	//   2. idea-trailing-json: matches text followed by a brace expression;
	//      the template uses literal \n (not a real newline) to split prefix and JSON block.
	// Both renderers' json() calls fall through to raw when the braces aren't valid JSON.
	cfgPath := filepath.Join(dir, "test.yml")
	cfg := fmt.Sprintf(`files:
  - id: app
    paths: [%q]
renderers:
  - name: json-line
    line_regex: '^\s*(\{.*\})\s*$'
    template: 'json($1)'
  - name: idea-trailing-json
    line_regex: '^(.*?\s)(\{.+\})\s*$'
    template: '$1\njson($2)'
`, logPath)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	// Run in one-shot mode so the process exits after draining the file.
	s := startListener(t, "--config", cfgPath, "--once", "--no-tui", "--no-color")

	// Collect all output until the stream closes (process exits).
	var allLines []string
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
collectNonJSON:
	for {
		select {
		case line, ok := <-s.ch:
			if !ok {
				break collectNonJSON
			}
			allLines = append(allLines, line)
		case <-timer.C:
			t.Fatal("timed out waiting for log-listener to exit")
		}
	}

	output := strings.Join(allLines, "\n")

	// The macro line must appear intact (non-JSON braces fell through to raw).
	if !strings.Contains(output, `Saved path macros: {DB_ARTIFACTS_BUNDLE=C:\x\artifacts}`) {
		t.Errorf("expected macro line intact in output; got:\n  %s", strings.Join(allLines, "\n  "))
	}
	// The macro braces must NOT have been pretty-printed as JSON.
	if strings.Contains(output, `DB_ARTIFACTS_BUNDLE":`) {
		t.Errorf("macro braces were incorrectly JSON-rendered; got:\n  %s", strings.Join(allLines, "\n  "))
	}
	// The valid JSON payload must have been pretty-printed (2-space indent).
	if !strings.Contains(output, `"a": 1`) {
		t.Errorf("expected pretty-printed JSON '\"a\": 1' in output; got:\n  %s", strings.Join(allLines, "\n  "))
	}
}

// TestE2EOnceMatchesRawContent runs --once over the real testdata/some.log
// with no renderers (pure passthrough) and asserts every emitted line, with
// the "[group] basename: " prefix stripped, reconstructs the raw file line for
// line — no dropping, splitting, reordering, or mangling.
func TestE2EOnceMatchesRawContent(t *testing.T) {
	logPath, err := filepath.Abs("testdata/some.log")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	// Split the way the tailer does (on \n); drop the trailing empty element
	// from a final newline. Normalize \r so the test is CRLF-agnostic.
	rawLines := strings.Split(string(raw), "\n")
	if n := len(rawLines); n > 0 && rawLines[n-1] == "" {
		rawLines = rawLines[:n-1]
	}
	for i := range rawLines {
		rawLines[i] = strings.TrimRight(rawLines[i], "\r")
	}

	s := startListener(t, "-f", logPath, "--once", "--no-tui", "--no-color")
	// Drain to EOF: --once exits, which closes the stream.
	_, all, _ := s.Await(20*time.Second, func(string) bool { return false })

	const mid = "] some.log: " // after the "[<group>" opening
	var bodies []string
	for _, line := range all {
		if !strings.HasPrefix(line, "[") {
			continue
		}
		i := strings.Index(line, mid)
		if i < 0 {
			continue
		}
		bodies = append(bodies, strings.TrimRight(line[i+len(mid):], "\r"))
	}

	if len(bodies) != len(rawLines) {
		t.Fatalf("line count mismatch: --once emitted %d, file has %d", len(bodies), len(rawLines))
	}
	for i := range rawLines {
		if bodies[i] != rawLines[i] {
			t.Fatalf("line %d differs:\n  raw: %q\n  out: %q", i, rawLines[i], bodies[i])
		}
	}
}
