# TUI Focus Indicator + Visual Selection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `│` focused-block margin indicator (shows what `y` will copy) and a vim-style `v` visual line-selection mode that copies a `range:<id>..<id>` reference via OSC 52.

**Architecture:** Both reuse the existing left-margin bar pattern (`exceptionBar` prepend in `renderStream`) and the `entryIDForLine` + OSC 52 copy path. The focus column derives from `cursorIndex()` (no persistent cursor); visual mode is a modal sub-state routed at the top of key handling like `searchInput`, with its own cursor/anchor. Full-row background highlighting is intentionally avoided (embedded ANSI resets would clear it) in favour of margin bars.

**Tech Stack:** Go 1.26, bubbletea/lipgloss, `go-runewidth` (`dispWidth`), `go-osc52/v2`.

**Spec:** `docs/superpowers/specs/2026-06-07-tui-focus-and-visual-select-design.md`

---

## File Structure

- `internal/tui/copyref.go` (modify) — extract `osc52Copy(ref string)` shared by both copy paths.
- `internal/tui/app.go` (modify) — visual-mode model fields + `newModel` init; `renderStream` margin wiring; modal route `if m.visualMode`; `ActionVisualSelect` dispatch; `renderFooter` VISUAL hint; eviction clamp in `trimToCap`.
- `internal/tui/focusbar.go` (new) — `focusBar`, `focusBarStyle`, `focusBarWidth`.
- `internal/tui/visual.go` (new) — `handleVisualKey`, `enterVisual`, `exitVisual`, `ensureVisualVisible`, `buildVisualRef`, `copyVisualSelection`, `visualBar`, styles/widths.
- `internal/keymap/actions.go`, `defaults.go` (modify) — `ActionVisualSelect` / `v`.
- `internal/tui/focusbar_test.go`, `internal/tui/visual_test.go` (new).
- `KEYBINDINGS.md` (regenerated), `README.md`, `CHANGELOG.md`.

---

### Task 1: Extract `osc52Copy` shared helper

**Files:**
- Modify: `internal/tui/copyref.go`

- [ ] **Step 1: Read the current `copyReference`** in `internal/tui/copyref.go`. It contains `_, _ = osc52.New(ref).WriteTo(os.Stderr)`. We extract that one line into a helper so visual mode can reuse it (DRY).

- [ ] **Step 2: Add the helper and call it.** In `internal/tui/copyref.go`, add:
```go
// osc52Copy writes ref to the terminal clipboard via the OSC 52 escape on
// stderr (stderr, not stdout, so it does not corrupt the stdout-driven render).
func osc52Copy(ref string) {
	_, _ = osc52.New(ref).WriteTo(os.Stderr)
}
```
and change `copyReference`'s body to call it:
```go
func copyReference(m *model) string {
	ref := buildReference(m)
	if ref == "" {
		return ""
	}
	osc52Copy(ref)
	return ref
}
```
(Keep the `osc52` and `os` imports — `osc52Copy` still uses both.)

- [ ] **Step 3: Run** `go test ./internal/tui/` → expect PASS (the existing copyref tests still pass; pure refactor).

- [ ] **Step 4: Commit**
```bash
git add internal/tui/copyref.go
git commit -m "refactor(tui): extract osc52Copy shared by copy paths

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 2: Focused-block column (`focusBar`)

**Files:**
- Modify: `internal/tui/app.go` (model struct fields; `newModel`; `renderStream`)
- Create: `internal/tui/focusbar.go`
- Create: `internal/tui/focusbar_test.go`

- [ ] **Step 1: Add visual-mode model fields** (declared now so `focusBar` can reference `visualMode`; behaviour added in Task 3). In `internal/tui/app.go`, add to the `model` struct (near `searchHit`):
```go
	// Visual selection mode (vim-style `v`): visualMode gates the modal key
	// path; visualCursor is the moving line; visualAnchor is the selection
	// start (-1 until the first space sets it).
	visualMode   bool
	visualCursor int
	visualAnchor int
```
And in `newModel`, add `visualAnchor: -1,` to the struct literal (next to `searchHit: -1,`).

- [ ] **Step 2: Write the failing test** — create `internal/tui/focusbar_test.go`:
```go
package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/render"
)

func seedFocus(m *model, vals ...string) {
	for _, v := range vals {
		m.appendEvent(render.Event{Group: "g", File: "/a.log",
			Rendered: []render.Part{{Type: "text", Value: v}}})
	}
}

func TestFocusBarOnBlockOnly(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	// idx 0 single, [1,2] a multi-line block, idx 3 single.
	seedFocus(m, "single one", "block head:\n  cont a", "single two")
	// m.lines: 0 single, 1 head, 2 cont, 3 single  (the block is lines 1-2)
	m.tailMode = false
	m.streamTop = 1 // cursor in the block
	m.ensureBlocks()
	if _, ok := m.focusBar(1); !ok {
		t.Error("block head (1) should be focused")
	}
	if _, ok := m.focusBar(2); !ok {
		t.Error("block cont (2) should be focused")
	}
	if _, ok := m.focusBar(0); ok {
		t.Error("single line 0 should NOT be focused")
	}
	if _, ok := m.focusBar(3); ok {
		t.Error("single line 3 should NOT be focused")
	}
}

func TestFocusBarGoneWhenCursorOffBlock(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	seedFocus(m, "block head:\n  cont a", "single")
	m.tailMode = false
	m.streamTop = 2 // the trailing single line (block is lines 0-1)
	m.ensureBlocks()
	if _, ok := m.focusBar(0); ok {
		t.Error("cursor off the block → no focus bar")
	}
}

func TestFocusBarSuppressedInTailAndVisual(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	seedFocus(m, "block head:\n  cont a")
	m.ensureBlocks()
	m.tailMode = true // tailing → cursorIndex() == -1
	if _, ok := m.focusBar(0); ok {
		t.Error("tail mode → no focus bar")
	}
	m.tailMode = false
	m.streamTop = 0
	m.visualMode = true // visual mode owns the gutter
	if _, ok := m.focusBar(0); ok {
		t.Error("visual mode → no focus bar")
	}
}

func TestFocusBarWidthSafe(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 24, Height: 6})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	// A focused EXCEPTION block → stacks │ + ▌ in the margin.
	seedFocus(m, "panic: "+strings.Repeat("X", 80), "  at frame")
	m.tailMode = false
	m.streamTop = 0
	view := m.renderStream(m.contentHeight())
	if !strings.Contains(view, "│") || !strings.Contains(view, "▌") {
		t.Fatalf("expected both focus and exception bars:\n%s", view)
	}
	for _, ln := range strings.Split(view, "\n") {
		if w := dispWidth(ln); w != m.width {
			t.Errorf("row should be exactly width %d, got %d: %q", m.width, w, ln)
		}
	}
}
```

- [ ] **Step 3: Run** `go test ./internal/tui/ -run TestFocusBar` → expect FAIL (`undefined: focusBar`).

- [ ] **Step 4: Implement `internal/tui/focusbar.go`:**
```go
package tui

import "github.com/charmbracelet/lipgloss"

// focusBarStyle renders the focused-block bar in an accent colour (cyan),
// distinct from the red exception bar (colour "9").
var focusBarStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))

// focusBarWidth is the display-column width of the "│ " prefix, MEASURED (the
// box-drawing │ U+2502 is East-Asian ambiguous, like the exception ▌), so a
// barred row's width accounting stays exact.
var focusBarWidth = dispWidth("│ ")

// focusBar returns the styled "│ " prefix and true when the row at idx belongs
// to the FOCUSED block — the multi-line block (End > Start) containing
// cursorIndex(), i.e. exactly the block `y` would copy. Suppressed in visual
// mode (the selection margin owns the gutter then) and when not focused on a
// multi-line block. The returned width (focusBarWidth) MUST be added to the
// row's visW so clipLine pads/clips against the true width.
func (m *model) focusBar(idx int) (string, bool) {
	if m.visualMode {
		return "", false
	}
	cur := m.cursorIndex()
	if cur < 0 {
		return "", false
	}
	m.ensureBlocks()
	for _, b := range m.blocks {
		if cur < b.Start {
			break // blocks are ordered; no later block contains cur
		}
		if cur <= b.End {
			if b.End > b.Start && idx >= b.Start && idx <= b.End {
				return focusBarStyle.Render("│") + " ", true
			}
			return "", false
		}
	}
	return "", false
}
```

- [ ] **Step 5: Wire `focusBar` into `renderStream`.** In `internal/tui/app.go`'s `renderStream`, change the per-row block so the focus bar is prepended **after** the exception bar (so focus ends up leftmost → `│ ▌ body`):
```go
		styled, visW := m.renderDisplayLineAt(idx)
		if bar, ok := m.exceptionBar(idx); ok {
			styled = bar + styled
			visW += exceptionBarWidth
		}
		if fb, ok := m.focusBar(idx); ok {
			styled = fb + styled
			visW += focusBarWidth
		}
		rendered = append(rendered, m.clipLine(styled, visW))
```

- [ ] **Step 6: Run** `go test ./internal/tui/ -run TestFocusBar` → PASS. Then `go test ./internal/tui/` → PASS (no regressions). Then `go test -race ./internal/tui/` → PASS.

- [ ] **Step 7: Commit**
```bash
git add internal/tui/app.go internal/tui/focusbar.go internal/tui/focusbar_test.go
git commit -m "feat(tui): focused-block column (│) showing the y-copy target

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 3: Visual mode core (state machine + copy, no rendering yet)

**Files:**
- Create: `internal/tui/visual.go`
- Create: `internal/tui/visual_test.go`
- Modify: `internal/tui/app.go` (modal route; `ActionVisualSelect` dispatch)
- Modify: `internal/keymap/actions.go`, `internal/keymap/defaults.go`
- Regenerate: `KEYBINDINGS.md`

- [ ] **Step 1: Write the failing test** — create `internal/tui/visual_test.go`:
```go
package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/render"
)

func seedVisual(m *model, vals ...string) {
	for i, v := range vals {
		m.appendEvent(render.Event{ID: "L" + itoa36(i), Group: "g", File: "/a.log",
			Rendered: []render.Part{{Type: "text", Value: v}}})
	}
}

func key(m *model, k tea.KeyMsg) *model {
	m2, _ := m.Update(k)
	return m2.(*model)
}

var (
	keyV     = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}}
	keyJ     = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}}
	keySpace = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}}
	keyEsc   = tea.KeyMsg{Type: tea.KeyEsc}
)

func newVisualModel(t *testing.T, vals ...string) *model {
	t.Helper()
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	seedVisual(m, vals...)
	return m
}

func TestVisualEnter(t *testing.T) {
	m := newVisualModel(t, "a", "b", "c")
	m.tailMode = false
	m.streamTop = 0
	m = key(m, keyV)
	if !m.visualMode || m.visualAnchor != -1 {
		t.Fatalf("after v: visualMode=%v anchor=%d", m.visualMode, m.visualAnchor)
	}
	if m.tailMode {
		t.Error("v should leave tail mode")
	}
}

func TestVisualTwoSpaceCopiesRange(t *testing.T) {
	m := newVisualModel(t, "a", "b", "c", "d") // IDs L0..L3, one line each
	m.tailMode = false
	m.streamTop = 0
	m = key(m, keyV)       // enter, cursor at line 0 (L0)
	m = key(m, keyJ)       // cursor → line 1 (L1)
	m = key(m, keySpace)   // anchor = L1
	if m.visualAnchor != 1 {
		t.Fatalf("anchor should be 1, got %d", m.visualAnchor)
	}
	m = key(m, keyJ)       // cursor → line 2 (L2)
	m = key(m, keySpace)   // copy range L1..L2, exit
	if m.visualMode {
		t.Error("second space should exit visual mode")
	}
	if m.flash != "copied range:L1..L2" {
		t.Fatalf("flash = %q, want copied range:L1..L2", m.flash)
	}
}

func TestVisualRefNormalisesOrder(t *testing.T) {
	m := newVisualModel(t, "a", "b", "c")
	m.visualAnchor = 2
	m.visualCursor = 0
	if got := buildVisualRef(m); got != "range:L0..L2" {
		t.Fatalf("buildVisualRef = %q, want range:L0..L2", got)
	}
}

func TestVisualEscCancels(t *testing.T) {
	m := newVisualModel(t, "a", "b", "c")
	m.tailMode = false
	m.streamTop = 0
	m = key(m, keyV)
	m = key(m, keyJ)
	m = key(m, keySpace) // anchor set
	m = key(m, keyEsc)   // cancel
	if m.visualMode {
		t.Error("esc should exit visual mode")
	}
	if m.flash != "" {
		t.Errorf("esc must not copy/flash, got %q", m.flash)
	}
}
```
(`itoa36` already exists in `internal/tui/idparity_test.go`, same package.)

- [ ] **Step 2: Run** `go test ./internal/tui/ -run TestVisual` → expect FAIL (`undefined: buildVisualRef`, and `v` not bound).

- [ ] **Step 3: Implement `internal/tui/visual.go`:**
```go
package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// enterVisual starts visual selection mode with the cursor on the top visible
// row and no anchor yet. Leaves tail mode so the cursor is stable.
func (m *model) enterVisual() {
	if len(m.lines) == 0 {
		return
	}
	m.unstickFromTail()
	m.tailMode = false
	m.visualMode = true
	m.visualAnchor = -1
	if vis := m.collectVisible(m.contentHeight()); len(vis) > 0 {
		m.visualCursor = vis[0]
	} else {
		m.visualCursor = m.streamTop
	}
	if m.visualCursor < 0 {
		m.visualCursor = 0
	}
}

// exitVisual leaves visual mode and clears the anchor.
func (m *model) exitVisual() {
	m.visualMode = false
	m.visualAnchor = -1
}

// ensureVisualVisible scrolls streamTop so visualCursor stays on screen.
func (m *model) ensureVisualVisible() {
	h := m.contentHeight()
	if h <= 0 {
		return
	}
	if m.visualCursor < m.streamTop {
		m.streamTop = m.visualCursor
	} else if m.visualCursor >= m.streamTop+h {
		m.streamTop = m.visualCursor - h + 1
	}
	if m.streamTop < 0 {
		m.streamTop = 0
	}
}

// buildVisualRef is the pure seam: the range over the inclusive line span
// [min(anchor,cursor), max], as range:<entryID(min)>..<entryID(max)>, or "" if
// either endpoint can't be resolved.
func buildVisualRef(m *model) string {
	lo, hi := m.visualAnchor, m.visualCursor
	if lo > hi {
		lo, hi = hi, lo
	}
	a, b := m.entryIDForLine(lo), m.entryIDForLine(hi)
	if a == "" || b == "" {
		return ""
	}
	return fmt.Sprintf("range:%s..%s", a, b)
}

// copyVisualSelection copies the current selection's reference (OSC 52) and
// flashes it.
func (m *model) copyVisualSelection() {
	ref := buildVisualRef(m)
	if ref == "" {
		return
	}
	osc52Copy(ref)
	m.flash = "copied " + ref
}

// handleVisualKey processes keys while in visual mode. Only up/down (arrows +
// j/k), space, and esc act; any other key is ignored (stays in visual mode).
func (m *model) handleVisualKey(msg tea.KeyMsg) *model {
	switch msg.String() {
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
	case " ":
		if m.visualAnchor < 0 {
			m.visualAnchor = m.visualCursor
		} else {
			m.copyVisualSelection()
			m.exitVisual()
		}
	case "esc":
		m.exitVisual()
	}
	return m
}
```

- [ ] **Step 4: Route visual keys + add the enter action.** In `internal/tui/app.go`'s `Update`, in the `tea.KeyMsg` branch, add the modal route right after `m.flash = ""` and before `if m.searchInput {`:
```go
		if m.visualMode {
			return m.handleVisualKey(msg), nil
		}
```
And add the dispatch case in the action `switch` (near `case keymap.ActionSaveViewport:`):
```go
		case keymap.ActionVisualSelect:
			m.enterVisual()
```

- [ ] **Step 5: Register the keymap action.** In `internal/keymap/actions.go` add the constant:
```go
	ActionVisualSelect Action = "visual_select"
```
and a registry row in the action list:
```go
	{ActionVisualSelect, "Visual select", "Enter visual line-selection mode (space sets then copies a range; esc cancels).", "main"},
```
In `internal/keymap/defaults.go` add the default key (verify `v` is unused first — it is):
```go
		ActionVisualSelect:         {"v"},
```

- [ ] **Step 6: Run + regenerate docs.**
- `go test ./internal/tui/ -run TestVisual` → PASS.
- `go test ./internal/keymap/` → `TestDocsUpToDate` FAILS (KEYBINDINGS.md stale). If `TestAllActionsUniqueAndNonEmpty` asserts a hardcoded action count, bump it by 1.
- `./build.sh keybindings-docs`
- `go test ./internal/keymap/` → PASS.

- [ ] **Step 7: Full suite** `go test ./... && go vet ./...` → PASS.

- [ ] **Step 8: Commit**
```bash
git add internal/tui/visual.go internal/tui/visual_test.go internal/tui/app.go internal/keymap/ KEYBINDINGS.md
git commit -m "feat(tui): visual selection mode core (v / space / esc → range copy)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 4: Visual selection rendering (`visualBar`) + footer hint

**Files:**
- Modify: `internal/tui/visual.go` (add `visualBar` + styles/widths)
- Modify: `internal/tui/app.go` (`renderStream` visual branch; `renderFooter` hint)
- Modify: `internal/tui/visual_test.go` (rendering test)

- [ ] **Step 1: Write the failing test** — append to `internal/tui/visual_test.go`:
```go
import "strings" // ensure present in the import block

func TestVisualBarRendersCursorAndSelection(t *testing.T) {
	m := newVisualModel(t, "a", "b", "c", "d")
	m.tailMode = false
	m.streamTop = 0
	m = key(m, keyV)     // cursor on line 0
	m = key(m, keyJ)     // cursor → 1
	m = key(m, keySpace) // anchor = 1
	m = key(m, keyJ)     // cursor → 2 (selection 1..2)
	view := m.renderStream(m.contentHeight())
	if !strings.Contains(view, "▶") {
		t.Fatalf("expected the visual cursor caret ▶:\n%s", view)
	}
	if !strings.Contains(view, "┃") {
		t.Fatalf("expected a selection bar ┃:\n%s", view)
	}
	for _, ln := range strings.Split(view, "\n") {
		if w := dispWidth(ln); w != m.width {
			t.Errorf("row should be exactly width %d, got %d: %q", m.width, w, ln)
		}
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/tui/ -run TestVisualBar` → FAIL (no `▶`/`┃` because rendering isn't wired).

- [ ] **Step 3: Implement `visualBar`** — add to `internal/tui/visual.go`:
```go
import "github.com/charmbracelet/lipgloss" // add to the import block

// visualCaretStyle/visualSelStyle: bright caret for the cursor row, accent bar
// for the rest of the selection.
var (
	visualCaretStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("11")) // bright yellow
	visualSelStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("14")) // cyan
	// Both prefixes MUST render to the same display width so clipLine accounts
	// for them uniformly. Measured; ▶ and ┃ are East-Asian ambiguous.
	visualBarWidth = dispWidth("▶ ")
)

// visualBar returns the gutter prefix and true for rows in visual mode: a caret
// on the cursor row, a selection bar on rows within the (anchored) selection.
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
If `dispWidth("▶ ") != dispWidth("┃ ")` on your platform, pad the narrower one to `visualBarWidth` with a trailing space so both prefixes are exactly `visualBarWidth` columns; the width-safety test will catch a mismatch.

- [ ] **Step 4: Wire into `renderStream`.** Replace the per-row margin block (from Task 2) so visual mode owns the gutter exclusively:
```go
		styled, visW := m.renderDisplayLineAt(idx)
		if m.visualMode {
			if vb, ok := m.visualBar(idx); ok {
				styled = vb + styled
				visW += visualBarWidth
			}
		} else {
			if bar, ok := m.exceptionBar(idx); ok {
				styled = bar + styled
				visW += exceptionBarWidth
			}
			if fb, ok := m.focusBar(idx); ok {
				styled = fb + styled
				visW += focusBarWidth
			}
		}
		rendered = append(rendered, m.clipLine(styled, visW))
```

- [ ] **Step 5: Footer hint.** In `internal/tui/app.go`'s `renderFooter`, add a branch at the TOP (before `if m.searchInput {`):
```go
	if m.visualMode {
		return headerBg.Width(m.width).MaxHeight(1).Render(" VISUAL  ↑↓ move · space set/copy · esc cancel ")
	}
```

- [ ] **Step 6: Run** `go test ./internal/tui/ -run TestVisual` → PASS. Then `go test ./internal/tui/ && go test -race ./internal/tui/` → PASS.

- [ ] **Step 7: Commit**
```bash
git add internal/tui/visual.go internal/tui/app.go internal/tui/visual_test.go
git commit -m "feat(tui): visual-mode caret/selection margin + footer hint

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 5: Clamp visual indices on eviction

**Files:**
- Modify: `internal/tui/app.go` (`trimToCap`)
- Modify: `internal/tui/visual_test.go`

`trimToCap` evicts whole entries from the head and drags `streamTop`/`searchHit` down by the evicted line count. `visualCursor`/`visualAnchor` are absolute `m.lines` indices and must track the same shift, or a long-running visual session would point at the wrong lines after eviction.

- [ ] **Step 1: Write the failing test** — append to `internal/tui/visual_test.go`:
```go
func TestVisualIndicesClampOnEviction(t *testing.T) {
	m := newModel(3) // cap 3 lines
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	seedVisual(m, "a", "b", "c") // lines 0,1,2
	m.tailMode = false
	m.streamTop = 0
	m = key(m, keyV)      // cursor 0
	m = key(m, keyJ)      // cursor 1
	m = key(m, keySpace)  // anchor 1
	// Appending two more entries evicts the two oldest lines (cap 3).
	seedVisual(m, "d", "e")
	if m.visualCursor < 0 || m.visualCursor >= len(m.lines) {
		t.Errorf("visualCursor out of range after eviction: %d (len %d)", m.visualCursor, len(m.lines))
	}
	if m.visualAnchor >= len(m.lines) {
		t.Errorf("visualAnchor out of range after eviction: %d (len %d)", m.visualAnchor, len(m.lines))
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/tui/ -run TestVisualIndicesClamp` → expect FAIL (indices not adjusted).

- [ ] **Step 3: Implement** — in `internal/tui/app.go` `trimToCap`, in the block that already adjusts `streamTop`/`searchHit` after computing `dropLines` (only when entries were dropped), add:
```go
	if m.visualMode {
		m.visualCursor -= dropLines
		if m.visualCursor < 0 {
			m.visualCursor = 0
		}
		if m.visualAnchor >= 0 {
			m.visualAnchor -= dropLines
			if m.visualAnchor < 0 {
				m.visualAnchor = -1 // anchor scrolled off → unset
			}
		}
	}
```
Place this alongside the existing `if !m.tailMode { m.streamTop -= dropLines … }` / `if m.searchHit >= 0 { … }` adjustments (after `m.entries`/`m.lines` are resliced, using the same `dropLines`).

- [ ] **Step 4: Run** `go test ./internal/tui/ -run TestVisual && go test ./internal/tui/` → PASS. `go test -race ./internal/tui/` → PASS.

- [ ] **Step 5: Commit**
```bash
git add internal/tui/app.go internal/tui/visual_test.go
git commit -m "fix(tui): drag visual cursor/anchor on scrollback eviction

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 6: Docs (README, CHANGELOG)

**Files:**
- Modify: `README.md`, `CHANGELOG.md`

- [ ] **Step 1: Update README.md.** In the TUI keybindings table, add a `v` row: "Visual select — enter line-selection mode; `space` sets the start then copies a `range:` reference, `esc` cancels." Near the `y` copy-reference description, add a sentence on the focused-block `│` indicator: "A cyan `│` in the left margin marks the block `y` will copy; it disappears when the cursor isn't on a multi-line block." Match the README's existing tone/structure (read it first).

- [ ] **Step 2: Update CHANGELOG.md.** Add an entry under `[Unreleased]` (match the existing format): "TUI: focused-block `│` indicator (live preview of the `y` copy target) and a vim-style `v` visual line-selection mode that copies a `range:<id>..<id>` reference via OSC 52."

- [ ] **Step 3: Verify** `go test ./...` → PASS (including `TestDocsUpToDate`; do NOT regenerate `KEYBINDINGS.md` again — it was regenerated in Task 3).

- [ ] **Step 4: Commit**
```bash
git add README.md CHANGELOG.md
git commit -m "docs: focused-block indicator + visual selection mode

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Final verification (after all tasks)
```bash
go test ./... && go vet ./... && go test -race ./internal/tui/ && CGO_ENABLED=0 ./build.sh build-static
```
All must be green. Then dispatch a final whole-implementation code review before finishing the branch.

## Notes
- Prepend order in `renderStream` is load-bearing: exception is prepended first, focus second, so focus ends up **leftmost** (`│ ▌ body`), matching the approved preview.
- Visual mode owns the gutter exclusively (no focus/exception bars while selecting), keeping the margin unambiguous.
- `cursorIndex()` returns -1 in tail mode, so the focus bar is naturally absent while tailing.
