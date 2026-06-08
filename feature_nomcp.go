//go:build nomcp

package main

import (
	"errors"
	"io"

	"github.com/homeend/log-listener/internal/config"
	"github.com/homeend/log-listener/internal/linebuf"
)

// startMCP is the no-MCP stub. Requesting --mcp on a binary built without MCP
// support is a hard error; otherwise it is a no-op. Imports no mcp package.
func startMCP(cfg *config.Config, _ *linebuf.Buffer, _ io.Writer) (io.Closer, error) {
	if cfg.MCPAddr != "" {
		return nil, errors.New("--mcp: this binary was built without MCP support (use a full build)")
	}
	return nil, nil
}
