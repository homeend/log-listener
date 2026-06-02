package configwatch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// waitSignal blocks for up to d for one signal on ch, returning true if one
// arrived.
func waitSignal(ch <-chan struct{}, d time.Duration) bool {
	select {
	case <-ch:
		return true
	case <-time.After(d):
		return false
	}
}

func TestNotifiesOnChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yml")
	if err := os.WriteFile(path, []byte("a: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := New(path, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if err := os.WriteFile(path, []byte("a: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !waitSignal(w.Changes(), 2*time.Second) {
		t.Fatal("expected a change signal after writing the file")
	}
}

func TestCoalescesBurst(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yml")
	if err := os.WriteFile(path, []byte("a: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := New(path, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	for i := 0; i < 5; i++ {
		if err := os.WriteFile(path, []byte("a: x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !waitSignal(w.Changes(), 2*time.Second) {
		t.Fatal("expected one coalesced signal")
	}
	// The burst must not produce a second prompt signal right after.
	if waitSignal(w.Changes(), 300*time.Millisecond) {
		t.Fatal("burst should coalesce into a single signal")
	}
}

func TestIgnoresSiblingFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yml")
	if err := os.WriteFile(path, []byte("a: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := New(path, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// Writing a different file in the same directory must not signal.
	if err := os.WriteFile(filepath.Join(dir, "other.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if waitSignal(w.Changes(), 400*time.Millisecond) {
		t.Fatal("sibling file change must not signal")
	}
}
