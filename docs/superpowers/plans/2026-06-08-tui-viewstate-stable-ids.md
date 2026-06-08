# TUI View-State as Stable IDs (slice 5-3) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Re-express the TUI's four view-state values (`streamTop`, `searchHit`, `visualCursor`, `visualAnchor`) as stable `(entryID, rowOffset)` anchors instead of absolute `m.lines` indices, so they survive eviction without the `dragViewStateDown` index-drag, which is then deleted.

**Architecture:** Wrap-first / flip-last accessor seam. Each value gets getter/setter accessors that *first* wrap the existing `int` field (zero behavior change, green). All call sites — production and tests — migrate to the accessors while the field is still authoritative (always green, same field underneath). *Last*, per value, the storage flips from `int` to a `rowAnchor`, rewriting **only the accessor internals** to resolve against `m.window` + `m.displayCache` via two shared helpers (`rowForAnchor` / `anchorForRow`). The compiler enforces completeness: removing the field fails the build at any missed site. `dragViewStateDown` is deleted once all four values are anchors.

**Tech Stack:** Go 1.26, bubbletea/lipgloss TUI, `internal/tui` package. No new dependencies. Tests via `go test ./internal/tui/...`.

**Spec:** `docs/superpowers/specs/2026-06-08-tui-viewstate-stable-ids-design.md` (read it first — especially the unresolvable-write rule and the corrected regression-net section).

---

## Background the implementer needs

**The reconcile model (slice 5-1).** `reconcile()` (in `internal/tui/app.go`) builds three things from ONE buffer snapshot, kept consistent: `m.lines` (flat `[]displayLine`), `m.window` (`[]*linebuf.Entry` in display order), and `m.displayCache` (`map[entryID][]displayLine`). The number of display rows an entry owns is `len(m.displayCache[e.ID])` (a tall JSON/XML block owns many rows; a plain line owns 1). **Readers must never call `m.buf.Snapshot` again** — they index `m.window`/`m.displayCache` via `m.visibleEntries()` (which returns `m.window`). The proven accumulation walk is in `entryIDForLine` (`internal/tui/copyref.go:12`).

**The current view-state fields** (`internal/tui/app.go`):
- `streamTop int` (line 304) — absolute index of first visible row when `!tailMode`.
- `searchHit int` (line 334) — absolute index of current search hit, `-1` when none.
- `visualCursor int` (line 341) — moving line in visual selection.
- `visualAnchor int` (line 342) — selection start, `-1` until first space sets it.

**The drag being removed** (`internal/tui/app.go:944-974`, `dragViewStateDown`): on eviction of `dropped` head rows it shifts each value down, conditionally and with per-value clamp/unset:
- `streamTop`: only when `!m.tailMode`; clamp at 0.
- `searchHit`: when `>= 0`; below 0 → unset to `-1`.
- `visualCursor`/`visualAnchor`: only when `m.visualMode`; cursor clamps at 0, anchor (when `>= 0`) unsets to `-1` below 0.
- Always sets `m.blockFocused = false`.

After all four values are anchors, eviction needs no drag: a stored anchor whose entry was evicted simply fails to resolve, and each getter returns that value's clamp result.

**The two regression tests (the behavior contract):**
- `TestReconcileEvictionDragsViewState` (`internal/tui/reconcile_test.go`) — writes `m.streamTop = 0` (before any reconcile → empty window) and `m.streamTop = 2`, asserts `m.streamTop == 1` after a 1-row eviction.
- `TestVisualIndicesClampOnEviction` (`internal/tui/visual_test.go`) — sets cursor/anchor via `keyV`/`keyJ`/`keySpace` (production handlers), asserts `m.visualCursor == 0` and `m.visualAnchor == -1` after two evictions.

These keep their assertions and semantics; their *field access* swaps to accessors within the flip commit for that value (see Tasks 4 and 6).

**Build/test commands** (from repo root):
- Single TUI test: `go test -run TestName ./internal/tui/`
- Whole package: `go test ./internal/tui/`
- Full suite: `go test ./...`
- Vet: `go vet ./...`
- Race: `go test -race ./internal/tui/`

**Commit discipline:** every task ends green (build + `go test ./internal/tui/`). Never leave a half-migrated field across a commit boundary.

---

## File structure

| File | Responsibility | Change |
|------|----------------|--------|
| `internal/tui/viewanchor.go` | NEW. `rowAnchor` type + `rowForAnchor`/`anchorForRow` resolvers (shared by all flips). | Create |
| `internal/tui/viewanchor_test.go` | NEW. Unit tests for the resolvers (round-trip, eviction, empty window, mid-block, re-render shrink). | Create |
| `internal/tui/app.go` | The four fields + accessors; `dragViewStateDown` deletion; reconcile no longer calls the drag. | Modify |
| `internal/tui/search.go`, `copyref.go`, `copytext.go`, `visual.go`, `render*.go`, etc. | Call-site migration to accessors (mechanical). | Modify |
| `internal/tui/reconcile_test.go`, `visual_test.go`, other `*_test.go` | Read/write swaps to accessors (mechanical) + the two regression tests' access swap. | Modify |

The accessor names (fixed — use these exactly everywhere):

| Value | Getter | Setter | Anchor field (after flip) |
|-------|--------|--------|---------------------------|
| `streamTop` | `streamTopRow() int` | `setStreamTopRow(i int)` | `streamTopA rowAnchor` |
| `searchHit` | `searchHitRow() int` | `setSearchHitRow(i int)` | `searchHitA rowAnchor` |
| `visualCursor` | `visualCursorRow() int` | `setVisualCursorRow(i int)` | `visualCursorA rowAnchor` |
| `visualAnchor` | `visualAnchorRow() int` | `setVisualAnchorRow(i int)` | `visualAnchorA rowAnchor` |

---

## Task 1: Shared resolver helpers (`viewanchor.go`) — pure additive, green

This task adds the resolver machinery the later flips depend on. Nothing calls it yet; it is pure addition. TDD it in isolation.

**Files:**
- Create: `internal/tui/viewanchor.go`
- Create: `internal/tui/viewanchor_test.go`

- [ ] **Step 1: Write the failing resolver tests**

Create `internal/tui/viewanchor_test.go`. These tests build a model with a known window via the existing test helper `seedSearch` (it appends single-row text events to group "g"; see `internal/tui/regex_test.go:25`). Each seeded event owns exactly 1 display row, so `m.lines` index == entry position, which makes the arithmetic checkable by hand.

```go
package tui

import "testing"

func TestAnchorRoundTripSingleRowEntries(t *testing.T) {
	m := seedSearch(t, "a", "b", "c", "d") // rows 0..3, one row each
	m.reconcile()
	for i := 0; i < 4; i++ {
		a := m.anchorForRow(i)
		if a.id == "" {
			t.Fatalf("row %d: got sentinel anchor, want resolvable", i)
		}
		got, ok := m.rowForAnchor(a)
		if !ok || got != i {
			t.Fatalf("round-trip row %d: got (%d,%v), want (%d,true)", i, got, ok, i)
		}
	}
}

func TestAnchorForRowOutOfRangeIsSentinel(t *testing.T) {
	m := seedSearch(t, "a", "b") // rows 0,1
	m.reconcile()
	if a := m.anchorForRow(-1); a.id != "" {
		t.Fatalf("negative idx: want sentinel, got %+v", a)
	}
	if a := m.anchorForRow(99); a.id != "" {
		t.Fatalf("past-end idx: want sentinel, got %+v", a)
	}
}

func TestAnchorForRowEmptyWindowIsSentinel(t *testing.T) {
	m := newModel(100) // no events, no reconcile → empty window
	if a := m.anchorForRow(0); a.id != "" {
		t.Fatalf("empty window: want sentinel, got %+v", a)
	}
}

func TestRowForAnchorSentinelNotOK(t *testing.T) {
	m := seedSearch(t, "a")
	m.reconcile()
	if _, ok := m.rowForAnchor(rowAnchor{}); ok {
		t.Fatal("sentinel anchor must resolve ok=false")
	}
}

func TestRowForAnchorEvictedEntryNotOK(t *testing.T) {
	// Tiny scrollback so appends evict the oldest entry.
	m := newModel(2) // cap 2 rows
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	ev := func(v string) render.Event {
		return render.Event{Group: "g", File: "/a.log",
			Rendered: []render.Part{{Type: "text", Value: v}}}
	}
	m.appendEvent(ev("a"))
	m.reconcile()
	a := m.anchorForRow(0) // anchor on entry "a"
	if a.id == "" {
		t.Fatal("setup: anchor on 'a' should be resolvable")
	}
	m.appendEvent(ev("b"))
	m.appendEvent(ev("c"))
	m.reconcile() // window now holds {b,c}; "a" evicted
	if _, ok := m.rowForAnchor(a); ok {
		t.Fatal("anchor on evicted entry must resolve ok=false")
	}
}

func TestRowForAnchorClampsOffsetIntoEntry(t *testing.T) {
	m := seedSearch(t, "a")
	m.reconcile()
	id := m.window[0].ID
	// Offset past the entry's row count must clamp to the last row of the entry.
	got, ok := m.rowForAnchor(rowAnchor{id: id, off: 99})
	if !ok || got != 0 {
		t.Fatalf("clamp: got (%d,%v), want (0,true)", got, ok)
	}
}
```

This test file imports `tea` and `render` (used in `TestRowForAnchorEvictedEntryNotOK`). Add the imports:

```go
import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/render"
)
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run TestAnchor ./internal/tui/`
Expected: FAIL to compile — `undefined: rowAnchor`, `m.anchorForRow`, `m.rowForAnchor`.

- [ ] **Step 3: Implement `viewanchor.go`**

Create `internal/tui/viewanchor.go`:

```go
package tui

// rowAnchor is a stable pointer into the display stream: the ID of the entry
// that owns a row plus the row's offset within that entry's display lines. It
// survives head eviction (unlike an absolute m.lines index) because the entry
// ID is stable. The zero value (id == "") is the sentinel: an unresolvable or
// "no current value" anchor. Getters resolve a sentinel — or an anchor whose
// entry has left the window — to their per-value clamp result.
type rowAnchor struct {
	id  string
	off int
}

// rowForAnchor maps an anchor to an absolute m.lines index in the current
// reconcile window. ok=false when the anchor is the sentinel or its entry is
// no longer visible (evicted or scrolled out of the window). The offset is
// clamped into the entry's current row count, because a re-render can change
// how many display rows an entry owns. Walks m.window + m.displayCache exactly
// as entryIDForLine does (one consistent snapshot — never re-snapshot buf).
func (m *model) rowForAnchor(a rowAnchor) (int, bool) {
	if a.id == "" {
		return 0, false
	}
	off := 0
	for _, e := range m.visibleEntries() {
		n := len(m.displayCache[e.ID])
		if e.ID == a.id {
			if n == 0 {
				return off, true
			}
			o := a.off
			if o < 0 {
				o = 0
			}
			if o >= n {
				o = n - 1
			}
			return off + o, true
		}
		off += n
	}
	return 0, false
}

// anchorForRow is the inverse: the (entryID, rowOffset) owning absolute row
// idx in the current window. Returns the sentinel rowAnchor{} when idx is out
// of range (negative, empty window, or past the last row) — getters resolve
// that to their per-value clamp result (the unresolvable-write rule).
func (m *model) anchorForRow(idx int) rowAnchor {
	if idx < 0 {
		return rowAnchor{}
	}
	off := 0
	for _, e := range m.visibleEntries() {
		n := len(m.displayCache[e.ID])
		if idx < off+n {
			return rowAnchor{id: e.ID, off: idx - off}
		}
		off += n
	}
	return rowAnchor{}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -run TestAnchor ./internal/tui/`
Expected: PASS (6 tests).

- [ ] **Step 5: Vet + full package green**

Run: `go vet ./internal/tui/ && go test ./internal/tui/`
Expected: PASS, no vet complaints.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/viewanchor.go internal/tui/viewanchor_test.go
git commit -m "feat(tui): add rowAnchor + rowForAnchor/anchorForRow resolvers (5-3)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 2: `streamTop` accessor seam — wrap the int field, green

Add getter/setter that wrap the existing `streamTop int` field verbatim. No behavior change; nothing migrated yet.

**Files:**
- Modify: `internal/tui/app.go` (add two methods near the `streamTop` field, ~line 304)

- [ ] **Step 1: Add the wrapping accessors**

In `internal/tui/app.go`, immediately after the `model` struct definition (find a stable insertion point — put it right before `func (m *model) reconcile()` at line 877, grouping all four seams together later). Add:

```go
// streamTopRow returns the absolute m.lines index of the first visible row when
// browsing. Stage-0 seam: wraps the streamTop field verbatim (no behavior
// change). The flip (Task 4) rewrites only this body to resolve an anchor.
func (m *model) streamTopRow() int { return m.streamTop }

// setStreamTopRow sets the first-visible-row position. Stage-0 seam: wraps the
// streamTop field verbatim. The flip rewrites only this body to store an anchor.
func (m *model) setStreamTopRow(i int) { m.streamTop = i }
```

- [ ] **Step 2: Build + test green (no behavior change)**

Run: `go build ./... && go test ./internal/tui/`
Expected: PASS. The accessors are unused; this only proves they compile.

- [ ] **Step 3: Commit**

```bash
git add internal/tui/app.go
git commit -m "refactor(tui): add streamTop accessor seam (wraps field, no behavior change)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 3: Migrate all `streamTop` call sites to the accessors — mechanical, green

Replace every direct `m.streamTop` read with `m.streamTopRow()` and every write with `m.setStreamTopRow(...)`, **except** the field declaration (line 304), the two accessor bodies (Task 2), and `dragViewStateDown` (line 950 — that whole function is deleted in Task 4, so leave its direct field use alone until then). Every site still touches the same field, so it stays green.

**Files:**
- Modify: `internal/tui/app.go` and any other non-test `.go` files referencing `streamTop` (search first).

- [ ] **Step 1: Enumerate the write sites (need manual rewrite — assignments)**

Run: `grep -rn "m\.streamTop\s*=" internal/tui/*.go | grep -v _test.go`
For each line that is an assignment (`m.streamTop = X`, `m.streamTop -= X`, `m.streamTop += X`), rewrite it:
- `m.streamTop = X` → `m.setStreamTopRow(X)`
- `m.streamTop -= X` → `m.setStreamTopRow(m.streamTopRow() - X)`
- `m.streamTop += X` → `m.setStreamTopRow(m.streamTopRow() + X)`

Do **not** touch the assignment inside `dragViewStateDown` (`m.streamTop -= dropped` and the `m.streamTop = 0` clamp) — that function is deleted in Task 4. Do not touch the seam bodies.

- [ ] **Step 2: Replace the read sites**

Run: `grep -rn "m\.streamTop\b" internal/tui/*.go | grep -v _test.go`
Every remaining `m.streamTop` that is NOT an assignment LHS, NOT the field decl, NOT inside the two seam bodies, NOT inside `dragViewStateDown` → replace with `m.streamTopRow()`.

- [ ] **Step 3: Verify no stray direct field refs remain (outside the allowed four)**

Run: `grep -rn "m\.streamTop\b" internal/tui/*.go | grep -v _test.go`
Expected remaining lines ONLY: the field decl (`streamTop int`), `streamTopRow` body, `setStreamTopRow` body, and the two uses inside `dragViewStateDown`. If anything else remains, migrate it.

- [ ] **Step 4: Build + test green**

Run: `go build ./... && go test ./internal/tui/ && go vet ./internal/tui/`
Expected: PASS. Same field underneath → behavior identical.

- [ ] **Step 5: Migrate test reads/writes too (so the flip in Task 4 compiles)**

Run: `grep -rn "\.streamTop\b" internal/tui/*_test.go`
For each test site:
- read `x.streamTop` → `x.streamTopRow()`
- write `x.streamTop = X` → `x.setStreamTopRow(X)`
Apply to ALL test files including `reconcile_test.go`'s `TestReconcileEvictionDragsViewState` (`m.streamTop = 0`, `m.streamTop = 2`, `m.streamTop != 1` → `m.setStreamTopRow(0)`, `m.setStreamTopRow(2)`, `m.streamTopRow() != 1`). The assertions/semantics are unchanged; only the access form changes. (While the field still exists this is a no-op behaviorally; it must be done now so Task 4's field removal compiles.)

- [ ] **Step 6: Verify no direct test field refs remain**

Run: `grep -rn "\.streamTop\b" internal/tui/*_test.go`
Expected: empty.

- [ ] **Step 7: Build + test green**

Run: `go test ./internal/tui/`
Expected: PASS (including `TestReconcileEvictionDragsViewState`, still asserting `== 1`).

- [ ] **Step 8: Commit**

```bash
git add internal/tui/
git commit -m "refactor(tui): migrate streamTop call sites to accessors (prod+tests, green)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 4: Flip `streamTop` storage to an anchor — the real work for this value

Change the field from `int` to `rowAnchor`, rewrite ONLY the two accessor bodies to resolve via the helpers, and remove the `streamTop` branch from `dragViewStateDown`. The compiler guarantees no site reads the field directly (Task 3 proved the grep empty outside the allowed spots).

**Files:**
- Modify: `internal/tui/app.go`

- [ ] **Step 1: Replace the field declaration**

In `internal/tui/app.go` line 304, change:
```go
	streamTop int // absolute index of the first visible row when !tailMode
```
to:
```go
	streamTopA rowAnchor // anchor for the first visible row when !tailMode (see viewanchor.go)
```

- [ ] **Step 2: Rewrite the accessor bodies to resolve the anchor**

Replace the two seam bodies from Task 2 with:
```go
// streamTopRow returns the absolute m.lines index of the first visible row when
// browsing. Resolves the stored anchor against the current window; an evicted
// or unresolvable anchor clamps to row 0 (top of the now-shorter window) —
// exactly the old dragViewStateDown streamTop behavior.
func (m *model) streamTopRow() int {
	idx, ok := m.rowForAnchor(m.streamTopA)
	if !ok {
		return 0
	}
	return idx
}

// setStreamTopRow stores the first-visible-row position as a stable anchor. An
// unresolvable index (empty window / out of range — e.g. before the first
// reconcile) stores the sentinel, which streamTopRow resolves to 0.
func (m *model) setStreamTopRow(i int) { m.streamTopA = m.anchorForRow(i) }
```

- [ ] **Step 3: Remove the `streamTop` branch from `dragViewStateDown`**

In `dragViewStateDown` (`internal/tui/app.go` ~948), delete the leading block:
```go
	if !m.tailMode {
		m.streamTop -= dropped
		if m.streamTop < 0 {
			m.streamTop = 0
		}
	}
```
Leave the `searchHit`, visual, and `m.blockFocused = false` parts intact (they flip in later tasks).

- [ ] **Step 4: Build to confirm completeness**

Run: `go build ./...`
Expected: PASS. If it fails with `m.streamTop undefined`, a call site was missed in Task 3 — migrate it, then rebuild. (This is the compiler enforcing the flip.)

- [ ] **Step 5: Run the streamTop regression test**

Run: `go test -run TestReconcileEvictionDragsViewState ./internal/tui/ -v`
Expected: PASS. This now proves the anchor round-trips: `setStreamTopRow(2)` anchors row 2 (entry "c"); after "d" evicts the oldest row, `streamTopRow()` resolves "c" to row 1.

- [ ] **Step 6: Full package + race + vet green**

Run: `go test ./internal/tui/ && go test -race ./internal/tui/ && go vet ./internal/tui/`
Expected: PASS. Watch especially scroll/page tests — `streamTop` drives the viewport.

- [ ] **Step 7: Commit**

```bash
git add internal/tui/app.go
git commit -m "refactor(tui): flip streamTop to a stable anchor; drop its drag branch (5-3)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 5: `searchHit` — seam, migrate, flip (combined, value-scoped)

Same three moves as Tasks 2-4 but for `searchHit`. Its clamp is **unset to -1** (not 0). Combined into one task because the per-step pattern is now established; still commit at each green sub-stage.

**Files:**
- Modify: `internal/tui/app.go`, `internal/tui/search.go`, `internal/tui/copyref.go`, and any other files referencing `searchHit` (search first), plus `*_test.go`.

- [ ] **Step 1: Add the seam (wraps the int field)**

In `internal/tui/app.go` near the `searchHit` field, add:
```go
// searchHitRow returns the absolute m.lines index of the current search hit, or
// -1 when none. Stage-0 seam wraps the field; the flip rewrites only this body.
func (m *model) searchHitRow() int { return m.searchHit }

// setSearchHitRow sets the current hit index (-1 = no hit). Stage-0 seam.
func (m *model) setSearchHitRow(i int) { m.searchHit = i }
```
Build + test green, commit: `refactor(tui): add searchHit accessor seam`.

- [ ] **Step 2: Migrate production call sites**

Run: `grep -rn "m\.searchHit\b" internal/tui/*.go | grep -v _test.go`
Replace reads → `m.searchHitRow()`, writes → `m.setSearchHitRow(...)`, EXCEPT the field decl, the two seam bodies, and the `searchHit` block inside `dragViewStateDown` (deleted in this task's Step 4). Note `search.go` uses `m.searchHit` in `searchNext`/`searchPrev`/`jumpToHit` and compound forms like `from := m.searchHit + 1` → `from := m.searchHitRow() + 1`; `m.searchHit = idx` → `m.setSearchHitRow(idx)`. `copyref.go:33` `m.searchHit >= 0` → `m.searchHitRow() >= 0`.
Verify: `grep -rn "m\.searchHit\b" internal/tui/*.go | grep -v _test.go` shows only the decl, two seam bodies, and the drag block. Build + test green.

- [ ] **Step 3: Migrate test call sites**

Run: `grep -rn "\.searchHit\b" internal/tui/*_test.go`
Reads → `searchHitRow()`, writes → `setSearchHitRow(...)`. Verify grep empty. Build + test green. Commit: `refactor(tui): migrate searchHit call sites to accessors (prod+tests, green)`.

- [ ] **Step 4: Flip storage to an anchor**

In `app.go`, change the field:
```go
	searchHit   int
```
to:
```go
	searchHitA rowAnchor
```
Rewrite the seam bodies:
```go
// searchHitRow returns the absolute m.lines index of the current search hit, or
// -1 when there is none or the hit's entry scrolled off (matching the old
// dragViewStateDown unset-on-eviction behavior).
func (m *model) searchHitRow() int {
	idx, ok := m.rowForAnchor(m.searchHitA)
	if !ok {
		return -1
	}
	return idx
}

// setSearchHitRow stores the hit position as a stable anchor. A negative or
// unresolvable index stores the sentinel, which searchHitRow resolves to -1.
func (m *model) setSearchHitRow(i int) { m.searchHitA = m.anchorForRow(i) }
```
Delete the `searchHit` block from `dragViewStateDown`:
```go
	if m.searchHit >= 0 {
		m.searchHit -= dropped
		if m.searchHit < 0 {
			m.searchHit = -1
		}
	}
```

- [ ] **Step 5: Build + test + race + vet green**

Run: `go build ./... && go test ./internal/tui/ && go test -race ./internal/tui/ && go vet ./internal/tui/`
Expected: PASS. If `m.searchHit undefined`, a site was missed — migrate and rebuild. Pay attention to search tests (`search_test.go`, `regex_test.go`) and copy-reference tests.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/
git commit -m "refactor(tui): flip searchHit to a stable anchor; drop its drag branch (5-3)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 6: `visualCursor` + `visualAnchor` — seam, migrate, flip (as a pair)

Both flip together because the visual eviction test and the drag treat them as a unit (gated on `m.visualMode`). `visualCursor` clamps to 0; `visualAnchor` unsets to -1.

**Files:**
- Modify: `internal/tui/app.go`, `internal/tui/visual.go` (and any other files referencing these — search first), plus `*_test.go` including `visual_test.go`.

- [ ] **Step 1: Add both seams (wrap the int fields)**

In `internal/tui/app.go` near the visual fields:
```go
// visualCursorRow returns the moving line of the visual selection as an absolute
// m.lines index. Stage-0 seam wraps the field; the flip rewrites only this body.
func (m *model) visualCursorRow() int { return m.visualCursor }

// setVisualCursorRow sets the visual cursor line. Stage-0 seam.
func (m *model) setVisualCursorRow(i int) { m.visualCursor = i }

// visualAnchorRow returns the visual selection start as an absolute m.lines
// index, or -1 before the first space sets it. Stage-0 seam.
func (m *model) visualAnchorRow() int { return m.visualAnchor }

// setVisualAnchorRow sets the visual selection start (-1 = unset). Stage-0 seam.
func (m *model) setVisualAnchorRow(i int) { m.visualAnchor = i }
```
Build + test green. Commit: `refactor(tui): add visualCursor/visualAnchor accessor seams`.

- [ ] **Step 2: Migrate production call sites for both values**

Run: `grep -rn "m\.visualCursor\b\|m\.visualAnchor\b" internal/tui/*.go | grep -v _test.go`
Reads → `visualCursorRow()` / `visualAnchorRow()`; writes → `setVisualCursorRow(...)` / `setVisualAnchorRow(...)`. Compound forms (e.g. `m.visualCursor--`, `m.visualCursor += n`) become `m.setVisualCursorRow(m.visualCursorRow() - 1)` etc. EXCLUDE the field decls, the four seam bodies, and the visual block inside `dragViewStateDown` (deleted at Step 4). Verify the grep shows only those excluded sites. Build + test green.

- [ ] **Step 3: Migrate test call sites for both values**

Run: `grep -rn "\.visualCursor\b\|\.visualAnchor\b" internal/tui/*_test.go`
Reads → getters, writes → setters. This includes `TestVisualIndicesClampOnEviction`'s final assertions `m.visualCursor != 0` → `m.visualCursorRow() != 0` and `m.visualAnchor != -1` → `m.visualAnchorRow() != -1` (assertions/semantics unchanged). Verify grep empty. Build + test green. Commit: `refactor(tui): migrate visual cursor/anchor call sites to accessors (prod+tests)`.

- [ ] **Step 4: Flip both fields to anchors and delete the drag's visual block**

In `app.go`, change:
```go
	visualCursor int
	visualAnchor int
```
to:
```go
	visualCursorA rowAnchor
	visualAnchorA rowAnchor
```
Rewrite the four seam bodies:
```go
// visualCursorRow returns the moving line of the visual selection as an absolute
// m.lines index. An evicted/unresolvable anchor clamps to 0 (matching the old
// dragViewStateDown visualCursor behavior).
func (m *model) visualCursorRow() int {
	idx, ok := m.rowForAnchor(m.visualCursorA)
	if !ok {
		return 0
	}
	return idx
}

// setVisualCursorRow stores the visual cursor as a stable anchor; an
// unresolvable index stores the sentinel, which visualCursorRow resolves to 0.
func (m *model) setVisualCursorRow(i int) { m.visualCursorA = m.anchorForRow(i) }

// visualAnchorRow returns the visual selection start as an absolute m.lines
// index, or -1 when unset or the anchored entry scrolled off (matching the old
// dragViewStateDown visualAnchor unset behavior).
func (m *model) visualAnchorRow() int {
	idx, ok := m.rowForAnchor(m.visualAnchorA)
	if !ok {
		return -1
	}
	return idx
}

// setVisualAnchorRow stores the selection start as a stable anchor; a negative
// or unresolvable index stores the sentinel, which visualAnchorRow returns as -1.
func (m *model) setVisualAnchorRow(i int) { m.visualAnchorA = m.anchorForRow(i) }
```
Delete the visual block from `dragViewStateDown`:
```go
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
```

- [ ] **Step 5: Build + run the visual regression test**

Run: `go build ./... && go test -run TestVisualIndicesClampOnEviction ./internal/tui/ -v`
Expected: PASS. The cursor/anchor are set through `keyV`/`keyJ`/`keySpace` (now routing through setters), so two evictions resolve the anchors to cursor=0 (clamp) and anchor=-1 (unset).

- [ ] **Step 6: Full package + race + vet green**

Run: `go test ./internal/tui/ && go test -race ./internal/tui/ && go vet ./internal/tui/`
Expected: PASS. Watch `visual_test.go` and copy-text tests (visual selection drives `entryRowSpan`).

- [ ] **Step 7: Commit**

```bash
git add internal/tui/
git commit -m "refactor(tui): flip visualCursor/visualAnchor to stable anchors; drop drag block (5-3)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 7: Delete `dragViewStateDown` and its reconcile call

All four branches are gone; the function body is now only `m.blockFocused = false`. Eviction behavior is fully handled by the resolvers. Replace the now-vestigial drag with the one residual side effect.

**Files:**
- Modify: `internal/tui/app.go`

- [ ] **Step 1: Inspect the residual drag body**

Run: `grep -n "dragViewStateDown\|blockFocused" internal/tui/app.go`
Confirm `dragViewStateDown` now contains only `m.blockFocused = false` (plus its signature/comment). The four value branches were deleted in Tasks 4-6.

- [ ] **Step 2: Inline the residual side effect into reconcile**

In `reconcile()` (`internal/tui/app.go` ~939), replace:
```go
	if dropped > 0 {
		m.dragViewStateDown(dropped)
	}
```
with:
```go
	if dropped > 0 {
		// Head rows were evicted. View-state anchors (streamTop/searchHit/
		// visual) resolve against the new window automatically; the only
		// residual effect is clearing the focused-block indicator, since the
		// block it pointed at may have shifted or gone.
		m.blockFocused = false
	}
```

- [ ] **Step 3: Delete the `dragViewStateDown` function**

Remove the entire `dragViewStateDown` function (its comment block + body) from `internal/tui/app.go`.

- [ ] **Step 4: Verify it's gone and unreferenced**

Run: `grep -rn "dragViewStateDown" internal/tui/`
Expected: empty (no definition, no caller, no test reference).

- [ ] **Step 5: Build + full TUI suite + race + vet green**

Run: `go build ./... && go test ./internal/tui/ && go test -race ./internal/tui/ && go vet ./internal/tui/`
Expected: PASS, including both eviction regression tests.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/app.go
git commit -m "refactor(tui): delete dragViewStateDown; eviction handled by anchors (5-3)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 8: Final verification + stale-comment sweep

Confirm the success criteria and clean up any comments that still describe view-state as absolute indices.

**Files:**
- Modify: `internal/tui/app.go` (comment touch-ups only, if any)

- [ ] **Step 1: Confirm no absolute-index view-state fields remain**

Run: `grep -rn "streamTop\b\|searchHit\b\|visualCursor\b\|visualAnchor\b" internal/tui/*.go | grep -v "Row\|streamTopA\|searchHitA\|visualCursorA\|visualAnchorA" | grep -v _test.go`
Expected: empty (no bare `int` fields, only the `...A rowAnchor` fields and the `...Row()` accessors, which the filter excludes). If a comment line matches, fix the comment to describe the anchor (e.g. update the `streamTopA`/`searchHitA` field comments and the `searchHit` doc block at app.go ~325).

- [ ] **Step 2: Update the lingering field-comment references**

Check the struct comments that still say "absolute index": the `searchHit` doc near app.go:325 ("absolute index into m.lines of the current hit") and `reRenderAll`'s comment about "Index anchors (streamTop, searchHit) are clamped". Reword to reflect anchors, e.g. for `reRenderAll`: "View-state anchors (streamTop, searchHit) resolve against the rebuilt window; a collapsed block may visibly shift the viewport, which is correct." (Functional behavior unchanged — `reRenderAll` already drops the cache and forces reconcile.)

- [ ] **Step 3: Full repo suite, race, vet**

Run: `go test ./... && go test -race ./internal/tui/ && go vet ./...`
Expected: PASS across all packages.

- [ ] **Step 4: Confirm the build flavors still compile (CGO-free invariant)**

Run: `go build -tags nomcp ./... && go build -tags nosse ./... && go build ./...`
Expected: PASS (this slice touches only `internal/tui`, but the locked rules require the tagged builds stay green).

- [ ] **Step 5: Commit any comment fixes**

```bash
git add internal/tui/
git commit -m "docs(tui): reword view-state comments for anchor representation (5-3)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Self-review notes (already reconciled against the spec)

- **Spec coverage:** streamTop (Tasks 2-4), searchHit (Task 5), visualCursor+visualAnchor (Task 6), `dragViewStateDown` deletion (Task 7), resolvers + evicted/unresolvable semantics (Task 1 + each flip), regression tests preserved with accessor swaps (Tasks 3/5/6), success criteria (Task 8). All spec sections map to a task.
- **Unresolvable-write rule:** implemented once in `anchorForRow` (returns sentinel) + each getter's `!ok` fallback (0 / -1 / 0 / -1). Matches the spec's "one rule per value."
- **Per-value clamp asymmetry:** streamTop→0, searchHit→-1, visualCursor→0, visualAnchor→-1 — encoded in the getter fallbacks, verified by the two regression tests.
- **Type consistency:** anchor type `rowAnchor{id string; off int}`; helpers `rowForAnchor(rowAnchor)(int,bool)` / `anchorForRow(int) rowAnchor`; fields `streamTopA`/`searchHitA`/`visualCursorA`/`visualAnchorA`; accessors `streamTopRow`/`setStreamTopRow` etc. — used identically across all tasks.
- **Known watch-point for the implementer:** the old `streamTop` drag was gated on `!m.tailMode` (skipped in tail mode); the anchor getter resolves unconditionally. In tail mode `streamTop` is unused (viewport pinned to bottom), so this is not a behavior change — but if any tail-mode scroll test regresses, that is where to look. The full TUI suite is the guard.
