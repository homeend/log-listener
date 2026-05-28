package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func drain(t *testing.T, w *Watcher, want int, timeout time.Duration) []Event {
	t.Helper()
	out := []Event{}
	deadline := time.After(timeout)
	for len(out) < want {
		select {
		case ev := <-w.Events():
			out = append(out, ev)
		case <-deadline:
			return out
		}
	}
	return out
}

func TestWatcherEmitsAppendedLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	w, err := New(nil, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.Add(path, "default", false); err != nil {
		t.Fatal(err)
	}

	// Give fsnotify a moment to settle.
	time.Sleep(50 * time.Millisecond)
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("hello\nworld\n")
	f.Close()

	got := drain(t, w, 2, 2*time.Second)
	if len(got) != 2 {
		t.Fatalf("want 2 events, got %d: %v", len(got), got)
	}
	if got[0].Line != "hello" || got[1].Line != "world" {
		t.Fatalf("unexpected events: %v", got)
	}
	if got[0].Group != "default" {
		t.Fatalf("group not propagated: %v", got[0])
	}
}

func TestWatcherPicksUpNewFiles(t *testing.T) {
	dir := t.TempDir()

	matcher := func(path string) (string, bool) {
		if filepath.Ext(path) == ".log" {
			return "g1", true
		}
		return "", false
	}
	w, err := New(matcher, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.WatchDir(dir); err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)
	path := filepath.Join(dir, "new.log")
	if err := os.WriteFile(path, []byte("line1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := drain(t, w, 1, 2*time.Second)
	if len(got) != 1 || got[0].Line != "line1" {
		t.Fatalf("want [line1], got %v", got)
	}
	if got[0].Group != "g1" {
		t.Fatalf("group=%q, want g1", got[0].Group)
	}
}

func TestWatcherPicksUpFileInNewSubdir(t *testing.T) {
	dir := t.TempDir()

	fileMatcher := func(path string) (string, bool) {
		if filepath.Ext(path) == ".log" {
			return "g1", true
		}
		return "", false
	}
	w, err := New(fileMatcher, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	// Watch ALL new dirs (mimicking a "watch everything under here" matcher).
	w.SetDirMatcher(func(_ string) bool { return true })
	if err := w.WatchDir(dir); err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)

	// Create a new SUBDIRECTORY, then a file inside it.
	sub := filepath.Join(dir, "newsub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond) // let watcher pick up the new subdir
	if err := os.WriteFile(filepath.Join(sub, "deep.log"), []byte("nested\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := drain(t, w, 1, 2*time.Second)
	if len(got) != 1 || got[0].Line != "nested" {
		t.Fatalf("want [nested], got %+v", got)
	}
	if got[0].Group != "g1" {
		t.Fatalf("group=%q want g1", got[0].Group)
	}
}

func TestWatcherIgnoresUnmatchedNewFiles(t *testing.T) {
	dir := t.TempDir()
	matcher := func(path string) (string, bool) {
		if filepath.Ext(path) == ".log" {
			return "g1", true
		}
		return "", false
	}
	w, _ := New(matcher, 100*time.Millisecond)
	defer w.Close()
	w.WatchDir(dir)
	time.Sleep(50 * time.Millisecond)

	os.WriteFile(filepath.Join(dir, "ignored.txt"), []byte("x\n"), 0o644)

	got := drain(t, w, 1, 500*time.Millisecond)
	if len(got) != 0 {
		t.Fatalf("unmatched file produced events: %v", got)
	}
}
