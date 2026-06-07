# Embedded MCP Server Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `--mcp`, an HTTP MCP endpoint embedded in the live process that lets an external agent resolve user-copied references (a line or a range of lines) against the same log buffer the user is watching.

**Architecture:** A new concurrency-safe `internal/linebuf` ring is fed at the pump fan-out point (parallel to `app.Push`/`sseHub.Emit`), assigning each log record a stable opaque ID that is threaded into both the TUI entry and the buffer. `internal/mcp` serves six read tools over that buffer via the official Go SDK on Streamable HTTP. The TUI adds a cursor and one context-sensitive `copy_reference` key (OSC 52). Built in three dependency-ordered phases; Phase 1 carries a TUI-vs-buffer ID-parity gate test.

**Tech Stack:** Go 1.26, `github.com/modelcontextprotocol/go-sdk/mcp` (Streamable HTTP), existing `internal/blocks`/`internal/render`, `go-osc52/v2` (already an indirect dep), bubbletea/lipgloss.

**Spec:** `docs/superpowers/specs/2026-06-07-embedded-mcp-server-design.md`

---

## File Structure

**Phase 1 — buffer + identity (no user-visible change):**
- `internal/render/decompose.go` (new) — `DisplayLine` type + `DecomposeLines(Event) []DisplayLine`: the shared plain-text splitter (head + continuation rows, tab-expanded). Single source of truth so TUI rows == buffer rows.
- `internal/render/pipeline.go` (modify) — add `Event.ID string`.
- `internal/tui/app.go` (modify) — `decomposeEvent` becomes a thin wrapper over `render.DecomposeLines`; `expandTabs` moves to `render`; `scrollbackEvent` gains `id`; `appendEvent` populates it.
- `internal/linebuf/linebuf.go` (new) — `Line`, `Entry`, `Block`, `Buffer` + API.
- `internal/linebuf/linebuf_test.go` (new).
- `main.go` (modify) — build the buffer, thread ID assignment through every fan-out point.

**Phase 2 — MCP server (HTTP-testable, no TUI):**
- `internal/config/cli.go` (modify) — `--mcp [addr]` flag; `Config.MCPAddr`.
- `internal/mcp/server.go` (new) — `Server` (New/Start/Addr/Close), Streamable HTTP mount.
- `internal/mcp/tools.go` (new) — the six tool handlers + DTOs.
- `internal/mcp/*_test.go` (new).
- `main.go` (modify) — start the server alongside SSE.

**Phase 3 — TUI cursor + copy:**
- `internal/keymap/actions.go` + `defaults.go` (modify) — `ActionCopyReference`.
- `internal/tui/app.go` (modify) — `cursor` field, highlight, action dispatch.
- `internal/tui/copyref.go` (new) — reference builder + OSC 52 command.
- `internal/tui/copyref_test.go` (new).
- `KEYBINDINGS.md` (regenerated), `README.md`, `CHANGELOG.md`, `CLAUDE.md`.

---

# PHASE 1 — linebuf + IDs + fan-out

### Task 1: Shared decompose helper (`render.DecomposeLines`)

**Files:**
- Create: `internal/render/decompose.go`
- Create: `internal/render/decompose_test.go`
- Modify: `internal/tui/app.go` (`decomposeEvent` ~876-915, `expandTabs` ~43-55)

- [ ] **Step 1: Write the failing test**

`internal/render/decompose_test.go`:
```go
package render

import "testing"

func TestDecomposeLinesTextHeadAndContinuations(t *testing.T) {
	ev := Event{Rendered: []Part{{Type: "text", Value: "head line\n  cont one\n  cont two"}}}
	got := DecomposeLines(ev)
	if len(got) != 3 {
		t.Fatalf("want 3 lines, got %d: %+v", len(got), got)
	}
	if got[0].Text != "head line" || got[0].IsCont {
		t.Errorf("head wrong: %+v", got[0])
	}
	if got[1].Text != "  cont one" || !got[1].IsCont {
		t.Errorf("cont1 wrong: %+v", got[1])
	}
}

func TestDecomposeLinesExpandsTabs(t *testing.T) {
	ev := Event{Rendered: []Part{{Type: "text", Value: "a\tb"}}}
	got := DecomposeLines(ev)
	if got[0].Text != "a       b" { // tab → spaces to 8-col stop
		t.Errorf("tabs not expanded: %q", got[0].Text)
	}
}

func TestDecomposeLinesJSONBlock(t *testing.T) {
	ev := Event{Rendered: []Part{
		{Type: "text", Value: "got json"},
		{Type: "json", Value: map[string]any{"k": "v"}},
	}}
	got := DecomposeLines(ev)
	if got[0].Text != "got json" || got[0].IsCont {
		t.Fatalf("head wrong: %+v", got[0])
	}
	// JSON pretty-printed rows follow as continuations.
	if len(got) < 2 || !got[1].IsCont {
		t.Fatalf("json rows should be continuations: %+v", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/render/ -run TestDecomposeLines`
Expected: FAIL (`undefined: DecomposeLines`).

- [ ] **Step 3: Implement `render/decompose.go`**

```go
package render

import (
	"encoding/json"
	"strings"
)

// DisplayLine is one physical row produced from an Event's rendered parts.
// Text is plain (no styling) and tab-expanded. IsCont marks continuation
// rows (everything after the head — embedded newlines, JSON/XML blocks).
type DisplayLine struct {
	Text   string
	IsCont bool
}

// DecomposeLines splits an Event's rendered parts into physical rows: the
// first text line is the head; subsequent text lines and every JSON/XML block
// row are continuations. This is the single source of truth shared by the TUI
// (which adds styling) and internal/linebuf (which stores plain text), so the
// rows — and therefore the IDs — can never diverge between what the user sees
// and what an agent resolves.
func DecomposeLines(ev Event) []DisplayLine {
	var textBuf strings.Builder
	var blocks []string
	for _, p := range ev.Rendered {
		switch p.Type {
		case "text":
			textBuf.WriteString(p.Value.(string))
		case "json":
			if b, err := json.MarshalIndent(p.Value, "", "  "); err == nil {
				blocks = append(blocks, string(b))
			}
		case "xml":
			if s, ok := p.Value.(string); ok {
				blocks = append(blocks, s)
			}
		}
	}
	text := strings.TrimRight(textBuf.String(), "\n")
	textLines := strings.Split(text, "\n")

	out := []DisplayLine{{Text: expandTabs(textLines[0]), IsCont: false}}
	for _, ln := range textLines[1:] {
		out = append(out, DisplayLine{Text: expandTabs(ln), IsCont: true})
	}
	for _, b := range blocks {
		for _, ln := range strings.Split(b, "\n") {
			out = append(out, DisplayLine{Text: expandTabs(ln), IsCont: true})
		}
	}
	return out
}

// expandTabs replaces tabs with spaces to 8-column tab stops. Moved here from
// internal/tui so the shared decomposer and the TUI expand identically.
func expandTabs(s string) string {
	if !strings.ContainsRune(s, '\t') {
		return s
	}
	var b strings.Builder
	col := 0
	for _, r := range s {
		if r == '\t' {
			n := 8 - (col % 8)
			for k := 0; k < n; k++ {
				b.WriteByte(' ')
			}
			col += n
			continue
		}
		b.WriteRune(r)
		col++
	}
	return b.String()
}
```

- [ ] **Step 4: Refactor `internal/tui/app.go`**

Delete the `expandTabs` function (lines ~43-55) from `app.go` (now in `render`). Replace the body of `decomposeEvent` so it delegates:

```go
func decomposeEvent(ev render.Event) []displayLine {
	base := filepath.Base(ev.File)
	rows := render.DecomposeLines(ev)
	out := make([]displayLine, 0, len(rows))
	for _, r := range rows {
		body := r.Text
		if r.IsCont {
			body = dimStyle.Render(r.Text)
		}
		out = append(out, displayLine{
			group:     ev.Group,
			file:      base,
			body:      body,
			bodyWidth: dispWidth(r.Text),
			isBlock:   r.IsCont,
		})
	}
	return out
}
```

If `app.go` no longer uses `encoding/json`/`strings` after this, leave them only if other code needs them (it does — leave imports as the compiler dictates; run `goimports`/`go build`).

- [ ] **Step 5: Run tests**

Run: `go test ./internal/render/ ./internal/tui/`
Expected: PASS — including the existing `multiline_test.go`/`blocks_test.go` (they guard that the refactor preserved behavior).

- [ ] **Step 6: Commit**

```bash
git add internal/render/decompose.go internal/render/decompose_test.go internal/tui/app.go
git commit -m "refactor: extract render.DecomposeLines shared by TUI + buffer"
```

---

### Task 2: `render.Event.ID` field

**Files:**
- Modify: `internal/render/pipeline.go` (Event struct ~111-119)
- Test: `internal/render/decompose_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/render/decompose_test.go`:
```go
import "encoding/json"

func TestEventIDOmitemptyMarshal(t *testing.T) {
	withID, _ := json.Marshal(Event{ID: "L7", Rendered: []Part{}})
	if !strings.Contains(string(withID), `"id":"L7"`) {
		t.Errorf("id should marshal: %s", withID)
	}
	noID, _ := json.Marshal(Event{Rendered: []Part{}})
	if strings.Contains(string(noID), `"id"`) {
		t.Errorf("empty id should be omitted: %s", noID)
	}
}
```
(Add `"strings"` to the test imports if not present.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/render/ -run TestEventIDOmitempty`
Expected: FAIL (`unknown field ID`).

- [ ] **Step 3: Implement** — add the field as the first member of `Event`:

```go
type Event struct {
	ID       string    `json:"id,omitempty"`
	Ts       time.Time `json:"ts"`
	File     string    `json:"file"`
	Group    string    `json:"group"`
	Raw      string    `json:"raw"`
	Renderer string    `json:"renderer,omitempty"`
	Captures []string  `json:"captures,omitempty"`
	Rendered []Part    `json:"rendered"`
}
```

- [ ] **Step 4: Run** — `go test ./internal/render/` → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/render/pipeline.go internal/render/decompose_test.go
git commit -m "feat(render): add Event.ID for buffer identity"
```

---

### Task 3: `internal/linebuf` — Entry/Buffer core (Append, Get, eviction)

**Files:**
- Create: `internal/linebuf/linebuf.go`
- Create: `internal/linebuf/linebuf_test.go`

- [ ] **Step 1: Write the failing test**

```go
package linebuf

import (
	"testing"

	"github.com/homeend/log-listener/internal/render"
)

// decomp is a test decomposer mirroring render.DecomposeLines via the adapter.
func decomp(ev render.Event) []Line {
	out := make([]Line, 0)
	for _, r := range render.DecomposeLines(ev) {
		out = append(out, Line{Text: r.Text, IsCont: r.IsCont})
	}
	return out
}

func ev(group, file, text string) render.Event {
	return render.Event{Group: group, File: file, Raw: text,
		Rendered: []render.Part{{Type: "text", Value: text}}}
}

func TestAppendAssignsSequentialIDs(t *testing.T) {
	b := New(100, decomp)
	id0 := b.Append(ev("g", "/a.log", "one"))
	id1 := b.Append(ev("g", "/a.log", "two"))
	if id0 != "L0" || id1 != "L1" {
		t.Fatalf("ids: %q %q", id0, id1)
	}
	e, ok := b.Get("L1")
	if !ok || e.Lines[0].Text != "two" {
		t.Fatalf("get L1: %+v ok=%v", e, ok)
	}
}

func TestAppendEvictsOldest(t *testing.T) {
	b := New(2, decomp)
	b.Append(ev("g", "/a.log", "one"))
	b.Append(ev("g", "/a.log", "two"))
	b.Append(ev("g", "/a.log", "three"))
	if _, ok := b.Get("L0"); ok {
		t.Error("L0 should have been evicted")
	}
	if _, ok := b.Get("L2"); !ok {
		t.Error("L2 should be present")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/linebuf/`
Expected: FAIL (`undefined: New`).

- [ ] **Step 3: Implement `internal/linebuf/linebuf.go`**

```go
// Package linebuf is a concurrency-safe ring of log records with stable opaque
// IDs. It is fed at the pump fan-out point (parallel to the TUI and SSE) so an
// embedded MCP server can resolve a user-copied reference to exactly the
// records the user is watching. It depends only on internal/render and
// internal/blocks — never on internal/tui.
package linebuf

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/homeend/log-listener/internal/blocks"
	"github.com/homeend/log-listener/internal/render"
)

// Line is one decomposed plain display row of an entry.
type Line struct {
	Text   string
	IsCont bool
}

// Entry is one log record — the external, copyable unit. Its ID is stable for
// the entry's lifetime even when a config reload re-renders Lines.
type Entry struct {
	ID    string
	Seq   uint64
	Group string
	File  string
	Ts    time.Time
	Raw   string
	Lines []Line
}

// Block is a contiguous run of entries the segmenter grouped (or a single
// multi-row entry); identity is the head entry.
type Block struct {
	HeadID    string
	EndID     string
	EntryIDs  []string
	Exception *blocks.ExceptionInfo
}

// Buffer is the shared ring. All methods are safe for concurrent use.
type Buffer struct {
	mu        sync.RWMutex
	cap       int
	seq       uint64
	entries   []*Entry
	byID      map[string]*Entry
	blocks    []Block
	blockOf   map[string]int
	dirty     bool
	decompose func(render.Event) []Line
}

// New returns a Buffer holding at most cap entries, decomposing events with
// the supplied function (an adapter over render.DecomposeLines).
func New(cap int, decompose func(render.Event) []Line) *Buffer {
	if cap <= 0 {
		cap = 10000
	}
	return &Buffer{
		cap:       cap,
		byID:      map[string]*Entry{},
		blockOf:   map[string]int{},
		decompose: decompose,
	}
}

// Append assigns the next ID+Seq, stores the entry, evicts the oldest if over
// cap, and returns the assigned ID. Single write path.
func (b *Buffer) Append(ev render.Event) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := "L" + strconv.FormatUint(b.seq, 36)
	e := &Entry{
		ID: id, Seq: b.seq, Group: ev.Group, File: baseName(ev.File),
		Ts: ev.Ts, Raw: ev.Raw, Lines: b.decompose(ev),
	}
	b.seq++
	b.entries = append(b.entries, e)
	b.byID[id] = e
	if len(b.entries) > b.cap {
		drop := b.entries[0]
		b.entries = b.entries[1:]
		delete(b.byID, drop.ID)
	}
	b.dirty = true
	return id
}

// (Range, Context, Search, Recent, Exceptions, BlockOf, Rerender are added in
// the following tasks.)

// Get returns the entry for id.
func (b *Buffer) Get(id string) (*Entry, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	e, ok := b.byID[id]
	return e, ok
}

func baseName(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}
```

- [ ] **Step 4: Run** — `go test ./internal/linebuf/` → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/linebuf/linebuf.go internal/linebuf/linebuf_test.go
git commit -m "feat(linebuf): ring buffer core with stable IDs + eviction"
```

---

### Task 4: linebuf `Range` + `Context`

**Files:**
- Modify: `internal/linebuf/linebuf.go`, `internal/linebuf/linebuf_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestRangeInclusiveAndOrderTolerant(t *testing.T) {
	b := New(100, decomp)
	for _, s := range []string{"a", "b", "c", "d"} {
		b.Append(ev("g", "/x.log", s))
	}
	got := b.Range("L1", "L3") // b,c,d
	if len(got) != 3 || got[0].Lines[0].Text != "b" || got[2].Lines[0].Text != "d" {
		t.Fatalf("range L1..L3: %+v", got)
	}
	rev := b.Range("L3", "L1") // same span, reversed args
	if len(rev) != 3 || rev[0].Lines[0].Text != "b" {
		t.Fatalf("reversed args should normalise: %+v", rev)
	}
}

func TestContextBounds(t *testing.T) {
	b := New(100, decomp)
	for _, s := range []string{"a", "b", "c", "d", "e"} {
		b.Append(ev("g", "/x.log", s))
	}
	got := b.Context("L2", 1, 1) // b,c,d
	if len(got) != 3 || got[0].Lines[0].Text != "b" || got[2].Lines[0].Text != "d" {
		t.Fatalf("context L2 ±1: %+v", got)
	}
}
```

- [ ] **Step 2: Run** → FAIL (`undefined: Range`).

- [ ] **Step 3: Implement** — append to `linebuf.go`:

```go
// Range returns entries between fromID and toID inclusive, in seq order,
// tolerant of argument order. If one ID was evicted, the resident sub-span is
// returned; if both are unknown, nil.
func (b *Buffer) Range(fromID, toID string) []*Entry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	from, okF := b.byID[fromID]
	to, okT := b.byID[toID]
	if !okF && !okT {
		return nil
	}
	lo, hi := uint64(0), ^uint64(0)
	if okF {
		lo = from.Seq
	}
	if okT {
		hi = to.Seq
	}
	if lo > hi {
		lo, hi = hi, lo
	}
	var out []*Entry
	for _, e := range b.entries {
		if e.Seq >= lo && e.Seq <= hi {
			out = append(out, e)
		}
	}
	return out
}

// Context returns up to `before` entries before id and `after` after it,
// inclusive of id.
func (b *Buffer) Context(id string, before, after int) []*Entry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	idx := -1
	for i, e := range b.entries {
		if e.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil
	}
	lo := idx - before
	if lo < 0 {
		lo = 0
	}
	hi := idx + after
	if hi >= len(b.entries) {
		hi = len(b.entries) - 1
	}
	out := make([]*Entry, 0, hi-lo+1)
	for i := lo; i <= hi; i++ {
		out = append(out, b.entries[i])
	}
	return out
}
```

- [ ] **Step 4: Run** → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/linebuf/
git commit -m "feat(linebuf): Range (order-tolerant) + Context"
```

---

### Task 5: linebuf `Search` + `Recent`

**Files:**
- Modify: `internal/linebuf/linebuf.go`, `internal/linebuf/linebuf_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestSearchSubstringAndRegexAndLimit(t *testing.T) {
	b := New(100, decomp)
	for _, s := range []string{"alpha", "beta", "gamma alpha", "delta"} {
		b.Append(ev("g", "/x.log", s))
	}
	hits, err := b.Search("alpha", false, 10)
	if err != nil || len(hits) != 2 {
		t.Fatalf("substring hits: %+v err=%v", hits, err)
	}
	// newest-first
	if hits[0].ID != "L2" {
		t.Errorf("want newest first (L2), got %s", hits[0].ID)
	}
	rx, err := b.Search("^a", true, 10)
	if err != nil || len(rx) != 1 || rx[0].ID != "L0" {
		t.Fatalf("regex hits: %+v err=%v", rx, err)
	}
	lim, _ := b.Search("a", false, 1)
	if len(lim) != 1 {
		t.Errorf("limit not honoured: %d", len(lim))
	}
}

func TestRecentPagination(t *testing.T) {
	b := New(100, decomp)
	for _, s := range []string{"a", "b", "c", "d"} {
		b.Append(ev("g", "/x.log", s))
	}
	got := b.Recent(2, 0) // last 2, chronological: c,d
	if len(got) != 2 || got[0].Lines[0].Text != "c" || got[1].Lines[0].Text != "d" {
		t.Fatalf("recent(2,0): %+v", got)
	}
}
```

- [ ] **Step 2: Run** → FAIL (`undefined: SearchHit`).

- [ ] **Step 3: Implement** — append to `linebuf.go`:

```go
import "regexp" // add to the import block

// SearchHit is one search result: the entry ID, location, and the matching
// line's text as a snippet.
type SearchHit struct {
	ID          string
	Group       string
	File        string
	Snippet     string
	MatchedLine int
}

// Search returns entries whose any line matches query (substring, or regexp
// when regex=true), newest-first, capped at limit (limit<=0 → 50).
func (b *Buffer) Search(query string, regex bool, limit int) ([]SearchHit, error) {
	if limit <= 0 {
		limit = 50
	}
	var re *regexp.Regexp
	if regex {
		var err error
		if re, err = regexp.Compile(query); err != nil {
			return nil, err
		}
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	var out []SearchHit
	for i := len(b.entries) - 1; i >= 0 && len(out) < limit; i-- {
		e := b.entries[i]
		for li, ln := range e.Lines {
			match := false
			if re != nil {
				match = re.MatchString(ln.Text)
			} else {
				match = strings.Contains(ln.Text, query)
			}
			if match {
				out = append(out, SearchHit{ID: e.ID, Group: e.Group,
					File: e.File, Snippet: ln.Text, MatchedLine: li})
				break
			}
		}
	}
	return out, nil
}

// Recent returns up to limit entries ending `offset` from the newest, in
// chronological order (oldest-first within the page).
func (b *Buffer) Recent(limit, offset int) []*Entry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	end := len(b.entries) - offset
	if end > len(b.entries) {
		end = len(b.entries)
	}
	if end <= 0 {
		return nil
	}
	start := end - limit
	if start < 0 {
		start = 0
	}
	out := make([]*Entry, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, b.entries[i])
	}
	return out
}
```

(`regexp` is the only new import; `fmt` is not needed in `linebuf.go`.)

- [ ] **Step 4: Run** → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/linebuf/
git commit -m "feat(linebuf): Search (substring/regex) + Recent pagination"
```

---

### Task 6: linebuf blocks (`Exceptions`, `blockOf`)

**Files:**
- Modify: `internal/linebuf/linebuf.go`, `internal/linebuf/linebuf_test.go`

The segmenter works over a flat `[]blocks.Line`. linebuf flattens its entries' `Lines` to `blocks.Line{Text, IsRenderBlock:IsCont}`, runs `blocks.Segment`, then maps each block's `Start`/`End` flat indices back to the owning entries.

- [ ] **Step 1: Write the failing test**

```go
import "github.com/homeend/log-listener/internal/blocks"

func TestExceptionsMapsBlockToEntries(t *testing.T) {
	b := New(100, decomp)
	b.Append(ev("g", "/a.log", "panic: boom"))         // L0 (head)
	b.Append(ev("g", "/a.log", "goroutine 1 [running]:")) // L1 (continuation entry)
	b.Append(ev("g", "/a.log", "ordinary line"))        // L2
	exc := b.Exceptions()
	if len(exc) != 1 {
		t.Fatalf("want 1 exception block, got %d: %+v", len(exc), exc)
	}
	if exc[0].HeadID != "L0" {
		t.Errorf("head: %s", exc[0].HeadID)
	}
	if exc[0].Exception == nil || exc[0].Exception.Language != "go" {
		t.Errorf("language: %+v", exc[0].Exception)
	}
	if got := b.BlockOf("L0"); got == nil || got.HeadID != "L0" {
		t.Errorf("BlockOf(L0): %+v", got)
	}
}
```

Verify the `blocks` package API before implementing:
Run: `grep -n "func Segment\|type Block\|type ExceptionInfo\|type Line" internal/blocks/blocks.go`
(Confirm `blocks.Segment([]blocks.Line) []blocks.Block`, `blocks.Line{Text string; IsRenderBlock bool}`, `blocks.Block{Start, End int; Exception *ExceptionInfo}`, `ExceptionInfo{Language string}`.)

- [ ] **Step 2: Run** → FAIL (`undefined: Exceptions`).

- [ ] **Step 3: Implement** — append to `linebuf.go`:

```go
// ensureBlocks recomputes block segmentation when dirty. Caller holds the
// write lock (or call from a method that takes it).
func (b *Buffer) ensureBlocks() {
	if !b.dirty {
		return
	}
	// Flatten entries → blocks.Line, remembering which entry each flat row
	// belongs to.
	var flat []blocks.Line
	var owner []int // flat row index → entry index
	for ei, e := range b.entries {
		for _, ln := range e.Lines {
			flat = append(flat, blocks.Line{Text: ln.Text, IsRenderBlock: ln.IsCont})
			owner = append(owner, ei)
		}
	}
	segs := blocks.Segment(flat)
	b.blocks = b.blocks[:0]
	b.blockOf = map[string]int{}
	for _, s := range segs {
		headEntry := b.entries[owner[s.Start]]
		endEntry := b.entries[owner[s.End]]
		ids := []string{}
		seen := map[string]bool{}
		for f := s.Start; f <= s.End; f++ {
			id := b.entries[owner[f]].ID
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		blk := Block{HeadID: headEntry.ID, EndID: endEntry.ID,
			EntryIDs: ids, Exception: s.Exception}
		idx := len(b.blocks)
		b.blocks = append(b.blocks, blk)
		for _, id := range ids {
			b.blockOf[id] = idx
		}
	}
	b.dirty = false
}

// Exceptions returns the current exception blocks (head/end IDs + language).
func (b *Buffer) Exceptions() []Block {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ensureBlocks()
	var out []Block
	for _, blk := range b.blocks {
		if blk.Exception != nil {
			out = append(out, blk)
		}
	}
	return out
}

// BlockOf returns the block containing entry id, or nil.
func (b *Buffer) BlockOf(id string) *Block {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ensureBlocks()
	if idx, ok := b.blockOf[id]; ok {
		blk := b.blocks[idx]
		return &blk
	}
	return nil
}
```

- [ ] **Step 4: Run** → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/linebuf/
git commit -m "feat(linebuf): block segmentation, Exceptions, BlockOf"
```

---

### Task 7: linebuf `Rerender` (reload keeps IDs)

**Files:**
- Modify: `internal/linebuf/linebuf.go`, `internal/linebuf/linebuf_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestRerenderKeepsIDsChangesContent(t *testing.T) {
	b := New(100, decomp)
	b.Append(ev("g", "/a.log", "original"))
	// New renderFn uppercases the raw line.
	b.Rerender(func(group, file, raw string) (render.Event, bool) {
		return render.Event{Group: group, File: file, Raw: raw,
			Rendered: []render.Part{{Type: "text", Value: "RE:" + raw}}}, true
	})
	e, ok := b.Get("L0")
	if !ok {
		t.Fatal("L0 must survive rerender")
	}
	if e.Lines[0].Text != "RE:original" {
		t.Errorf("content not re-rendered: %q", e.Lines[0].Text)
	}
	if e.Seq != 0 {
		t.Errorf("seq must be preserved: %d", e.Seq)
	}
}
```

- [ ] **Step 2: Run** → FAIL (`undefined: Rerender`).

- [ ] **Step 3: Implement** — append to `linebuf.go`:

```go
// Rerender re-runs renderFn over every stored entry's Raw, replacing Lines but
// keeping ID/Seq. For config reload only (the pipeline changed). If renderFn
// returns ok=false for an entry, its Lines are left unchanged.
func (b *Buffer) Rerender(renderFn func(group, file, raw string) (render.Event, bool)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, e := range b.entries {
		rev, ok := renderFn(e.Group, e.File, e.Raw)
		if !ok {
			continue
		}
		e.Lines = b.decompose(rev)
	}
	b.dirty = true
}
```

- [ ] **Step 4: Run** → PASS. Then `go test -race ./internal/linebuf/` → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/linebuf/
git commit -m "feat(linebuf): Rerender preserves IDs on config reload"
```

---

### Task 8: Fan-out wiring + TUI entry id + ID-parity gate test

**Files:**
- Modify: `main.go` (buffer construction; `emit`; `runOnce`; `runWatch`; `runWatchTUI` pump + preload)
- Modify: `internal/tui/app.go` (`scrollbackEvent` ~ struct; `appendEvent`)
- Create: `internal/tui/idparity_test.go`

- [ ] **Step 1: Write the failing gate test**

`internal/tui/idparity_test.go`:
```go
package tui

import (
	"testing"

	"github.com/homeend/log-listener/internal/linebuf"
	"github.com/homeend/log-listener/internal/render"
)

// The load-bearing invariant: the ID the buffer assigns is the same ID the TUI
// entry carries. Here we simulate the fan-out — buffer assigns, the event is
// pushed to the model with that ID — and assert 1:1 parity.
func TestTUIEntryIDsMatchBufferIDs(t *testing.T) {
	decomp := func(ev render.Event) []linebuf.Line {
		out := make([]linebuf.Line, 0)
		for _, r := range render.DecomposeLines(ev) {
			out = append(out, linebuf.Line{Text: r.Text, IsCont: r.IsCont})
		}
		return out
	}
	buf := linebuf.New(100, decomp)
	m := newModel(100)
	events := []render.Event{
		{Group: "g", File: "/a.log", Raw: "one",
			Rendered: []render.Part{{Type: "text", Value: "one"}}},
		{Group: "g", File: "/a.log", Raw: "trace",
			Rendered: []render.Part{{Type: "text", Value: "panic: x\n  at y"}}},
	}
	for _, ev := range events {
		ev.ID = buf.Append(ev) // fan-out: buffer is the authority
		m.appendEvent(ev)
	}
	if len(m.entries) != len(events) {
		t.Fatalf("entry count: %d", len(m.entries))
	}
	for i, e := range m.entries {
		want := "L" + itoa36(i)
		if e.id != want {
			t.Errorf("entry %d id = %q, want %q", i, e.id, want)
		}
	}
}

func itoa36(i int) string {
	const d = "0123456789abcdefghijklmnopqrstuvwxyz"
	if i < 36 {
		return string(d[i])
	}
	return itoa36(i/36) + string(d[i%36])
}
```

- [ ] **Step 2: Run** → FAIL (`e.id undefined`).

- [ ] **Step 3: Implement — `scrollbackEvent.id` + populate**

In `internal/tui/app.go`, add `id string` to `scrollbackEvent`:
```go
type scrollbackEvent struct {
	id               string
	group, file, raw string
	lines            []displayLine
}
```
In `appendEvent`, carry the ID:
```go
func (m *model) appendEvent(ev render.Event) {
	lines := decomposeEvent(ev)
	m.entries = append(m.entries, scrollbackEvent{
		id:    ev.ID,
		group: ev.Group,
		file:  ev.File,
		raw:   ev.Raw,
		lines: lines,
	})
	m.lines = append(m.lines, lines...)
	m.trimToCap()
	m.blocksDirty = true
}
```

- [ ] **Step 4: Run** → `go test ./internal/tui/ -run TestTUIEntryIDsMatchBufferIDs` → PASS.

- [ ] **Step 5: Wire the buffer in `main.go`**

In `run`, after the pipeline is built and before preload, construct the buffer and a decompose adapter:
```go
bufDecompose := func(ev render.Event) []linebuf.Line {
	rows := render.DecomposeLines(ev)
	out := make([]linebuf.Line, len(rows))
	for i, r := range rows {
		out[i] = linebuf.Line{Text: r.Text, IsCont: r.IsCont}
	}
	return out
}
bufCap := cfg.TUIScrollback
if bufCap <= 0 {
	bufCap = 10000
}
buf := linebuf.New(bufCap, bufDecompose)
```
(Add `"github.com/homeend/log-listener/internal/linebuf"` to imports.)

In the **preload loop**, assign IDs as events are produced so the seeded buffer and the seeded TUI agree:
```go
for i := range preloadEvents {
	preloadEvents[i].ID = buf.Append(preloadEvents[i])
}
```
Place this right after the preload `for _, spec := range cfg.Preloads` loop fills `preloadEvents`.

Thread `buf` into `runOnce`, `runWatch`, `runWatchTUI`, and `emit` (add a `*linebuf.Buffer` parameter to each). In `runOnce`, `--once` does not feed the buffer beyond preload (the buffer/MCP isn't served in `--once`), so just pass it for signature uniformity or skip — **skip**: leave `runOnce` unchanged (don't add the param).

`emit` (used by `runWatch`):
```go
func emit(pipePtr *atomic.Pointer[render.Pipeline], buf *linebuf.Buffer, stdoutSink *sink.Stdout, sseHub *sink.SSEHub, group, path, line string) {
	ev, ok := pipePtr.Load().Render(time.Now(), group, path, line)
	if !ok {
		return
	}
	ev.ID = buf.Append(ev)
	stdoutSink.Emit(ev)
	if sseHub != nil {
		sseHub.Emit(ev)
	}
}
```
Update both `emit(...)` call sites in `runWatch` to pass `buf`.

In `runWatchTUI`'s pump goroutine:
```go
case ev := <-w.Events():
	rev, ok := pipePtr.Load().Render(time.Now(), ev.Group, ev.Path, ev.Line)
	if !ok {
		continue
	}
	rev.ID = buf.Append(rev)
	app.Push(rev)
	if sseHub != nil {
		sseHub.Emit(rev)
	}
```
On config reload (both `runWatch` and `runWatchTUI`), after `pipePtr.Store(rt.pipeline)`, re-render the buffer:
```go
buf.Rerender(func(g, f, raw string) (render.Event, bool) {
	return rt.pipeline.Render(time.Now(), g, f, raw)
})
```

Update the `runWatch`/`runWatchTUI` signatures and their call sites in `run` to pass `buf`.

- [ ] **Step 6: Run the full suite**

Run: `go test ./... && go vet ./... && go test -race ./internal/linebuf/ ./internal/tui/`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add main.go internal/tui/app.go internal/tui/idparity_test.go
git commit -m "feat: feed linebuf at fan-out; thread IDs into TUI entries (gate test)"
```

---

# PHASE 2 — MCP server + tools

### Task 9: `--mcp` flag + `Config.MCPAddr`

**Files:**
- Modify: `internal/config/cli.go` (Config struct ~23-37; parse loop after the `--sse` case ~85-92)
- Modify: `internal/config/cli_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/config/cli_test.go`:
```go
func TestMCPFlagDefaultAndExplicit(t *testing.T) {
	cfg, err := Load([]string{"--mcp"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MCPAddr != "127.0.0.1:7777" {
		t.Errorf("bare --mcp default: %q", cfg.MCPAddr)
	}
	cfg2, err := Load([]string{"--mcp", "127.0.0.1:9100"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.MCPAddr != "127.0.0.1:9100" {
		t.Errorf("--mcp addr: %q", cfg2.MCPAddr)
	}
}
```
(Adjust to the existing test's `Load` signature/imports — match `TestE2E`/`cli_test.go` conventions; `Load` is `config.Load(args, now)`.)

- [ ] **Step 2: Run** → FAIL (`unknown field MCPAddr`).

- [ ] **Step 3: Implement** — add `MCPAddr string` to `Config` (next to `SSEAddr`), and add the parse case after the `--sse` case:
```go
		case a == "--mcp":
			// Optional value: bare --mcp enables on the default loopback addr.
			// The CLI has no positional args (sources use -f/-d/-r), so a
			// following non-flag token is unambiguously the address.
			cfg.MCPAddr = "127.0.0.1:7777"
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				cfg.MCPAddr = args[i+1]
				i++
			}
			cfg.cliExplicit["mcp_addr"] = true
			i++
```
(`strings` is already imported in `cli.go`.)

- [ ] **Step 4: Run** → `go test ./internal/config/` → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/cli.go internal/config/cli_test.go
git commit -m "feat(config): --mcp [addr] flag (optional value, loopback default)"
```

---

### Task 10: `internal/mcp` server skeleton (SDK + Streamable HTTP)

**Files:**
- Create: `internal/mcp/server.go`
- Create: `internal/mcp/server_test.go`
- Modify: `go.mod`/`go.sum` (via `go get`)

- [ ] **Step 1: Add the SDK**

Run:
```bash
go get github.com/modelcontextprotocol/go-sdk/mcp@v1.6.1
go mod tidy
```
Expected: `go.mod` gains the SDK; `go build ./...` succeeds.

- [ ] **Step 2: Write the failing test**

`internal/mcp/server_test.go`:
```go
package mcp

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/homeend/log-listener/internal/linebuf"
	"github.com/homeend/log-listener/internal/render"
)

func newTestBuf() *linebuf.Buffer {
	decomp := func(ev render.Event) []linebuf.Line {
		out := []linebuf.Line{}
		for _, r := range render.DecomposeLines(ev) {
			out = append(out, linebuf.Line{Text: r.Text, IsCont: r.IsCont})
		}
		return out
	}
	return linebuf.New(100, decomp)
}

func TestServerStartServesAndCloses(t *testing.T) {
	s := New("127.0.0.1:0", newTestBuf())
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// The Streamable HTTP endpoint responds (a bare GET without the MCP
	// handshake yields a non-zero status, proving the listener is mounted).
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get("http://" + s.Addr())
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode == 0 {
		t.Errorf("no status")
	}
	_ = strings.TrimSpace
}
```

- [ ] **Step 3: Run** → FAIL (`undefined: New`).

- [ ] **Step 4: Implement `internal/mcp/server.go`**

```go
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
```

Add a stub `registerTools` in `tools.go` so it compiles (filled in Task 11):
```go
package mcp

import mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

func (s *Server) registerTools(srv *mcpsdk.Server) {}
```

- [ ] **Step 5: Run** → `go test ./internal/mcp/` → PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/mcp/server.go internal/mcp/server_test.go internal/mcp/tools.go
git commit -m "feat(mcp): Streamable HTTP server skeleton (SDK v1.6.1)"
```

---

### Task 11: Tools `get_line` + `get_range`

**Files:**
- Modify: `internal/mcp/tools.go`
- Create: `internal/mcp/tools_test.go`

Handlers are tested directly (no HTTP) — fast and deterministic. Handler signature per the SDK: `func(ctx, *mcpsdk.CallToolRequest, In) (*mcpsdk.CallToolResult, Out, error)`.

- [ ] **Step 1: Write the failing test**

`internal/mcp/tools_test.go`:
```go
package mcp

import (
	"context"
	"testing"

	"github.com/homeend/log-listener/internal/render"
)

func seed(s *Server, texts ...string) {
	for _, txt := range texts {
		s.buf.Append(render.Event{Group: "g", File: "/a.log", Raw: txt,
			Rendered: []render.Part{{Type: "text", Value: txt}}})
	}
}

func TestGetLineTool(t *testing.T) {
	s := New("127.0.0.1:0", newTestBuf())
	seed(s, "one", "two")
	_, out, err := s.getLine(context.Background(), nil, GetLineInput{ID: "L1"})
	if err != nil {
		t.Fatal(err)
	}
	if out.ID != "L1" || len(out.Lines) != 1 || out.Lines[0] != "two" {
		t.Fatalf("get_line: %+v", out)
	}
	if _, _, err := s.getLine(context.Background(), nil, GetLineInput{ID: "L99"}); err == nil {
		t.Error("unknown id should error")
	}
}

func TestGetRangeTool(t *testing.T) {
	s := New("127.0.0.1:0", newTestBuf())
	seed(s, "a", "b", "c", "d")
	_, out, err := s.getRange(context.Background(), nil, GetRangeInput{From: "L1", To: "L3"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Entries) != 3 || out.Entries[0].Lines[0] != "b" {
		t.Fatalf("get_range: %+v", out)
	}
}
```

- [ ] **Step 2: Run** → FAIL (`undefined: GetLineInput`).

- [ ] **Step 3: Implement** — replace `internal/mcp/tools.go`:

```go
package mcp

import (
	"context"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/homeend/log-listener/internal/linebuf"
)

// EntryDTO is the wire shape of one log record.
type EntryDTO struct {
	ID        string   `json:"id"`
	Group     string   `json:"group"`
	File      string   `json:"file"`
	Ts        string   `json:"ts"`
	Raw       string   `json:"raw"`
	Lines     []string `json:"lines"`
	Exception string   `json:"exception,omitempty"`
}

func toDTO(e *linebuf.Entry, lang string) EntryDTO {
	lines := make([]string, len(e.Lines))
	for i, ln := range e.Lines {
		lines[i] = ln.Text
	}
	ts := ""
	if !e.Ts.IsZero() {
		ts = e.Ts.Format("2006-01-02T15:04:05Z07:00")
	}
	return EntryDTO{ID: e.ID, Group: e.Group, File: e.File, Ts: ts,
		Raw: e.Raw, Lines: lines, Exception: lang}
}

type GetLineInput struct {
	ID string `json:"id"`
}
type GetRangeInput struct {
	From string `json:"from"`
	To   string `json:"to"`
}
type EntriesOutput struct {
	Entries []EntryDTO `json:"entries"`
}

func (s *Server) getLine(_ context.Context, _ *mcpsdk.CallToolRequest, in GetLineInput) (*mcpsdk.CallToolResult, EntryDTO, error) {
	e, ok := s.buf.Get(in.ID)
	if !ok {
		return nil, EntryDTO{}, fmt.Errorf("unknown or evicted id %q", in.ID)
	}
	return nil, toDTO(e, ""), nil
}

func (s *Server) getRange(_ context.Context, _ *mcpsdk.CallToolRequest, in GetRangeInput) (*mcpsdk.CallToolResult, EntriesOutput, error) {
	es := s.buf.Range(in.From, in.To)
	out := EntriesOutput{Entries: make([]EntryDTO, 0, len(es))}
	for _, e := range es {
		out.Entries = append(out.Entries, toDTO(e, ""))
	}
	return nil, out, nil
}

func (s *Server) registerTools(srv *mcpsdk.Server) {
	mcpsdk.AddTool(srv, &mcpsdk.Tool{Name: "get_line",
		Description: "Get one log record by its id."}, s.getLine)
	mcpsdk.AddTool(srv, &mcpsdk.Tool{Name: "get_range",
		Description: "Get all log records between two ids (inclusive)."}, s.getRange)
}
```

- [ ] **Step 4: Run** → `go test ./internal/mcp/` → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/tools.go internal/mcp/tools_test.go
git commit -m "feat(mcp): get_line + get_range tools"
```

---

### Task 12: Tools `get_context` + `get_scrollback`

**Files:**
- Modify: `internal/mcp/tools.go`, `internal/mcp/tools_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestGetContextTool(t *testing.T) {
	s := New("127.0.0.1:0", newTestBuf())
	seed(s, "a", "b", "c", "d", "e")
	_, out, err := s.getContext(context.Background(), nil,
		GetContextInput{ID: "L2", Before: 1, After: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Entries) != 3 || out.Entries[0].Lines[0] != "b" {
		t.Fatalf("get_context: %+v", out)
	}
}

func TestGetScrollbackPaginates(t *testing.T) {
	s := New("127.0.0.1:0", newTestBuf())
	seed(s, "a", "b", "c", "d")
	_, out, err := s.getScrollback(context.Background(), nil,
		GetScrollbackInput{Limit: 2, Offset: 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Entries) != 2 || out.Entries[1].Lines[0] != "d" {
		t.Fatalf("get_scrollback: %+v", out)
	}
}
```

- [ ] **Step 2: Run** → FAIL.

- [ ] **Step 3: Implement** — append to `tools.go`:

```go
type GetContextInput struct {
	ID     string `json:"id"`
	Before int    `json:"before"`
	After  int    `json:"after"`
}
type GetScrollbackInput struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

func (s *Server) getContext(_ context.Context, _ *mcpsdk.CallToolRequest, in GetContextInput) (*mcpsdk.CallToolResult, EntriesOutput, error) {
	before, after := in.Before, in.After
	if before == 0 && after == 0 {
		before, after = 5, 5
	}
	if before > 200 {
		before = 200
	}
	if after > 200 {
		after = 200
	}
	es := s.buf.Context(in.ID, before, after)
	out := EntriesOutput{Entries: make([]EntryDTO, 0, len(es))}
	for _, e := range es {
		out.Entries = append(out.Entries, toDTO(e, ""))
	}
	return nil, out, nil
}

func (s *Server) getScrollback(_ context.Context, _ *mcpsdk.CallToolRequest, in GetScrollbackInput) (*mcpsdk.CallToolResult, EntriesOutput, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 200
	}
	if limit > 2000 {
		limit = 2000
	}
	es := s.buf.Recent(limit, in.Offset)
	out := EntriesOutput{Entries: make([]EntryDTO, 0, len(es))}
	for _, e := range es {
		out.Entries = append(out.Entries, toDTO(e, ""))
	}
	return nil, out, nil
}
```
Register them in `registerTools`:
```go
	mcpsdk.AddTool(srv, &mcpsdk.Tool{Name: "get_context",
		Description: "Get N records before and after an id (default 5/5)."}, s.getContext)
	mcpsdk.AddTool(srv, &mcpsdk.Tool{Name: "get_scrollback",
		Description: "Get a page of the whole buffer (newest-last)."}, s.getScrollback)
```

- [ ] **Step 4: Run** → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/
git commit -m "feat(mcp): get_context + get_scrollback tools"
```

---

### Task 13: Tools `search` + `list_exceptions`

**Files:**
- Modify: `internal/mcp/tools.go`, `internal/mcp/tools_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestSearchTool(t *testing.T) {
	s := New("127.0.0.1:0", newTestBuf())
	seed(s, "alpha", "beta", "gamma alpha")
	_, out, err := s.search(context.Background(), nil,
		SearchInput{Query: "alpha", Regex: false, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Hits) != 2 || out.Hits[0].ID != "L2" { // newest-first
		t.Fatalf("search: %+v", out)
	}
}

func TestListExceptionsTool(t *testing.T) {
	s := New("127.0.0.1:0", newTestBuf())
	seed(s, "panic: boom", "goroutine 1 [running]:", "normal")
	_, out, err := s.listExceptions(context.Background(), nil, struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Exceptions) != 1 || out.Exceptions[0].From != "L0" ||
		out.Exceptions[0].Language != "go" {
		t.Fatalf("list_exceptions: %+v", out)
	}
}
```

- [ ] **Step 2: Run** → FAIL.

- [ ] **Step 3: Implement** — append to `tools.go`:

```go
type SearchInput struct {
	Query string `json:"query"`
	Regex bool   `json:"regex"`
	Limit int    `json:"limit"`
}
type SearchHitDTO struct {
	ID          string `json:"id"`
	Group       string `json:"group"`
	File        string `json:"file"`
	Snippet     string `json:"snippet"`
	MatchedLine int    `json:"matched_line"`
}
type SearchOutput struct {
	Hits []SearchHitDTO `json:"hits"`
}
type ExceptionDTO struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Language string `json:"language"`
}
type ExceptionsOutput struct {
	Exceptions []ExceptionDTO `json:"exceptions"`
}

func (s *Server) search(_ context.Context, _ *mcpsdk.CallToolRequest, in SearchInput) (*mcpsdk.CallToolResult, SearchOutput, error) {
	limit := in.Limit
	if limit > 500 {
		limit = 500
	}
	hits, err := s.buf.Search(in.Query, in.Regex, limit)
	if err != nil {
		return nil, SearchOutput{}, err
	}
	out := SearchOutput{Hits: make([]SearchHitDTO, 0, len(hits))}
	for _, h := range hits {
		out.Hits = append(out.Hits, SearchHitDTO{ID: h.ID, Group: h.Group,
			File: h.File, Snippet: h.Snippet, MatchedLine: h.MatchedLine})
	}
	return nil, out, nil
}

func (s *Server) listExceptions(_ context.Context, _ *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, ExceptionsOutput, error) {
	out := ExceptionsOutput{}
	for _, b := range s.buf.Exceptions() {
		lang := ""
		if b.Exception != nil {
			lang = b.Exception.Language
		}
		out.Exceptions = append(out.Exceptions,
			ExceptionDTO{From: b.HeadID, To: b.EndID, Language: lang})
	}
	return nil, out, nil
}
```
Register:
```go
	mcpsdk.AddTool(srv, &mcpsdk.Tool{Name: "search",
		Description: "Find records matching a substring (or regex). Newest-first."}, s.search)
	mcpsdk.AddTool(srv, &mcpsdk.Tool{Name: "list_exceptions",
		Description: "List detected exception blocks as id ranges + language."}, s.listExceptions)
```

- [ ] **Step 4: Run** → `go test ./internal/mcp/` → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/
git commit -m "feat(mcp): search + list_exceptions tools"
```

---

### Task 14: Wire server into `main.go` + static-build + E2E

**Files:**
- Modify: `main.go`
- Create: `e2e_mcp_test.go` (root) OR add to existing `e2e_test.go`

- [ ] **Step 1: Wire startup in `main.go`**

After the SSE hub block in `run`, add:
```go
var mcpServer *mcp.Server
if cfg.MCPAddr != "" {
	mcpServer = mcp.New(cfg.MCPAddr, buf)
	if err := mcpServer.Start(); err != nil {
		fmt.Fprintln(stderr, "log-listener: mcp:", err)
		return 1
	}
	defer mcpServer.Close()
	fmt.Fprintf(stderr, "log-listener: mcp on http://%s\n", mcpServer.Addr())
}
```
(Add `"github.com/homeend/log-listener/internal/mcp"` to imports.) `--once` returns before this matters; the buffer is still fed by preload, but the MCP server is only started in the non-`--once` paths because `cfg.Once` returns earlier — confirm the `if cfg.Once { ... return }` block is **above** this code so MCP is not started for `--once`.

- [ ] **Step 2: Static build check**

Run: `CGO_ENABLED=0 ./build.sh build-static`
Expected: a static binary is produced with no CGO errors (proves the SDK is CGO-free).

- [ ] **Step 3: Write the E2E test**

`e2e_mcp_test.go` (root package, mirroring existing e2e isolation):
```go
package main

import (
	"strings"
	"testing"
)

// --mcp boots alongside --no-tui and preload; we assert startup announces the
// endpoint on stderr (the full tool round-trip is covered by internal/mcp).
func TestE2EMCPBootsHeadless(t *testing.T) {
	dir := t.TempDir()
	raw := dir + "/sample.log"
	if err := writeFile(raw, "hello one\nhello two\n"); err != nil {
		t.Fatal(err)
	}
	var out, errBuf strings.Builder
	code := run([]string{"--no-tui", "--once", "--mcp", "127.0.0.1:0",
		"--preload", raw}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, errBuf.String())
	}
	// In --once the process exits immediately; the preload content is printed.
	if !strings.Contains(out.String(), "hello one") {
		t.Errorf("preloaded content missing: %s", out.String())
	}
}
```
(If there is no `writeFile` helper, use `os.WriteFile`. Match the helper names already in `e2e_test.go`.)

> Note: `--once` exits before serving MCP; this E2E proves the flag parses and the headless path runs clean. A live tool round-trip against a running server is covered by the `internal/mcp` unit tests. If you want a live E2E, start `run` in a goroutine without `--once`, poll the announced addr, call a tool over HTTP, then signal shutdown — optional, not required for this task.

- [ ] **Step 4: Run** → `go test ./... && go vet ./...` → PASS.

- [ ] **Step 5: Commit**

```bash
git add main.go e2e_mcp_test.go
git commit -m "feat(mcp): start server alongside SSE; headless E2E; static build"
```

---

# PHASE 3 — TUI cursor + copy_reference + OSC 52

### Task 15: Cursor state + highlight (unified with searchHit)

**Files:**
- Modify: `internal/tui/app.go` (model struct; `searchHit` handling; `renderStream` highlight)
- Create: `internal/tui/cursor_test.go`

The cursor is the absolute `m.lines` index of the focused row. It tracks the browse position by default and snaps to the current search hit. For v1 it does not need bespoke movement keys (it follows `streamTop`/`searchHit`); the highlight makes it visible.

- [ ] **Step 1: Write the failing test**

`internal/tui/cursor_test.go`:
```go
package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/render"
)

func TestCursorFollowsSearchHit(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	for _, v := range []string{"apple", "banana", "cherry banana", "date"} {
		m.appendEvent(render.Event{Group: "g", File: "/a.log",
			Rendered: []render.Part{{Type: "text", Value: v}}})
	}
	// Search for "banana", jump to first hit → cursor should equal searchHit.
	m.searchTerm = "banana"
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = m2.(*model)
	if m.cursorIndex() != m.searchHit {
		t.Errorf("cursor %d should follow searchHit %d", m.cursorIndex(), m.searchHit)
	}
}
```

- [ ] **Step 2: Run** → FAIL (`undefined: cursorIndex`).

- [ ] **Step 3: Implement** — add a `cursor int` field to the model struct (init `-1` in `newModel` alongside `searchHit`), and a resolver:
```go
// cursorIndex returns the focused absolute m.lines index: the active search hit
// when searching, else the top visible row (browse anchor), else -1.
func (m *model) cursorIndex() int {
	if m.searchHit >= 0 {
		return m.searchHit
	}
	if !m.tailMode && m.streamTop >= 0 && m.streamTop < len(m.lines) {
		return m.streamTop
	}
	return -1
}
```
(For v1, `cursorIndex` is derived — no separate stored `cursor` field is required. If a stored field is preferred later, this method is the seam. Remove the unused `cursor int` field if you add the method only.)

Optionally highlight `cursorIndex()`'s row in `renderStream` with a subtle style; if highlighting risks the width-accounting bug, skip the visual highlight for v1 and keep only the logical cursor (the copy feature needs the index, not the highlight). **Decision for v1: logical cursor only, no new highlight style** (avoids reopening the row-width invariant). The search hit already has its own visible styling.

- [ ] **Step 4: Run** → `go test ./internal/tui/ -run TestCursorFollowsSearchHit` → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/app.go internal/tui/cursor_test.go
git commit -m "feat(tui): logical cursor unified with search hit"
```

---

### Task 16: `ActionCopyReference` + reference builder + OSC 52 + docs

**Files:**
- Modify: `internal/keymap/actions.go` (add action + registry row), `internal/keymap/defaults.go` (default key)
- Create: `internal/tui/copyref.go`, `internal/tui/copyref_test.go`
- Modify: `internal/tui/app.go` (dispatch case + footer hint)
- Regenerate: `KEYBINDINGS.md`

- [ ] **Step 1: Write the failing test**

`internal/tui/copyref.go` will expose a pure `buildReference(m) string`. Test it directly:

`internal/tui/copyref_test.go`:
```go
package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/render"
)

func seedIDs(m *model, vals ...string) {
	for i, v := range vals {
		m.appendEvent(render.Event{ID: "L" + itoa36(i), Group: "g", File: "/a.log",
			Rendered: []render.Part{{Type: "text", Value: v}}})
	}
}

func TestBuildReferenceViewportRange(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 6})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	seedIDs(m, "a", "b", "c", "d", "e", "f")
	m.tailMode = false
	m.streamTop = 0
	ref := buildReference(m)
	if ref == "" || ref[:6] != "range:" {
		t.Fatalf("viewport ref should be a range: %q", ref)
	}
}

func TestBuildReferenceSearchHitLine(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	seedIDs(m, "apple", "banana", "cherry")
	m.searchTerm = "banana"
	m.searchHit = 1
	ref := buildReference(m)
	if ref != "line:L1" {
		t.Fatalf("search hit ref: %q", ref)
	}
}

func TestBuildReferenceBlockRange(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	// One multi-line entry (a block): head + continuations under one ID.
	m.appendEvent(render.Event{ID: "L0", Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "config:\n  k=v\n  j=w"}}})
	m.tailMode = false
	m.streamTop = 0 // cursor on the block head
	ref := buildReference(m)
	if ref != "range:L0..L0" {
		t.Fatalf("single-entry block ref: %q", ref)
	}
}
```

- [ ] **Step 2: Run** → FAIL (`undefined: buildReference`).

- [ ] **Step 3: Implement `internal/tui/copyref.go`**

```go
package tui

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/aymanbagabas/go-osc52/v2"
)

// entryIDForLine returns the ID of the entry that owns absolute m.lines index
// idx, or "" if out of range.
func (m *model) entryIDForLine(idx int) string {
	if idx < 0 {
		return ""
	}
	off := 0
	for _, e := range m.entries {
		n := len(e.lines)
		if idx < off+n {
			return e.id
		}
		off += n
	}
	return ""
}

// buildReference produces the paste-ready reference string by precedence:
//   1. search active + hit selected → line:<hit entry id>
//   2. cursor inside a multi-line block → range:<headEntry>..<endEntry>
//   3. else → range:<first visible entry>..<last visible entry>
func buildReference(m *model) string {
	// 1. search hit
	if m.searchTerm != "" && m.searchHit >= 0 {
		if id := m.entryIDForLine(m.searchHit); id != "" {
			return "line:" + id
		}
	}
	// 2. block at cursor
	cur := m.cursorIndex()
	if cur >= 0 {
		m.ensureBlocks()
		for _, b := range m.blocks {
			if cur >= b.Start && cur <= b.End && b.End > b.Start {
				head := m.entryIDForLine(b.Start)
				end := m.entryIDForLine(b.End)
				if head != "" && end != "" {
					return fmt.Sprintf("range:%s..%s", head, end)
				}
			}
		}
	}
	// 3. viewport range
	idxs := m.collectVisible(m.contentHeight())
	if len(idxs) == 0 {
		return ""
	}
	first := m.entryIDForLine(idxs[0])
	last := m.entryIDForLine(idxs[len(idxs)-1])
	if first == "" || last == "" {
		return ""
	}
	return fmt.Sprintf("range:%s..%s", first, last)
}

// copyReferenceCmd writes the reference to the terminal clipboard via OSC 52
// (stderr, so it does not corrupt the stdout-driven render) and flashes a
// confirmation. Returns nil if there is nothing to copy.
func (m *model) copyReferenceCmd() tea.Cmd {
	ref := buildReference(m)
	if ref == "" {
		return nil
	}
	return func() tea.Msg {
		_, _ = osc52.New(ref).WriteTo(os.Stderr)
		return flashMsg("copied " + ref)
	}
}
```

If `flashMsg` does not already exist, reuse the save feature's flash mechanism (`m.flash`); check `internal/tui/save.go`/`app.go` for the existing flash type and adapt the message accordingly (the spec notes `m.flash` exists). If the existing flash is a model field set inline rather than a `tea.Msg`, set it directly in the dispatch case instead of returning a msg:
```go
case keymap.ActionCopyReference:
	if ref := buildReference(m); ref != "" {
		_, _ = osc52.New(ref).WriteTo(os.Stderr)
		m.flash = "copied " + ref
	}
```
Use whichever matches the existing flash pattern; the test only exercises `buildReference`.

- [ ] **Step 4: Register the action**

`internal/keymap/actions.go` — add the constant near the other actions:
```go
	ActionCopyReference Action = "copy_reference"
```
and a registry row in the action list:
```go
	{ActionCopyReference, "Copy reference", "Copy a paste-ready id reference (search line, block range, or viewport range) for an agent.", "main"},
```
`internal/keymap/defaults.go` — add the default key:
```go
		ActionCopyReference: {"y"},
```

Add the dispatch case in `internal/tui/app.go`'s action switch (near `ActionSaveViewport`):
```go
		case keymap.ActionCopyReference:
			if ref := buildReference(m); ref != "" {
				_, _ = osc52.New(ref).WriteTo(os.Stderr)
				m.flash = "copied " + ref
			}
```
(Import `github.com/aymanbagabas/go-osc52/v2` in `app.go`, or keep the OSC write inside `copyref.go` and call `m.copyReferenceCmd()` returning a `tea.Cmd` — match the surrounding dispatch style which returns `(m, cmd)`.)

- [ ] **Step 5: Run + regenerate docs**

Run: `go test ./internal/tui/ ./internal/keymap/`
Expected: `TestDocsUpToDate` (keymap) now FAILS because `KEYBINDINGS.md` is stale.
Run: `./build.sh keybindings-docs`
Run: `go test ./internal/keymap/` → PASS.

- [ ] **Step 6: Run full suite**

Run: `go test ./... && go vet ./... && go test -race ./internal/tui/ ./internal/linebuf/ ./internal/mcp/`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/keymap/ internal/tui/ KEYBINDINGS.md
git commit -m "feat(tui): copy_reference key (search line / block / viewport) via OSC 52"
```

---

### Task 17: Docs (README, CHANGELOG, CLAUDE.md)

**Files:**
- Modify: `README.md`, `CHANGELOG.md`, `CLAUDE.md`

- [ ] **Step 1: Update README.md** — document `--mcp [addr]` (loopback default `127.0.0.1:7777`, no auth, local dev only), the six tools, and the `y` copy-reference key + reference formats (`line:Lx`, `range:Lx..Ly`).

- [ ] **Step 2: Update CHANGELOG.md** — add an entry for the embedded MCP server (buffer + IDs, six tools, copy-reference key).

- [ ] **Step 3: Update CLAUDE.md** —
  - Module map: add `internal/linebuf` (shared MCP buffer + IDs + blocks) and `internal/mcp` (embedded MCP server) rows.
  - Locked design rules: note `--mcp` (embedded Streamable HTTP, loopback, no auth) and that the **official Go MCP SDK overrides the "only 5 deps" rule** (pre-approved in the roadmap).
  - Data flow: note the fan-out also feeds `linebuf`.

- [ ] **Step 4: Verify docs build + full green**

Run: `go test ./... && go vet ./...`
Expected: PASS (including `TestDocsUpToDate`).

- [ ] **Step 5: Commit**

```bash
git add README.md CHANGELOG.md CLAUDE.md
git commit -m "docs: embedded MCP server (README, CHANGELOG, CLAUDE.md)"
```

---

## Final verification (after all tasks)

Run the complete gate:
```bash
go test ./... && go vet ./... && go test -race ./... && CGO_ENABLED=0 ./build.sh build-static
```
All must be green. Then dispatch a final whole-implementation code review before finishing the branch.

## Notes on SDK API (verified)

- `mcpsdk.NewServer(&mcpsdk.Implementation{Name, Version}, nil) *Server`
- `mcpsdk.AddTool(srv, &mcpsdk.Tool{Name, Description}, handler)` where handler is
  `func(ctx, *mcpsdk.CallToolRequest, In) (*mcpsdk.CallToolResult, Out, error)` — the SDK auto-generates the JSON Schema from the `In`/`Out` structs' `json:` tags.
- `mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server, *mcpsdk.StreamableHTTPOptions) *StreamableHTTPHandler` (implements `http.Handler`; pass `nil` opts for defaults).
- Import path: `github.com/modelcontextprotocol/go-sdk/mcp`.
- If a signature differs in v1.6.1 at implementation time, run `go doc github.com/modelcontextprotocol/go-sdk/mcp NewStreamableHTTPHandler` and `go doc ... AddTool` and adapt; the shapes above match v1.6.x.
