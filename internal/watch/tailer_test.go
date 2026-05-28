package watch

import (
	"os"
	"path/filepath"
	"testing"
)

func appendLine(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
}

func TestTailerEmitsCompleteLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	if err := os.WriteFile(path, []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tl, err := NewTailer(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer tl.Close()

	lines, _, err := tl.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0] != "first" {
		t.Fatalf("want [first], got %v", lines)
	}

	appendLine(t, path, "second\nthird\n")
	lines, _, err = tl.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || lines[0] != "second" || lines[1] != "third" {
		t.Fatalf("want [second third], got %v", lines)
	}
}

func TestTailerBuffersPartialLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	if err := os.WriteFile(path, []byte("part"), 0o644); err != nil {
		t.Fatal(err)
	}

	tl, err := NewTailer(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer tl.Close()

	lines, _, err := tl.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 0 {
		t.Fatalf("partial line must not be emitted yet, got %v", lines)
	}

	appendLine(t, path, "ial\n")
	lines, _, err = tl.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0] != "partial" {
		t.Fatalf("want [partial], got %v", lines)
	}
}

func TestTailerStripsCRLF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	if err := os.WriteFile(path, []byte("dos\r\nunix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tl, _ := NewTailer(path, true)
	defer tl.Close()
	lines, _, _ := tl.Tick()
	if len(lines) != 2 || lines[0] != "dos" || lines[1] != "unix" {
		t.Fatalf("got %v", lines)
	}
}

func TestTailerRotateByRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tl, _ := NewTailer(path, true)
	defer tl.Close()
	lines, _, _ := tl.Tick()
	if len(lines) != 1 {
		t.Fatalf("initial read got %v", lines)
	}

	// Simulate rotation: rename old, create new with same name.
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	lines, rotated, err := tl.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if !rotated {
		t.Fatal("must detect rotation")
	}
	if len(lines) != 2 || lines[0] != "alpha" || lines[1] != "beta" {
		t.Fatalf("after rotation want [alpha beta], got %v", lines)
	}
}

func TestTailerTruncate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tl, _ := NewTailer(path, true)
	defer tl.Close()
	tl.Tick() // consume initial

	// Truncate via OpenFile O_TRUNC then write fresh.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("fresh\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	lines, rotated, err := tl.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if !rotated {
		t.Fatal("truncation must be reported as rotated=true")
	}
	if len(lines) != 1 || lines[0] != "fresh" {
		t.Fatalf("want [fresh], got %v", lines)
	}
}
