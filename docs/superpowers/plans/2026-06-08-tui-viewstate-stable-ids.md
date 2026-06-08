# TUI View-State as Stable IDs (slice 5-3) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Re-express the TUI's four view-state values (`streamTop`, `searchHit`, `visualCursor`, `visualAnchor`) as stable `(entryID, rowOffset)` anchors instead of absolute `m.lines` indices, so they survive eviction/re-render without the `dragViewStateDown` index-drag and `reRenderAll`'s clamp block, which are then deleted.

**Architecture:** Wrap-first / flip-last accessor seam. Each value gets getter/setter accessors that *first* wrap the existing `int` field (zero behavior change, green). All call sites — production and tests — migrate to the accessors while the field is still authoritative (always green, same field underneath). *Last*, per value, the storage flips from `int` to a `rowAnchor`, rewriting **only the accessor internals** to resolve against `m.window` + `m.displayCache` via two shared helpers (`rowForAnchor` / `anchorForRow`). The compiler enforces completeness: removing the field fails the build at any missed site. `dragViewStateDown` is deleted once all four values are anchors; `reRenderAll`'s clamp block is deleted per-value with each flip.

**Tech Stack:** Go 1.26, bubbletea/lipgloss TUI, `internal/tui` package. No new dependencies. Tests via `go test ./internal/tui/...`.

**Spec:** `docs/superpowers/specs/2026-06-08-tui-viewstate-stable-ids-design.md` (read it first — especially the **index-domain rule** for `anchorForRow`, the unresolvable-write rule, and the `reRenderAll`-clamp-deletion note).

---

## Background the implementer needs

**The reconcile model (slice 5-1).** `reconcile()` (in `internal/tui/reconcile.go:67`) builds three things from ONE buffer snapshot, kept consistent: `m.lines` (flat `[]displayLine`), `m.window` (`[]*linebuf.Entry` in display order), and `m.displayCache` (`map[entryID][]displayLine`). The number of display rows an entry owns is `len(m.displayCache[e.ID])` (a tall JSON/XML block owns many rows; a plain line owns 1). **Readers must never call `m.buf.Snapshot` again** — they index `m.window`/`m.displayCache` via `m.visibleEntries()` (`internal/tui/reconcile.go:171`, returns `m.window`). The proven accumulation walk is in `entryIDForLine` (`internal/tui/copyref.go:12`).

**Current view-state fields** (`internal/tui/app.go`):
- `streamTop int` (line 277) — absolute index of first visible row when `!tailMode`.
- `searchHit int` (line 307) — absolute index of current search hit, `-1` when none.
- `visualCursor int` (line 314) — moving line in visual selection.
- `visualAnchor int` (line 315) — selection start, `-1` until first space sets it.

(Line numbers are a guide; if they have drifted, find the field by name in the `model` struct.)

**The drag being removed** (`internal/tui/reconcile.go:138`, `dragViewStateDown`): on eviction of `dropped` head rows it shifts each value down, conditionally and with per-value clamp/unset:
- `streamTop`: only when `!m.tailMode`; clamp at 0.
- `searchHit`: when `>= 0`; below 0 → unset to `-1`.
- `visualCursor`/`visualAnchor`: only when `m.visualMode`; cursor clamps at 0, anchor (when `>= 0`) unsets to `-1` below 0.
- Always sets `m.blockFocused = false`.

**The clamp block being removed** (`internal/tui/reconcile.go:198-207`, tail of `reRenderAll`): after a toggle-driven re-render it re-clamps `m.streamTop` (`> len(m.lines)` → `= len(m.lines)`; `< 0` → `0`) and `m.searchHit` (`>= len(m.lines)` → `-1`). This exists only because the values are stale-able ints; under anchors the resolver clamps automatically, so it becomes dead code. It is deleted per-value (streamTop lines with Task 5, searchHit lines with Task 6).

After all four values are anchors, eviction needs no drag and re-render needs no clamp: a stored anchor whose entry was evicted simply fails to resolve (getter returns the per-value clamp result), and an anchor whose entry's row count changed has its offset clamped into the new count by `rowForAnchor`.

**The viewport ops are ordinary call sites.** `scrollBy` (`internal/tui/viewport.go:13`), `moveVisualCursor` (`internal/tui/visual.go:110`), `unstickFromTail`/`maybeReStick` (`viewport.go`) read-modify-write these fields. They migrate like any other site — the accessors sit *underneath* them (`scrollBy` becomes `m.setStreamTopRow(m.streamTopRow()+delta)`). **Watch-point:** `scrollBy(delta>0)` deliberately lets the target run *past end* and relies on `maybeReStick` to re-pin to tail; this is exactly why `anchorForRow` clamps a past-end index to the **last row** (not the sentinel). Task 1 adds the regression test that guards this; Task 2 implements the rule.

**The regression tests (the behavior contract):**
- `TestReconcileEvictionDragsViewState` (`internal/tui/reconcile_test.go`) — writes `m.streamTop = 0` (before any reconcile → empty window) and `m.streamTop = 2`, asserts `m.streamTop == 1` after a 1-row eviction.
- `TestVisualIndicesClampOnEviction` (`internal/tui/visual_test.go`) — sets cursor/anchor via `keyV`/`keyJ`/`keySpace` (production handlers), asserts `m.visualCursor == 0` and `m.visualAnchor == -1` after two evictions.
- `TestScrollDownPastEndReSticks` (`internal/tui/app_test.go`, **NEW in Task 1**) — scroll down past the end re-sticks to tail.

These keep their assertions and semantics; their *field access* swaps to accessors within the flip commit for that value (the re-stick test reads no view-state field, so it needs no swap).

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
| `internal/tui/app_test.go` | Add the scroll-down-past-end → re-stick regression test (Task 1). | Modify |
| `internal/tui/viewanchor.go` | NEW. `rowAnchor` type + `rowForAnchor`/`anchorForRow` resolvers + the eight value accessors (added as wrapping seams, later flipped). | Create |
| `internal/tui/viewanchor_test.go` | NEW. Unit tests for the resolvers (round-trip, eviction, empty window, mid-block, past-end clamp, re-render shrink). | Create |
| `internal/tui/app.go` | The four fields flip from `int` to `rowAnchor`. | Modify |
| `internal/tui/reconcile.go` | `dragViewStateDown` shrinks per flip then is deleted; `reRenderAll` clamp block deleted per flip; reconcile call updated. | Modify |
| `internal/tui/blocks.go`, `search.go`, `update.go`, `viewport.go`, `visual.go`, `copyref.go` | Production call-site migration to accessors (mechanical, grep-driven). | Modify |
| `internal/tui/*_test.go` | Read/write swaps to accessors (mechanical) + the two eviction tests' access swap. | Modify |

The accessor names (fixed — use these exactly everywhere):

| Value | Getter | Setter | Anchor field (after flip) |
|-------|--------|--------|---------------------------|
| `streamTop` | `streamTopRow() int` | `setStreamTopRow(i int)` | `streamTopA rowAnchor` |
| `searchHit` | `searchHitRow() int` | `setSearchHitRow(i int)` | `searchHitA rowAnchor` |
| `visualCursor` | `visualCursorRow() int` | `setVisualCursorRow(i int)` | `visualCursorA rowAnchor` |
| `visualAnchor` | `visualAnchorRow() int` | `setVisualAnchorRow(i int)` | `visualAnchorA rowAnchor` |

---

## Task 1: Re-stick safety-net test (against current int code) — green baseline

The suite does NOT currently assert that scrolling *down past the end* re-sticks to tail (the existing re-stick test uses `End`, a direct tail jump). Add that test now, against the `int` code, so it passes as a baseline and is committed before any flip. It is the cross-flip guard for the past-end resolver rule.

**Files:**
- Modify: `internal/tui/app_test.go`

- [ ] **Step 1: Add the test**

In `internal/tui/app_test.go` (which already imports `fmt`, `strings`, `tea`, and `render`), add:

```go
func TestScrollDownPastEndReSticks(t *testing.T) {
	m := newModel(1000)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	for i := 0; i < 50; i++ {
		m.appendEvent(render.Event{
			Group: "g", File: "/x.log",
			Rendered: []render.Part{{Type: "text", Value: fmt.Sprintf("line %d", i)}},
		})
	}
	// Leave tail mode and jump to the very top.
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyHome})
	m = m2.(*model)
	if m.tailMode {
		t.Fatal("setup: Home should leave tail mode")
	}
	// Scroll DOWN far past the end. This must re-stick to tail via maybeReStick,
	// NOT jump to the top. (Under a naive anchor flip that collapsed a past-end
	// write to the sentinel, streamTopRow() would resolve to 0 and this would
	// stay in browse mode — which is the regression this test guards.)
	m.scrollBy(1000)
	if !m.tailMode {
		t.Fatal("scrolling down past the end must re-stick to tailMode")
	}
}
```

- [ ] **Step 2: Run it — must pass against the int baseline**

Run: `go test -run TestScrollDownPastEndReSticks ./internal/tui/ -v`
Expected: PASS (with `int` storage, `scrollBy(1000)` sets `streamTop=1000`; `maybeReStick` loops from 1000 to 50 → 0 enabled ≤ rows → `tailMode=true`).

- [ ] **Step 3: Full package green**

Run: `go test ./internal/tui/`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/tui/app_test.go
git commit -m "test(tui): scroll-down-past-end re-sticks to tail (cross-flip guard, 5-3)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 2: Shared resolver helpers (`viewanchor.go`) — pure additive, green

Add the resolver machinery the later flips depend on. Nothing calls it yet; it is pure addition. TDD it in isolation. **The index-domain rule matters:** `idx < 0` or empty window → sentinel; `idx` in range → exact anchor; `idx` past-end in a non-empty window → clamp to the last row (resolvable).

**Files:**
- Create: `internal/tui/viewanchor.go`
- Create: `internal/tui/viewanchor_test.go`

- [ ] **Step 1: Write the failing resolver tests**

Create `internal/tui/viewanchor_test.go`. These build a model with a known window via the existing test helper `seedSearch` (appends single-row text events to group "g"; see `internal/tui/regex_test.go`). Each seeded event owns exactly 1 display row, so `m.lines` index == entry position.

```go
package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/render"
)

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

func TestAnchorForRowNegativeIsSentinel(t *testing.T) {
	m := seedSearch(t, "a", "b")
	m.reconcile()
	if a := m.anchorForRow(-1); a.id != "" {
		t.Fatalf("negative idx: want sentinel, got %+v", a)
	}
}

func TestAnchorForRowPastEndClampsToLastRow(t *testing.T) {
	m := seedSearch(t, "a", "b", "c") // rows 0,1,2
	m.reconcile()
	a := m.anchorForRow(99) // past end, non-empty window
	if a.id == "" {
		t.Fatal("past-end in a non-empty window must clamp to the last row, not the sentinel")
	}
	got, ok := m.rowForAnchor(a)
	if !ok || got != 2 {
		t.Fatalf("past-end clamp: got (%d,%v), want (2,true)", got, ok)
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
	m := newModel(2)
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

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run 'TestAnchor|TestRowForAnchor' ./internal/tui/`
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

// anchorForRow is the inverse: the (entryID, rowOffset) owning absolute row idx
// in the current window. Index-domain rule:
//   - idx < 0            -> sentinel (preserves searchHit/visualAnchor's -1 unset)
//   - empty window       -> sentinel
//   - idx in [0, total)  -> the exact owning anchor
//   - idx >= total       -> clamp to the LAST row (a resolvable anchor), NOT the
//     sentinel. scrollBy(delta>0) intentionally over-scrolls past the end and
//     relies on maybeReStick to re-pin to tail; collapsing that to the sentinel
//     would resolve to row 0 and jump to the top instead of re-sticking.
func (m *model) anchorForRow(idx int) rowAnchor {
	if idx < 0 {
		return rowAnchor{}
	}
	off := 0
	any := false
	lastID := ""
	lastN := 0
	for _, e := range m.visibleEntries() {
		n := len(m.displayCache[e.ID])
		if idx < off+n {
			return rowAnchor{id: e.ID, off: idx - off}
		}
		off += n
		any, lastID, lastN = true, e.ID, n
	}
	if !any {
		return rowAnchor{} // empty window
	}
	o := lastN - 1
	if o < 0 {
		o = 0
	}
	return rowAnchor{id: lastID, off: o}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -run 'TestAnchor|TestRowForAnchor' ./internal/tui/`
Expected: PASS (7 tests).

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

## Task 3: `streamTop` accessor seam — wrap the int field, green

Add getter/setter that wrap the existing `streamTop int` field verbatim. No behavior change; nothing migrated yet. Put the seams in `viewanchor.go` (co-located with the resolvers they will eventually call).

**Files:**
- Modify: `internal/tui/viewanchor.go`

- [ ] **Step 1: Add the wrapping accessors**

Append to `internal/tui/viewanchor.go`:

```go
// streamTopRow returns the absolute m.lines index of the first visible row when
// browsing. Stage-0 seam: wraps the streamTop field verbatim (no behavior
// change). The flip (Task 5) rewrites only this body to resolve an anchor.
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
git add internal/tui/viewanchor.go
git commit -m "refactor(tui): add streamTop accessor seam (wraps field, no behavior change)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 4: Migrate all `streamTop` call sites to the accessors — mechanical, green

Replace every direct `m.streamTop` read with `m.streamTopRow()` and every write with `m.setStreamTopRow(...)`, **except** the field declaration, the two accessor bodies (Task 3), the `streamTop` branch inside `dragViewStateDown`, and the `streamTop` lines of `reRenderAll`'s clamp block (those four are deleted in Task 5; leave them touching the field until then). Every site still touches the same field, so it stays green.

**Files:**
- Modify: `internal/tui/*.go` non-test files referencing `streamTop` (grep first), plus `*_test.go`.

- [ ] **Step 1: Enumerate the production write sites**

Run: `grep -rn 'm\.streamTop\s*\(=\|+=\|-=\)' internal/tui/*.go | grep -v _test.go`
For each assignment, rewrite:
- `m.streamTop = X` → `m.setStreamTopRow(X)`
- `m.streamTop += X` → `m.setStreamTopRow(m.streamTopRow() + X)`
- `m.streamTop -= X` → `m.setStreamTopRow(m.streamTopRow() - X)`

Includes the ops methods: `viewport.go` `scrollBy` (`m.streamTop += delta`, `m.streamTop = 0`), `unstickFromTail` (`m.streamTop = idx + 1`, `m.streamTop = 0`); `blocks.go` (`m.streamTop = idx`, `= 0`); `search.go` (`m.streamTop = fil[top]`, `= top`); `update.go` (`m.streamTop = 0` ×2); `visual.go` (`m.streamTop = m.visualCursor`, `= m.visualCursor - h + 1`, `= 0`).

**Do NOT touch:** the `m.streamTop -= dropped` / `m.streamTop = 0` inside `dragViewStateDown` (`reconcile.go`), and the `m.streamTop > len(m.lines)` / `m.streamTop = len(m.lines)` / `m.streamTop < 0` clamp inside `reRenderAll` (`reconcile.go`). Those are removed in Task 5.

(Note: `maybeReStick` reads `m.streamTop` but does not assign it — handled in Step 2.)

- [ ] **Step 2: Replace the read sites**

Run: `grep -rn 'm\.streamTop\b' internal/tui/*.go | grep -v _test.go`
Every remaining `m.streamTop` that is NOT an assignment LHS, NOT the field decl, NOT inside the two seam bodies, NOT inside `dragViewStateDown`, NOT inside the `reRenderAll` clamp block → replace with `m.streamTopRow()`. This includes `maybeReStick`'s `for i := m.streamTop; ...` (→ `for i := m.streamTopRow(); ...`), `visual.go` reads (`m.visualCursor = m.streamTop` → `... = m.streamTopRow()`), and all render/view reads.

- [ ] **Step 3: Verify no stray direct field refs remain (outside the allowed spots)**

Run: `grep -rn 'm\.streamTop\b' internal/tui/*.go | grep -v _test.go`
Expected remaining lines ONLY: the field decl, `streamTopRow` body, `setStreamTopRow` body, the two `dragViewStateDown` uses, and the three `reRenderAll`-clamp uses. If anything else remains, migrate it.

- [ ] **Step 4: Build + test green**

Run: `go build ./... && go test ./internal/tui/ && go vet ./internal/tui/`
Expected: PASS. Same field underneath → behavior identical (including `TestScrollDownPastEndReSticks`).

- [ ] **Step 5: Migrate test reads/writes too (so the flip in Task 5 compiles)**

Run: `grep -rn '\.streamTop\b' internal/tui/*_test.go`
For each test site: read `x.streamTop` → `x.streamTopRow()`; write `x.streamTop = X` → `x.setStreamTopRow(X)`. Apply to ALL test files, including `app_test.go` (`lockedTop := m.streamTop` → `m.streamTopRow()`; `m.streamTop != lockedTop` → `m.streamTopRow() != lockedTop`; `m.streamTop != 0` → `m.streamTopRow() != 0`) and `reconcile_test.go`'s `TestReconcileEvictionDragsViewState` (`m.streamTop = 0`/`= 2` → setters; `m.streamTop != 1` → `m.streamTopRow() != 1`). Assertions/semantics unchanged.

- [ ] **Step 6: Verify no direct test field refs remain**

Run: `grep -rn '\.streamTop\b' internal/tui/*_test.go`
Expected: empty.

- [ ] **Step 7: Build + test green**

Run: `go test ./internal/tui/`
Expected: PASS (including both new/eviction tests).

- [ ] **Step 8: Commit**

```bash
git add internal/tui/
git commit -m "refactor(tui): migrate streamTop call sites to accessors (prod+tests, green)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 5: Flip `streamTop` storage to an anchor — the real work for this value

Change the field from `int` to `rowAnchor`, rewrite ONLY the two accessor bodies, remove the `streamTop` branch from `dragViewStateDown`, and delete the `streamTop` lines of `reRenderAll`'s clamp block. The compiler guarantees no site reads the field directly (Task 4 proved the grep empty outside the allowed spots).

**Files:**
- Modify: `internal/tui/app.go`, `internal/tui/viewanchor.go`, `internal/tui/reconcile.go`

- [ ] **Step 1: Replace the field declaration**

In `internal/tui/app.go`, change:
```go
	streamTop int // absolute index of the first visible row when !tailMode
```
to:
```go
	streamTopA rowAnchor // anchor for the first visible row when !tailMode (see viewanchor.go)
```

- [ ] **Step 2: Rewrite the accessor bodies to resolve the anchor**

In `internal/tui/viewanchor.go`, replace the two seam bodies from Task 3 with:
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

// setStreamTopRow stores the first-visible-row position as a stable anchor. A
// past-end index clamps to the last row (so over-scroll re-sticks); a negative
// index or empty window stores the sentinel, which streamTopRow resolves to 0.
func (m *model) setStreamTopRow(i int) { m.streamTopA = m.anchorForRow(i) }
```

- [ ] **Step 3: Remove the `streamTop` branch from `dragViewStateDown`**

In `dragViewStateDown` (`internal/tui/reconcile.go`), delete the leading block:
```go
	if !m.tailMode {
		m.streamTop -= dropped
		if m.streamTop < 0 {
			m.streamTop = 0
		}
	}
```
Leave the `searchHit`, visual, and `m.blockFocused = false` parts intact (they go in later tasks).

- [ ] **Step 4: Delete the `streamTop` clamp from `reRenderAll`**

In `reRenderAll` (`internal/tui/reconcile.go`), delete:
```go
	if m.streamTop > len(m.lines) {
		m.streamTop = len(m.lines)
	}
	if m.streamTop < 0 {
		m.streamTop = 0
	}
```
Leave the `searchHit` clamp (`if m.searchHit >= len(m.lines) { m.searchHit = -1 }`) for Task 6. The anchor now self-clamps against the re-rendered window.

- [ ] **Step 5: Build to confirm completeness**

Run: `go build ./...`
Expected: PASS. If it fails with `m.streamTop undefined`, a call site was missed in Task 4 — migrate it, then rebuild. (The compiler enforcing the flip.)

- [ ] **Step 6: Run the streamTop + re-stick regression tests**

Run: `go test -run 'TestReconcileEvictionDragsViewState|TestScrollDownPastEndReSticks|TestModelHomeJumpsToOldest|TestModelScrollbackTrimAdjustsStreamTop' ./internal/tui/ -v`
Expected: PASS. The eviction test now proves the anchor round-trips (row 2 → evict → resolves to 1); the re-stick test proves over-scroll re-pins to tail via the past-end clamp.

- [ ] **Step 7: Full package + race + vet green**

Run: `go test ./internal/tui/ && go test -race ./internal/tui/ && go vet ./internal/tui/`
Expected: PASS. Watch scroll/page/visual tests — `streamTop` drives the viewport.

- [ ] **Step 8: Commit**

```bash
git add internal/tui/
git commit -m "refactor(tui): flip streamTop to a stable anchor; drop its drag + reRenderAll clamp (5-3)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 6: `searchHit` — seam, migrate, flip (combined, value-scoped)

Same three moves as Tasks 3-5 but for `searchHit`. Its clamp is **unset to -1** (not 0). Commit at each green sub-stage.

**Files:**
- Modify: `internal/tui/app.go`, `internal/tui/viewanchor.go`, `internal/tui/reconcile.go`, `internal/tui/search.go`, `internal/tui/update.go`, `internal/tui/copyref.go`, and any other files referencing `searchHit` (grep first), plus `*_test.go`.

- [ ] **Step 1: Add the seam (wraps the int field)**

Append to `internal/tui/viewanchor.go`:
```go
// searchHitRow returns the absolute m.lines index of the current search hit, or
// -1 when none. Stage-0 seam wraps the field; the flip rewrites only this body.
func (m *model) searchHitRow() int { return m.searchHit }

// setSearchHitRow sets the current hit index (-1 = no hit). Stage-0 seam.
func (m *model) setSearchHitRow(i int) { m.searchHit = i }
```
Build + test green. Commit: `refactor(tui): add searchHit accessor seam`.

- [ ] **Step 2: Migrate production call sites**

Run: `grep -rn 'm\.searchHit\b' internal/tui/*.go | grep -v _test.go`
Replace reads → `m.searchHitRow()`, writes → `m.setSearchHitRow(...)`, EXCEPT the field decl, the two seam bodies, the `searchHit` block inside `dragViewStateDown`, and the `searchHit` clamp inside `reRenderAll` (both removed at Step 4). Compound forms: `from := m.searchHit + 1` → `from := m.searchHitRow() + 1`; `m.searchHit = idx` → `m.setSearchHitRow(idx)`; `m.searchHit >= 0` → `m.searchHitRow() >= 0`.
Verify the grep shows only the excluded spots. Build + test green.

- [ ] **Step 3: Migrate test call sites**

Run: `grep -rn '\.searchHit\b' internal/tui/*_test.go`
Reads → `searchHitRow()`, writes → `setSearchHitRow(...)`. Verify grep empty. Build + test green. Commit: `refactor(tui): migrate searchHit call sites to accessors (prod+tests, green)`.

- [ ] **Step 4: Flip storage to an anchor + delete its drag branch and reRenderAll clamp**

In `app.go`, change `searchHit int` → `searchHitA rowAnchor`.
In `viewanchor.go`, rewrite the seam bodies:
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
In `reconcile.go`, delete the `searchHit` block from `dragViewStateDown`:
```go
	if m.searchHit >= 0 {
		m.searchHit -= dropped
		if m.searchHit < 0 {
			m.searchHit = -1
		}
	}
```
and delete the `searchHit` clamp from `reRenderAll`:
```go
	if m.searchHit >= len(m.lines) {
		m.searchHit = -1
	}
```

- [ ] **Step 5: Build + test + race + vet green**

Run: `go build ./... && go test ./internal/tui/ && go test -race ./internal/tui/ && go vet ./internal/tui/`
Expected: PASS. If `m.searchHit undefined`, a site was missed — migrate and rebuild. Watch search tests (`search_test.go`, `regex_test.go`) and copy-reference tests.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/
git commit -m "refactor(tui): flip searchHit to a stable anchor; drop its drag + reRenderAll clamp (5-3)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 7: `visualCursor` + `visualAnchor` — seam, migrate, flip (as a pair)

Both flip together because the visual eviction test and the drag treat them as a unit (gated on `m.visualMode`). `visualCursor` clamps to 0; `visualAnchor` unsets to -1.

**Files:**
- Modify: `internal/tui/app.go`, `internal/tui/viewanchor.go`, `internal/tui/reconcile.go`, `internal/tui/visual.go` (and any other files referencing these — grep first), plus `*_test.go` including `visual_test.go`.

- [ ] **Step 1: Add both seams (wrap the int fields)**

Append to `internal/tui/viewanchor.go`:
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

Run: `grep -rn 'm\.visualCursor\b\|m\.visualAnchor\b' internal/tui/*.go | grep -v _test.go`
Reads → `visualCursorRow()` / `visualAnchorRow()`; writes → `setVisualCursorRow(...)` / `setVisualAnchorRow(...)`. Compound forms (e.g. `m.visualCursor += delta` in `moveVisualCursor`) become `m.setVisualCursorRow(m.visualCursorRow() + delta)`; `m.visualAnchor = m.visualCursor` → `m.setVisualAnchorRow(m.visualCursorRow())`. EXCLUDE the field decls, the four seam bodies, and the visual block inside `dragViewStateDown` (removed at Step 4). Verify the grep shows only those excluded sites. Build + test green.

- [ ] **Step 3: Migrate test call sites for both values**

Run: `grep -rn '\.visualCursor\b\|\.visualAnchor\b' internal/tui/*_test.go`
Reads → getters, writes → setters. Includes `TestVisualIndicesClampOnEviction`'s assertions `m.visualCursor != 0` → `m.visualCursorRow() != 0` and `m.visualAnchor != -1` → `m.visualAnchorRow() != -1`. Verify grep empty. Build + test green. Commit: `refactor(tui): migrate visual cursor/anchor call sites to accessors (prod+tests)`.

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
In `viewanchor.go`, rewrite the four seam bodies:
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
In `reconcile.go`, delete the visual block from `dragViewStateDown`:
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
Expected: PASS. Cursor/anchor set via `keyV`/`keyJ`/`keySpace` (now routing through setters); two evictions resolve to cursor=0 (clamp) and anchor=-1 (unset).

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

## Task 8: Delete `dragViewStateDown` and simplify its reconcile call

All four branches are gone; the function body is now only `m.blockFocused = false`. Eviction is fully handled by the resolvers. Inline the one residual side effect.

**Files:**
- Modify: `internal/tui/reconcile.go`

- [ ] **Step 1: Inspect the residual drag body**

Run: `grep -n 'dragViewStateDown\|blockFocused' internal/tui/reconcile.go`
Confirm `dragViewStateDown` now contains only `m.blockFocused = false` (plus its signature/comment).

- [ ] **Step 2: Inline the residual side effect into reconcile**

In `reconcile()` (`internal/tui/reconcile.go`), replace:
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

Remove the entire `dragViewStateDown` function (comment block + body) from `internal/tui/reconcile.go`.

- [ ] **Step 4: Verify it's gone and unreferenced**

Run: `grep -rn 'dragViewStateDown' internal/tui/`
Expected: empty.

- [ ] **Step 5: Build + full TUI suite + race + vet green**

Run: `go build ./... && go test ./internal/tui/ && go test -race ./internal/tui/ && go vet ./internal/tui/`
Expected: PASS, including both eviction regression tests + the re-stick test.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/reconcile.go
git commit -m "refactor(tui): delete dragViewStateDown; eviction handled by anchors (5-3)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 9: Final verification + stale-comment sweep + CHANGELOG

Confirm the success criteria, clean up comments that still describe view-state as absolute indices, and record the change.

**Files:**
- Modify: `internal/tui/*.go` (comment touch-ups only, if any), `CHANGELOG.md`

- [ ] **Step 1: Confirm no absolute-index view-state fields remain**

Run: `grep -rn 'streamTop\b\|searchHit\b\|visualCursor\b\|visualAnchor\b' internal/tui/*.go | grep -v 'Row\|streamTopA\|searchHitA\|visualCursorA\|visualAnchorA' | grep -v _test.go`
Expected: empty (only the `...A rowAnchor` fields and `...Row()` accessors remain, which the filter excludes). If a comment line matches, fix it to describe the anchor.

- [ ] **Step 2: Update lingering field-comment references**

Reword any struct/method comments still saying "absolute index" for these values (e.g. the `searchHit` field doc, `reRenderAll`'s remaining comment about clamping). Functional behavior unchanged.

- [ ] **Step 3: Full repo suite, race, vet**

Run: `go test ./... && go test -race ./internal/tui/ && go vet ./...`
Expected: PASS across all packages.

- [ ] **Step 4: Confirm the build flavors still compile (CGO-free invariant)**

Run: `go build -tags nomcp ./... && go build -tags nosse ./... && go build ./...`
Expected: PASS (this slice touches only `internal/tui`, but the locked rules require the tagged builds stay green).

- [ ] **Step 5: Add a CHANGELOG entry**

Under the current `### Internal:` section of `CHANGELOG.md`, add a bullet, e.g.:
```
- TUI view-state (`streamTop`/`searchHit`/`visualCursor`/`visualAnchor`) is now
  stored as stable `(entryID, rowOffset)` anchors that survive eviction and
  re-render; the `dragViewStateDown` index-drag and `reRenderAll`'s clamp block
  are removed. No user-visible behavior change.
```

- [ ] **Step 6: Commit**

```bash
git add internal/tui/ CHANGELOG.md
git commit -m "docs(tui): anchor-representation comment sweep + CHANGELOG (5-3)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Self-review notes (reconciled against the refreshed spec)

- **Spec coverage:** re-stick guard (Task 1), resolvers + index-domain rule (Task 2), streamTop seam/migrate/flip (Tasks 3-5), searchHit (Task 6), visualCursor+visualAnchor (Task 7), `dragViewStateDown` deletion (Task 8), `reRenderAll` clamp deletion (Tasks 5/6), regression tests preserved with accessor swaps (Tasks 4/6/7), success criteria + CHANGELOG (Task 9). Every spec section maps to a task.
- **Index-domain rule:** implemented once in `anchorForRow` — `idx<0`/empty → sentinel; in-range → exact; past-end-nonempty → last row. Each getter's `!ok` fallback supplies the per-value clamp (0 / -1 / 0 / -1).
- **Past-end is load-bearing:** Task 1's `TestScrollDownPastEndReSticks` fails under a sentinel→0 translation and passes under the last-row clamp — the proof the rule is needed.
- **Type consistency:** anchor `rowAnchor{id string; off int}`; helpers `rowForAnchor(rowAnchor)(int,bool)` / `anchorForRow(int) rowAnchor`; fields `streamTopA`/`searchHitA`/`visualCursorA`/`visualAnchorA`; accessors `streamTopRow`/`setStreamTopRow` etc. — identical across all tasks.
- **Two past-end writers handled:** `scrollBy` down (resolver clamp) and `reRenderAll`'s `m.streamTop = len(m.lines)` (deleted with the clamp block, never reaches a setter).
- **Watch-point:** the old `streamTop` drag was gated on `!m.tailMode`; the anchor getter resolves unconditionally. In tail mode `streamTop` is unused (viewport pinned to bottom), so this is not a behavior change — but if a tail-mode scroll test regresses, that is where to look. The full TUI suite is the guard.
