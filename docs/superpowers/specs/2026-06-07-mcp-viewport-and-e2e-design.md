# MCP `get_viewport` + End-to-End Verification — Design

**Date:** 2026-06-07
**Status:** Approved (pending spec review)
**Branch:** extends `feat/embedded-mcp-server`.

## Summary

Two related additions so an agent (and our tests) can verify, against a running
instance, that the MCP server returns the ranges/results a user would see:

1. **`get_viewport` MCP tool** — returns the TUI's *current on-screen* entry
   range (first..last visible entry id) plus the resolved entries — exactly what
   the user sees and what `y` copies. When **no TUI is attached** (headless /
   `--no-tui`), it returns an error ("viewport not available — no TUI attached;
   use get_scrollback"). No whole-buffer fallback.
2. **End-to-end MCP tests** — preload a known fixture, start the real server, and
   drive it with the official MCP **client** to verify: `get_viewport` returns the
   whole file under a PTY-attached TUI (it fits on screen); `get_viewport` errors
   headlessly; and `search` / `list_exceptions` + `get_range` / `get_line` return
   the expected entries from the preloaded content. This also closes the
   HTTP-round-trip coverage gap flagged in the MCP final review.

## Goals / Non-goals

**Goals:** expose the live on-screen range over MCP with faithful TUI semantics;
a clear headless error; e2e verification of viewport, search, exceptions, and
range resolution against a preloaded fixture through the real Streamable-HTTP
server using the SDK client.

**Non-goals (YAGNI):** a headless whole-buffer "viewport" fallback (explicitly
rejected); tracking multiple concurrent TUIs (one process = one TUI); pushing
viewport changes to the agent (pull-only); exposing block-focus or search-hit
selection as a separate tool (the user asked for the viewport specifically —
`get_viewport` returns what's *shown*, independent of block focus).

## Current baseline

- `linebuf.Buffer` is the shared ring read by the MCP server; it already has a
  mutex and accessor methods (`Get`, `Range`, …). The MCP server holds `*Buffer`.
- The TUI model renders via `renderStream(rows)`, which computes
  `visible := m.collectVisible(rows)` — the absolute `m.lines` indices currently
  on screen. `m.entryIDForLine(idx)` maps a line index → its owning entry id.
- `tui.Options` carries callbacks (`SetRendererOn`, `RenderFn`) stored on the
  model in `New` (e.g. `m.setRendererEnabled = opts.SetRendererOn`). `main.go`'s
  `runWatchTUI` builds `tui.Options`; `runWatch`/`runOnce` (headless) never build
  a model.
- MCP tools (`internal/mcp/tools.go`) are typed handlers
  `func(ctx, *CallToolRequest, In) (*CallToolResult, Out, error)`; `EntryDTO` /
  `EntriesOutput` already exist. A returned non-nil error becomes an `IsError`
  tool result.
- E2E helpers exist: `startListener(t, args…)` (runs the binary in-process in a
  goroutine, cancelable), `pickFreeAddr(t)`, `waitForHTTP`, and (PTY)
  `e2eBinary(t)` + `creack/pty` (`e2e_tui_test.go`).
- SDK client: `mcp.NewClient(&mcp.Implementation{…}, nil)`,
  `client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: "http://"+addr}, nil)`
  → `*ClientSession`; `session.CallTool(ctx, &mcp.CallToolParams{Name, Arguments})`
  → `*CallToolResult` (`StructuredContent` / `Content` / `IsError`).

## Component 1: viewport slot in `linebuf`

`linebuf.Buffer` gains a thread-safe viewport slot (its own mutex, independent of
the ring lock so publishing never contends with reads/writes):

```go
// in Buffer:
vpMu       sync.RWMutex
vpFrom     string
vpTo       string
vpAttached bool

// SetViewport records the TUI's current on-screen entry range and marks a TUI
// as attached. Called by the model on each render (TUI mode only).
func (b *Buffer) SetViewport(from, to string) {
    b.vpMu.Lock(); defer b.vpMu.Unlock()
    b.vpFrom, b.vpTo, b.vpAttached = from, to, true
}

// Viewport returns the last-published on-screen range. attached is false until a
// TUI has published at least once (i.e. headless runs report not-attached).
func (b *Buffer) Viewport() (from, to string, attached bool) {
    b.vpMu.RLock(); defer b.vpMu.RUnlock()
    return b.vpFrom, b.vpTo, b.vpAttached
}
```

(Separate `vpMu` so a render-time `SetViewport` never blocks on an in-flight
tool read of the ring, and vice-versa.)

## Component 2: model publishes the viewport

- `tui.Options` gains `SetViewport func(from, to string)`; the model stores it as
  `m.setViewport` in `New`.
- `renderStream` publishes after computing `visible`:
  ```go
  visible := m.collectVisible(rows)
  m.publishViewport(visible)
  ...
  ```
  ```go
  // publishViewport reports the on-screen entry range (first..last visible
  // entry id) to the shared buffer, if a publisher is wired. No-op when the
  // callback is nil (tests) or nothing is visible.
  func (m *model) publishViewport(visible []int) {
      if m.setViewport == nil { return }
      if len(visible) == 0 { m.setViewport("", ""); return }
      from := m.entryIDForLine(visible[0])
      to := m.entryIDForLine(visible[len(visible)-1])
      m.setViewport(from, to)
  }
  ```
  Publishing from `renderStream` is the single point that already knows the exact
  on-screen rows, so the published range is precisely what is displayed (and it
  updates on scroll, resize, and live append, since all re-render).
- `main.go` `runWatchTUI` wires `SetViewport: buf.SetViewport` into the
  `tui.Options`. `runWatch`/`runOnce` do not — so `vpAttached` stays false
  headlessly.

## Component 3: `get_viewport` MCP tool

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
    for _, e := range es { out.Entries = append(out.Entries, toDTO(e, "")) }
    return nil, out, nil
}
```
Registered in `registerTools` as `get_viewport`, description: "The TUI's current
on-screen entry range and entries (what the user sees / `y` copies). Errors when
no TUI is attached." Reuses `EmptyInput` (the `list_exceptions` empty-input type).

Edge: an attached TUI showing an empty buffer publishes `from=to=""`;
`Range("","")` returns nil → `Entries: []`, `from/to ""`. Acceptable (no entries
on screen).

## Component 4: end-to-end tests

A new MCP **client** helper (in the e2e test package):
```go
// mcpCall connects to a running MCP server at addr, calls tool `name` with
// args, and returns the result (retrying Connect until the server is up or the
// deadline passes).
func mcpCall(t *testing.T, addr, name string, args any) *mcpsdk.CallToolResult
```
It loops `client.Connect(ctx, &mcpsdk.StreamableClientTransport{Endpoint: "http://"+addr}, nil)` with a short backoff until success (the server may still be starting), then `session.CallTool`. Results are decoded from `res.StructuredContent` (or the JSON in `res.Content[0]` text) into the expected Go struct for assertions.

**Fixture** (`fixture.log`, written to a temp dir), known content:
```
2026-06-07 10:00:00 INFO start
2026-06-07 10:00:01 INFO user=alice action=login
panic: boom
goroutine 1 [running]:
	main.crash()
2026-06-07 10:00:02 INFO done
```
Raw-mode preload makes each line one entry (`L0`..`L5`); the buffer segments the
panic block (`L2` head + `L3` `goroutine ` cont-signature + `L4` indented frame)
and flags it `language: "go"`.

**Test A — headless buffer verification (`e2e_mcp_tools_test.go`):**
Start `startListener(t, "--no-tui", "--no-color", "--mcp", addr, "--preload", fixture)`
(no `--once`), drain stdout. Then via `mcpCall`:
- `search {query:"alice"}` → one hit, id `L1`.
- `list_exceptions {}` → one exception, `{from:"L2", to:"L4", language:"go"}`.
- `get_range {from:"L2", to:"L4"}` → 3 entries, first line `panic: boom`.
- `get_line {id:"L0"}` → first line contains `start`.
- `get_viewport {}` → **IsError** (or CallTool error) — no TUI attached.

**Test B — TUI viewport via PTY (`e2e_mcp_tui_test.go`, `//go:build !windows`):**
`pickFreeAddr`, launch `e2eBinary` under `pty.Start` with
`--preload fixture --no-color --mcp addr`, set size `Rows:30, Cols:120` so the
whole 6-line file fits. (`--preload` is required, not `-f`: a `-f` tail starts at
EOF and shows nothing, so there would be no viewport content.) Wait for the MCP server (retry
`mcpCall`). Call `get_viewport {}` → assert `from:"L0"`, `to:"L5"`, `len(entries)==6`,
and the entries' lines match the fixture. (Optionally send a `gg`/Home key to
ensure top, but with a fitting file the viewport is the whole file regardless.)
Cleanup kills the process (existing PTY-test teardown pattern).

Both tests assert against the **known preloaded content**, so the expected ranges
are deterministic.

## Architecture / files

- `internal/linebuf/linebuf.go` (or new `viewport.go`) — viewport slot + methods.
- `internal/tui/app.go` — `Options.SetViewport`; `m.setViewport`; `publishViewport`;
  call in `renderStream`.
- `main.go` — wire `SetViewport: buf.SetViewport` in `runWatchTUI`.
- `internal/mcp/tools.go` — `ViewportOutput`, `getViewport`, registration.
- `internal/mcp/tools_test.go` — unit: attached (SetViewport then getViewport
  returns the range) and not-attached (error).
- `e2e_mcp_tools_test.go` (new) — Test A + `mcpCall` helper.
- `e2e_mcp_tui_test.go` (new, `!windows`) — Test B.
- `README.md`, `CHANGELOG.md` — document `get_viewport` (7th tool) + its headless
  behavior.

## Testing strategy

- **Unit (`internal/mcp`)**: `getViewport` returns the range after `SetViewport`;
  errors when never set. `SetViewport`/`Viewport` round-trip + `attached`
  transition (in `internal/linebuf`).
- **TUI (`internal/tui`)**: `publishViewport` calls the callback with the
  first/last visible entry ids for a seeded model (table: full buffer visible →
  first..last; scrolled → the visible sub-range); no-op when callback nil.
- **E2E**: Tests A and B above (the headers/decoding verified against the SDK
  client API).

## Conventions

Phase commits per repo convention; each leaves `go test ./...`, `go vet ./...`,
`go test -race ./...` green. `e2e_mcp_tui_test.go` is `//go:build !windows`
(PTY). Update README + CHANGELOG on delivery.
