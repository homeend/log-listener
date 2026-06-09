package watch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/homeend/log-listener/internal/diag"
)

// Add must record a TAILER-OPEN trace line carrying the tailer's start offset
// and inode — the forensic data needed to diagnose reload-time re-reads.
func TestAddLogsTailerOpenOffset(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "app.log")
	if err := os.WriteFile(logPath, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dbgPath := filepath.Join(dir, "debug.log")
	dl, err := diag.New(0, dbgPath)
	if err != nil {
		t.Fatal(err)
	}

	w, err := New(nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	w.SetDiag(dl)
	// fromStart=false → tailer starts at EOF (the 14-byte file size).
	if err := w.Add(logPath, "g", false); err != nil {
		t.Fatal(err)
	}
	w.Close()
	dl.Close()

	data, _ := os.ReadFile(dbgPath)
	got := string(data)
	if !strings.Contains(got, "TAILER-OPEN") {
		t.Fatalf("no TAILER-OPEN line:\n%s", got)
	}
	if !strings.Contains(got, "pos=14") {
		t.Fatalf("TAILER-OPEN should record EOF offset pos=14:\n%s", got)
	}
	if !strings.Contains(got, "fromStart=false") {
		t.Fatalf("TAILER-OPEN should record fromStart:\n%s", got)
	}
}
