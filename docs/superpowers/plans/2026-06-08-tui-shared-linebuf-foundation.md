# TUI on Shared `linebuf` — Foundation (slice 5-1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The TUI sources its source-records from the shared `linebuf.Buffer` (deleting its duplicate `m.entries` store) via a per-frame-stable snapshot, so the TUI and MCP read one record store. Display behavior is user-visibly identical.

**Architecture:** `linebuf` gains a generation counter + bounded `Snapshot(limit)`. The pump notifies the TUI of buffer changes (`BufChangedMsg`) instead of pushing events; the TUI reconciles a `displayLine` cache keyed by entry ID, building rows as a **pure transform** from `linebuf.Entry` (`Group`, basename `File`, `Lines{Text,IsCont}`) — no re-render. `m.entries` is deleted; the four readers (reRenderAll, filter, copyref, copytext) source from the snapshot. View-state stays index-based (5-3 will make it ID-based).

**Tech Stack:** Go 1.26, bubbletea, `go-runewidth`. No new deps.

**Spec:** `docs/superpowers/specs/2026-06-08-tui-shared-linebuf-foundation-design.md`

---

## Key facts (verified against current code)

- `linebuf.Buffer` has `mu sync.RWMutex`, `entries []*Entry`, `seq uint64`. `Append` (write-locked) appends + evicts head over `cap`. `Rerender` (write-locked) replaces each entry via copy-on-write (`ne := *e; b.entries[i] = &ne`), so entries are **immutable** → pointer snapshots are safe to read after unlock.
- `linebuf.Entry{ID, Seq, Group, File(basename), Ts, Raw, Lines []Line{Text,IsCont}}`. It already holds everything the TUI display needs.
- TUI `decomposeEvent(ev render.Event)` builds `[]displayLine` from `render.DecomposeLines(ev)` rows (`Text`,`IsCont`) + `filepath.Base(ev.File)`. The same `Text`/`IsCont` are already in `Entry.Lines`, and `Entry.File` is already the basename → the TUI can build rows directly from an `Entry`.
- TUI model: `entries []scrollbackEvent` (source), `lines []displayLine` (flat display), view-state indices `streamTop`/`searchHit`/`visualCursor`/`visualAnchor` into `m.lines`. `appendEvent` appends+`trimToCap`; `trimToCap` evicts whole head entries and drags the indices by dropped-line count. `App.Push(ev)`→`EventMsg`→`appendEvent`. `reRenderAll` re-renders `m.entries` via `renderFn`. `filteredIndices`, `copyref.go`, `copytext.go` iterate `m.entries`.
- `tui.Options` has `RenderFn func(group,file,raw)(render.Event,bool)`, `SetViewport`, `InitialEvents`, `Scrollback`; **no** `Buffer`. `main.go` `runWatchTUI` pump: `rev.ID = buf.Append(rev); app.Push(rev); fanout.Emit(rev)`. `main.go` sizes `buf` cap = `cfg.TUIScrollback` (default 10000) and TUI `Scrollback` = same.
- Cap units differ: `linebuf` caps by **entries**, TUI by **display-lines**. The TUI keeps its own line-cap window during reconcile; since rows ≥ entries, `linebuf` never under-supplies.

---

## Task 1: `linebuf` — generation counter + bounded snapshot

**Files:** Modify `internal/linebuf/linebuf.go`; Test `internal/linebuf/linebuf_test.go`.

- [ ] **Step 1: Write failing tests**

Add to `internal/linebuf/linebuf_test.go`:

```go
func TestGenBumpsOnAppendAndRerender(t *testing.T) {
	b := New(10, func(ev render.Event) []Line { return []Line{{Text: ev.Raw}} })
	g0 := b.Gen()
	b.Append(render.Event{Raw: "a"})
	g1 := b.Gen()
	if g1 == g0 {
		t.Fatal("gen did not bump on Append")
	}
	b.Rerender(func(g, f, raw string) (render.Event, bool) {
		return render.Event{Raw: raw + "!"}, true
	})
	if b.Gen() == g1 {
		t.Fatal("gen did not bump on Rerender")
	}
}

func TestSnapshotReturnsLastLimitAndGen(t *testing.T) {
	b := New(100, func(ev render.Event) []Line { return []Line{{Text: ev.Raw}} })
	for _, s := range []string{"a", "b", "c", "d"} {
		b.Append(render.Event{Raw: s})
	}
	snap, gen := b.Snapshot(2)
	if gen != b.Gen() {
		t.Fatalf("snapshot gen %d != Gen() %d", gen, b.Gen())
	}
	if len(snap) != 2 || snap[0].Raw != "c" || snap[1].Raw != "d" {
		t.Fatalf("Snapshot(2) = %v, want [c d]", rawsOf(snap))
	}
	all, _ := b.Snapshot(0)
	if len(all) != 4 {
		t.Fatalf("Snapshot(0) len = %d, want 4 (all)", len(all))
	}
}

func rawsOf(es []*Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Raw
	}
	return out
}

func TestSnapshotIsStableAcrossConcurrentAppend(t *testing.T) {
	b := New(1000, func(ev render.Event) []Line { return []Line{{Text: ev.Raw}} })
	for i := 0; i < 50; i++ {
		b.Append(render.Event{Raw: "x"})
	}
	done := make(chan struct{})
	go func() {
		for i := 0; i < 500; i++ {
			b.Append(render.Event{Raw: "y"})
		}
		close(done)
	}()
	for i := 0; i < 500; i++ {
		snap, _ := b.Snapshot(100)
		for _, e := range snap { // read fields — must not race/panic
			_ = e.Raw
			_ = e.Lines
		}
	}
	<-done
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/linebuf/ -run 'TestGen|TestSnapshot' -v`
Expected: FAIL — `b.Gen` / `b.Snapshot` undefined.

- [ ] **Step 3: Implement `gen`, `Gen()`, `Snapshot(limit)`**

In `internal/linebuf/linebuf.go`, add a `gen uint64` field to `Buffer` (after `seq uint64`):

```go
	seq       uint64
	gen       uint64
```

In `Append`, before `return id`, add `b.gen++` (covers append + eviction, both under the write lock). In `Rerender`, before `b.dirty = true`, add `b.gen++`.

Add the two methods (near `Recent`):

```go
// Gen returns a counter that increments on every change to the entry set or
// contents (Append, eviction, Rerender). Cheap to poll for change-detection.
func (b *Buffer) Gen() uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.gen
}

// Snapshot returns a copy of the last `limit` entry pointers (limit <= 0 = all)
// and the current gen, taken atomically under the read lock. Entries are
// immutable (Rerender replaces, never mutates), so the returned pointers are
// safe to read after the lock is released.
func (b *Buffer) Snapshot(limit int) ([]*Entry, uint64) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	start := 0
	if limit > 0 && limit < len(b.entries) {
		start = len(b.entries) - limit
	}
	out := make([]*Entry, len(b.entries)-start)
	copy(out, b.entries[start:])
	return out, b.gen
}
```

- [ ] **Step 4: Run tests to verify they pass + race**

Run: `go test ./internal/linebuf/ -run 'TestGen|TestSnapshot' -v && go test -race ./internal/linebuf/`
Expected: PASS; no race.

- [ ] **Step 5: Commit**

```bash
git add internal/linebuf/linebuf.go internal/linebuf/linebuf_test.go
git commit -m "feat(linebuf): add gen counter + bounded Snapshot(limit)"
```

---

## Task 2: TUI sources records from `linebuf` (delete `m.entries`)

This is the core task. The model gains the shared buffer, a display cache, and a reconcile; the four readers source from the snapshot; `m.entries` is deleted. Existing tests stay green because `appendEvent`/`appendStored` become `buf.Append + reconcile` shims (linebuf assigns `L0,L1,…`, matching the tests' seeded IDs).

**Files:** Modify `internal/tui/app.go`, `internal/tui/copyref.go`, `internal/tui/copytext.go`.

- [ ] **Step 1: Add the buffer + cache fields; construct an owned buffer when none is supplied**

In `tui.Options` (app.go ~line 120) add:

```go
	Buffer            *linebuf.Buffer        // shared record store (TUI mode); tests/standalone get an owned one
```

In the `model` struct: **delete** `entries []scrollbackEvent` and add:

```go
	buf          *linebuf.Buffer
	displayCache map[string][]displayLine // entry ID → its display rows
	lastGen      uint64
	prevIDLines  map[string]int           // entry ID → row count at last reconcile (for eviction drag)
```

In `newModel(scrollback int)` initialize the maps:

```go
	displayCache: map[string][]displayLine{},
	prevIDLines:  map[string]int{},
```

In `New(opts Options)`: after creating `m`, wire the buffer (owned fallback keeps the model standalone-testable):

```go
	if opts.Buffer != nil {
		m.buf = opts.Buffer
	} else {
		m.buf = linebuf.New(scrollback, func(ev render.Event) []linebuf.Line {
			rows := render.DecomposeLines(ev)
			out := make([]linebuf.Line, len(rows))
			for i, r := range rows {
				out[i] = linebuf.Line{Text: r.Text, IsCont: r.IsCont}
			}
			return out
		})
	}
```

Add the import `"github.com/homeend/log-listener/internal/linebuf"` to app.go if not present.

- [ ] **Step 2: Add the pure-transform row builder + reconcile**

Add to app.go:

```go
// displayLinesFromEntry builds the TUI display rows for a linebuf entry as a
// pure transform — no re-render. Mirrors decomposeEvent but reads the already-
// decomposed Lines (Text/IsCont) and basename File the entry already holds.
func displayLinesFromEntry(e *linebuf.Entry) []displayLine {
	out := make([]displayLine, 0, len(e.Lines))
	for _, ln := range e.Lines {
		body := ln.Text
		if ln.IsCont {
			body = dimStyle.Render(ln.Text)
		}
		out = append(out, displayLine{
			group:     e.Group,
			file:      e.File,
			body:      body,
			bodyWidth: dispWidth(ln.Text),
			isBlock:   ln.IsCont,
		})
	}
	return out
}

// reconcile pulls a bounded snapshot from the shared buffer and rebuilds
// m.lines + the ID-keyed display cache, keeping only the tail that fits the
// scrollback line window. View-state indices are dragged down by however many
// head rows were evicted since the last reconcile (matching old trimToCap).
func (m *model) reconcile() {
	if m.buf == nil {
		return
	}
	if g := m.buf.Gen(); g == m.lastGen && m.lines != nil {
		return // coalesce: nothing changed
	}
	snap, gen := m.buf.Snapshot(m.scrollback)

	// Build/reuse display rows per entry; compute the tail that fits scrollback.
	type built struct {
		id    string
		lines []displayLine
	}
	rebuilt := make([]built, len(snap))
	total := 0
	for i, e := range snap {
		dls, ok := m.displayCache[e.ID]
		if !ok {
			dls = displayLinesFromEntry(e)
			m.displayCache[e.ID] = dls
		}
		rebuilt[i] = built{id: e.ID, lines: dls}
		total += len(dls)
	}
	// Trim the HEAD so total display rows <= scrollback (the TUI line window).
	startEntry := 0
	for m.scrollback > 0 && total > m.scrollback && startEntry < len(rebuilt) {
		total -= len(rebuilt[startEntry].lines)
		startEntry++
	}

	// Eviction drag: count head rows present last reconcile but gone now.
	present := make(map[string]struct{}, len(rebuilt)-startEntry)
	for _, b := range rebuilt[startEntry:] {
		present[b.id] = struct{}{}
	}
	dropped := 0
	for id, n := range m.prevIDLines {
		if _, keep := present[id]; !keep {
			dropped += n
		}
	}

	// Rebuild m.lines and prune the cache to the visible window.
	flat := make([]displayLine, 0, total)
	newPrev := make(map[string]int, len(rebuilt)-startEntry)
	for _, b := range rebuilt[startEntry:] {
		flat = append(flat, b.lines...)
		newPrev[b.id] = len(b.lines)
	}
	for id := range m.displayCache {
		if _, keep := newPrev[id]; !keep {
			delete(m.displayCache, id)
		}
	}
	m.lines = flat
	m.prevIDLines = newPrev
	m.lastGen = gen
	m.blocksDirty = true

	if !m.tailMode && dropped > 0 {
		m.dragViewStateDown(dropped)
	}
}
```

- [ ] **Step 3: Extract the index-drag from `trimToCap` into `dragViewStateDown`**

Port the exact `streamTop`/`searchHit`/`visualCursor`/`visualAnchor` adjustments from the current `trimToCap` body into:

```go
// dragViewStateDown shifts absolute m.lines indices down by `dropped` rows when
// head entries were evicted, preserving today's trimToCap semantics (clamp at
// 0; unset visualAnchor when it scrolls off).
func (m *model) dragViewStateDown(dropped int) {
	m.streamTop -= dropped
	if m.streamTop < 0 {
		m.streamTop = 0
	}
	if m.searchHit >= 0 {
		m.searchHit -= dropped
		if m.searchHit < 0 {
			m.searchHit = -1
		}
	}
	if m.visualMode {
		m.visualCursor -= dropped
		if m.visualCursor < 0 {
			m.visualCursor = 0
		}
		if m.visualAnchor >= 0 {
			m.visualAnchor -= dropped
			if m.visualAnchor < 0 {
				m.visualAnchor = -1
			}
		}
	}
}
```

(Copy the precise clamp/unset rules from the existing `trimToCap` — match them exactly so `TestVisualIndicesClampOnEviction` still passes.)

- [ ] **Step 4: Turn `appendEvent`/`appendStored` into buf-append+reconcile shims; delete `trimToCap`**

Replace `appendEvent` and `appendStored` bodies (keep names — tests use them):

```go
// appendEvent appends an event to the shared buffer and reconciles. In
// production the pump appends to the buffer and sends BufChangedMsg; this shim
// is the seed/test path (and any in-model append) that does both.
func (m *model) appendEvent(ev render.Event) {
	if m.buf == nil {
		return
	}
	m.buf.Append(ev)
	m.reconcile()
}

// appendStored seeds a pre-built event by raw text (tests). It routes through
// the buffer so the model stays single-sourced.
func (m *model) appendStored(e scrollbackEvent) {
	m.buf.Append(render.Event{ID: e.id, Group: e.group, File: e.file, Raw: e.raw})
	m.reconcile()
}
```

Delete the `trimToCap` function (its line-cap role is now in `reconcile`, its drag in `dragViewStateDown`).

Note for the implementer: `scrollbackEvent` may now be unused except as the `appendStored` parameter shape; if it becomes entirely unused, delete it and change `appendStored` to take `(id, group, file, raw string)`. Verify with the compiler.

- [ ] **Step 5: Switch the production input path — `Push`→`NotifyChanged`, `EventMsg`→`BufChangedMsg`**

In app.go, replace `App.Push` with:

```go
// NotifyChanged tells the TUI the shared buffer changed; the model reconciles
// from it on the bubbletea goroutine. Replaces the old Push(ev) — the pump now
// appends to the buffer itself, then calls this.
func (a *App) NotifyChanged() {
	a.mu.Lock()
	if a.done {
		a.mu.Unlock()
		return
	}
	prog := a.prog
	a.mu.Unlock()
	prog.Send(BufChangedMsg{})
}
```

Rename the `EventMsg` type to `BufChangedMsg` (drop its `Event` field — it carries nothing):

```go
type BufChangedMsg struct{}
```

In `Update`, replace:

```go
	case EventMsg:
		m.appendEvent(msg.Event)
```

with:

```go
	case BufChangedMsg:
		m.reconcile()
```

- [ ] **Step 6: Reimplement `reRenderAll` via `buf.Rerender` + cache clear**

Replace `reRenderAll`:

```go
// reRenderAll re-renders the shared buffer under the current pipeline, clears
// the display cache (existing IDs now have new Lines), and reconciles. After
// this, MCP sees the same rendering the TUI shows.
func (m *model) reRenderAll() {
	if m.renderFn == nil || m.buf == nil {
		return
	}
	m.buf.Rerender(func(g, f, raw string) (render.Event, bool) {
		return m.renderFn(g, f, raw)
	})
	m.displayCache = map[string][]displayLine{}
	m.reconcile()
}
```

(Then prune any now-dead clamp code that followed the old `reRenderAll` body if it duplicated reconcile's clamping — keep behavior identical; verify against tests.)

- [ ] **Step 7: Re-source the three readers from the snapshot**

`filteredIndices` (app.go): iterate a snapshot instead of `m.entries`. Replace `for _, e := range m.entries { n := len(e.lines); ... for _, dl := range e.lines {` with a walk over `m.buf.Snapshot(m.scrollback)` building each entry's rows via `displayLinesFromEntry` (or reuse `m.displayCache[e.ID]`), keeping the same `off`/index accumulation against `m.lines`. The function must still return absolute `m.lines` indices, so walk the SAME windowed entry set `reconcile` used — reuse `m.prevIDLines` order via the snapshot tail. Concretely: take `snap, _ := m.buf.Snapshot(m.scrollback)`, drop the head not in `m.prevIDLines`, then iterate the kept entries using `m.displayCache[id]` for their rows.

`copyref.go` and `copytext.go`: replace `for _, e := range m.entries` with a walk over the same windowed snapshot (`m.buf.Snapshot(m.scrollback)` filtered to `m.prevIDLines` keys), using `e.ID`/`m.displayCache[e.ID]` where the old code used `e.id`/`e.lines`. Keep the selection/walk logic identical.

Add a small helper to avoid duplication:

```go
// visibleEntries returns the snapshot entries currently in the display window,
// in order (those whose rows are in m.lines).
func (m *model) visibleEntries() []*linebuf.Entry {
	snap, _ := m.buf.Snapshot(m.scrollback)
	out := snap[:0:0]
	for _, e := range snap {
		if _, ok := m.prevIDLines[e.ID]; ok {
			out = append(out, e)
		}
	}
	return out
}
```

Use `m.visibleEntries()` in `filteredIndices`, `copyref.go`, and `copytext.go`, sourcing rows from `m.displayCache[e.ID]`.

- [ ] **Step 8: Handle the `m.entries = nil` clear (Clear action) and `applyReload`**

The `ActionClear` handler (app.go ~555) does `m.entries = nil`. Replace with a buffer-aware clear: it must empty what the TUI shows without wiping MCP's buffer unexpectedly — preserve today's semantics (Clear empties the in-memory view). Set `m.displayCache = map[string][]displayLine{}`, `m.prevIDLines = map[string]int{}`, `m.lines = nil`, and record a "cleared floor" so reconcile shows only entries appended after the clear. Simplest faithful approach: add `clearedAfterID string` set to the latest entry ID at clear time; in `reconcile`, skip snapshot entries with `Seq <= clearedSeq`. Add `clearedSeq uint64` (0 = none); on Clear set it to the last snapshot entry's `Seq`; in the reconcile entry loop, `if e.Seq <= m.clearedSeq { continue }` (use `> 0` guard). This keeps Clear a TUI-only view reset, matching today.

`applyReload`: it calls `reRenderAll` (now buffer-backed) — confirm it still works; remove any direct `m.entries` access in `applyReload`.

- [ ] **Step 9: Build, run the full TUI suite (the regression net)**

Run: `go build ./... && go test ./internal/tui/ && go vet ./...`
Expected: PASS. Existing tests seed via `appendEvent`/`seedVisual` → now route through the owned buffer and reconcile; assertions on `m.lines`, search, copy, visual selection, and `TestVisualIndicesClampOnEviction` must hold. If a test set a custom non-sequential `ev.ID`, it will now get a buffer-assigned `L<seq>` — fix those few tests to use the buffer-assigned IDs (they already expect `L0,L1,…`).

- [ ] **Step 10: Commit**

```bash
git add internal/tui/app.go internal/tui/copyref.go internal/tui/copytext.go
git commit -m "feat(tui): source records from shared linebuf; delete m.entries"
```

---

## Task 3: `main.go` wiring — pass the shared buffer, switch the pump

**Files:** Modify `main.go`.

- [ ] **Step 1: Pass the shared buffer into the TUI and switch the pump to notify**

In `runWatchTUI` (main.go), in the `tui.New(tui.Options{…})` call, add `Buffer: buf,`. Then change the pump loop body from:

```go
				rev.ID = buf.Append(rev)
				app.Push(rev)
				fanout.Emit(rev)
```

to:

```go
				rev.ID = buf.Append(rev)
				app.NotifyChanged()
				fanout.Emit(rev)
```

- [ ] **Step 2: Route preload through the buffer instead of `InitialEvents`**

`main.go` already appends preload events to `buf` (the preload loop assigns `buf.Append`). Confirm: if `InitialEvents: preloadEvents` is still passed to `tui.New` and the model also reconciles from `buf` (which already contains the preload entries via the startup `buf.Append`), the preload would be double-counted. Fix: **remove** `InitialEvents: preloadEvents` from the `tui.New` options in `runWatchTUI` (the buffer already holds them; the model's first `reconcile` shows them). Verify the model takes an initial reconcile — add an `Init`-time or first-`BufChangedMsg` reconcile: send one `app.NotifyChanged()` right after `tui.New`/before/at `Run` start so the seeded buffer renders. (If `tui.New` already snapshots on construction via `reconcile`, this is redundant — verify and keep exactly one initial reconcile.)

- [ ] **Step 3: Verify the TUI `InitialEvents` path is removed cleanly**

If `Options.InitialEvents` is now unused everywhere, delete the field and the code that consumed it in `tui.New`. If other call sites (tests) use it, keep it but make `New` seed it through `m.buf.Append` + reconcile (consistent with the shim). Verify with the compiler.

- [ ] **Step 4: Full verification (default + race + e2e)**

Run: `go test ./... && go vet ./... && go test -race ./...`
Expected: all green. Pay attention to the TUI e2e tests (`e2e_tui_test.go`, `e2e_mcp_tui_test.go`) — they drive the real binary; the on-screen output and MCP `get_viewport` must be unchanged.

- [ ] **Step 5: Commit**

```bash
git add main.go internal/tui/app.go
git commit -m "feat(main): wire TUI to the shared linebuf; pump notifies instead of pushing"
```

---

## Task 4: Foundation tests — concurrency, coalescing, reconcile parity

**Files:** Create/extend `internal/tui/reconcile_test.go`.

- [ ] **Step 1: Write reconcile + coalescing + concurrency tests**

```go
package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/linebuf"
	"github.com/homeend/log-listener/internal/render"
)

func newBufModel(t *testing.T, cap int) *model {
	t.Helper()
	m := newModel(cap)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	return m
}

func TestReconcileBuildsLinesFromBuffer(t *testing.T) {
	m := newBufModel(t, 100)
	for _, s := range []string{"a", "b", "c"} {
		m.buf.Append(render.Event{Group: "g", File: "/x.log",
			Rendered: []render.Part{{Type: "text", Value: s}}})
	}
	m.reconcile()
	if len(m.lines) != 3 {
		t.Fatalf("m.lines = %d, want 3", len(m.lines))
	}
}

func TestReconcileCoalescesWhenGenUnchanged(t *testing.T) {
	m := newBufModel(t, 100)
	m.buf.Append(render.Event{Group: "g", File: "/x.log",
		Rendered: []render.Part{{Type: "text", Value: "a"}}})
	m.reconcile()
	// Mutate the cache to a sentinel; a no-op reconcile must NOT rebuild it.
	before := len(m.lines)
	m.reconcile() // gen unchanged → early return
	if len(m.lines) != before {
		t.Fatalf("coalesced reconcile changed m.lines: %d != %d", len(m.lines), before)
	}
}

func TestReconcileReusesCacheForExistingIDs(t *testing.T) {
	m := newBufModel(t, 100)
	m.buf.Append(render.Event{Group: "g", File: "/x.log",
		Rendered: []render.Part{{Type: "text", Value: "a"}}})
	m.reconcile()
	cached := m.displayCache["L0"]
	m.buf.Append(render.Event{Group: "g", File: "/x.log",
		Rendered: []render.Part{{Type: "text", Value: "b"}}})
	m.reconcile()
	if &cached[0] != &m.displayCache["L0"][0] {
		t.Fatal("existing entry's cached display rows were rebuilt (should be reused)")
	}
}

func TestReconcileEvictionDragsViewState(t *testing.T) {
	m := newBufModel(t, 3) // 3-line window
	m.tailMode = false
	m.streamTop = 0
	for _, s := range []string{"a", "b", "c"} {
		m.buf.Append(render.Event{Group: "g", File: "/x.log",
			Rendered: []render.Part{{Type: "text", Value: s}}})
	}
	m.reconcile()
	m.streamTop = 2
	m.buf.Append(render.Event{Group: "g", File: "/x.log",
		Rendered: []render.Part{{Type: "text", Value: "d"}}})
	m.reconcile() // window slides by 1 → streamTop dragged to 1
	if m.streamTop != 1 {
		t.Fatalf("streamTop = %d, want 1 (dragged by 1 evicted row)", m.streamTop)
	}
}
```

Adjust expected IDs/counts to the real decomposition if a renderer wraps lines; the implementer verifies against actual output.

- [ ] **Step 2: Run + race**

Run: `go test ./internal/tui/ -run TestReconcile -v && go test -race ./internal/tui/`
Expected: PASS; no race.

- [ ] **Step 3: Commit**

```bash
git add internal/tui/reconcile_test.go
git commit -m "test(tui): reconcile build/coalesce/cache-reuse/eviction-drag"
```

---

## Self-Review

**1. Spec coverage:**
- linebuf `gen`/`Gen()`/`Snapshot(limit)` → Task 1. ✓
- TUI holds shared buffer; `Push`→`NotifyChanged`/`BufChangedMsg` → Task 2 S1,S5; Task 3 S1. ✓
- Reconcile + ID-keyed cache + pure transform from `Entry.Lines`; `m.entries` deleted → Task 2 S1,S2,S4. ✓
- Cap = TUI display-line window during reconcile → Task 2 S2 (head-trim loop). ✓
- View-state index parity (drag on eviction) → Task 2 S3; Task 4 S1. ✓
- Four readers re-sourced (reRenderAll via buf.Rerender+clear; filter/copyref/copytext via snapshot) → Task 2 S6,S7. ✓
- Renderer toggle re-renders linebuf (MCP consistency) → Task 2 S6. ✓
- Coalescing via gen → Task 2 S2; Task 4 S1. ✓
- Preload via buffer, not InitialEvents → Task 3 S2,S3. ✓
- Test harness preserved via appendEvent shim → Task 2 S4,S9. ✓
- Concurrency/race + reconcile parity tests → Task 1 S1; Task 4 S1. ✓
- Non-goals (search still on m.lines; view-state stays index-based) respected — no search/linebuf delegation here. ✓

**2. Placeholder scan:** The reader re-sourcing (Task 2 S7) and Clear handling (S8) describe the transform precisely (which loop, which fields, the `visibleEntries` helper) rather than giving full final bodies, because they are mechanical edits of existing loops whose exact surrounding code the implementer reads in place; the helper and field substitutions are fully specified. No TBD/"handle later".

**3. Type consistency:** `Snapshot(limit) ([]*Entry, uint64)`, `Gen() uint64`, `displayLinesFromEntry(*linebuf.Entry) []displayLine`, `reconcile()`, `dragViewStateDown(int)`, `visibleEntries() []*linebuf.Entry`, `BufChangedMsg{}`, `NotifyChanged()` — used consistently across tasks. `displayCache map[string][]displayLine`, `prevIDLines map[string]int`, `lastGen uint64`, `clearedSeq uint64` named consistently.
