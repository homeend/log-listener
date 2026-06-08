# TUI Viewport / Selection Operations Layer (slice 5-3, pivoted) — Design

**Date:** 2026-06-08
**Status:** Draft — design agreed in brainstorming; pending user review of this written spec.
**Scope:** `internal/tui` — extract scattered viewport/selection index arithmetic into a small set of intent-level methods. No storage-representation change.

## Context

Slice **5-3** of cycle #5 (TUI on the shared model — see
[[plugin-architecture-roadmap]]). The original 5-3 proposed re-expressing the
four view-state values (`streamTop`, `searchHit`, `visualCursor`,
`visualAnchor`) as stable `(entryID, rowOffset)` anchors. A call-site inventory
showed that path touches ~248 sites and, crucially, **relocates** complexity —
every `m.streamTop` read becomes an `m.streamTopRow()` accessor call that still
returns an int the caller does arithmetic on. That is not the abstraction the
user asked for ("make it easier to compose, not use low-level calls
everywhere"), and the bug that motivated the stable-ID flip (a 5-1 TOCTOU) is
already fixed. That spec is now SUPERSEDED/DEFERRED
(`2026-06-08-tui-viewstate-stable-ids-design.md`).

This pivot delivers the actual composability win: the inventory surfaced **two
genuine repetition clusters** where the *same low-level arithmetic is written
out repeatedly*. Wrapping those in intent-level methods reduces call-site
complexity measurably, where the user wanted it. Storage stays `int`;
`dragViewStateDown` stays. This is a pure operations-extraction refactor.

## What the inventory found (the justification)

Production call sites bucketed by operation (excluding `dragViewStateDown` and
unrelated single reads):

- **Scroll arithmetic — 6 sites, one repeated shape.** `ActionScrollUp/PageUp/
  FastUp` each do `unstickFromTail(); streamTop -= step; clamp≥0`
  (`app.go:624–628, 651–654, 680–683`). `ActionScrollDown/PageDown/FastDown`
  each do `if !tailMode { streamTop += step; maybeReStick() }`
  (`app.go:638–639, 668–669, 696–697`). The up/down asymmetry (up unsticks +
  clamps to 0; down is tail-gated + re-sticks on catch-up) is currently
  *implicit*, duplicated across all six. **→ `scrollBy(delta)`.**
- **Selection-bounds idiom — 3 verbatim copies.** `lo, hi := m.visualAnchor,
  m.visualCursor; if lo > hi { lo, hi = hi, lo }` (guarded by
  `visualAnchor >= 0`, falling back to the caret row) appears in `visualBar`
  (`visual.go:32–39`), `buildVisualText` (`visual.go:93–99`), and
  `buildVisualRef` (`visual.go:116–122`). **→ `selectionBounds() (lo, hi int)`.**
- **Visual cursor move — 2 sites.** `if cursor > 0 { cursor-- };
  ensureVisualVisible()` and the `< len-1` mirror (`visual.go:169–177`).
  **→ `moveVisualCursor(delta)`.**

**Deliberately NOT extracted (YAGNI — single-caller or already a method):**
- `centerOn` (jump-to-hit centering, `search.go:231–261`) — one caller
  (`jumpToHit`); extracting just moves code without reducing call sites.
- `scrollToTop` (`tailMode=false; streamTop=0`, `app.go:704–705`) — one caller
  (`ActionTop`); a 1-site wrapper is not a reduction.
- `ensureVisible` — **already exists** as `ensureVisualVisible()`
  (`visual.go:74–87`). Left as-is (it is `moveVisualCursor`'s helper).
- `searchHit` sites — mostly `idx == m.searchHit` / `searchHit >= 0` equality
  markers; an `isSearchHit(idx)` wrapper adds a name without reducing
  composition.

This keeps the layer honest: only the two real clusters (plus the minor cursor
move) become methods. The baseline to beat is "scattered inline arithmetic";
each new method must be visibly simpler **at the call sites**, not just relocate
code.

## The operations

All are methods on `*model` in `internal/tui`. Behavior is byte-for-byte the
current behavior — this is extraction, not redesign.

### `scrollBy(delta int)` — new file `internal/tui/viewport.go`

```go
// scrollBy moves the browse viewport by delta display rows: negative scrolls up
// (toward older lines), positive scrolls down (toward newer). It owns the
// up/down asymmetry that was previously duplicated across the six scroll
// actions:
//   - Up (delta < 0): leave tail mode (unstickFromTail) and clamp at the top
//     (streamTop never goes below 0).
//   - Down (delta > 0): only meaningful while browsing (tail mode ignores it,
//     since the viewport is already pinned to the bottom); after moving, let
//     maybeReStick re-enter tail mode if the view caught up to the latest line.
// delta == 0 is a no-op.
func (m *model) scrollBy(delta int) {
	switch {
	case delta < 0:
		m.unstickFromTail()
		m.streamTop += delta
		if m.streamTop < 0 {
			m.streamTop = 0
		}
	case delta > 0:
		if m.tailMode {
			return
		}
		m.streamTop += delta
		m.maybeReStick()
	}
}
```

Call sites (the inner streamTop manipulation only; the surrounding
`showFiles`/`matcher` branches in each action stay put):
- `ActionScrollUp` else-branch → `m.scrollBy(-1)`
- `ActionScrollDown` else-branch → `m.scrollBy(1)` (the `!tailMode` guard moves
  inside `scrollBy`, so the handler drops its own `else if !m.tailMode`)
- `ActionPageUp` else-branch → `m.scrollBy(-page)`
- `ActionPageDown` else-branch → `m.scrollBy(page)`
- `ActionFastUp` else-branch → `m.scrollBy(-vertFastStep)`
- `ActionFastDown` else-branch → `m.scrollBy(vertFastStep)`

### `selectionBounds() (lo, hi int)` — in `internal/tui/visual.go`

```go
// selectionBounds returns the inclusive [lo, hi] row span of the current visual
// selection. With an anchor set it is the ordered (anchor, cursor) pair; with
// no anchor (visualAnchor < 0) it is the caret row alone (lo == hi ==
// visualCursor). Centralizes the order-the-pair idiom previously copied in
// visualBar, buildVisualText, and buildVisualRef.
func (m *model) selectionBounds() (lo, hi int) {
	lo, hi = m.visualCursor, m.visualCursor
	if m.visualAnchor >= 0 {
		lo, hi = m.visualAnchor, m.visualCursor
		if lo > hi {
			lo, hi = hi, lo
		}
	}
	return lo, hi
}
```

Call sites:
- `buildVisualText` (`visual.go:92–100`): replace lines 93–99 with `lo, hi :=
  m.selectionBounds()`.
- `buildVisualRef` (`visual.go:115–122`): replace lines 116–122 with `lo, hi :=
  m.selectionBounds()`.
- `visualBar` (`visual.go:25–42`): the cursor-row caret check stays; the
  anchored-range branch (`visualAnchor >= 0 { lo,hi := ...; if idx in [lo,hi] }`)
  uses `selectionBounds`. **Care:** `visualBar` must keep returning the caret on
  the cursor row *before* testing the bar range, and the bar must only render
  when an anchor is set. Implement as:
  ```go
  func (m *model) visualBar(idx int) (string, bool) {
  	if !m.visualMode {
  		return "", false
  	}
  	if idx == m.visualCursor {
  		return visualCaretStyle.Render("▶") + " ", true
  	}
  	if m.visualAnchor >= 0 {
  		lo, hi := m.selectionBounds()
  		if idx >= lo && idx <= hi {
  			return visualSelStyle.Render("┃") + " ", true
  		}
  	}
  	return "", false
  }
  ```
  (With an anchor set and `idx != cursor`, `selectionBounds` returns the ordered
  anchor/cursor pair — identical to the inline code it replaces.)

### `moveVisualCursor(delta int)` — in `internal/tui/visual.go`

```go
// moveVisualCursor moves the visual caret by delta rows, clamped to the line
// range [0, len(m.lines)-1], then scrolls to keep it on screen. Centralizes the
// up/down cursor-move cases in handleVisualKey.
func (m *model) moveVisualCursor(delta int) {
	m.visualCursor += delta
	if m.visualCursor < 0 {
		m.visualCursor = 0
	}
	if m.visualCursor > len(m.lines)-1 {
		m.visualCursor = len(m.lines) - 1
	}
	m.ensureVisualVisible()
}
```

Call sites in `handleVisualKey` (`visual.go:167–177`):
- `case "up", "k":` → `m.moveVisualCursor(-1)`
- `case "down", "j":` → `m.moveVisualCursor(1)`

**Behavior-equivalence note:** the original guards were `if cursor > 0 {
cursor-- }` and `if cursor < len-1 { cursor++ }` — i.e. clamp-and-don't-move at
the ends. `moveVisualCursor` clamps the *result* to the same range, which is
identical for delta ±1 (the only deltas used). It also tolerates larger deltas
correctly, should a future caller page the caret.

## Non-goals

- No storage-representation change. `streamTop`/`searchHit`/`visualCursor`/
  `visualAnchor` stay `int`; `dragViewStateDown` and the reconcile drag call
  stay exactly as they are.
- No new view behavior. Scroll, selection, and rendering are pixel-identical;
  this is extraction only.
- No `centerOn`/`scrollToTop`/`isSearchHit`/`searchHit` changes (see YAGNI list).
- No MCP, no search-semantics, no shared-record-model change.

## Testing strategy

Behavior preservation is the contract. The existing TUI suite
(scroll/page/visual/search/copy, including `TestReconcileEvictionDragsViewState`
and `TestVisualIndicesClampOnEviction`) must stay **green unchanged** — these
tests read the same `int` fields the operations write, so no test edits are
needed. Additionally:
- Unit tests for `scrollBy` covering: up clamps at 0; up unsticks tail; down is
  a no-op in tail mode; down past the bottom re-sticks (tailMode true);
  multi-row deltas (page/fast).
- Unit test for `selectionBounds`: no-anchor → caret row; anchor below cursor →
  ordered; anchor above cursor → ordered.
- Unit test for `moveVisualCursor`: clamps at both ends; moves within range.

`go test ./...`, `go vet ./...`, `go test -race ./internal/tui/` green. Tagged
builds (`-tags nomcp`, `-tags nosse`) stay green (locked CGO-free invariant).

## Success criteria

- `scrollBy(delta)` exists in `internal/tui/viewport.go` and is the only place
  the six scroll actions touch `streamTop`; each action's inner branch is one
  call.
- `selectionBounds()` exists in `visual.go`; the order-the-pair idiom appears
  exactly once (no remaining inline copies in `visualBar`/`buildVisualText`/
  `buildVisualRef`).
- `moveVisualCursor(delta)` exists in `visual.go`; `handleVisualKey`'s up/down
  cases are one call each.
- All existing TUI tests pass unchanged; new unit tests for the three operations
  pass; `go test ./...`, `go vet ./...`, race, and tagged builds green.
- Net call-site reduction is real: 6 scroll sites → 6 one-line calls + 1 method;
  3 selection-bounds copies → 1 method; 2 cursor-move cases → 2 one-line calls.
