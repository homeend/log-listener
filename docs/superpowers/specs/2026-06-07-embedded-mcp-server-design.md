# Embedded MCP Server — Design

**Date:** 2026-06-07
**Status:** Approved (pending spec review)
**Roadmap:** feature #1 of the streaming→agent arc
(`2026-06-07-streaming-agent-features-roadmap.md`). #2 (save), #3 (block
annotation), and preload are merged; this is the last and largest.

## Summary

Add `--mcp [addr]`: an HTTP MCP endpoint running **inside the live process**,
sharing a buffer fed from the same event stream the user is watching, so an
external agent can query exactly what the user sees. The user copies a
**reference** to what they are looking at (a line or a range of lines) via a
keybinding (OSC 52 clipboard) and pastes it to an agent; the agent resolves it
against the MCP tools.

Three independently-testable layers, built in dependency order:

1. **`internal/linebuf`** — a concurrency-safe ring of log records with stable
   IDs, fed at the pump fan-out point (parallel to `app.Push` / `sseHub.Emit`).
   No user-visible change.
2. **`internal/mcp`** — the official Go SDK server over Streamable HTTP, exposing
   six read tools over the buffer. HTTP-testable without a TUI.
3. **TUI cursor + `copy_reference` key + OSC 52** — the only model-touching
   layer, smallest, last.

## Goals / Non-goals

**Goals:** an agent can resolve a user-copied reference to the exact log records
the user sees; `get_line`/`get_range`/`get_context`/`search`/`list_exceptions`/
`get_scrollback` over a shared in-process buffer; works in TUI **and** `--no-tui`;
references survive config-reload re-rendering; local-only, no auth; static binary
still builds.

**Non-goals (YAGNI):** authentication / remote exposure; a server-side `save_*`
tool (the agent has filesystem access and writes content it pulled itself);
write/mutation tools; resources/prompts/sampling beyond tools; MCP in `--once`
mode (it scans and exits — no live buffer to share); per-display-line external
IDs (see Identity); a config-driven processor registry (still one exception
processor, as in #3).

## Current architecture (the seams this feature uses)

- **`render.Event`** (`internal/render/pipeline.go`) carries
  `{Ts, File, Group, Raw, Renderer, Captures, Rendered []Part}`. JSON-tagged; the
  **only** marshal site is the SSE hub (`internal/sink/sse.go:101`).
- **Fan-out points** where a rendered event is dispatched to sinks:
  - `runWatchTUI` pump goroutine: `app.Push(rev)` + `sseHub.Emit(rev)`
    (`main.go` ~472-479).
  - `runWatch` loop: `emit(...)` → `stdoutSink.Emit` + `sseHub.Emit`
    (`main.go:398`).
  - `runOnce`: `stdoutSink.Emit` + `sseHub.Emit` (excluded — see Non-goals).
  - Preload seeding (`main.go` + `runWatch`/`runWatchTUI`): events emitted /
    pushed before live tailing.
- **`decomposeEvent(ev render.Event) []displayLine`** (`internal/tui/app.go`) is
  a **pure function**, already shared by `appendEvent` and `reRenderAll`. It
  splits an event's rendered parts into a head row + continuation rows
  (`expandTabs`'d), styling continuations dim. The plain-text splitting core is
  what `linebuf` needs (without styling/width).
- **`internal/blocks`**: `Segment([]blocks.Line) []Block`; `Block{Start, End int;
  Exception *ExceptionInfo}` — positional indices, no IDs. One exception
  processor (`Annotate`). `blocks.Line{Text string; IsRenderBlock bool}`.
- **TUI model** (`internal/tui/app.go`): `m.entries []scrollbackEvent`
  (`{group, file, raw string; lines []displayLine}`), `m.lines []displayLine`
  (flat), evicted **whole-entry** by `trimToCap` so entries and lines stay in
  lockstep. Browse anchor `streamTop`; current search hit `searchHit` (absolute
  `m.lines` index, moved by `n`/`p` = `ActionNextMatch`/`ActionPrevMatch`).
- **`internal/sink/SSEHub`**: `New`/`Start`/`Close`, own mutex, background
  `http.Server`, signal/`defer` lifecycle in `main.go`. The model for the MCP
  server's lifecycle.
- **`internal/keymap`**: named `Action`s, per-OS defaults, action→handler switch
  in the model's `Update` (`internal/tui/app.go:501`), `KEYBINDINGS.md` generated
  via `--keybindings-doc`, guarded by `TestDocsUpToDate`.
- **Deps:** `go-osc52/v2` is **already** an indirect dep (via lipgloss/termenv),
  so OSC 52 copy adds nothing.

## Identity model

**The load-bearing invariant:** a record's ID is assigned **exactly once, at the
fan-out point, before either consumer**, and threaded into *both* the TUI entry
and the buffer entry. If the TUI and the buffer ever assign IDs independently
they diverge silently and every copied reference becomes unresolvable. One write
path.

### External (agent-facing, copyable) handle = the entry

The copyable ID identifies **one log record (entry)**, not one visual row. An
entry's identity never changes even when a config reload re-renders it into a
different row count, so **no copied reference can dangle**.

- Viewport copy → `range:<firstVisibleEntry>..<lastVisibleEntry>`.
- Block copy → `range:<headEntry>..<endEntry>` (a single multi-row entry is
  `head==end`).
- Search-hit copy → `line:<hitEntry>`.

Rounding viewport boundaries to entry edges is intentional ("you don't want half
an event").

### Internal data structure (per-line, for O(1) lookup)

Inside `linebuf`, each entry holds its decomposed plain lines, and the buffer
keeps maps so search and block lookup are O(1) — the structure the user asked
for. The per-line detail is *internal*; the agent is handed entry IDs.

### ID format

Opaque short token from a monotonic counter, e.g. `L` + base36(seq):
`L0`, `L1`, … Tokens are **not** order-comparable by the agent; ordering is the
internal `Seq`. (Counter, not random — collision-free, debuggable, and the seq
is recoverable for range resolution.)

### Block ID

No separate block-ID namespace. A block reference is a line-ID **range**
(`headEntry..endEntry`); `list_exceptions` returns those ranges. Block identity
(the head entry) is inherently stable.

## Component 1: `internal/linebuf`

A neutral package (depends on `internal/render`, `internal/blocks`, and the
shared decompose helper; **no** dependency on `internal/tui`).

```go
package linebuf

// Line is one decomposed display row of an entry (internal granularity).
type Line struct {
    Text   string // plain, expandTabs'd — identical to what the TUI shows
    IsCont bool   // continuation (block) row?
}

// Entry is one log record = the external, copyable unit.
type Entry struct {
    ID    string       // opaque, stable for the entry's lifetime ("L42")
    Seq   uint64       // monotonic ordering key
    Group string
    File  string       // basename
    Ts    time.Time
    Raw   string        // original raw line (lets the buffer re-render on reload)
    Lines []Line        // decomposed plain rows (head = Lines[0])
}

// Block is a contiguous run of entries (or a single multi-row entry) the
// segmenter grouped; identity is the head entry.
type Block struct {
    HeadID    string
    EndID     string
    EntryIDs  []string                // every member entry ID
    Exception *blocks.ExceptionInfo   // nil unless a processor matched
}

type Buffer struct {
    mu      sync.RWMutex
    cap     int                  // max entries; >= TUI scrollback
    seq     uint64               // monotonic ID counter
    entries []*Entry             // ring, in order
    byID    map[string]*Entry    // entry ID -> entry        (O(1) get_line)
    blocks  []Block
    blockOf map[string]int       // entry ID -> blocks index (O(1) "which block?")
    decompose func(render.Event) []Line // injected shared decomposer
}
```

### API

```go
func New(cap int, decompose func(render.Event) []Line) *Buffer

// Append assigns the next ID+Seq, stores the entry, evicts the oldest entry
// if over cap, marks blocks dirty, and returns the assigned ID. Single write
// path; safe for the one feeding goroutine. Called UNDER the lock by both
// startup preload (main goroutine) and the live pump.
func (b *Buffer) Append(ev render.Event) string

// Get returns the entry for id (nil, false if evicted/unknown).
func (b *Buffer) Get(id string) (*Entry, bool)

// Range returns entries between fromID and toID inclusive, in seq order,
// tolerant of argument order (resolves both to Seq, walks low..high). Unknown
// IDs that were evicted yield the still-resident sub-span; both unknown -> nil.
func (b *Buffer) Range(fromID, toID string) []*Entry

// Context returns up to `before` entries before id and `after` after it.
func (b *Buffer) Context(id string, before, after int) []*Entry

// Search returns entries whose any line matches (substring or regex), newest-
// first, capped at limit; each hit reports the line index that matched.
func (b *Buffer) Search(query string, regex bool, limit int) ([]SearchHit, error)

// Exceptions returns the current exception blocks (head/end IDs + language).
func (b *Buffer) Exceptions() []Block

// Recent returns the last n entries (drives get_scrollback pagination).
func (b *Buffer) Recent(limit, offset int) []*Entry

// Rerender re-runs renderFn over every stored entry's Raw, replacing Lines but
// KEEPING ID/Seq. Called on config reload only. Marks blocks dirty.
func (b *Buffer) Rerender(renderFn func(group, file, raw string) (render.Event, bool))
```

Blocks are recomputed lazily (dirty flag) via `blocks.Segment` over the flat
`Line` sequence, then `Start`/`End` line indices are mapped back to the owning
entries' IDs to build `Block`/`blockOf`. Same segmentation the TUI uses — same
results.

### Shared decompose

Extract `decomposeEvent`'s plain-text splitting core into a neutral helper
(proposed: `render.DecomposeLines(ev render.Event) []render.DisplayLine` where
`DisplayLine{Text string; IsCont bool}`, `Text` already `expandTabs`'d). The
TUI's `decomposeEvent` is refactored to call it and add lipgloss styling +
`dispWidth`; `linebuf.New` is given an adapter to `[]linebuf.Line`. Both
consumers hit the same splitter → rows and IDs cannot drift. Existing
`multiline_test`/`blocks_test` guard the TUI behavior through the refactor.

### Cap

`cap` defaults to the TUI scrollback value (`cfg.TUIScrollback`) so any line the
user can scroll to is always resolvable; configurable later if needed. Eviction
is per-entry from the head (mirrors `trimToCap`).

## Component 2: `render.Event.ID` + fan-out wiring

- Add `ID string` `json:"id,omitempty"` to `render.Event` (additive;
  SSE clients ignore unknown/empty fields).
- At each live fan-out point, the buffer assigns the ID and it flows to the
  other consumers:

```go
rev, ok := pipePtr.Load().Render(...)
if !ok { continue }
rev.ID = buf.Append(rev) // buffer is the ID authority; assigns once
app.Push(rev)            // TUI entry carries the same ID
if sseHub != nil { sseHub.Emit(rev) }
```

- Preload events are appended to the buffer (assigning IDs) **before** the pump
  goroutine starts, then seeded into the TUI with those IDs.
- `runWatch` (`--no-tui`): `emit()` gains the buffer and does the same assign →
  `stdoutSink.Emit` / `sseHub.Emit`.
- `reRenderAll` (TUI renderer toggle) does **not** touch the buffer — toggles are
  a view concern. Only config reload calls `buf.Rerender`.

The TUI `scrollbackEvent` gains an `id string` field, populated from `ev.ID` in
`appendEvent`, so the copy handler can map a visible row → its entry → ID.

## Component 3: `internal/mcp` (server + tools)

Official Go SDK `github.com/modelcontextprotocol/go-sdk` (v1.6.1, CGO-free).
Streamable HTTP via the SDK's `StreamableHTTPHandler`, mounted on an
`http.Server` bound to loopback — lifecycle mirroring `SSEHub`
(`New`/`Start`/`Close`, started in `run`, `defer Close`, stopped on signal).

```go
package mcp

type Server struct { /* addr, *http.Server, *linebuf.Buffer */ }
func New(addr string, buf *linebuf.Buffer) *Server
func (s *Server) Start() error  // binds listener, serves in a goroutine
func (s *Server) Addr() string
func (s *Server) Close() error
```

**Flag/config:** `--mcp [addr]` (default `127.0.0.1:7777`); YAML `output.mcp`.
Parsed in `internal/config/cli.go` as a new `case` (mirrors `--sse`); `Config`
gains `MCPAddr string`. Bare `--mcp` uses the default addr.

### Tools (all read-only, over the buffer)

A shared `EntryDTO` is the wire shape returned by the record tools:

```go
type EntryDTO struct {
    ID       string   `json:"id"`
    Group    string   `json:"group"`
    File     string   `json:"file"`
    Ts       string   `json:"ts"`            // RFC3339
    Raw      string   `json:"raw"`
    Lines    []string `json:"lines"`         // plain rows; Lines[0] is the head
    Exception string  `json:"exception,omitempty"` // language if in an exception block
}
```

1. **`get_line`** `{id string}` → `EntryDTO` (error if evicted/unknown).
2. **`get_range`** `{from string, to string}` → `[]EntryDTO` (inclusive, seq-ordered,
   argument-order tolerant). Powers the viewport/block reference.
3. **`get_context`** `{id string, before int, after int}` → `[]EntryDTO`
   (defaults before=after=5; capped, e.g. 200).
4. **`search`** `{query string, regex bool, limit int}` →
   `[]{id, file, group, snippet, matchedLine int}` (newest-first; default
   limit 50, max 500).
5. **`list_exceptions`** `{}` → `[]{from string, to string, language string}` —
   ready to feed straight into `get_range`.
6. **`get_scrollback`** `{limit int, offset int}` → `[]EntryDTO` — paginated
   whole-buffer access (default limit 200, max 2000), newest-last.

All tools acquire the buffer's `RLock` for the read and copy out plain data (no
buffer internals escape).

## Component 4: TUI cursor + `copy_reference` + OSC 52

- **Cursor:** a `cursor int` model field (absolute `m.lines` index), unified with
  `searchHit` — `n`/`p` set the cursor to the hit; new cursor-movement is folded
  into the existing scroll handlers (the cursor follows browse position) so no
  bespoke up/down keys are strictly required for v1. The cursor row is
  highlighted in `renderStream`.
- **`ActionCopyReference`** (new keymap action; default key `y`): builds the
  reference string by precedence, computed inside the model goroutine:
  1. **search mode active** (a hit is selected) → `line:<entryID of hit row>`;
  2. **else cursor sits inside a multi-line block** → `range:<headEntry>..<endEntry>`;
  3. **else** → `range:<firstVisibleEntry>..<lastVisibleEntry>`
     (from `collectVisible` / `streamTop` + content height).
  A visible row maps to its entry via the `entries`↔`lines` lockstep; the
  entry's `id` is the copyable token.
- **OSC 52 copy:** emit the clipboard escape for the reference string (via the
  already-present `go-osc52/v2`, or the raw escape). A transient flash confirms
  ("copied range:L12..L40").
- Register the action in `internal/keymap` (`actions.go` + `defaults.go`),
  regenerate `KEYBINDINGS.md` (`./build.sh keybindings-docs`); `TestDocsUpToDate`
  guards it. Add the hint to the footer.

If a copied entry has already been evicted from the buffer by the time the agent
resolves it, the tool returns a clear "unknown/evicted id" error; the cap default
(≥ scrollback) makes this impossible for currently-visible lines.

## Concurrency & lifecycle

- The buffer owns its `sync.RWMutex`. **Writers:** startup preload (main
  goroutine, before the pump exists) and the single feeding goroutine (pump in
  TUI mode; main loop in `--no-tui`). **Readers:** MCP HTTP handler goroutines.
  No shared mutable state with the bubbletea model — the TUI keeps its own
  `m.lines`.
- MCP server: started in `run` after the buffer is built; `defer s.Close()`;
  the existing signal handler triggers shutdown. `Start` returns immediately
  (serves in a goroutine), like `SSEHub.Start`. A bind failure prints to stderr
  and returns a non-zero exit, like `--sse`.

## Phases (implementation contract)

**Phase 1 — `internal/linebuf` + `render.Event.ID` + fan-out wiring + shared
decompose.** No user-visible change.
- **Gate test:** seed identical events into a TUI model and a `linebuf.Buffer`
  through the shared decomposer; assert the model's entry IDs and the buffer's
  entry IDs match 1:1 and in order. Plus: `Append`/`Get`/`Range`/`Context`/
  `Search`/`Exceptions`/`Recent`/`Rerender` unit tests; cap eviction; reload
  keeps IDs.

**Phase 2 — `internal/mcp` + `--mcp` flag + the six tools.** HTTP-testable
against a seeded buffer, no TUI.
- Tests: each tool over a seeded buffer (get_line hit/evicted; get_range
  order-tolerant + partial-eviction; get_context bounds; search substring+regex+
  limit; list_exceptions ranges feed get_range; get_scrollback pagination).
  `--mcp` flag parse; `build-static` (CGO_ENABLED=0) still succeeds with the SDK.

**Phase 3 — TUI cursor + `copy_reference` + OSC 52.** Only model-touching phase.
- Tests: precedence (search-hit → line; in-block → block range; else viewport
  range); reference strings resolve against a buffer seeded from the same events;
  `TestDocsUpToDate` after `KEYBINDINGS.md` regen.

Each phase ends green on `go test ./...`, `go vet ./...`, `go test -race ./...`,
per repo convention (two commits per phase: implementation + review fixes).

## Deps & docs

- go.mod gains `github.com/modelcontextprotocol/go-sdk` + transitive deps
  (`google/jsonschema-go`, `segmentio/encoding`(+`asm`), `yosida95/uritemplate`,
  `golang.org/x/oauth2`; `golang.org/x/sys` already present). Verified CGO-free.
- **CLAUDE.md:** record the dep-rule override (roadmap pre-approved it), add
  `internal/linebuf` and `internal/mcp` to the module map, note `--mcp` in the
  locked design rules.
- **README.md + CHANGELOG.md** updated on delivery; `KEYBINDINGS.md` regenerated
  (new `copy_reference` action).

## Testing strategy (summary)

- **Phase 1 gate:** TUI-vs-buffer ID parity (the invariant) is the centerpiece.
- **Buffer units:** all API methods, cap eviction, reload-keeps-IDs, O(1) maps.
- **MCP units:** tools over a seeded buffer via in-process SDK client or HTTP;
  static-build check.
- **TUI units:** copy-reference precedence + reference resolvability.
- **E2E (root):** `--mcp` boots alongside `--preload ... ` (headless) and a tool
  call returns the preloaded records — the full "agent sees what was loaded"
  path, isolated from ambient config like the other e2e tests.

## Conventions

`internal/`-only; no exported library surface. Phase commits per repo
convention. `.claude/settings.local.json` stays tracked. Update docs on delivery.
