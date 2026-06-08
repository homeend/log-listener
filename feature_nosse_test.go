//go:build nosse

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoSSEBuildRejectsSSEFlag(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "a.log")
	if err := os.WriteFile(logPath, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errBuf bytes.Buffer
	code := run([]string{"-f", logPath, "--sse", "127.0.0.1:0", "--once", "--no-tui", "--no-color"}, &out, &errBuf)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (stderr: %q)", code, errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "built without SSE support") {
		t.Fatalf("stderr = %q, want mention of 'built without SSE support'", errBuf.String())
	}
}

func TestNoSSEBuildRejectsYAMLSSE(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "a.log")
	if err := os.WriteFile(logPath, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "ll.yml")
	if err := os.WriteFile(cfgPath, []byte("output:\n  sse:\n    enabled: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errBuf bytes.Buffer
	code := run([]string{"--config", cfgPath, "-f", logPath, "--once", "--no-tui", "--no-color"}, &out, &errBuf)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (stderr: %q)", code, errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "built without SSE support") {
		t.Fatalf("stderr = %q, want mention of 'built without SSE support'", errBuf.String())
	}
}
