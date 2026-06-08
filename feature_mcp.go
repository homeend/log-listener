//go:build !nomcp

package main

import (
	"fmt"
	"io"

	"github.com/homeend/log-listener/internal/config"
	"github.com/homeend/log-listener/internal/linebuf"
	"github.com/homeend/log-listener/internal/mcp"
)

// startMCP builds and starts the embedded MCP server if --mcp was given.
// Returns the server (to defer Close) or a nil io.Closer when MCP wasn't
// requested. This is the ONLY file importing internal/mcp, so a nomcp build
// excludes it and the go-sdk dependency entirely.
func startMCP(cfg *config.Config, buf *linebuf.Buffer, stderr io.Writer) (io.Closer, error) {
	if cfg.MCPAddr == "" {
		return nil, nil
	}
	srv := mcp.New(cfg.MCPAddr, buf)
	if err := srv.Start(); err != nil {
		return nil, fmt.Errorf("mcp: %w", err)
	}
	fmt.Fprintf(stderr, "log-listener: mcp on http://%s\n", srv.Addr())
	return srv, nil
}
