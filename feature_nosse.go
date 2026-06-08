//go:build nosse

package main

import (
	"errors"

	"github.com/homeend/log-listener/internal/config"
	"github.com/homeend/log-listener/internal/sink"
)

// buildSSE is the no-SSE stub. Requesting SSE on a binary built without SSE
// support is a hard error; otherwise it is a no-op.
func buildSSE(cfg *config.Config) (sink.Sink, error) {
	if cfg.SSEAddr != "" {
		return nil, errors.New("--sse: this binary was built without SSE support (use a full build)")
	}
	return nil, nil
}
