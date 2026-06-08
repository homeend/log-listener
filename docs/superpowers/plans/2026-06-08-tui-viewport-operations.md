# TUI Viewport / Selection Operations Layer (slice 5-3, pivoted) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract the TUI's scattered viewport/selection index arithmetic into three intent-level methods (`scrollBy`, `selectionBounds`, `moveVisualCursor`) so call sites compose verbs instead of repeating low-level clamp math, with zero behavior change.

**Architecture:** Pure extraction refactor. The four view-state values stay `int` fields; `dragViewStateDown` stays. Each operation reproduces the exact current behavior of the sites it replaces, proven by the existing TUI suite staying green unchanged plus new per-operation unit tests. TDD each method, then migrate its call sites, committing at each green step.

**Tech Stack:** Go 1.26, bubbletea/lipgloss TUI, `internal/tui` package. No new dependencies. Tests via `go test ./internal/tui/...`.

**Spec:** `docs/superpowers/specs/2026-06-08-tui-viewport-operations-design.md` (read it first — especially the "What the inventory found" justification and the `visualBar` care note).

---

## Background the implementer needs

**The model.** `internal/tui/app.go` defines `type model struct` with view-state fields `streamTop int` (first visible row when browsing), `visualCursor int`, `visualAnchor int` (-1 = unset), and `tailMode bool` (viewport pinned to bottom). `m.lines` is the flat `[]displayLine` slice; `m.contentHeight()` is the visible row count.

**Existing helpers you will call (do not reimplement):**
- `m.unstickFromTail()` — leaves tail mode for upward scrolling.
- `m.maybeReStick()` — re-enters tail mode if the viewport caught up to the latest line.
- `m.ensureVisualVisible()` (`visual.go:74`) — scrolls `streamTop` so `visualCursor` stays on screen.
- `m.collectVisible(rows)` — returns the absolute indices currently visible.

**Constants:** `vertFastStep` (fast-scroll row count); `page := m.contentHeight()` is computed locally in the page actions.

**The current scroll block** lives in the main key-dispatch switch in `app.go` (the `ActionScrollUp/ScrollDown/PageUp/PageDown/FastUp/FastDown` cases, around lines 614–698). Each case has the shape:
```go
case keymap.ActionScrollUp:
    m.blockFocused = false
    if m.showFiles {
        ... filesScroll ...
    } else if m.matcher != nil {
        m.searchPrev()
    } else {
        m.unstickFromTail()
        m.streamTop--
        if m.streamTop < 0 {
            m.streamTop = 0
        }
    }
```
Only the **innermost `else` branch** (the `streamTop` manipulation) changes in this plan. The `showFiles` and `matcher` branches stay exactly as they are.

**Build/test commands** (from repo root `/mnt/t/others/log-listener`):
- Single test: `go test -run TestName ./internal/tui/`
- Package: `go test ./internal/tui/`
- Full suite: `go test ./...`
- Vet: `go vet ./...`
- Race: `go test -race ./internal/tui/`
- Tagged builds (locked CGO-free invariant): `go build -tags nomcp ./... && go build -tags nosse ./...`

**Test helpers available** (from existing `*_test.go`): `newModel(scrollback int) *model`; `seedSearch(t, vals...)` (appends single-row text events to group "g" and sizes the window 80×12 — see `regex_test.go:25`); `key(m, msg)` to feed a key. Single-row seeded events mean `m.lines` index == entry position.

**Commit discipline:** every step ends green (build + `go test ./internal/tui/`). Each task is one operation: add it (TDD), migrate its sites, commit.

---

## Task 1: `scrollBy(delta)` — new `viewport.go`, TDD

**Files:**
- Create: `internal/tui/viewport.go`
- Create: `internal/tui/viewport_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/tui/viewport_test.go`. These build a browse-mode model with a known number of rows and assert `streamTop`/`tailMode` after `scrollBy`.

```go
package tui

import "testing"

// scrollModel seeds n single-row events, sizes the window, and returns a model
// in browse mode (tail off) at streamTop=0.
func scrollModel(t *testing.T, n int) *model {
	t.Helper()
	vals := make([]string, n)
	for i := range vals {
		vals[i] = string(rune('a' + i))
	}
	m := seedSearch(t, vals...)
	m.reconcile()
	m.tailMode = false
	m.streamTop = 0
	return m
}

func TestScrollByUpClampsAtZero(t *testing.T) {
	m := scrollModel(t, 5)
	m.streamTop = 1
	m.scrollBy(-3) // would go to -2
	if m.streamTop != 0 {
		t.Fatalf("streamTop = %d, want 0 (clamped)", m.streamTop)
	}
}

func TestScrollByUpLeavesTailMode(t *testing.T) {
	m := scrollModel(t, 5)
	m.tailMode = true
	m.streamTop = 4
	m.scrollBy(-1)
	if m.tailMode {
		t.Fatal("scrollBy(up) must leave tail mode (unstickFromTail)")
	}
}

func TestScrollByDownIsNoOpInTailMode(t *testing.T) {
	m := scrollModel(t, 5)
	m.tailMode = true
	before := m.streamTop
	m.scrollBy(2)
	if m.streamTop != before {
		t.Fatalf("scrollBy(down) in tail mode moved streamTop %d->%d, want no-op", before, m.streamTop)
	}
	if !m.tailMode {
		t.Fatal("scrollBy(down) in tail mode must stay in tail mode")
	}
}

func TestScrollByDownMovesWhenBrowsing(t *testing.T) {
	m := scrollModel(t, 20)
	m.tailMode = false
	m.streamTop = 0
	m.scrollBy(3)
	if m.streamTop != 3 {
		t.Fatalf("streamTop = %d, want 3", m.streamTop)
	}
}

func TestScrollByZeroIsNoOp(t *testing.T) {
	m := scrollModel(t, 5)
	m.streamTop = 2
	m.scrollBy(0)
	if m.streamTop != 2 {
		t.Fatalf("streamTop = %d, want 2 (zero delta no-op)", m.streamTop)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run TestScrollBy ./internal/tui/`
Expected: FAIL to compile — `m.scrollBy undefined`.

- [ ] **Step 3: Implement `viewport.go`**

Create `internal/tui/viewport.go`:

```go
package tui

// scrollBy moves the browse viewport by delta display rows: negative scrolls up
// (toward older lines), positive scrolls down (toward newer). It owns the
// up/down asymmetry that was previously duplicated across the six scroll
// actions:
//   - Up (delta < 0): leave tail mode (unstickFromTail) and clamp at the top
//     (streamTop never goes below 0).
//   - Down (delta > 0): only meaningful while browsing — tail mode ignores it,
//     since the viewport is already pinned to the bottom; after moving, let
//     maybeReStick re-enter tail mode if the view caught up to the latest line.
//   - delta == 0 is a no-op.
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

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -run TestScrollBy ./internal/tui/`
Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/tui/viewport.go internal/tui/viewport_test.go
git commit -m "feat(tui): add scrollBy(delta) viewport operation (5-3)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 2: Migrate the 6 scroll actions to `scrollBy`

Replace the innermost `streamTop` manipulation in each scroll action with one `scrollBy` call. Leave the `showFiles`/`matcher` branches untouched.

**Files:**
- Modify: `internal/tui/app.go` (the six scroll-action cases, ~lines 614–698)

- [ ] **Step 1: `ActionScrollUp`**

In `app.go`, the `ActionScrollUp` else-branch, replace:
```go
			} else {
				m.unstickFromTail()
				m.streamTop--
				if m.streamTop < 0 {
					m.streamTop = 0
				}
			}
```
with:
```go
			} else {
				m.scrollBy(-1)
			}
```

- [ ] **Step 2: `ActionScrollDown`**

The `ActionScrollDown` case ends with `else if !m.tailMode { m.streamTop++; m.maybeReStick() }`. Replace that final branch:
```go
			} else if !m.tailMode {
				m.streamTop++
				m.maybeReStick()
			}
```
with (the tail-mode guard now lives inside `scrollBy`):
```go
			} else {
				m.scrollBy(1)
			}
```

- [ ] **Step 3: `ActionPageUp`**

Replace the else-branch:
```go
			} else {
				m.unstickFromTail()
				m.streamTop -= page
				if m.streamTop < 0 {
					m.streamTop = 0
				}
			}
```
with:
```go
			} else {
				m.scrollBy(-page)
			}
```

- [ ] **Step 4: `ActionPageDown`**

Replace the final branch:
```go
			} else if !m.tailMode {
				m.streamTop += page
				m.maybeReStick()
			}
```
with:
```go
			} else {
				m.scrollBy(page)
			}
```

- [ ] **Step 5: `ActionFastUp`**

Replace the else-branch:
```go
			} else {
				m.unstickFromTail()
				m.streamTop -= vertFastStep
				if m.streamTop < 0 {
					m.streamTop = 0
				}
			}
```
with:
```go
			} else {
				m.scrollBy(-vertFastStep)
			}
```

- [ ] **Step 6: `ActionFastDown`**

Replace the final branch:
```go
			} else if !m.tailMode {
				m.streamTop += vertFastStep
				m.maybeReStick()
			}
```
with:
```go
			} else {
				m.scrollBy(vertFastStep)
			}
```

- [ ] **Step 7: Verify no scroll-arithmetic remains in those actions**

Run: `grep -n "m\.streamTop\s*[-+]=\|m\.streamTop--\|m\.streamTop++" internal/tui/app.go`
Expected: only the line inside `dragViewStateDown` (`m.streamTop -= dropped`). The six scroll actions no longer touch `streamTop` directly.

- [ ] **Step 8: Build + full TUI suite + vet + race green**

Run: `go build ./... && go test ./internal/tui/ && go vet ./internal/tui/ && go test -race ./internal/tui/`
Expected: PASS. The existing scroll/page tests (`*_test.go`) prove behavior is unchanged.

- [ ] **Step 9: Commit**

```bash
git add internal/tui/app.go
git commit -m "refactor(tui): route the six scroll actions through scrollBy (5-3)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 3: `selectionBounds()` + migrate its 3 sites

Add the order-the-pair method, then replace the three verbatim copies. The `visualBar` rewrite needs care (caret row first, bar only when anchored).

**Files:**
- Modify: `internal/tui/visual.go`
- Create: `internal/tui/selection_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/tui/selection_test.go`:

```go
package tui

import "testing"

func TestSelectionBoundsNoAnchorIsCaretRow(t *testing.T) {
	m := seedSearch(t, "a", "b", "c")
	m.reconcile()
	m.visualCursor = 2
	m.visualAnchor = -1
	lo, hi := m.selectionBounds()
	if lo != 2 || hi != 2 {
		t.Fatalf("no anchor: got (%d,%d), want (2,2)", lo, hi)
	}
}

func TestSelectionBoundsAnchorBelowCursorIsOrdered(t *testing.T) {
	m := seedSearch(t, "a", "b", "c", "d")
	m.reconcile()
	m.visualAnchor = 1
	m.visualCursor = 3
	lo, hi := m.selectionBounds()
	if lo != 1 || hi != 3 {
		t.Fatalf("got (%d,%d), want (1,3)", lo, hi)
	}
}

func TestSelectionBoundsAnchorAboveCursorIsOrdered(t *testing.T) {
	m := seedSearch(t, "a", "b", "c", "d")
	m.reconcile()
	m.visualAnchor = 3
	m.visualCursor = 1
	lo, hi := m.selectionBounds()
	if lo != 1 || hi != 3 {
		t.Fatalf("got (%d,%d), want (1,3) (ordered)", lo, hi)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run TestSelectionBounds ./internal/tui/`
Expected: FAIL to compile — `m.selectionBounds undefined`.

- [ ] **Step 3: Implement `selectionBounds`**

In `internal/tui/visual.go`, add (e.g. just after `exitVisual`):

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

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -run TestSelectionBounds ./internal/tui/`
Expected: PASS (3 tests).

- [ ] **Step 5: Migrate `buildVisualText`**

In `visual.go`, replace the body of `buildVisualText`:
```go
func buildVisualText(m *model) string {
	lo, hi := m.visualCursor, m.visualCursor
	if m.visualAnchor >= 0 {
		lo, hi = m.visualAnchor, m.visualCursor
		if lo > hi {
			lo, hi = hi, lo
		}
	}
	return m.textForRows(rangeSlice(lo, hi))
}
```
with:
```go
func buildVisualText(m *model) string {
	lo, hi := m.selectionBounds()
	return m.textForRows(rangeSlice(lo, hi))
}
```

- [ ] **Step 6: Migrate `buildVisualRef`**

Replace the leading idiom in `buildVisualRef`:
```go
func buildVisualRef(m *model) string {
	lo, hi := m.visualCursor, m.visualCursor
	if m.visualAnchor >= 0 {
		lo, hi = m.visualAnchor, m.visualCursor
		if lo > hi {
			lo, hi = hi, lo
		}
	}
	a, b := m.entryIDForLine(lo), m.entryIDForLine(hi)
```
with:
```go
func buildVisualRef(m *model) string {
	lo, hi := m.selectionBounds()
	a, b := m.entryIDForLine(lo), m.entryIDForLine(hi)
```
(Leave the rest of the function unchanged.)

- [ ] **Step 7: Migrate `visualBar` (with care)**

Replace `visualBar`:
```go
func (m *model) visualBar(idx int) (string, bool) {
	if !m.visualMode {
		return "", false
	}
	if idx == m.visualCursor {
		return visualCaretStyle.Render("▶") + " ", true
	}
	if m.visualAnchor >= 0 {
		lo, hi := m.visualAnchor, m.visualCursor
		if lo > hi {
			lo, hi = hi, lo
		}
		if idx >= lo && idx <= hi {
			return visualSelStyle.Render("┃") + " ", true
		}
	}
	return "", false
}
```
with:
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
The caret-on-cursor-row check stays first; the `visualAnchor >= 0` guard stays (so the bar only renders when anchored); `selectionBounds` returns the same ordered pair the inline code did.

- [ ] **Step 8: Verify the idiom is gone**

Run: `grep -n "lo, hi = m.visualAnchor, m.visualCursor\|lo, hi := m.visualAnchor, m.visualCursor" internal/tui/*.go`
Expected: empty (the only remaining occurrence is *inside* `selectionBounds`, which uses `lo, hi = m.visualAnchor, m.visualCursor` — that one is expected; confirm no others remain in `visualBar`/`buildVisualText`/`buildVisualRef`).

- [ ] **Step 9: Build + full TUI suite + vet + race green**

Run: `go build ./... && go test ./internal/tui/ && go vet ./internal/tui/ && go test -race ./internal/tui/`
Expected: PASS. The visual copy/ref/bar tests prove behavior unchanged.

- [ ] **Step 10: Commit**

```bash
git add internal/tui/visual.go internal/tui/selection_test.go
git commit -m "refactor(tui): add selectionBounds(); collapse the order-the-pair idiom (5-3)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 4: `moveVisualCursor(delta)` + migrate its 2 sites

**Files:**
- Modify: `internal/tui/visual.go`
- Create: `internal/tui/movecursor_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/tui/movecursor_test.go`:

```go
package tui

import "testing"

func TestMoveVisualCursorWithinRange(t *testing.T) {
	m := seedSearch(t, "a", "b", "c", "d")
	m.reconcile()
	m.tailMode = false
	m.streamTop = 0
	m.visualMode = true
	m.visualCursor = 1
	m.moveVisualCursor(1)
	if m.visualCursor != 2 {
		t.Fatalf("visualCursor = %d, want 2", m.visualCursor)
	}
}

func TestMoveVisualCursorClampsAtTop(t *testing.T) {
	m := seedSearch(t, "a", "b", "c")
	m.reconcile()
	m.tailMode = false
	m.streamTop = 0
	m.visualMode = true
	m.visualCursor = 0
	m.moveVisualCursor(-1)
	if m.visualCursor != 0 {
		t.Fatalf("visualCursor = %d, want 0 (clamped at top)", m.visualCursor)
	}
}

func TestMoveVisualCursorClampsAtBottom(t *testing.T) {
	m := seedSearch(t, "a", "b", "c")
	m.reconcile()
	m.tailMode = false
	m.streamTop = 0
	m.visualMode = true
	m.visualCursor = 2 // last row (len 3 → max index 2)
	m.moveVisualCursor(1)
	if m.visualCursor != 2 {
		t.Fatalf("visualCursor = %d, want 2 (clamped at bottom)", m.visualCursor)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run TestMoveVisualCursor ./internal/tui/`
Expected: FAIL to compile — `m.moveVisualCursor undefined`.

- [ ] **Step 3: Implement `moveVisualCursor`**

In `internal/tui/visual.go`, add (e.g. just after `ensureVisualVisible`):

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

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -run TestMoveVisualCursor ./internal/tui/`
Expected: PASS (3 tests).

- [ ] **Step 5: Migrate `handleVisualKey`**

In `visual.go`, in the movement switch, replace:
```go
	case "up", "k":
		if m.visualCursor > 0 {
			m.visualCursor--
		}
		m.ensureVisualVisible()
	case "down", "j":
		if m.visualCursor < len(m.lines)-1 {
			m.visualCursor++
		}
		m.ensureVisualVisible()
```
with:
```go
	case "up", "k":
		m.moveVisualCursor(-1)
	case "down", "j":
		m.moveVisualCursor(1)
```

- [ ] **Step 6: Build + full TUI suite + vet + race green**

Run: `go build ./... && go test ./internal/tui/ && go vet ./internal/tui/ && go test -race ./internal/tui/`
Expected: PASS. `TestVisualIndicesClampOnEviction` and the visual movement tests prove behavior unchanged.

- [ ] **Step 7: Commit**

```bash
git add internal/tui/visual.go internal/tui/movecursor_test.go
git commit -m "refactor(tui): add moveVisualCursor; collapse handleVisualKey movement (5-3)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 5: Final verification

**Files:** none (verification only; commit any stray comment fix).

- [ ] **Step 1: Confirm the operations are the single home for their arithmetic**

Run: `grep -rn "m\.streamTop\s*[-+]=\|m\.streamTop--\|m\.streamTop++" internal/tui/*.go | grep -v _test.go`
Expected: only `dragViewStateDown`'s `m.streamTop -= dropped`. (Scroll math lives only in `scrollBy`.)

Run: `grep -rn "m\.visualCursor--\|m\.visualCursor++" internal/tui/*.go | grep -v _test.go`
Expected: empty (cursor moves live only in `moveVisualCursor`).

- [ ] **Step 2: Full repo suite, vet, race**

Run: `go test ./... && go vet ./... && go test -race ./internal/tui/`
Expected: PASS across all packages.

- [ ] **Step 3: Tagged builds (CGO-free locked invariant)**

Run: `go build -tags nomcp ./... && go build -tags nosse ./... && go build ./...`
Expected: PASS.

- [ ] **Step 4: Commit (only if a comment touch-up was needed; otherwise skip)**

```bash
git add internal/tui/
git commit -m "docs(tui): tidy comments after viewport-ops extraction (5-3)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Self-review notes (reconciled against the spec)

- **Spec coverage:** `scrollBy` (Tasks 1–2), `selectionBounds` (Task 3), `moveVisualCursor` (Task 4), success-criteria verification (Task 5). All three operations and their migrations map to tasks; the YAGNI exclusions (`centerOn`/`scrollToTop`/`ensureVisible`/`isSearchHit`) are intentionally absent.
- **No storage change:** no task touches the field declarations or `dragViewStateDown` logic; `streamTop -= dropped` is explicitly expected to remain (Task 2 Step 7, Task 5 Step 1).
- **Type consistency:** `scrollBy(delta int)`, `selectionBounds() (lo, hi int)`, `moveVisualCursor(delta int)` — used identically in tests and migrations. `selectionBounds` returns `(lo, hi)` in `[lo<=hi]` order; callers use them as inclusive bounds (matching `rangeSlice(lo, hi)` and `idx >= lo && idx <= hi`).
- **Behavior-equivalence watch-points:** (1) `ActionScrollDown/PageDown/FastDown` drop their own `!m.tailMode` guard because `scrollBy` enforces it — verified by the no-op-in-tail-mode test. (2) `moveVisualCursor` clamps the result rather than guarding the step; identical for ±1, the only deltas used. (3) `visualBar` keeps the caret-row check before the bar range. The full existing TUI suite is the backstop for all three.
