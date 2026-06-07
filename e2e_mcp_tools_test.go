package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/homeend/log-listener/internal/mcp"
)

const mcpFixture = `2026-06-07 10:00:00 INFO start
2026-06-07 10:00:01 INFO user=alice action=login
panic: boom
goroutine 1 [running]:
	main.crash()
2026-06-07 10:00:02 INFO done
`

func writeMCPFixture(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fixture.log")
	if err := os.WriteFile(p, []byte(mcpFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// mcpDial connects an MCP client to addr, retrying until the server is up.
func mcpDial(t *testing.T, addr string) *mcpsdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(8 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		c := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "e2e", Version: "v1"}, nil)
		sess, err := c.Connect(ctx, &mcpsdk.StreamableClientTransport{Endpoint: "http://" + addr}, nil)
		if err == nil {
			return sess
		}
		last = err
		time.Sleep(75 * time.Millisecond)
	}
	t.Fatalf("MCP connect %s: %v", addr, last)
	return nil
}

func mcpCall(t *testing.T, sess *mcpsdk.ClientSession, name string, args any) *mcpsdk.CallToolResult {
	t.Helper()
	res, err := sess.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	return res
}

// decodeResult unmarshals a tool's structured output into out, handling either
// StructuredContent or JSON text in Content.
func decodeResult(t *testing.T, res *mcpsdk.CallToolResult, out any) {
	t.Helper()
	var raw []byte
	if res.StructuredContent != nil {
		b, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatalf("marshal structured content: %v", err)
		}
		raw = b
	} else {
		for _, c := range res.Content {
			if tc, ok := c.(*mcpsdk.TextContent); ok {
				raw = []byte(tc.Text)
				break
			}
		}
	}
	if raw == nil {
		t.Fatalf("no decodable content in result: %+v", res)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("decode %T: %v (raw %s)", out, err, raw)
	}
}

func TestE2EMCPToolsAgainstPreload(t *testing.T) {
	fixture := writeMCPFixture(t)
	addr := pickFreeAddr(t)
	s := startListener(t, "--no-tui", "--no-color", "--mcp", addr, "--preload", fixture)
	go func() {
		for range s.ch {
		}
	}()

	sess := mcpDial(t, addr)
	defer sess.Close()

	var sr mcp.SearchOutput
	decodeResult(t, mcpCall(t, sess, "search", map[string]any{"query": "alice"}), &sr)
	if len(sr.Hits) != 1 || sr.Hits[0].ID != "L1" {
		t.Fatalf("search alice: %+v", sr)
	}

	var ex mcp.ExceptionsOutput
	decodeResult(t, mcpCall(t, sess, "list_exceptions", map[string]any{}), &ex)
	if len(ex.Exceptions) != 1 || ex.Exceptions[0].From != "L2" ||
		ex.Exceptions[0].To != "L4" || ex.Exceptions[0].Language != "go" {
		t.Fatalf("list_exceptions: %+v", ex)
	}

	var er mcp.EntriesOutput
	decodeResult(t, mcpCall(t, sess, "get_range", map[string]any{"from": "L2", "to": "L4"}), &er)
	if len(er.Entries) != 3 || er.Entries[0].Lines[0] != "panic: boom" {
		t.Fatalf("get_range: %+v", er)
	}

	var e0 mcp.EntryDTO
	decodeResult(t, mcpCall(t, sess, "get_line", map[string]any{"id": "L0"}), &e0)
	if e0.ID != "L0" || len(e0.Lines) == 0 || !contains(e0.Lines[0], "start") {
		t.Fatalf("get_line L0: %+v", e0)
	}

	res, err := sess.CallTool(context.Background(),
		&mcpsdk.CallToolParams{Name: "get_viewport", Arguments: map[string]any{}})
	if err == nil && (res == nil || !res.IsError) {
		t.Fatalf("get_viewport must error headlessly; err=%v res=%+v", err, res)
	}
}
