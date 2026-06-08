//go:build !nomcp

package main

import (
	"os"
	"strings"
	"testing"
)

// --mcp parses and the headless path runs clean alongside preload. In --once
// the process exits before serving; a live tool round-trip is covered by
// internal/mcp unit tests.
func TestE2EMCPBootsHeadless(t *testing.T) {
	dir := t.TempDir()
	raw := dir + "/sample.log"
	if err := os.WriteFile(raw, []byte("hello one\nhello two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Write a minimal config so the ambient ./log-listener.yml is not loaded,
	// keeping the test hermetic (same isolation as TestBadKeybindingExitsNonZero).
	cfgPath := dir + "/log-listener.yml"
	if err := os.WriteFile(cfgPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errBuf strings.Builder
	code := run([]string{"--no-tui", "--once", "--mcp", "127.0.0.1:0", "--preload", raw, "--config", cfgPath}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, errBuf.String())
	}
	if !strings.Contains(out.String(), "hello one") {
		t.Errorf("preloaded content missing: %s", out.String())
	}
}
