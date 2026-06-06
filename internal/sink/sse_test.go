package sink

import (
	"bufio"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/homeend/log-listener/internal/render"
)

func TestSSEHubStreamsEvents(t *testing.T) {
	hub := NewSSEHub("127.0.0.1:0")
	if err := hub.Start(); err != nil {
		t.Fatal(err)
	}
	defer hub.Close()

	url := "http://" + hub.Addr() + "/stream"
	// Open a streaming client.
	req, _ := http.NewRequest("GET", url, nil)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type=%q", ct)
	}

	// Give the server a tick to register the client.
	time.Sleep(50 * time.Millisecond)
	hub.Emit(render.Event{
		File: "/var/log/a.log", Group: "d1", Raw: "hello",
		Rendered: []render.Part{{Type: "text", Value: "hello"}},
	})

	// Read one SSE message.
	r := bufio.NewReader(resp.Body)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if strings.HasPrefix(line, "data: ") {
			payload := strings.TrimPrefix(strings.TrimSpace(line), "data: ")
			if !strings.Contains(payload, `"file":"/var/log/a.log"`) {
				t.Fatalf("payload missing file: %s", payload)
			}
			if !strings.Contains(payload, `"group":"d1"`) {
				t.Fatalf("payload missing group: %s", payload)
			}
			return
		}
	}
	t.Fatal("timed out waiting for SSE event")
}

func TestSSEHubIndexEndpoint(t *testing.T) {
	hub := NewSSEHub("127.0.0.1:0")
	if err := hub.Start(); err != nil {
		t.Fatal(err)
	}
	defer hub.Close()

	resp, err := http.Get("http://" + hub.Addr() + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("index status: %d", resp.StatusCode)
	}
}

func TestSSEHubEmitAfterCloseIsSafe(t *testing.T) {
	hub := NewSSEHub("127.0.0.1:0")
	if err := hub.Start(); err != nil {
		t.Fatal(err)
	}
	if err := hub.Close(); err != nil {
		t.Fatal(err)
	}
	// Should not panic, should not deadlock.
	hub.Emit(render.Event{Raw: "post-close"})
}

func TestSSEHubSlowClientDoesNotBlock(t *testing.T) {
	hub := NewSSEHub("127.0.0.1:0")
	if err := hub.Start(); err != nil {
		t.Fatal(err)
	}
	defer hub.Close()

	// Open a client but never read — simulating a slow consumer.
	req, _ := http.NewRequest("GET", "http://"+hub.Addr()+"/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	time.Sleep(50 * time.Millisecond)
	// Burst far more events than any reasonable client buffer.
	for i := 0; i < 1024; i++ {
		hub.Emit(render.Event{Raw: "burst"})
	}
	// If Emit was blocking, we'd never get here.
}
