//go:build !nosse

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestE2ESSEDeliversEvents — moved here so it is excluded from nosse builds,
// where the binary has no SSE server.
func TestE2ESSEDeliversEvents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sse.log")
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	// Pick an ephemeral port up front. There's a tiny race between
	// listening here and the binary starting; we close the listener and
	// pass the port string. In practice this rarely flakes.
	addr := pickFreeAddr(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := startListener(t,
		"-d", dir, "-r", `name:\.log$`,
		"--no-tui", "--no-color",
		"--sse", addr,
	)
	// Drain stdout in the background so the pipe never fills and blocks emit().
	go func() {
		for range s.ch {
		}
	}()

	// Wait for SSE server to come up.
	sseURL := "http://" + addr + "/stream"
	if err := waitForHTTP(sseURL, 3*time.Second); err != nil {
		t.Fatalf("SSE server didn't come up: %v", err)
	}

	// Subscribe.
	req, _ := http.NewRequestWithContext(ctx, "GET", sseURL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("sse status %d", resp.StatusCode)
	}

	time.Sleep(100 * time.Millisecond)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("sse-payload-marker\n")
	f.Close()

	// Read one SSE 'data:' line and verify the payload.
	r := bufio.NewReader(resp.Body)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("sse read: %v", err)
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(strings.TrimSpace(line), "data: ")
		var ev map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			t.Fatalf("invalid SSE JSON: %v\n%s", err, payload)
		}
		if !strings.Contains(payload, "sse-payload-marker") {
			t.Fatalf("payload missing marker: %s", payload)
		}
		return
	}
	t.Fatal("timed out waiting for SSE event with marker")
}
