package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

// TestE2ETUIShowsLiveEvents runs the binary in TUI mode under a pseudo-tty
// (so the TTY check passes and bubbletea takes the alt screen), writes a
// distinctive marker line into a watched file, then scans the pty output
// for that marker. This is the path the user reported broken.
func TestE2ETUIShowsLiveEvents(t *testing.T) {
	bin := e2eBinary(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "tui-live.log")
	if err := os.WriteFile(path, []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "-f", path, "--no-color")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty.Start: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = ptmx.Close()
		_ = cmd.Wait()
	})

	// Set a reasonable size so the TUI's WindowSizeMsg gives non-zero
	// height (View() returns "" when m.height == 0).
	if err := pty.Setsize(ptmx, &pty.Winsize{Rows: 30, Cols: 120}); err != nil {
		t.Logf("Setsize: %v (proceeding)", err)
	}

	var (
		out  bytes.Buffer
		outM sync.Mutex
	)
	go func() {
		buf := make([]byte, 8192)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				outM.Lock()
				out.Write(buf[:n])
				outM.Unlock()
				// Respond to terminal queries bubbletea/termenv send during
				// init. Real terminals auto-answer; pseudo-terminals don't,
				// so the subprocess would hang waiting.
				chunk := buf[:n]
				if bytes.Contains(chunk, []byte("\x1b]11;?")) {
					// OSC 11 background-color query → fake dark bg
					_, _ = ptmx.Write([]byte("\x1b]11;rgb:0000/0000/0000\x1b\\"))
				}
				if bytes.Contains(chunk, []byte("\x1b[6n")) {
					// CSI 6n cursor-position query → row=1 col=1
					_, _ = ptmx.Write([]byte("\x1b[1;1R"))
				}
			}
			if err != nil {
				return
			}
		}
	}()

	time.Sleep(800 * time.Millisecond) // let TUI initialize past query phase

	marker := "TUI-LIVE-MARKER-7351"
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(marker + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		outM.Lock()
		dump := out.String()
		outM.Unlock()
		if strings.Contains(dump, marker) {
			return // pass
		}
		time.Sleep(100 * time.Millisecond)
	}

	outM.Lock()
	final := out.String()
	outM.Unlock()
	t.Fatalf("TUI never rendered %q in 5s.\n--- raw pty output (last 800 bytes) ---\n%s",
		marker, tail(final, 800))
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}

// drainBg starts a goroutine that copies r to a sink. Used when tests don't
// care about the source but need it drained.
func drainBg(r io.Reader) { go func() { _, _ = io.Copy(io.Discard, r) }() }
