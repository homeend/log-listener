# TUI on the Shared `linebuf` Model — Foundation (slice 5-1) — Design

**Date:** 2026-06-08
**Status:** Approved (design)
**Scope:** `internal/linebuf`, `internal/tui`, `main.go` (TUI wiring), TUI tests.

## Context

This is **slice 5-1**, the foundation of cycle #5 ("TUI on the shared model")
in the plugin architecture roadmap (see [[plugin-architecture-roadmap]]). Cycle
#5 was decomposed because it is the largest/riskiest cycle:

- **5-1 (this spec):** TUI stops keeping its own duplicate record store and
  sources source-records from `linebuf` via a per-frame-stable snapshot.
- **5-2 (later):** TUI search/nav delegate to `linebuf` ops → identical results
  to MCP.
- **5-3 (later):** re-express TUI view-state in stable IDs (not slice indices).

### Why this is needed / current state

Today the TUI keeps `m.entries []scrollbackEvent` (source records) **and**
`linebuf` holds the same records (fed at fan-out for MCP). They are parallel,
independently-maintained copies. The TUI never reads `linebuf`; it has only a
`setViewport` callback. `app.Push(ev)` sends an `EventMsg` through bubbletea's
message loop, so `m.entries`/`m.lines` mutate on a single goroutine (no locks).

`m.lines []displayLine` is the TUI's **display layer** (wrapped rows, columns,
highlight) derived from `m.entries`. It is legitimately TUI-only and **stays**.
Only the duplicate *source-record* store (`m.entries`) is removed by this slice.

### Two facts that shape the design (verified)

1. **`linebuf` entries are effectively immutable (copy-on-write).** `Rerender`
   does `ne := *e; ne.Lines = …; b.entries[i] = &ne` — it replaces the slot,
   never mutates an entry in place. `Append` only appends/evicts from the ends.
   Therefore a snapshot that returns the current entry **pointer slice** is safe
   to read after releasing the lock; no deep value-copy is required.
2. **`m.entries` has real reach.** It is read by `appendEvent`, `trimToCap`,
   `reRenderAll` (renderer toggle), the filter ("show only matching"),
   `copyref.go`, and `copytext.go`. Deleting it touches all of these. Existing
   TUI tests also seed a buf-less model via `appendEvent`, so the test harness
   migrates too.

## Chosen approach

**Approach 1 — snapshot-on-change + ID-keyed display cache.** The pump notifies
the TUI of buffer changes; the TUI pulls a snapshot on its own (bubbletea)
goroutine and reconciles a display cache keyed by entry ID. `linebuf` is
authoritative for records; `m.entries` is deleted.

Rejected: Approach 2 (keep a mirror — fails the goal of sourcing from linebuf);
Approach 3 (windowed per-frame pull, no TUI store — pulls search/blocks rework,
i.e. 5-2, into this slice; highest risk).

## Design

### 1. `linebuf`: snapshot accessor + generation counter

- Add `gen uint64` to `Buffer`, incremented under the write lock on every
  mutation that changes the entry set or contents: `Append` (incl. eviction)
  and `Rerender`.
- `Snapshot(limit int) (entries []*Entry, gen uint64)` — under `RLock`, returns
  a copy of the last `limit` entry pointers (`limit <= 0` = all) and the current
  `gen`. Safe because entries are immutable (see fact 1). The TUI calls
  `Snapshot(scrollback)`, which bounds reconcile work to O(scrollback): since
  every entry is ≥ 1 display row, the last `scrollback` *entries* always contain
  at least the entries needed to fill a `scrollback`-*row* window.
- `Gen() uint64` — under `RLock`, returns the current `gen` for cheap
  change-detection (burst coalescing).

These are additive; existing `linebuf` behavior and the MCP read path are
unchanged.

### 2. TUI holds the shared buffer; `Push` becomes a notification

- `tui.Options` gains `Buffer *linebuf.Buffer` — the **same** instance the pump
  feeds and MCP reads. `main.go`'s `runWatchTUI` passes `buf` into `tui.New`.
- `App.Push(ev render.Event)` is replaced by `App.NotifyChanged()`, which sends
  a lightweight `BufChangedMsg{}` via `prog.Send`. The pump loop becomes:
  `rev.ID = buf.Append(rev); app.NotifyChanged(); fanout.Emit(rev)` — the record
  is in `linebuf` before the notify.
- Preload seeding: instead of `InitialEvents` appended into `m.entries`, the
  preload events are appended to `buf` before `tui.New` (as the pump would), and
  the model takes its initial snapshot on first render. (`main.go` already
  appends preload events to `buf` via `buf.Append` in the preload loop; the TUI
  just snapshots them.)

### 3. Reconcile + ID-keyed display cache; delete `m.entries`

Model changes:
- **Delete** `entries []scrollbackEvent`.
- **Add** `displayCache map[string][]displayLine` (entry ID → its display rows)
  and `lastGen uint64`.
- Keep `lines []displayLine` (the flat display cache for the hot path).

On `BufChangedMsg` (bubbletea goroutine):
1. If `buf.Gen() == lastGen`, return (coalesces bursts — many queued
   `BufChangedMsg` collapse to one reconcile).
2. `snap, gen := buf.Snapshot(scrollback)` (the last `scrollback` entries).
3. For each entry in `snap` in order: reuse `displayCache[id]` if present, else
   `decomposeEvent(entry.Event)` and store it. (The cache key is the entry ID;
   on append, only new IDs are decomposed — O(new lines), not O(buffer).)
4. Delete `displayCache` keys for IDs not in `snap` (evicted).
5. Rebuild `m.lines` by concatenating `displayCache[id]` across `snap` order,
   keeping only the **tail** whose cumulative display rows ≤ `scrollback` (the
   TUI's display-line window — see the cap note below). Drop `displayCache` keys
   for entries that fall outside this window.
6. Apply view-state eviction adjustment (section 5), set `lastGen = gen`, mark
   `blocksDirty = true`.

`appendEvent`/`appendStored`/`trimToCap` are removed; their roles move into the
reconcile.

**Cap note — the TUI keeps its display-line window; `linebuf` cannot
under-supply.** `linebuf` caps by **entry count** (`bufCap`), the TUI by
**display-line count** (`scrollback`); `main` derives both from
`cfg.TUIScrollback` (default 10000), but the units differ — one multi-line
entry is 1 linebuf entry yet several TUI rows. So the TUI continues to enforce
its own line cap during reconcile (keep the snapshot tail fitting in
`scrollback` rows), exactly as `trimToCap` did. Because display rows ≥ entries
(every entry is ≥ 1 row), `linebuf`'s entry window always contains at least as
many entries as the TUI's line window needs — so `linebuf` never evicts an entry
the TUI still wants to show. `tui.Options.Scrollback` therefore remains the
authoritative TUI display cap (unchanged), and `linebuf`'s capacity is
independent.

### 4. The four readers re-sourced

- `reRenderAll` (renderer toggle): clears `displayCache`, re-decomposes from the
  snapshot (entries' `Event` reflect the toggle via the model's `RenderFn`, or
  via `buf.Rerender` on reload), rebuilds `m.lines`.
- Filter ("show only matching"): iterates the snapshot instead of `m.entries`.
- `copyref.go` / `copytext.go`: their entry walks iterate the snapshot instead
  of `m.entries`.

Reload path (`Reload` / config reload): `buf.Rerender` already re-renders
`linebuf`; the TUI clears `displayCache` and reconciles, so the displayed text
follows the new renderers. (Today the TUI and `linebuf` re-render independently;
after this slice the TUI follows `linebuf`'s re-render — one source of truth.)

### 5. View-state parity (behavior-identical; IDs deferred to 5-3)

`streamTop`, `searchHit`, `visualCursor`, `visualAnchor` remain **absolute
`m.lines` indices**. On reconcile, when head entries were evicted, the model
computes `droppedLines` = total display rows of the head IDs that were present
last reconcile but are absent now, and drags those indices down by `droppedLines`
— exactly the adjustment `trimToCap` performs today (including the clamp/unset
rules for `visualAnchor`). `tailMode` behavior is unchanged (the view sticks to
the bottom; appends don't move `streamTop`). This preserves current behavior
byte-for-byte; re-expressing view-state as stable IDs is **slice 5-3**.

### 6. Test harness migration

TUI tests currently call `m.appendEvent(render.Event{…})` / `seedVisual` on a
buf-less model. They migrate to a shared helper that appends to a real
`linebuf.Buffer` and reconciles (the path real code takes). `newModel` / `tui.New`
gain the buffer; a test helper `seed(m, events…)` does `buf.Append` + reconcile
so existing assertions on `m.lines`, view-state, search, copy, and visual
selection keep working against the buf-sourced model.

## Non-goals (explicitly deferred)

- **Search still scans `m.lines`** this slice. Delegating TUI search to
  `linebuf.Search` is **slice 5-2**.
- **View-state stays index-based.** Stable-ID view-state is **slice 5-3**.
- No change to display formatting, wrapping, columns, highlight, blocks
  semantics, or what MCP returns.
- No change to the non-TUI (stdout/SSE/file) paths.

## Testing

- **Concurrency:** `go test -race` on a scenario where the pump appends to `buf`
  concurrently while the model snapshots/reconciles — no race, stable frames.
- **Reconcile correctness:** appending N events yields `m.lines` identical to the
  pre-refactor `appendEvent` result; only new IDs are decomposed (cache reuse
  asserted).
- **Coalescing:** multiple `BufChangedMsg` with no gen change cause one reconcile.
- **Eviction parity:** with a small cap, head eviction drops the right entries and
  drags `streamTop`/`searchHit`/`visualCursor`/`visualAnchor` exactly as the old
  `trimToCap` (port/keep the existing eviction tests, e.g.
  `TestVisualIndicesClampOnEviction`).
- **Re-render parity:** renderer toggle / reload re-decomposes from the snapshot
  and `m.lines` matches the old `reRenderAll`.
- **Reader parity:** filter, `copyref`, `copytext` produce identical results
  sourced from the snapshot (existing tests pass under the migrated harness).
- All existing TUI + e2e tests green; `go vet`; `go test -race ./...`.

## Success criteria

- `m.entries` is gone; the TUI sources records from the shared `linebuf` via
  `Snapshot()`; `m.lines` (display layer) and all current display behavior are
  unchanged.
- `linebuf` has `Snapshot()`/`Gen()` + a generation counter; entries remain
  immutable; MCP read path unaffected.
- The TUI and MCP now read the **same** record store (one source of truth for
  records); no second independent record copy in the TUI.
- Behavior is user-visibly identical (display, scrolling, search, copy, visual
  selection, eviction, reload) — verified by the migrated/extended test suite.
- `go test ./...`, `go vet ./...`, `go test -race ./...` green.
