package watch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// skipToEOF semantics are deterministic at the tailer level (no live loop):
// it skips exactly the unread bytes, lands pos at EOF, and a later Tick yields
// only post-skip content.
func TestTailerSkipToEOF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	if err := os.WriteFile(path, []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tl, err := NewTailer(path, true) // fromStart: pos=0, nothing read yet
	if err != nil {
		t.Fatal(err)
	}
	defer tl.Close()

	appendLine(t, path, "d\ne\n")
	full := int64(len("a\nb\nc\nd\ne\n")) // 10

	skipped, err := tl.skipToEOF()
	if err != nil {
		t.Fatal(err)
	}
	if skipped != full {
		t.Fatalf("skipped: want %d, got %d", full, skipped)
	}
	if tl.Pos() != full {
		t.Fatalf("pos after skip: want %d, got %d", full, tl.Pos())
	}

	appendLine(t, path, "f\n")
	lines, _, err := tl.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0] != "f" {
		t.Fatalf("post-skip tail: want [f], got %v", lines)
	}
}

func TestTailerSkipToEOFAlreadyAtEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	if err := os.WriteFile(path, []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tl, err := NewTailer(path, false) // fromStart=false: pos already at EOF
	if err != nil {
		t.Fatal(err)
	}
	defer tl.Close()
	skipped, err := tl.skipToEOF()
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 0 {
		t.Fatalf("nothing to skip: want 0, got %d", skipped)
	}
}

// Lag exposes per-file structure and the event-channel capacity; at EOF the
// per-file lag is zero.
func TestWatcherLagReportsFilesAndPendingCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	if err := os.WriteFile(path, []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := New(nil, 0) // no matcher, polling off
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.Add(path, "g", false); err != nil {
		t.Fatal(err)
	}

	st := w.Lag()
	if st.PendingCap != 1024 {
		t.Fatalf("PendingCap: want 1024, got %d", st.PendingCap)
	}
	if len(st.Files) != 1 {
		t.Fatalf("Files: want 1, got %d", len(st.Files))
	}
	abs, _ := filepath.Abs(path)
	if st.Files[0].Path != abs {
		t.Fatalf("path: want %s, got %s", abs, st.Files[0].Path)
	}
	if st.Files[0].Lag != 0 {
		t.Fatalf("lag at EOF: want 0, got %d", st.Files[0].Lag)
	}
}

// SkipToEOF lands every tailer at its file end regardless of how much the live
// loop had already read; afterward total lag is zero.
func TestWatcherSkipToEOFReachesEOF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	if err := os.WriteFile(path, []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := New(nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.Add(path, "g", false); err != nil {
		t.Fatal(err)
	}

	// A backlog written without draining w.Events(); the loop may or may not
	// have read it by skip time — the post-skip invariant holds either way.
	appendLine(t, path, strings.Repeat("backlog line\n", 50))
	_ = w.SkipToEOF() // exact skipped count is timing-dependent, not asserted
	if lag := w.Lag().TotalBytes; lag != 0 {
		t.Fatalf("after SkipToEOF total lag: want 0, got %d", lag)
	}
}

// Race coverage: Lag()/SkipToEOF() from the test goroutine while the watcher
// loop ticks and reads under concurrent appends.
func TestWatcherLagConcurrentWithTicks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	if err := os.WriteFile(path, []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := New(nil, 2*time.Millisecond) // polling drives Tick
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.Add(path, "g", false); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() { // keep the events channel drained so the loop keeps reading
		for {
			select {
			case <-w.Events():
			case <-done:
				return
			}
		}
	}()
	go func() {
		for i := 0; i < 300; i++ {
			appendLine(t, path, "x\n")
		}
		close(done)
	}()

	for {
		select {
		case <-done:
			return
		default:
			_ = w.Lag()
			_ = w.SkipToEOF()
		}
	}
}
