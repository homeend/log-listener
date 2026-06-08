//go:build nomcp

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoMCPBuildRejectsMCPFlag(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "a.log")
	if err := os.WriteFile(logPath, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errBuf bytes.Buffer
	// startMCP runs before the watch loop, so the stub's hard error returns
	// code 1 immediately (no blocking tail). --mcp is skipped in --once mode,
	// so use --no-tui live mode.
	code := run([]string{"-f", logPath, "--mcp", "127.0.0.1:0", "--no-tui", "--no-color"}, &out, &errBuf)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (stderr: %q)", code, errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "built without MCP support") {
		t.Fatalf("stderr = %q, want mention of 'built without MCP support'", errBuf.String())
	}
}
