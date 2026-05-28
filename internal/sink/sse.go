package sink

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"log-listener/internal/render"
)

// SSEHub is an HTTP/SSE broadcast hub. Each client connection opens a
// dedicated buffered channel; if a client falls behind, its channel fills
// and subsequent events are dropped for that client (other clients are
// unaffected). The hub never blocks the Emit() caller.
type SSEHub struct {
	addr string
	srv  *http.Server
	lis  net.Listener

	mu      sync.Mutex
	clients map[chan []byte]struct{}
	closed  bool
}

// NewSSEHub creates a hub bound to addr. The HTTP server is started by Start.
func NewSSEHub(addr string) *SSEHub {
	return &SSEHub{
		addr:    addr,
		clients: map[chan []byte]struct{}{},
	}
}

// Addr returns the actual listening address (useful when addr was ":0").
func (h *SSEHub) Addr() string {
	if h.lis != nil {
		return h.lis.Addr().String()
	}
	return h.addr
}

// Start opens the listener and begins serving. Returns immediately; the
// server runs in a background goroutine.
func (h *SSEHub) Start() error {
	lis, err := net.Listen("tcp", h.addr)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/stream", h.handleStream)
	mux.HandleFunc("/", h.handleIndex)
	h.lis = lis
	h.srv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		_ = h.srv.Serve(lis) // ignore http.ErrServerClosed on shutdown
	}()
	return nil
}

// Close shuts the server down, closes all client channels, and stops Emit
// from blocking on the broadcast lock.
func (h *SSEHub) Close() error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	for ch := range h.clients {
		close(ch)
		delete(h.clients, ch)
	}
	h.mu.Unlock()
	if h.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return h.srv.Shutdown(ctx)
	}
	return nil
}

// Emit broadcasts a JSON-serialized event to all connected clients. Slow
// clients see drops (their buffered channel is full), never block the hub.
func (h *SSEHub) Emit(ev render.Event) {
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	for ch := range h.clients {
		select {
		case ch <- data:
		default:
			// Client is full — drop. They'll catch up on the next event
			// they can consume.
		}
	}
}

func (h *SSEHub) register() chan []byte {
	ch := make(chan []byte, 256)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		close(ch)
		return ch
	}
	h.clients[ch] = struct{}{}
	return ch
}

func (h *SSEHub) unregister(ch chan []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[ch]; ok {
		delete(h.clients, ch)
		close(ch)
	}
}

func (h *SSEHub) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch := h.register()
	defer h.unregister(ch)

	ctx := r.Context()
	for {
		select {
		case data, ok := <-ch:
			if !ok {
				return
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
			flusher.Flush()
		case <-ctx.Done():
			return
		case <-time.After(15 * time.Second):
			// keepalive comment to defeat intermediary timeouts
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (h *SSEHub) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprintln(w, "log-listener SSE server. Stream is at /stream.")
}

// ErrHubClosed is returned by hub operations after Close.
var ErrHubClosed = errors.New("sink: hub closed")
