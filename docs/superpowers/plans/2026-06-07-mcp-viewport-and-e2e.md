# MCP `get_viewport` + End-to-End Verification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `get_viewport` MCP tool returning the TUI's exact on-screen entry range (erroring headlessly), and end-to-end tests that drive the real server with the MCP client to verify viewport, search, exceptions, and range resolution against a preloaded fixture.

**Architecture:** The TUI model publishes its on-screen first/last visible entry ids to a thread-safe slot on the shared `linebuf.Buffer` (wired only in TUI mode); the `get_viewport` tool reads that slot. E2E tests use the official MCP SDK client against a subprocess-launched server (headless and PTY).

**Tech Stack:** Go 1.26, `github.com/modelcontextprotocol/go-sdk/mcp` (server + client), bubbletea, `github.com/creack/pty`.

**Spec:** `docs/superpowers/specs/2026-06-07-mcp-viewport-and-e2e-design.md`

---

## File Structure

- `internal/linebuf/linebuf.go` (modify) — viewport slot (`SetViewport`/`Viewport` + fields, own mutex).
- `internal/tui/app.go` (modify) — `Options.SetViewport`; `m.setViewport`; `publishViewport`; call in `renderStream`.
- `main.go` (modify) — wire `SetViewport: buf.SetViewport` in `runWatchTUI`.
- `internal/mcp/tools.go` (modify) — `ViewportOutput`, `getViewport`, registration.
- `internal/mcp/tools_test.go` (modify) — unit tests.
- `internal/linebuf/linebuf_test.go` (modify) — viewport round-trip unit test.
- `internal/tui/viewport_test.go` (new) — `publishViewport` test.
- `e2e_mcp_tools_test.go` (new) — headless e2e + `mcpDial`/`mcpCall`/`decodeResult` helpers + fixture helper.
- `e2e_mcp_tui_test.go` (new, `//go:build !windows`) — PTY viewport e2e.
- `README.md`, `CHANGELOG.md`.

---

### Task 1: `linebuf` viewport slot

**Files:**
- Modify: `internal/linebuf/linebuf.go`
- Modify: `internal/linebuf/linebuf_test.go`

- [ ] **Step 1: Write the failing test** — append to `internal/linebuf/linebuf_test.go`:
```go
func TestViewportSlotRoundTrip(t *testing.T) {
	b := New(100, decomp)
	if _, _, attached := b.Viewport(); attached {
		t.Error("fresh buffer must report not-attached")
	}
	b.SetViewport("L0", "L5")
	from, to, attached := b.Viewport()
	if !attached || from != "L0" || to != "L5" {
		t.Fatalf("viewport = %q..%q attached=%v", from, to, attached)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/linebuf/ -run TestViewportSlot` → expect FAIL (`b.Viewport undefined`).

- [ ] **Step 3: Implement** — add to `internal/linebuf/linebuf.go`. Add these fields to the `Buffer` struct (after the existing fields):
```go
	vpMu       sync.RWMutex
	vpFrom     string
	vpTo       string
	vpAttached bool
```
And append these methods:
```go
// SetViewport records the TUI's current on-screen entry range (first..last
// visible entry id) and marks a TUI as attached. Published by the model on each
// render in TUI mode; never called headlessly. Uses its own lock so a
// render-time publish never contends with a tool read of the ring.
func (b *Buffer) SetViewport(from, to string) {
	b.vpMu.Lock()
	defer b.vpMu.Unlock()
	b.vpFrom, b.vpTo, b.vpAttached = from, to, true
}

// Viewport returns the last-published on-screen range. attached is false until
// a TUI has published at least once (headless runs report not-attached).
func (b *Buffer) Viewport() (from, to string, attached bool) {
	b.vpMu.RLock()
	defer b.vpMu.RUnlock()
	return b.vpFrom, b.vpTo, b.vpAttached
}
```
(`sync` is already imported in `linebuf.go`.)

- [ ] **Step 4: Run** `go test ./internal/linebuf/ && go vet ./internal/linebuf/ && go test -race ./internal/linebuf/` → PASS.

- [ ] **Step 5: Commit**
```bash
git add internal/linebuf/
git commit -m "feat(linebuf): thread-safe viewport slot (SetViewport/Viewport)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 2: Model publishes the on-screen range

**Files:**
- Modify: `internal/tui/app.go` (`Options`; `New`; model field; `renderStream`)
- Create: `internal/tui/viewport_test.go`
- Modify: `main.go` (`runWatchTUI` wiring)

- [ ] **Step 1: Write the failing test** — create `internal/tui/viewport_test.go`:
```go
package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/render"
)

func TestPublishViewportReportsVisibleRange(t *testing.T) {
	var gotFrom, gotTo string
	called := false
	m := newModel(100)
	m.setViewport = func(from, to string) { gotFrom, gotTo, called = from, to, true }
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	for i, v := range []string{"a", "b", "c"} {
		m.appendEvent(render.Event{ID: "L" + itoa36(i), Group: "g", File: "/a.log",
			Rendered: []render.Part{{Type: "text", Value: v}}})
	}
	_ = m.renderStream(m.contentHeight())
	if !called {
		t.Fatal("renderStream should publish the viewport")
	}
	if gotFrom != "L0" || gotTo != "L2" {
		t.Fatalf("viewport published %q..%q, want L0..L2", gotFrom, gotTo)
	}
}

func TestPublishViewportNoopWhenNilCallback(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	m.appendEvent(render.Event{ID: "L0", Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "x"}}})
	// setViewport is nil — must not panic.
	_ = m.renderStream(m.contentHeight())
}
```

- [ ] **Step 2: Run** `go test ./internal/tui/ -run TestPublishViewport` → expect FAIL (`m.setViewport undefined`).

- [ ] **Step 3: Implement.** In `internal/tui/app.go`:
  (a) Add to `Options` (next to `RenderFn`):
```go
	SetViewport func(from, to string) // publishes the on-screen entry range (TUI mode only)
```
  (b) Add a model field (near `renderFn`):
```go
	setViewport func(from, to string)
```
  (c) In `New`, store it (next to `m.renderFn = opts.RenderFn`):
```go
	m.setViewport = opts.SetViewport
```
  (d) Add the method:
```go
// publishViewport reports the on-screen entry range (first..last visible entry
// id) to the shared buffer, if a publisher is wired. No-op when the callback is
// nil (tests) or nothing is visible (publishes empty).
func (m *model) publishViewport(visible []int) {
	if m.setViewport == nil {
		return
	}
	if len(visible) == 0 {
		m.setViewport("", "")
		return
	}
	from := m.entryIDForLine(visible[0])
	to := m.entryIDForLine(visible[len(visible)-1])
	m.setViewport(from, to)
}
```
  (e) In `renderStream`, call it right after `visible := m.collectVisible(rows)`:
```go
	visible := m.collectVisible(rows)
	m.publishViewport(visible)
```

- [ ] **Step 4: Run** `go test ./internal/tui/ -run TestPublishViewport` → PASS. Then `go test ./internal/tui/` → PASS (no regression).

- [ ] **Step 5: Wire `main.go`.** In `runWatchTUI`, in the `tui.New(tui.Options{…})` literal, add:
```go
		SetViewport:   buf.SetViewport,
```
(alongside `RenderFn`/`InitialEvents`; `buf` is already a parameter of `runWatchTUI`).

- [ ] **Step 6: Run** `go build ./... && go test ./... && go vet ./...` → PASS.

- [ ] **Step 7: Commit**
```bash
git add internal/tui/app.go internal/tui/viewport_test.go main.go
git commit -m "feat(tui): publish on-screen entry range to the buffer

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 3: `get_viewport` MCP tool

**Files:**
- Modify: `internal/mcp/tools.go`
- Modify: `internal/mcp/tools_test.go`

- [ ] **Step 1: Write the failing test** — append to `internal/mcp/tools_test.go`:
```go
func TestGetViewportAttached(t *testing.T) {
	s := New("127.0.0.1:0", newTestBuf())
	seed(s, "a", "b", "c")
	s.buf.SetViewport("L0", "L2")
	_, out, err := s.getViewport(context.Background(), nil, EmptyInput{})
	if err != nil {
		t.Fatal(err)
	}
	if out.From != "L0" || out.To != "L2" || len(out.Entries) != 3 {
		t.Fatalf("get_viewport: %+v", out)
	}
}

func TestGetViewportErrorsWhenNoTUI(t *testing.T) {
	s := New("127.0.0.1:0", newTestBuf())
	seed(s, "a", "b")
	if _, _, err := s.getViewport(context.Background(), nil, EmptyInput{}); err == nil {
		t.Error("get_viewport must error when no TUI has attached")
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/mcp/ -run TestGetViewport` → expect FAIL (`s.getViewport undefined`).

- [ ] **Step 3: Implement** — append to `internal/mcp/tools.go`:
```go
type ViewportOutput struct {
	From    string     `json:"from"`
	To      string     `json:"to"`
	Entries []EntryDTO `json:"entries"`
}

func (s *Server) getViewport(_ context.Context, _ *mcpsdk.CallToolRequest, _ EmptyInput) (*mcpsdk.CallToolResult, ViewportOutput, error) {
	from, to, attached := s.buf.Viewport()
	if !attached {
		return nil, ViewportOutput{}, fmt.Errorf("viewport not available — no TUI attached (use get_scrollback)")
	}
	es := s.buf.Range(from, to)
	out := ViewportOutput{From: from, To: to, Entries: make([]EntryDTO, 0, len(es))}
	for _, e := range es {
		out.Entries = append(out.Entries, toDTO(e, ""))
	}
	return nil, out, nil
}
```
And register it in `registerTools` (with the other `AddTool` calls):
```go
	mcpsdk.AddTool(srv, &mcpsdk.Tool{Name: "get_viewport",
		Description: "The TUI's current on-screen entry range and entries (what the user sees / y copies). Errors when no TUI is attached."}, s.getViewport)
```

- [ ] **Step 4: Run** `go test ./internal/mcp/ && go vet ./internal/mcp/` → PASS.

- [ ] **Step 5: Commit**
```bash
git add internal/mcp/
git commit -m "feat(mcp): get_viewport tool (on-screen range; errors headless)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 4: Headless end-to-end MCP test

**Files:**
- Create: `e2e_mcp_tools_test.go`

This test launches the real binary headless with a preloaded fixture and drives it with the MCP SDK client. Uses existing helpers `startListener(t, args...)`, `pickFreeAddr(t)` (both in `e2e_test.go`, package `main`).

- [ ] **Step 1: Write the test** — create `e2e_mcp_tools_test.go`:
```go
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

// writeMCPFixture writes the fixture to a temp file and returns its path.
func writeMCPFixture(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fixture.log")
	if err := os.WriteFile(p, []byte(mcpFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// mcpDial connects an MCP client to a server at addr, retrying until it is up.
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

// mcpCall calls a tool and fails on protocol error.
func mcpCall(t *testing.T, sess *mcpsdk.ClientSession, name string, args any) *mcpsdk.CallToolResult {
	t.Helper()
	res, err := sess.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	return res
}

// decodeResult unmarshals a tool's structured output into out. The SDK may
// deliver the typed output as StructuredContent (preferred) or as JSON text in
// Content — handle both so a nil StructuredContent doesn't silently yield zeros.
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
			// The text content type carries the JSON; verify the concrete type
			// name with `go doc github.com/modelcontextprotocol/go-sdk/mcp TextContent`.
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
	go func() { // drain stdout so emit() never blocks
		for range s.ch {
		}
	}()

	sess := mcpDial(t, addr)
	defer sess.Close()

	// search "alice" → exactly L1
	var sr mcp.SearchOutput
	decodeResult(t, mcpCall(t, sess, "search", map[string]any{"query": "alice"}), &sr)
	if len(sr.Hits) != 1 || sr.Hits[0].ID != "L1" {
		t.Fatalf("search alice: %+v", sr)
	}

	// list_exceptions → one go block L2..L4
	var ex mcp.ExceptionsOutput
	decodeResult(t, mcpCall(t, sess, "list_exceptions", map[string]any{}), &ex)
	if len(ex.Exceptions) != 1 || ex.Exceptions[0].From != "L2" ||
		ex.Exceptions[0].To != "L4" || ex.Exceptions[0].Language != "go" {
		t.Fatalf("list_exceptions: %+v", ex)
	}

	// get_range L2..L4 → 3 entries, first is the panic line
	var er mcp.EntriesOutput
	decodeResult(t, mcpCall(t, sess, "get_range", map[string]any{"from": "L2", "to": "L4"}), &er)
	if len(er.Entries) != 3 || er.Entries[0].Lines[0] != "panic: boom" {
		t.Fatalf("get_range: %+v", er)
	}

	// get_line L0 → contains "start"
	var e0 mcp.EntryDTO
	decodeResult(t, mcpCall(t, sess, "get_line", map[string]any{"id": "L0"}), &e0)
	if e0.ID != "L0" || len(e0.Lines) == 0 ||
		!containsStr(e0.Lines[0], "start") {
		t.Fatalf("get_line L0: %+v", e0)
	}

	// get_viewport → error (no TUI attached)
	res, err := sess.CallTool(context.Background(),
		&mcpsdk.CallToolParams{Name: "get_viewport", Arguments: map[string]any{}})
	if err == nil && (res == nil || !res.IsError) {
		t.Fatalf("get_viewport must error headlessly; err=%v res=%+v", err, res)
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
```
> Note on `containsStr`: if `strings` is already imported in the e2e test package elsewhere, use `strings.Contains` instead of the hand-rolled helper and delete `containsStr`/`indexOf`. The hand-rolled versions avoid an import collision if `strings` is not imported in this new file.

- [ ] **Step 2: Run** `go test ./... -run TestE2EMCPToolsAgainstPreload -v`
Expected: PASS. (First run builds the e2e binary via `e2eBinary`.) If the `mcp` package's `SearchOutput`/`ExceptionsOutput`/`EntriesOutput`/`EntryDTO`/`SearchHitDTO`/`ExceptionDTO` field names differ from what `decodeResult` targets, align the test structs — they are exported in `internal/mcp/tools.go`; read it to confirm json tags (`hits`, `exceptions`, `entries`, `lines`, `id`, `from`, `to`, `language`).

- [ ] **Step 3: Verify the headless error path specifically.** If the SDK surfaces a tool error as a returned `error` (not `IsError`), the test's final block already accepts both. Confirm which path fired by temporarily logging; either is acceptable.

- [ ] **Step 4: Commit**
```bash
git add e2e_mcp_tools_test.go
git commit -m "test(e2e): MCP tools against a preloaded fixture (search/exceptions/range; viewport errors headless)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 5: PTY TUI viewport end-to-end test

**Files:**
- Create: `e2e_mcp_tui_test.go`

Runs the binary in TUI mode under a pseudo-tty so the model renders (and publishes its viewport), then queries `get_viewport`. Reuses `mcpDial`/`decodeResult` (Task 4, same package) and `e2eBinary`/`pickFreeAddr` (existing), and `creack/pty` (already used by `e2e_tui_test.go`).

- [ ] **Step 1: Write the test** — create `e2e_mcp_tui_test.go`:
```go
//go:build !windows

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
	// A tall terminal so the whole 6-line fixture fits → viewport == whole file.
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

	// Poll get_viewport until the TUI has rendered and published L0..L5.
	deadline := time.Now().Add(6 * time.Second)
	for {
		res, err := sess.CallTool(context.Background(),
			&mcpsdk.CallToolParams{Name: "get_viewport", Arguments: map[string]any{}})
		if err == nil && res != nil && !res.IsError {
			var vp mcp.ViewportOutput
			decodeResult(t, res, &vp)
			if vp.From == "L0" && vp.To == "L5" && len(vp.Entries) == 6 {
				if vp.Entries[2].Lines[0] != "panic: boom" {
					t.Fatalf("viewport entry L2 = %q, want panic: boom", vp.Entries[2].Lines[0])
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
```

- [ ] **Step 2: Run** `go test ./... -run TestE2EMCPViewportUnderPTY -v`
Expected: PASS. If the viewport never reaches `L0..L5`, check: (a) the TUI received a `WindowSizeMsg` (the drain goroutine + `Setsize` should trigger it); (b) preload events are seeded before `Run` (they are, via `InitialEvents`). If `Rows:40` still clips, the fixture is only 6 lines so any height ≥ ~8 fits — increase rows if a status bar/footer reduces content height.

- [ ] **Step 3: Commit**
```bash
git add e2e_mcp_tui_test.go
git commit -m "test(e2e): get_viewport under a PTY returns the on-screen range

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 6: Docs

**Files:**
- Modify: `README.md`, `CHANGELOG.md`

- [ ] **Step 1: README.md** — in the MCP tools list, add `get_viewport` as the 7th tool: "the TUI's current on-screen entry range and entries (what the user sees / `y` copies); errors when no TUI is attached (use `get_scrollback` headlessly)." Match the existing tool-table style.

- [ ] **Step 2: CHANGELOG.md** — add under `[Unreleased]`: "MCP: `get_viewport` tool returning the live TUI on-screen range (errors headlessly); end-to-end tests driving the server with the MCP client against a preloaded fixture (viewport, search, exceptions, range)."

- [ ] **Step 3: Verify** `go test ./...` → PASS (incl. `TestDocsUpToDate`; `KEYBINDINGS.md` is unaffected — no new keybinding).

- [ ] **Step 4: Commit**
```bash
git add README.md CHANGELOG.md
git commit -m "docs: get_viewport MCP tool + e2e verification

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Final verification (after all tasks)
```bash
go test ./... && go vet ./... && go test -race ./internal/... && CGO_ENABLED=0 ./build.sh build-static
```
All green. Then dispatch a final whole-implementation review before finishing the branch.

## Notes
- The viewport slot uses a **separate mutex** from the ring so render-time publishing never contends with tool reads.
- `get_viewport` is published from `renderStream`, the single place that knows the exact on-screen rows, so it stays faithful across scroll/resize/append.
- E2E tests assert against the **known fixture**, so expected ids/ranges are deterministic (`L0`..`L5`, exception `L2..L4`).
- The e2e package is `main`; it imports `internal/mcp` for the exported DTOs used to decode tool results.
