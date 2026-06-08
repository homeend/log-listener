# TUI Scroll Operations Round 2 (scrollFiles + panBy) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract the file-overlay scroll and horizontal pan arithmetic into two intent-level methods (`scrollFiles`, `panBy`), collapsing the 6 `showFiles` scroll branches and the 4 pan handlers, with zero behavior change.

**Architecture:** Continuation of the viewport-operations layer. Add each method (TDD) in `internal/tui/viewport.go` alongside `scrollBy`, then migrate its call sites in `app.go`. `filesScroll`/`horizScroll` stay `int`. Behavior is byte-equivalent; existing file-overlay and horizontal-scroll tests stay green.

**Tech Stack:** Go 1.26, bubbletea TUI, `internal/tui`. No new deps.

**Spec:** `docs/superpowers/specs/2026-06-08-tui-scroll-ops-round2-design.md` (read it — especially the clamp-order note for `scrollFiles` and the out-of-scope list).

---

## Background

`internal/tui/app.go` `model` has `filesScroll int` (file-overlay cursor) and `horizScroll int` (horizontal pan offset, ≥0). Constants: `vertFastStep=10`, `horizStep=10`, `horizFastStep=50`. `m.files` is `[]FileEntry`. The six scroll actions (`ActionScrollUp/ScrollDown/PageUp/PageDown/FastUp/FastDown`) each branch on `m.showFiles` for the file-overlay path; the four pan actions are `ActionScrollLeft/ScrollRight/FastLeft/FastRight`. `scrollBy` (from the prior slice) already lives in `viewport.go`.

**Commands** (from `/mnt/t/others/log-listener`): `go test ./internal/tui/`, `go test ./...`, `go vet ./...`, `go test -race ./internal/tui/`, `go build -tags nomcp ./... && go build -tags nosse ./...`. Test helper `seedSearch(t, vals...)` exists in `regex_test.go`.

**Commit discipline:** each task ends green (build + `go test ./internal/tui/`).

---

## Task 1: `scrollFiles(delta)` + migrate the 6 showFiles branches

**Files:**
- Modify: `internal/tui/viewport.go` (add method)
- Modify: `internal/tui/app.go` (6 showFiles branches)
- Create: `internal/tui/scrollfiles_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/tui/scrollfiles_test.go`:

```go
package tui

import "testing"

// filesModel returns a model with n file entries and filesScroll at 0.
func filesModel(t *testing.T, n int) *model {
	t.Helper()
	m := seedSearch(t, "x") // gives a sized model
	m.files = make([]FileEntry, n)
	for i := range m.files {
		m.files[i] = FileEntry{Path: string(rune('a' + i))}
	}
	m.showFiles = true
	m.filesScroll = 0
	return m
}

func TestScrollFilesWithinRange(t *testing.T) {
	m := filesModel(t, 5)
	m.filesScroll = 1
	m.scrollFiles(2)
	if m.filesScroll != 3 {
		t.Fatalf("filesScroll = %d, want 3", m.filesScroll)
	}
}

func TestScrollFilesClampsAtTop(t *testing.T) {
	m := filesModel(t, 5)
	m.filesScroll = 0
	m.scrollFiles(-3)
	if m.filesScroll != 0 {
		t.Fatalf("filesScroll = %d, want 0 (clamped at top)", m.filesScroll)
	}
}

func TestScrollFilesClampsAtBottom(t *testing.T) {
	m := filesModel(t, 5)
	m.filesScroll = 4 // last index
	m.scrollFiles(10)
	if m.filesScroll != 4 {
		t.Fatalf("filesScroll = %d, want 4 (clamped at bottom)", m.filesScroll)
	}
}

func TestScrollFilesEmptyListStaysZero(t *testing.T) {
	m := filesModel(t, 0)
	m.filesScroll = 0
	m.scrollFiles(5) // high clamp to len-1=-1, then low clamp to 0
	if m.filesScroll != 0 {
		t.Fatalf("filesScroll = %d, want 0 (empty list)", m.filesScroll)
	}
}
```

Note: `FileEntry` is `struct { Path string; Group string }` (confirmed in `app.go:54`). The tests only need `len(m.files) == n`, so the `Path` values are cosmetic — `m.files = make([]FileEntry, n)` alone would also work.

- [ ] **Step 2: Verify tests FAIL to compile**

Run: `go test -run TestScrollFiles ./internal/tui/`
Expected: `m.scrollFiles undefined`.

- [ ] **Step 3: Implement scrollFiles**

In `internal/tui/viewport.go`, add after `scrollBy`:

```go
// scrollFiles moves the file-overlay cursor by delta entries, clamped to the
// file list range [0, len(m.files)-1]. Centralizes the showFiles branches of
// the six scroll actions. Clamp order (high then low) matches the old
// PageDown/FastDown code, so the empty-list edge (len-1 == -1) resolves to 0,
// and the ±1 result equals the old guard-and-skip moves.
func (m *model) scrollFiles(delta int) {
	m.filesScroll += delta
	if m.filesScroll > len(m.files)-1 {
		m.filesScroll = len(m.files) - 1
	}
	if m.filesScroll < 0 {
		m.filesScroll = 0
	}
}
```

- [ ] **Step 4: Verify tests PASS**

Run: `go test -run TestScrollFiles ./internal/tui/`
Expected: PASS (4 tests).

- [ ] **Step 5: Migrate the 6 showFiles branches in app.go (Edit tool, exact match, TAB indent)**

Edit A — `ActionScrollUp` showFiles branch:
OLD:
```
			if m.showFiles {
				if m.filesScroll > 0 {
					m.filesScroll--
				}
			} else if m.matcher != nil {
				m.searchPrev()
```
NEW:
```
			if m.showFiles {
				m.scrollFiles(-1)
			} else if m.matcher != nil {
				m.searchPrev()
```

Edit B — `ActionScrollDown` showFiles branch:
OLD:
```
			if m.showFiles {
				if m.filesScroll < len(m.files)-1 {
					m.filesScroll++
				}
			} else if m.matcher != nil {
				m.searchNext()
```
NEW:
```
			if m.showFiles {
				m.scrollFiles(1)
			} else if m.matcher != nil {
				m.searchNext()
```

Edit C — `ActionPageUp` showFiles branch:
OLD:
```
			if m.showFiles {
				m.filesScroll -= page
				if m.filesScroll < 0 {
					m.filesScroll = 0
				}
			} else {
				m.scrollBy(-page)
			}
```
NEW:
```
			if m.showFiles {
				m.scrollFiles(-page)
			} else {
				m.scrollBy(-page)
			}
```

Edit D — `ActionPageDown` showFiles branch:
OLD:
```
			if m.showFiles {
				m.filesScroll += page
				if m.filesScroll > len(m.files)-1 {
					m.filesScroll = len(m.files) - 1
				}
				if m.filesScroll < 0 {
					m.filesScroll = 0
				}
			} else {
				m.scrollBy(page)
			}
```
NEW:
```
			if m.showFiles {
				m.scrollFiles(page)
			} else {
				m.scrollBy(page)
			}
```

Edit E — `ActionFastUp` showFiles branch:
OLD:
```
			if m.showFiles {
				m.filesScroll -= vertFastStep
				if m.filesScroll < 0 {
					m.filesScroll = 0
				}
			} else {
				m.scrollBy(-vertFastStep)
			}
```
NEW:
```
			if m.showFiles {
				m.scrollFiles(-vertFastStep)
			} else {
				m.scrollBy(-vertFastStep)
			}
```

Edit F — `ActionFastDown` showFiles branch:
OLD:
```
			if m.showFiles {
				m.filesScroll += vertFastStep
				if m.filesScroll > len(m.files)-1 {
					m.filesScroll = len(m.files) - 1
				}
				if m.filesScroll < 0 {
					m.filesScroll = 0
				}
			} else {
				m.scrollBy(vertFastStep)
			}
```
NEW:
```
			if m.showFiles {
				m.scrollFiles(vertFastStep)
			} else {
				m.scrollBy(vertFastStep)
			}
```

Do NOT touch the `ActionTop`/`ActionBottom` file branches (jumps — out of scope), the reset-to-0 sites, or any read.

- [ ] **Step 6: Verify no filesScroll delta-arithmetic remains in the actions**

Run: `grep -n "m\.filesScroll\s*[-+]=\|m\.filesScroll--\|m\.filesScroll++" internal/tui/app.go`
Expected: empty (the only `filesScroll` writes left are `= 0` resets, `= len(m.files)-1` in ActionBottom, and the reconcile reset-to-0 clamps — none of which are `+=/-=/++/--`).

- [ ] **Step 7: Build + suite + vet + race green**

Run: `go build ./... && go test ./internal/tui/ && go vet ./internal/tui/ && go test -race ./internal/tui/`
Expected: PASS. Existing file-overlay tests prove behavior unchanged.

- [ ] **Step 8: Commit**

```bash
git add internal/tui/viewport.go internal/tui/app.go internal/tui/scrollfiles_test.go
git commit -m "refactor(tui): add scrollFiles; collapse the six file-overlay scroll branches

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 2: `panBy(delta)` + migrate the 4 pan handlers

**Files:**
- Modify: `internal/tui/viewport.go` (add method)
- Modify: `internal/tui/app.go` (4 pan handlers)
- Create: `internal/tui/panby_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/tui/panby_test.go`:

```go
package tui

import "testing"

func TestPanByRightMoves(t *testing.T) {
	m := seedSearch(t, "x")
	m.horizScroll = 0
	m.panBy(10)
	if m.horizScroll != 10 {
		t.Fatalf("horizScroll = %d, want 10", m.horizScroll)
	}
}

func TestPanByLeftClampsAtZero(t *testing.T) {
	m := seedSearch(t, "x")
	m.horizScroll = 5
	m.panBy(-50)
	if m.horizScroll != 0 {
		t.Fatalf("horizScroll = %d, want 0 (clamped at left edge)", m.horizScroll)
	}
}

func TestPanByRightHasNoUpperClamp(t *testing.T) {
	m := seedSearch(t, "x")
	m.horizScroll = 1000
	m.panBy(50)
	if m.horizScroll != 1050 {
		t.Fatalf("horizScroll = %d, want 1050 (no upper clamp)", m.horizScroll)
	}
}
```

- [ ] **Step 2: Verify tests FAIL to compile**

Run: `go test -run TestPanBy ./internal/tui/`
Expected: `m.panBy undefined`.

- [ ] **Step 3: Implement panBy**

In `internal/tui/viewport.go`, add after `scrollFiles`:

```go
// panBy pans the horizontal view by delta columns, clamped at the left edge
// (horizScroll ≥ 0). There is no right-edge clamp: the renderer clips overlong
// lines, so over-panning right shows blank — matching the old inline
// ScrollRight/FastRight behavior. Centralizes the four pan handlers.
func (m *model) panBy(delta int) {
	m.horizScroll += delta
	if m.horizScroll < 0 {
		m.horizScroll = 0
	}
}
```

- [ ] **Step 4: Verify tests PASS**

Run: `go test -run TestPanBy ./internal/tui/`
Expected: PASS (3 tests).

- [ ] **Step 5: Migrate the 4 pan handlers in app.go (Edit tool, exact match, TAB indent)**

OLD:
```
		case keymap.ActionScrollLeft:
			m.horizScroll -= horizStep
			if m.horizScroll < 0 {
				m.horizScroll = 0
			}
		case keymap.ActionScrollRight:
			m.horizScroll += horizStep
		case keymap.ActionFastLeft:
			m.horizScroll -= horizFastStep
			if m.horizScroll < 0 {
				m.horizScroll = 0
			}
		case keymap.ActionFastRight:
			m.horizScroll += horizFastStep
```
NEW:
```
		case keymap.ActionScrollLeft:
			m.panBy(-horizStep)
		case keymap.ActionScrollRight:
			m.panBy(horizStep)
		case keymap.ActionFastLeft:
			m.panBy(-horizFastStep)
		case keymap.ActionFastRight:
			m.panBy(horizFastStep)
```

Do NOT touch `ActionResetHoriz` (`m.horizScroll = 0`) or the `horizScroll = 0` on clear, or reads.

- [ ] **Step 6: Verify no horizScroll delta-arithmetic remains in the actions**

Run: `grep -n "m\.horizScroll\s*[-+]=" internal/tui/app.go`
Expected: empty (only `= 0` resets and reads remain in app.go; note `search.go` still assigns `m.horizScroll = ns` in `adjustHorizToHit` — that is a direct positional set, not delta arithmetic, and is out of scope; leave it).

- [ ] **Step 7: Build + suite + vet + race green**

Run: `go build ./... && go test ./internal/tui/ && go vet ./internal/tui/ && go test -race ./internal/tui/`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/tui/viewport.go internal/tui/app.go internal/tui/panby_test.go
git commit -m "refactor(tui): add panBy; collapse the four horizontal pan handlers

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 3: Final verification

- [ ] **Step 1: Confirm the ops are the single home for that arithmetic**

Run: `grep -rn "m\.filesScroll\s*[-+]=\|m\.filesScroll--\|m\.filesScroll++\|m\.horizScroll\s*[-+]=" internal/tui/*.go | grep -v _test.go`
Expected: only the lines inside `scrollFiles` and `panBy` themselves (in `viewport.go`).

- [ ] **Step 2: Full suite + vet + race**

Run: `go test ./... && go vet ./... && go test -race ./internal/tui/`
Expected: PASS.

- [ ] **Step 3: Tagged builds**

Run: `go build -tags nomcp ./... && go build -tags nosse ./... && go build ./...`
Expected: PASS.

---

## Self-review notes

- **Spec coverage:** `scrollFiles` (Task 1), `panBy` (Task 2), verification (Task 3). The out-of-scope items (groupsScroll/renderersScroll, filesScroll reset/jumps, horizScroll resets/reads, `adjustHorizToHit`'s positional set) are intentionally untouched and called out in the migration steps.
- **Type consistency:** `scrollFiles(delta int)`, `panBy(delta int)` — methods on `*model`, used identically in tests and migrations.
- **Behavior-equivalence watch-points:** (1) `scrollFiles` clamp order is high-then-low, matching the old PageDown/FastDown code (and subsuming the guard-and-skip ±1 moves and low-only PageUp/FastUp). (2) `panBy` has no upper clamp (right pan relies on the renderer clipping) — tested by `TestPanByRightHasNoUpperClamp`. The existing TUI suite is the backstop.
