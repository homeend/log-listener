# TUI Word Wrap Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a toggleable word-wrap mode to the streaming TUI that wraps long lines to multiple terminal rows instead of clipping them behind horizontal pan.

**Architecture:** Wrap happens entirely at paint time (render-time expansion). `m.lines` / `displayCache` / `rowAnchor` are untouched — they stay width-independent. A single helper (`wrapLine`) splits one fully-styled row into N terminal rows by calling the existing ANSI-aware `clipANSIWindow` at advancing column offsets. `collectVisible` becomes height-aware so the viewport never overflows; Page/Fast jumps translate terminal-rows → logical-lines; pan is disabled while wrapping. All new behavior is gated on `m.wordWrap`, so the wrap-OFF path is byte-identical to today.

**Tech Stack:** Go 1.26, bubbletea, lipgloss, go-runewidth. Tests via `go test ./...`.

**Spec:** `docs/superpowers/specs/2026-06-09-tui-word-wrap-design.md`

---

## File map

| File | Change |
|------|--------|
| `internal/tui/width.go` | NEW `wrapLine` helper (reuses `clipANSIWindow`). |
| `internal/tui/app.go` | `model.wordWrap bool`; `Options.WordWrap`; `New` wires it. |
| `internal/tui/view.go` | Extract `renderVisibleRow`; height-aware `collectVisible` + `visibleRowCost`; `renderStreamWrapped`; footer `wrap` indicator. |
| `internal/tui/viewport.go` | `panBy` no-op when wrapping; `vstep` terminal-row→line translation. |
| `internal/tui/update.go` | `ActionToggleWordWrap` handler; Page/Fast use `vstep`. |
| `internal/keymap/actions.go` | `ActionToggleWordWrap` const + `AllActions` entry. |
| `internal/keymap/actions_test.go` | Action-count guard 38 → 39. |
| `internal/keymap/defaults.go` | `ActionToggleWordWrap: {"w"}`. |
| `KEYBINDINGS.md` | Regenerated. |
| `internal/config/yaml.go` | `TUI.WordWrap *bool` + flatten. |
| `internal/config/cli.go` | `Config.TUIWordWrap bool`. |
| `main.go` | `Options.WordWrap: cfg.TUIWordWrap`. |
| `log-listener.example.yml` | Document `word_wrap: false`. |
| `README.md`, `CHANGELOG.md` | Document the feature. |

**Untouched (verify you did not edit):** `internal/tui/viewanchor.go`, `internal/render/*`, `internal/linebuf/*`.

---

### Task 1: `wrapLine` helper

**Files:**
- Modify: `internal/tui/width.go`
- Test: `internal/tui/width_test.go`

`clipANSIWindow(line, skip, width)` (in `view.go`) already returns the `[skip, skip+width)` display-column window of a styled line, padded to exactly `width` columns, preserving ANSI escapes and replacing a straddling wide rune with a filler space. `wrapLine` calls it at advancing offsets to produce the wrapped rows.

- [ ] **Step 1: Write the failing test**

Add to `internal/tui/width_test.go`:

```go
func TestWrapLineSingleRowWhenFits(t *testing.T) {
	got := wrapLine("hello", 5, 10)
	if len(got) != 1 {
		t.Fatalf("want 1 row, got %d: %q", len(got), got)
	}
	if stripANSI(got[0]) != "hello     " { // padded to width 10
		t.Fatalf("want padded to width, got %q", stripANSI(got[0]))
	}
}

func TestWrapLineSplitsOverflow(t *testing.T) {
	// 12 visible cols into width 5 => ceil(12/5) = 3 rows.
	line := "abcdefghijkl"
	got := wrapLine(line, 12, 5)
	if len(got) != 3 {
		t.Fatalf("want 3 rows, got %d", len(got))
	}
	for i, r := range got {
		if w := dispWidth(stripANSI(r)); w != 5 {
			t.Fatalf("row %d width = %d, want 5 (%q)", i, w, r)
		}
	}
	if joined := stripANSI(got[0]) + stripANSI(got[1]) + stripANSI(got[2]); joined != "abcdefghijkl   " {
		t.Fatalf("rejoined rows lost content: %q", joined)
	}
}

func TestWrapLineAlwaysAtLeastOneRow(t *testing.T) {
	if got := wrapLine("", 0, 10); len(got) != 1 {
		t.Fatalf("empty line should still occupy 1 row, got %d", len(got))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestWrapLine ./internal/tui/`
Expected: FAIL — `undefined: wrapLine`.

- [ ] **Step 3: Implement `wrapLine`**

Append to `internal/tui/width.go`:

```go
// wrapLine splits a fully-styled terminal row of visible width visW into
// ceil(visW/width) rows of exactly `width` display columns, reusing
// clipANSIWindow so ANSI styling, the search highlight, and wide-rune straddle
// are preserved across the wrap boundary. Always returns at least one row.
func wrapLine(line string, visW, width int) []string {
	if width <= 0 {
		return []string{line}
	}
	n := (visW + width - 1) / width
	if n < 1 {
		n = 1
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, clipANSIWindow(line, i*width, width))
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -run TestWrapLine ./internal/tui/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/width.go internal/tui/width_test.go
git commit -m "feat(tui): wrapLine helper splitting a styled row into wrapped rows"
```

---

### Task 2: `wordWrap` model state + Options wiring

**Files:**
- Modify: `internal/tui/app.go`
- Test: `internal/tui/app_test.go`

State only — no behavior yet. Mirrors how `truncateFiles` is wired (`Options` field → `New` assignment → model field).

- [ ] **Step 1: Write the failing test**

Add to `internal/tui/app_test.go`. This compiles only once the `model.wordWrap`
field and `Options.WordWrap` both exist, and it pins the safe default (off). The
`New` copy of `Options.WordWrap → m.wordWrap` is one line mirroring three sibling
field assignments; its effect is exercised end-to-end by the build in Task 9.

```go
func TestWordWrapDefaultsOff(t *testing.T) {
	m := newModel(100)
	if m.wordWrap {
		t.Fatal("word wrap should default off")
	}
	var o Options
	if o.WordWrap {
		t.Fatal("Options.WordWrap should default false")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestOptionsWordWrap ./internal/tui/`
Expected: FAIL — `m.wordWrap undefined` / `unknown field WordWrap in Options`.

- [ ] **Step 3: Add the field, the Option, and the New wiring**

In `internal/tui/app.go`, add to the `Options` struct (after `FilenameWidth int`):

```go
	WordWrap      bool                   // tui.word_wrap default
```

Add to the `model` struct, right after the `truncateFiles` / `filenameWidth` block:

```go
	// Word wrap: when true, long lines wrap to multiple terminal rows instead
	// of being clipped behind horizontal pan. Paint-time only; m.lines and the
	// viewstate anchors are unaffected.
	wordWrap bool
```

In `New`, after `m.filenameWidth = opts.FilenameWidth`:

```go
	m.wordWrap = opts.WordWrap
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -run TestOptionsWordWrap ./internal/tui/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/app.go internal/tui/app_test.go
git commit -m "feat(tui): wordWrap model field + Options.WordWrap wiring"
```

---

### Task 3: Extract `renderVisibleRow` (pure refactor)

**Files:**
- Modify: `internal/tui/view.go`
- Test: `internal/tui/view_test.go` (create if absent)

`renderStream` currently inlines "render the displayLine, then prepend the visual/exception/focus gutter bar and add its width." Extract that into `renderVisibleRow(idx)` so it can be the single source of width truth shared by the paint path AND the height accounting (Task 4). This task changes no behavior.

- [ ] **Step 1: Write the failing test**

Create `internal/tui/view_test.go` (or append):

```go
package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/render"
)

func TestRenderVisibleRowIncludesPrefixWidth(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	m.appendEvent(render.Event{Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "hello"}}})
	styled, visW := m.renderVisibleRow(0)
	// prefix "[g] a.log: " (11 cols) + body "hello" (5) = 16.
	if visW != 16 {
		t.Fatalf("visW = %d, want 16", visW)
	}
	if got := dispWidth(stripANSI(styled)); got != 16 {
		t.Fatalf("styled width = %d, want 16", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestRenderVisibleRow ./internal/tui/`
Expected: FAIL — `m.renderVisibleRow undefined`.

- [ ] **Step 3: Extract the helper and call it from `renderStream`**

In `internal/tui/view.go`, add the method (place it just above `renderStream`):

```go
// renderVisibleRow builds the full terminal row for line idx, including any
// leading gutter bar (visual selection, exception mark, or focus). It is the
// single source of width truth shared by the paint path (renderStream) and the
// wrap height accounting (visibleRowCost).
func (m *model) renderVisibleRow(idx int) (string, int) {
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
	return styled, visW
}
```

Then replace the per-line block inside `renderStream`'s `for _, idx := range visible` loop. The loop currently reads:

```go
	for _, idx := range visible {
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
	}
```

Replace it with:

```go
	for _, idx := range visible {
		styled, visW := m.renderVisibleRow(idx)
		rendered = append(rendered, m.clipLine(styled, visW))
	}
```

- [ ] **Step 4: Run tests to verify pass (incl. no regression)**

Run: `go test ./internal/tui/`
Expected: PASS — new test passes and every existing tui test still passes (pure refactor).

- [ ] **Step 5: Commit**

```bash
git add internal/tui/view.go internal/tui/view_test.go
git commit -m "refactor(tui): extract renderVisibleRow as the shared row-width source"
```

---

### Task 4: Height-aware `collectVisible` + `visibleRowCost`

**Files:**
- Modify: `internal/tui/view.go`
- Test: `internal/tui/view_test.go`

When wrapping, a logical line occupies `ceil(visW/width)` terminal rows. `collectVisible` must stop once the collected lines' **summed** height fills `rows` terminal rows — not once it has `rows` lines — or the viewport overflows and re-triggers the vanishing-header glitch. Gated on `wordWrap`: when off, cost is always 1 and the walk is byte-identical to today.

- [ ] **Step 1: Write the failing test**

Add to `internal/tui/view_test.go`:

```go
func TestVisibleRowCostWrapOffIsOne(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	m.appendEvent(render.Event{Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "short"}}})
	if c := m.visibleRowCost(0); c != 1 {
		t.Fatalf("wrap off cost = %d, want 1", c)
	}
}

func TestCollectVisibleHeightAwareWhenWrapping(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	// Each line is ~60 visible cols of body; prefix "[g] a.log: " = 11 => ~71.
	long := "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
	for i := 0; i < 10; i++ {
		m.appendEvent(render.Event{Group: "g", File: "/a.log",
			Rendered: []render.Part{{Type: "text", Value: long}}})
	}
	m.wordWrap = true
	m.width = 40 // 71 cols / 40 => 2 rows per line
	// Ask for 6 terminal rows: 2 rows/line => 3 lines fill it.
	got := m.collectVisible(6)
	if len(got) != 3 {
		t.Fatalf("height-aware collect returned %d lines, want 3", len(got))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run "TestVisibleRowCost|TestCollectVisibleHeightAware" ./internal/tui/`
Expected: FAIL — `m.visibleRowCost undefined` (and the collect test returns 6, not 3).

- [ ] **Step 3: Add `visibleRowCost` and make `collectVisible` budget by height**

In `internal/tui/view.go`, add above `collectVisible`:

```go
// visibleRowCost is how many terminal rows line idx occupies when painted: 1
// unless word wrap is on and the rendered row is wider than the viewport.
func (m *model) visibleRowCost(idx int) int {
	if !m.wordWrap {
		return 1
	}
	_, visW := m.renderVisibleRow(idx)
	if m.width <= 0 || visW <= m.width {
		return 1
	}
	return (visW + m.width - 1) / m.width
}
```

Replace the body of `collectVisible` with the height-budgeted version (each branch now accumulates `used` terminal rows instead of counting entries; with wrap off `visibleRowCost == 1`, so it collects exactly `rows` entries as before):

```go
func (m *model) collectVisible(rows int) []int {
	if rows <= 0 || len(m.lines) == 0 {
		return nil
	}
	if m.filterMode {
		fil := m.filteredIndices()
		if len(fil) == 0 {
			return nil
		}
		start := 0
		for start < len(fil) && fil[start] < m.streamTopRow() {
			start++
		}
		if start >= len(fil) {
			start = len(fil) - 1
		}
		out := make([]int, 0, rows)
		used := 0
		for k := start; k < len(fil) && used < rows; k++ {
			out = append(out, fil[k])
			used += m.visibleRowCost(fil[k])
		}
		return out
	}
	out := make([]int, 0, rows)
	used := 0
	if m.tailMode {
		for i := len(m.lines) - 1; i >= 0 && used < rows; i-- {
			if !m.lineEnabled(m.lines[i]) {
				continue
			}
			out = append(out, i)
			used += m.visibleRowCost(i)
		}
		// reverse (we collected newest→oldest)
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
		return out
	}
	for i := m.streamTopRow(); i < len(m.lines) && used < rows; i++ {
		if !m.lineEnabled(m.lines[i]) {
			continue
		}
		out = append(out, i)
		used += m.visibleRowCost(i)
	}
	return out
}
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./internal/tui/`
Expected: PASS — new tests pass; existing collectVisible/viewport tests still pass (wrap-off path unchanged).

- [ ] **Step 5: Commit**

```bash
git add internal/tui/view.go internal/tui/view_test.go
git commit -m "feat(tui): height-aware collectVisible + visibleRowCost for wrap"
```

---

### Task 5: `renderStreamWrapped` paint path

**Files:**
- Modify: `internal/tui/view.go`
- Test: `internal/tui/view_test.go`

With wrap on, expand every visible line into its wrapped rows, then window to exactly `rows`: tail mode bottom-aligns (drops the topmost line's leading rows so the newest line stays pinned to the bottom); browse mode top-aligns (drops the bottom line's trailing rows). Pad short.

- [ ] **Step 1: Write the failing test**

This test distinguishes *wrapping* from the existing *clip* path. With 3 visible
lines (height-aware `collectVisible` from Task 4 returns 3 for 6 rows at 2 rows
each) the wrap path fills **all 6 rows with content** (3 lines × 2 wrapped rows).
The non-wrap clip path renders only 3 content rows + 3 blank pad rows, so the
"every row has content" assertion fails until `renderStreamWrapped` exists.

Add to `internal/tui/view_test.go`:

```go
func TestRenderStreamWrappedFillsRowsWithContinuations(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	long := "yyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyy"
	for i := 0; i < 8; i++ {
		m.appendEvent(render.Event{Group: "g", File: "/a.log",
			Rendered: []render.Part{{Type: "text", Value: long}}})
	}
	m.wordWrap = true
	m.width = 40 // ~71 visible cols per line => 2 wrapped rows each
	out := m.renderStream(6)
	lines := strings.Split(out, "\n")
	if len(lines) != 6 {
		t.Fatalf("renderStream produced %d rows, want exactly 6", len(lines))
	}
	for i, ln := range lines {
		if w := dispWidth(stripANSI(ln)); w != 40 {
			t.Fatalf("row %d width = %d, want 40", i, w)
		}
		if !strings.Contains(ln, "y") {
			t.Fatalf("row %d is blank; wrap should fill it with a continuation: %q", i, ln)
		}
	}
}
```

Add `"strings"` to the test file's imports if not present.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestRenderStreamWrapped ./internal/tui/`
Expected: FAIL — rows 4–6 are blank pad (the non-wrap clip path drops each line's
tail instead of wrapping it), so the `Contains("y")` assertion fails.

- [ ] **Step 3: Add `renderStreamWrapped` and dispatch from `renderStream`**

In `internal/tui/view.go`, add:

```go
// renderStreamWrapped paints the visible lines with word wrap on: each line
// expands to ceil(visW/width) terminal rows. When the expanded rows overflow
// the viewport, tail mode bottom-aligns (keeps the newest rows, dropping the
// topmost line's leading rows) and browse mode top-aligns (keeps the oldest,
// dropping the bottom line's trailing rows). Short of a full screen, pad.
func (m *model) renderStreamWrapped(visible []int, rows int) string {
	segs := make([]string, 0, rows+8)
	for _, idx := range visible {
		styled, visW := m.renderVisibleRow(idx)
		segs = append(segs, wrapLine(styled, visW, m.width)...)
	}
	if len(segs) > rows {
		if m.tailMode {
			segs = segs[len(segs)-rows:]
		} else {
			segs = segs[:rows]
		}
	}
	for len(segs) < rows {
		segs = append(segs, m.blankRow())
	}
	return strings.Join(segs, "\n")
}
```

In `renderStream`, add the dispatch right after `m.publishViewport(visible)` and before the existing `rendered := make(...)` loop:

```go
	if m.wordWrap {
		return m.renderStreamWrapped(visible, rows)
	}
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./internal/tui/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/view.go internal/tui/view_test.go
git commit -m "feat(tui): renderStreamWrapped paint path with tail/browse windowing"
```

---

### Task 6: Keybinding `w` + toggle handler + doc regen

**Files:**
- Modify: `internal/keymap/actions.go`, `internal/keymap/defaults.go`, `internal/keymap/actions_test.go`, `internal/tui/update.go`, `KEYBINDINGS.md`
- Test: `internal/tui/update_test.go` (append) + existing `TestDocsUpToDate`

- [ ] **Step 1: Write the failing test**

Add to `internal/tui/update_test.go`:

```go
func TestToggleWordWrapKeyFlipsAndResetsPan(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m = m2.(*model)
	m.horizScroll = 30
	m = key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	if !m.wordWrap {
		t.Fatal("w should turn word wrap on")
	}
	if m.horizScroll != 0 {
		t.Fatalf("enabling wrap should reset horizScroll, got %d", m.horizScroll)
	}
	m = key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	if m.wordWrap {
		t.Fatal("w should turn word wrap off again")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestToggleWordWrapKey ./internal/tui/`
Expected: FAIL — `w` is unbound, so `m.wordWrap` stays false.

- [ ] **Step 3: Add the action, default key, AllActions entry, count guard, handler, regen docs**

In `internal/keymap/actions.go`, add to the const block (after `ActionToggleFilenameTrunc`):

```go
	ActionToggleWordWrap       Action = "toggle_word_wrap"
```

Add to `AllActions` (after the `ActionToggleFilenameTrunc` entry, before `ActionHelp`):

```go
	{ActionToggleWordWrap, "Toggle word wrap", "Wrap long lines to multiple rows instead of horizontal scrolling.", "main"},
```

In `internal/keymap/defaults.go`, add (after `ActionToggleFilenameTrunc: {"f"},`):

```go
		ActionToggleWordWrap:       {"w"},
```

In `internal/keymap/actions_test.go`, bump the count guard 38 → 39:

```go
	if len(AllActions) != 39 {
		t.Errorf("expected 39 named actions, got %d", len(AllActions))
```

In `internal/tui/update.go`, add a case in the main key switch (place after `case keymap.ActionResetHoriz:`):

```go
		case keymap.ActionToggleWordWrap:
			m.wordWrap = !m.wordWrap
			if m.wordWrap {
				m.horizScroll = 0
			}
```

Regenerate the keybindings doc:

```bash
./build.sh keybindings-docs
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./internal/tui/ ./internal/keymap/`
Expected: PASS — toggle test passes, action-count guard passes, `TestDocsUpToDate` passes (KEYBINDINGS.md regenerated).

- [ ] **Step 5: Commit**

```bash
git add internal/keymap/actions.go internal/keymap/defaults.go internal/keymap/actions_test.go internal/tui/update.go internal/tui/update_test.go KEYBINDINGS.md
git commit -m "feat(tui): bind w to toggle word wrap (+ doc regen)"
```

---

### Task 7: wrap ⊥ pan + footer `wrap` indicator

**Files:**
- Modify: `internal/tui/viewport.go`, `internal/tui/view.go`
- Test: `internal/tui/viewport_test.go`, `internal/tui/view_test.go`

When wrapping there is nothing to pan to, so `panBy` is a no-op, and the footer shows `wrap` where it normally shows `col: N` so it never implies panning is active.

- [ ] **Step 1: Write the failing tests**

Add to `internal/tui/viewport_test.go`:

```go
func TestPanByNoopWhenWrapping(t *testing.T) {
	m := newModel(100)
	m.wordWrap = true
	m.panBy(20)
	if m.horizScroll != 0 {
		t.Fatalf("pan should be a no-op while wrapping, got %d", m.horizScroll)
	}
}
```

Add to `internal/tui/view_test.go`:

```go
func TestFooterShowsWrapWhenWrapping(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m = m2.(*model)
	m.wordWrap = true
	foot := stripANSI(m.renderFooter())
	if !strings.Contains(foot, "wrap") {
		t.Fatalf("footer should show wrap indicator, got %q", foot)
	}
	if strings.Contains(foot, "col:") {
		t.Fatalf("footer should not show col: while wrapping, got %q", foot)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run "TestPanByNoopWhenWrapping|TestFooterShowsWrap" ./internal/tui/`
Expected: FAIL — pan still moves; footer still shows `col: 0`.

- [ ] **Step 3: Guard `panBy` and switch the footer column stat**

In `internal/tui/viewport.go`, add the guard at the top of `panBy`:

```go
func (m *model) panBy(delta int) {
	if m.wordWrap {
		return
	}
	m.horizScroll += delta
	if m.horizScroll < 0 {
		m.horizScroll = 0
	}
}
```

In `internal/tui/view.go` `renderFooter`, build a column-stat string and use it. Replace the final `return` of the normal branch:

```go
	colStat := fmt.Sprintf("col: %d", m.horizScroll)
	if m.wordWrap {
		colStat = "wrap"
	}
	return dimStyle.Width(m.width).MaxHeight(1).Render(fmt.Sprintf(" events: %d · %s · %s%s · %s%s · files: %d%s ",
		len(m.lines), pos, colStat, cols, groupStat, rendStat, len(m.files), search))
```

(Delete the old format line that interpolated `m.horizScroll` as `col: %d`.)

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./internal/tui/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/viewport.go internal/tui/view.go internal/tui/viewport_test.go internal/tui/view_test.go
git commit -m "feat(tui): disable pan + show wrap in footer when wrapping"
```

---

### Task 8: Wrap-aware vertical scroll (Page/Fast + tail re-stick)

**Files:**
- Modify: `internal/tui/viewport.go`, `internal/tui/update.go`
- Test: `internal/tui/viewport_test.go`

`viewport.go` has **three** logical-line walks that must become height-aware
when wrapping; Task 4 fixed the one inside `collectVisible`. The other two are
`unstickFromTail` (runs on every up-scroll out of tail mode) and `maybeReStick`
(runs on every down-scroll). Both count logical lines against `contentHeight`
terminal rows, so with wrap on:
- `unstickFromTail` lands ~`contentHeight` *logical* lines back instead of one
  screen of *terminal rows* — the first up-arrow from tail jumps several lines.
- `maybeReStick` re-pins to tail when `contentHeight` *logical* lines remain
  below (≈2× the terminal rows) — scrolling down snaps to tail prematurely.

Separately, Page/Fast jumps move a fixed number of logical lines
(`contentHeight`, `vertFastStep`); with wrap on that overshoots by several
screens. `vstep(termRows)` converts a terminal-row jump into a logical-line
delta using the current visible window (which already handles tail vs browse).

All three fixes use `m.visibleRowCost(idx)` (from Task 4). Wrap off:
`visibleRowCost == 1`, so every count and `vstep` is byte-identical to today.

- [ ] **Step 1: Write the failing tests**

Add to `internal/tui/viewport_test.go`. The helper seeds 20 wrapped lines
(2 terminal rows each at width 40, `contentHeight` 10):

```go
func seedWrapped(t *testing.T, n int) *model {
	t.Helper()
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12}) // contentHeight 10
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	long := "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz" // 60 cols
	for i := 0; i < n; i++ {
		m.appendEvent(render.Event{Group: "g", File: "/a.log",
			Rendered: []render.Part{{Type: "text", Value: long}}})
	}
	m.wordWrap = true
	m.width = 40 // prefix(11)+body(60)=71 cols => 2 rows per line
	return m
}

func TestVstepWrapOffIsIdentity(t *testing.T) {
	m := newModel(100)
	if got := m.vstep(10); got != 10 {
		t.Fatalf("wrap off vstep(10) = %d, want 10", got)
	}
}

func TestVstepShrinksWhenWrapping(t *testing.T) {
	m := seedWrapped(t, 20)
	// 6 terminal rows of wrapped lines => 3 logical lines.
	if got := m.vstep(6); got != 3 {
		t.Fatalf("wrapping vstep(6) = %d, want 3", got)
	}
}

func TestUnstickFromTailNoJumpWhenWrapping(t *testing.T) {
	m := seedWrapped(t, 20)
	// In tail mode the top visible line is the first index collectVisible
	// returns for one screen of terminal rows.
	top := m.collectVisible(m.contentHeight())[0]
	m.scrollBy(-1) // unstick + move up one logical line
	if got := m.streamTopRow(); got != top-1 {
		t.Fatalf("up-from-tail landed at %d, want %d (one line above the visible top)", got, top-1)
	}
}

func TestNoPrematureReStickWhenWrapping(t *testing.T) {
	m := seedWrapped(t, 20)
	m.tailMode = false
	m.setStreamTopRow(11) // lines 11..19 below = 9 logical lines = 18 terminal rows > 10
	m.scrollBy(1)         // down one line; must NOT re-stick (still >1 screen of rows below)
	if m.tailMode {
		t.Fatal("scrolling down re-stuck to tail prematurely (counted lines, not rows)")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run "TestVstep|TestUnstickFromTailNoJump|TestNoPrematureReStick" ./internal/tui/`
Expected: FAIL — `m.vstep undefined`; the unstick test lands ~`contentHeight`
lines back; the re-stick test flips `tailMode` true.

- [ ] **Step 3: Make all three walks height-aware**

In `internal/tui/viewport.go`, add `vstep`:

```go
// vstep converts a vertical jump of termRows terminal rows into a logical-line
// scroll delta, honoring word wrap. The visible window (collectVisible) already
// accounts for tail vs browse, so the number of lines it returns for termRows
// rows is the matching line delta. Wrap off: termRows unchanged (1 row = 1 line).
func (m *model) vstep(termRows int) int {
	if !m.wordWrap {
		return termRows
	}
	n := len(m.collectVisible(termRows))
	if n < 1 {
		n = 1
	}
	return n
}
```

In `unstickFromTail`, replace the backward line count with a terminal-row
budget. The loop currently reads:

```go
	rows := m.contentHeight()
	count := 0
	idx := len(m.lines) - 1
	for ; idx >= 0 && count < rows; idx-- {
		if m.lineEnabled(m.lines[idx]) {
			count++
		}
	}
	m.setStreamTopRow(idx + 1)
```

Replace with:

```go
	rows := m.contentHeight()
	used := 0
	idx := len(m.lines) - 1
	for ; idx >= 0 && used < rows; idx-- {
		if m.lineEnabled(m.lines[idx]) {
			used += m.visibleRowCost(idx)
		}
	}
	m.setStreamTopRow(idx + 1)
```

In `maybeReStick`, replace the forward line count. The body currently reads:

```go
	rows := m.contentHeight()
	enabled := 0
	for i := m.streamTopRow(); i < len(m.lines); i++ {
		if m.lineEnabled(m.lines[i]) {
			enabled++
		}
	}
	if enabled <= rows {
		m.tailMode = true
	}
```

Replace with:

```go
	rows := m.contentHeight()
	used := 0
	for i := m.streamTopRow(); i < len(m.lines); i++ {
		if m.lineEnabled(m.lines[i]) {
			used += m.visibleRowCost(i)
		}
	}
	if used <= rows {
		m.tailMode = true
	}
```

In `internal/tui/update.go`, route Page/Fast through `vstep` (leave the
`showFiles` branches calling `scrollFiles` untouched):

`ActionPageUp` — change `m.scrollBy(-page)` to:
```go
				m.scrollBy(-m.vstep(page))
```

`ActionPageDown` — change `m.scrollBy(page)` to:
```go
				m.scrollBy(m.vstep(page))
```

`ActionFastUp` — change `m.scrollBy(-vertFastStep)` to:
```go
				m.scrollBy(-m.vstep(vertFastStep))
```

`ActionFastDown` — change `m.scrollBy(vertFastStep)` to:
```go
				m.scrollBy(m.vstep(vertFastStep))
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./internal/tui/`
Expected: PASS — the four new tests pass; existing page/fast/tail tests still
pass (wrap-off cost is 1, so all three walks are unchanged off-path).

- [ ] **Step 5: Commit**

```bash
git add internal/tui/viewport.go internal/tui/update.go internal/tui/viewport_test.go
git commit -m "feat(tui): wrap-aware vertical scroll (Page/Fast + unstick/re-stick)"
```

---

### Task 9: Config plumbing (`tui.word_wrap`)

**Files:**
- Modify: `internal/config/yaml.go`, `internal/config/cli.go`, `main.go`, `log-listener.example.yml`
- Test: `internal/config/yaml_test.go`

Mirror the `truncate_filenames` plumbing exactly: raw YAML `*bool` → flatten into resolved `Config` → `tui.Options` → model.

- [ ] **Step 1: Write the failing test**

Add to `internal/config/yaml_test.go`:

```go
func TestTUIWordWrapFlattens(t *testing.T) {
	yc := &YAMLConfig{TUI: &TUI{WordWrap: boolPtr(true)}}
	cfg := &Config{cliExplicit: map[string]bool{}}
	yc.mergeYAMLInto(cfg)
	if !cfg.TUIWordWrap {
		t.Fatal("tui.word_wrap: true should set cfg.TUIWordWrap")
	}
}
```

If `boolPtr` does not already exist in the test package, check first:
`grep -rn "func boolPtr" internal/config/`. If absent, add to the test file:
```go
func boolPtr(b bool) *bool { return &b }
```
(Confirm the exact `mergeYAMLInto` receiver/signature with
`grep -n "func.*mergeYAMLInto" internal/config/yaml.go` and match the call in the test.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestTUIWordWrapFlattens ./internal/config/`
Expected: FAIL — `unknown field WordWrap in struct literal of type TUI` / `cfg.TUIWordWrap undefined`.

- [ ] **Step 3: Add the field through every layer**

In `internal/config/yaml.go`, add to the `TUI` struct (after `FilenameWidth`):

```go
	WordWrap          *bool `yaml:"word_wrap,omitempty"`
```

In the flatten block of `mergeYAMLInto` (after the `FilenameWidth` block):

```go
		if t.WordWrap != nil {
			cfg.TUIWordWrap = *t.WordWrap
		}
```

In `internal/config/cli.go`, add to the `Config` struct (after `TUIFilenameWidth`):

```go
	TUIWordWrap          bool // tui.word_wrap; default false
```

In `main.go`, add to the `tui.New(tui.Options{...})` literal (after `FilenameWidth: cfg.TUIFilenameWidth,`):

```go
		WordWrap:      cfg.TUIWordWrap,
```

In `log-listener.example.yml`, add under the `tui:` block (after the `filename_width` line):

```yaml
  word_wrap: false             # wrap long lines instead of horizontal pan (toggle live with 'w')
```

- [ ] **Step 4: Run tests to verify pass + build**

Run: `go test ./internal/config/ && go build ./...`
Expected: PASS and a clean build (main.go wiring compiles).

- [ ] **Step 5: Commit**

```bash
git add internal/config/yaml.go internal/config/cli.go internal/config/yaml_test.go main.go log-listener.example.yml
git commit -m "feat(config): tui.word_wrap default for word wrap"
```

---

### Task 10: Docs — README + CHANGELOG

**Files:**
- Modify: `README.md`, `CHANGELOG.md`

- [ ] **Step 1: Update README**

Find the TUI keybindings / features section (search `grep -n "truncate\|filename\|Visual select\|## Keybindings\|tui:" README.md`). Add a line describing word wrap next to the other display toggles, e.g.:

```markdown
- `w` — toggle **word wrap**: long lines wrap to multiple rows instead of
  horizontal panning. Vertical scroll moves a whole wrapped line at a time.
  Default via `tui.word_wrap` (off).
```

If README documents the `tui:` YAML block, add `word_wrap: false` there too, matching the `truncate_filenames` entry's style.

- [ ] **Step 2: Update CHANGELOG**

Add an entry under the current unreleased/most-recent section, matching the existing format:

```markdown
- **TUI word wrap (`w`).** Long lines wrap to multiple terminal rows instead of
  being clipped behind horizontal pan. Render-time only — viewstate anchors and
  the shared buffer are unaffected. Vertical scroll moves a whole wrapped line at
  a time; pan is disabled while wrapping. Startup default via `tui.word_wrap`.
```

- [ ] **Step 3: Commit**

```bash
git add README.md CHANGELOG.md
git commit -m "docs: README + CHANGELOG for TUI word wrap"
```

---

## Final verification (after all tasks)

Run the full gate suite — all must be green:

```bash
go test ./...
go vet ./...
go test -race ./...
```

Then dispatch the final whole-branch code review per subagent-driven-development.

## Spec coverage check

| Spec requirement | Task |
|------------------|------|
| Render-time `wrapLine` reusing `clipANSIWindow` | 1 |
| `model.wordWrap` + Options wiring | 2, 9 |
| Single source of width truth (`renderVisibleRow`) | 3 |
| Height-aware `collectVisible` (tail + browse + filter) | 4 |
| `renderStreamWrapped` tail/browse windowing, pad to rows | 5 |
| `w` key + `ActionToggleWordWrap` + KEYBINDINGS regen + count 38→39 | 6 |
| Toggle resets `horizScroll` | 6 |
| wrap ⊥ pan (`panBy` no-op) + footer `wrap` | 7 |
| Page/Fast terminal-row→line translation | 8 |
| Height-aware `unstickFromTail` + `maybeReStick` (no scroll-jump / premature re-stick) | 8 |
| `tui.word_wrap` config (yaml → cli → main → example) | 9 |
| `rowAnchor` / `viewanchor.go` untouched | (verified — no task edits it) |
| README + CHANGELOG | 10 |

**v1 limitations (intentionally not implemented):** tall-line tail not independently scrollable; gutter/selection bar on first wrapped row only. Documented in the spec; no task addresses them by design.
