package diag

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixedClock() func() time.Time {
	return func() time.Time { return time.Date(2026, 6, 9, 20, 0, 0, 0, time.UTC) }
}

func TestRingDumpOldestFirst(t *testing.T) {
	l, _ := New(3, "")
	l.now = fixedClock()
	l.Logf("RELOAD", "n=1")
	l.Logf("RELOAD", "n=2")
	l.Logf("RELOAD", "n=3")
	l.Logf("RELOAD", "n=4") // evicts n=1
	got := l.Dump()
	if strings.Contains(got, "n=1") {
		t.Fatalf("oldest event should have been evicted from the ring:\n%s", got)
	}
	for _, want := range []string{"n=2", "n=3", "n=4"} {
		if !strings.Contains(got, want) {
			t.Fatalf("dump missing %q:\n%s", want, got)
		}
	}
	// oldest-first ordering
	if i2, i4 := strings.Index(got, "n=2"), strings.Index(got, "n=4"); i2 > i4 {
		t.Fatalf("dump should be oldest-first:\n%s", got)
	}
}

func TestFileMirror(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "debug.log")
	l, err := New(10, path)
	if err != nil {
		t.Fatal(err)
	}
	l.now = fixedClock()
	l.Logf("TAILER-OPEN", "path=/a.log pos=5000")
	l.Close()
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "2026-06-09T20:00:00Z TAILER-OPEN path=/a.log pos=5000") {
		t.Fatalf("file mirror missing the event:\n%s", data)
	}
}

func TestNilLoggerIsNoop(t *testing.T) {
	var l *Logger
	l.Logf("RELOAD", "no panic")
	if l.Dump() != "" {
		t.Fatal("nil Dump should be empty")
	}
	if err := l.Close(); err != nil {
		t.Fatalf("nil Close should be nil, got %v", err)
	}
}
