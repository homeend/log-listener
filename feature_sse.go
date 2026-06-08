//go:build !nosse

package main

import (
	"fmt"

	"github.com/homeend/log-listener/internal/config"
	"github.com/homeend/log-listener/internal/sink"
)

// buildSSE starts the SSE broadcast hub if --sse / output.sse was configured,
// returning it as a sink.Sink for the Fanout (which owns Close). Returns a nil
// Sink when SSE wasn't requested. Replaces the wiring previously inline in run().
func buildSSE(cfg *config.Config) (sink.Sink, error) {
	if cfg.SSEAddr == "" {
		return nil, nil
	}
	hub := sink.NewSSEHub(cfg.SSEAddr)
	if err := hub.Start(); err != nil {
		return nil, fmt.Errorf("sse: %w", err)
	}
	return hub, nil
}
