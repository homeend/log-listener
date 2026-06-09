package diag

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoggerWritesGreppableLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "debug.log")
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	l.now = func() time.Time { return time.Date(2026, 6, 9, 20, 0, 0, 0, time.UTC) }
	l.Logf("RELOAD", "rebuilt=%v files=%d", false, 3)
	l.Logf("TAILER-OPEN", "path=%s pos=%d", "/a.log", 5000)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{
		"2026-06-09T20:00:00Z RELOAD rebuilt=false files=3",
		"2026-06-09T20:00:00Z TAILER-OPEN path=/a.log pos=5000",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("log missing %q; got:\n%s", want, got)
		}
	}
}

func TestNilLoggerIsNoop(t *testing.T) {
	var l *Logger // nil
	l.Logf("RELOAD", "should not panic")
	if err := l.Close(); err != nil {
		t.Fatalf("nil Close should be nil, got %v", err)
	}
}
