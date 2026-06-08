//go:build !windows && !nomcp

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/creack/pty"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/homeend/log-listener/internal/mcp"
)

func TestE2EMCPViewportUnderPTY(t *testing.T) {
	bin := e2eBinary(t)
	fixture := filepath.Join(t.TempDir(), "vp.log")
	if err := os.WriteFile(fixture, []byte(mcpFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	addr := pickFreeAddr(t)

	cmd := exec.Command(bin, "--preload", fixture, "--no-color", "--mcp", addr)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty.Start: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = ptmx.Close()
		_ = cmd.Wait()
	})
	if err := pty.Setsize(ptmx, &pty.Winsize{Rows: 40, Cols: 120}); err != nil {
		t.Logf("Setsize: %v (proceeding)", err)
	}
	// Drain pty output so the program isn't blocked writing the alt screen.
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := ptmx.Read(buf); err != nil {
				return
			}
		}
	}()

	sess := mcpDial(t, addr)
	defer sess.Close()

	deadline := time.Now().Add(6 * time.Second)
	for {
		res, err := sess.CallTool(context.Background(),
			&mcpsdk.CallToolParams{Name: "get_viewport", Arguments: map[string]any{}})
		if err == nil && res != nil && !res.IsError {
			var vp mcp.ViewportOutput
			decodeResult(t, res, &vp)
			if vp.From == "L0" && vp.To == "L5" && len(vp.Entries) == 6 {
				if vp.Entries[2].Lines[0] != "panic: boom" {
					t.Fatalf("viewport entry index 2 = %q, want panic: boom", vp.Entries[2].Lines[0])
				}
				return // success
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("get_viewport never returned the full file (last err=%v)", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
