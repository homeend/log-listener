// Package mcp embeds a Model Context Protocol server in the live process,
// served over Streamable HTTP on a loopback address, exposing read-only tools
// over the shared internal/linebuf buffer. Local dev aid only — no auth.
package mcp

import (
	"context"
	"net"
	"net/http"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/homeend/log-listener/internal/linebuf"
)

// Server wraps an MCP SDK server mounted on an http.Server. Lifecycle mirrors
// sink.SSEHub: New, Start (non-blocking), Addr, Close.
type Server struct {
	addr string
	buf  *linebuf.Buffer
	srv  *http.Server
	lis  net.Listener
}

// New builds a server bound to addr (not yet listening) reading from buf.
func New(addr string, buf *linebuf.Buffer) *Server {
	return &Server{addr: addr, buf: buf}
}

// newSDKServer constructs the MCP server and registers every tool.
func (s *Server) newSDKServer() *mcpsdk.Server {
	srv := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name: "log-listener", Version: "v1",
	}, nil)
	s.registerTools(srv) // defined in tools.go
	return srv
}

// Start opens the listener and serves in a background goroutine.
func (s *Server) Start() error {
	lis, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	sdk := s.newSDKServer()
	handler := mcpsdk.NewStreamableHTTPHandler(
		func(*http.Request) *mcpsdk.Server { return sdk }, nil)
	s.lis = lis
	s.srv = &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = s.srv.Serve(lis) }()
	return nil
}

// Addr returns the actual listening address (useful when addr was ":0").
func (s *Server) Addr() string {
	if s.lis != nil {
		return s.lis.Addr().String()
	}
	return s.addr
}

// Close shuts the HTTP server down.
func (s *Server) Close() error {
	if s.srv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return s.srv.Shutdown(ctx)
}
