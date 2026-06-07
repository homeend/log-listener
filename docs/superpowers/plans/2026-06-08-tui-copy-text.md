# TUI Copy-Text (`Y`) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a capital `Y` key that copies the displayed text of the current selection (mirroring lowercase `y`'s context), while `y` still copies the id reference.

**Architecture:** A new `ActionCopyText` (bound to `Y`) resolves the same selection precedence as `buildReference` — but in a SEPARATE `copytext.go` (no edits to the load-bearing `buildReference`) — then renders the selected rows to plain text via the existing `plainExportLine` and copies via OSC 52. A y/Y parity test prevents precedence drift.

**Tech Stack:** Go 1.26, bubbletea TUI, `internal/keymap` action system, `github.com/aymanbagabas/go-osc52/v2` (unwrapped base64 — multi-line safe).

**Reference spec:** `docs/superpowers/specs/2026-06-08-tui-copy-text-design.md`

---

## File Structure

- `internal/keymap/actions.go` — `ActionCopyText` const + `AllActions` doc entry.
- `internal/keymap/defaults.go` — `ActionCopyText: {"Y"}`.
- `KEYBINDINGS.md` — regenerated (guarded by `TestDocsUpToDate`).
- `internal/tui/copytext.go` (new) — selection→rows→plain-text helpers.
- `internal/tui/copytext_test.go` (new) — unit + parity tests.
- `internal/tui/visual.go` — `buildVisualText` + `copyVisualText`.
- `internal/tui/visual_test.go` — visual copy-text tests.
- `internal/tui/app.go` — `ActionCopyText` dispatch (normal) + visual-mode keymap route.
- `README.md`, `CHANGELOG.md` — document `Y`.

**Key test-harness facts (already in the codebase):**
- `newModel(100)` returns a `*model` with `m.km` set (keymap.Default).
- `seedIDs(m, vals...)` / `seedVisual(m, vals...)` append one text event per val with ids `L0, L1, …` (helper in `copyref_test.go` / `visual_test.go`).
- `key(m, tea.KeyMsg) *model` runs one Update (helper in `visual_test.go`).
- `plainExportLine(dl displayLine) string` and `m.snapshotViewport() []string` live in `save.go`.
- Entry rows: `m.lines` is the in-order concatenation of each `m.entries[i].lines`.

---

## Task 1: keymap action `Y` + regenerate doc

**Files:**
- Modify: `internal/keymap/actions.go` (const block ending `ActionVisualSelect`; `AllActions` slice)
- Modify: `internal/keymap/defaults.go` (bindings map)
- Modify: `KEYBINDINGS.md` (regenerated, do not hand-edit)
- Test: existing `internal/keymap/docfile_test.go::TestDocsUpToDate`

- [ ] **Step 1: Add the action constant**

In `internal/keymap/actions.go`, in the `const (...)` action block, add after `ActionCopyReference`:

```go
	ActionCopyText             Action = "copy_text"
```

- [ ] **Step 2: Add the AllActions doc entry**

In the `AllActions` slice, immediately after the `ActionCopyReference` entry, add:

```go
	{ActionCopyText, "Copy text", "Copy the selected text (search line, block, viewport, or visual selection) as displayed.", "main"},
```

- [ ] **Step 3: Add the default binding**

In `internal/keymap/defaults.go`, in the bindings map, add after the `ActionCopyReference: {"y"},` line:

```go
		ActionCopyText:             {"Y"},
```

- [ ] **Step 4: Run keymap tests to see the doc guard fail**

Run: `go test ./internal/keymap/ -run TestDocsUpToDate -v`
Expected: FAIL — `KEYBINDINGS.md` is now stale (missing the new action). (If other keymap tests assert every action has a binding, they should PASS since we added both.)

- [ ] **Step 5: Regenerate KEYBINDINGS.md**

Run: `./build.sh keybindings-docs`
Expected: prints `wrote ./KEYBINDINGS.md`. (Equivalent: `go run . --keybindings-doc > KEYBINDINGS.md`.)

- [ ] **Step 6: Verify keymap tests pass + Lookup works**

Run: `go test ./internal/keymap/ -v`
Expected: PASS (including `TestDocsUpToDate`). This confirms `Lookup("Y") == ActionCopyText` indirectly via the binding map; no new test needed here (the TUI integration test in Task 3 exercises the key end-to-end).

- [ ] **Step 7: Commit**

```bash
git add internal/keymap/actions.go internal/keymap/defaults.go KEYBINDINGS.md
git commit -m "feat(keymap): add copy_text action bound to Y"
```

---

## Task 2: `copytext.go` — selection resolution + plain text

**Files:**
- Create: `internal/tui/copytext.go`
- Create: `internal/tui/copytext_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/tui/copytext_test.go`:

```go
package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/render"
)

// joinPlain renders the given displayLines through plainExportLine and joins.
func joinPlain(dls []displayLine) string {
	parts := make([]string, len(dls))
	for i, dl := range dls {
		parts[i] = plainExportLine(dl)
	}
	return strings.Join(parts, "\n")
}

func TestSelectionTextViewportMatchesSnapshot(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 6})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	seedIDs(m, "a", "b", "c", "d", "e", "f")
	m.tailMode = false
	m.streamTop = 0
	got := buildSelectionText(m)
	want := strings.Join(m.snapshotViewport(), "\n")
	if got != want {
		t.Fatalf("viewport selection text:\n got %q\nwant %q", got, want)
	}
}

func TestSelectionTextSearchHitCopiesWholeEntry(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	// L0 single row; L1 a multi-row entry (embedded newlines).
	m.appendEvent(render.Event{ID: "L0", Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "start"}}})
	m.appendEvent(render.Event{ID: "L1", Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "config:\n  k=v\n  j=w"}}})
	m.searchTerm = "config"
	m.searchHit = 1 // row 1 is L1's head row
	got := buildSelectionText(m)
	want := joinPlain(m.entries[1].lines) // ALL of L1's rows, not just the hit row
	if got != want {
		t.Fatalf("search-hit selection text:\n got %q\nwant %q", got, want)
	}
	if !strings.Contains(got, "k=v") || !strings.Contains(got, "j=w") {
		t.Fatalf("expected the whole entry's rows, got %q", got)
	}
}

func TestSelectionTextFocusedBlock(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	// L0 lead; L1+L2 form a go-panic block ("goroutine " is a continuation sig).
	seedIDs(m, "start", "panic: boom", "goroutine 1 [running]:")
	m.tailMode = false
	m.streamTop = 0
	m = key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}}) // focus block at line 1
	s, e, ok := m.focusedBlockRange()
	if !ok {
		t.Fatal("expected a focused block range")
	}
	got := buildSelectionText(m)
	want := joinPlain(m.lines[s : e+1])
	if got != want {
		t.Fatalf("focused-block selection text:\n got %q\nwant %q", got, want)
	}
}

func TestSelectionTextEmptyBuffer(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 6})
	m = m2.(*model)
	if got := buildSelectionText(m); got != "" {
		t.Fatalf("empty buffer selection text = %q, want empty", got)
	}
	if txt, n := m.copySelectionText(); txt != "" || n != 0 {
		t.Fatalf("copySelectionText on empty = (%q,%d), want (\"\",0)", txt, n)
	}
}

// Parity guard: Y's selection ends must equal the entries y references.
func TestCopyTextParityWithReference(t *testing.T) {
	mk := func() *model {
		m := newModel(100)
		m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 8})
		m = m2.(*model)
		m.groupOrder = []string{"g"}
		m.groupEnabled["g"] = true
		return m
	}

	// viewport context
	mv := mk()
	seedIDs(mv, "a", "b", "c", "d")
	mv.tailMode = false
	mv.streamTop = 0
	assertParity(t, "viewport", mv, mv.selectedRows())

	// focused-block context
	mb := mk()
	seedIDs(mb, "start", "panic: boom", "goroutine 1 [running]:")
	mb.tailMode = false
	mb.streamTop = 0
	mb = key(mb, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	assertParity(t, "block", mb, mb.selectedRows())

	// search-hit context
	ms := mk()
	seedIDs(ms, "apple", "banana", "cherry")
	ms.searchTerm = "banana"
	ms.searchHit = 1
	assertParity(t, "search", ms, ms.selectedRows())
}

// assertParity checks that the first/last entry ids of rows match the ids
// encoded in buildReference(m). Works for both "line:X" and "range:A..B".
func assertParity(t *testing.T, ctx string, m *model, rows []int) {
	t.Helper()
	if len(rows) == 0 {
		t.Fatalf("%s: no rows selected", ctx)
	}
	gotFirst := m.entryIDForLine(rows[0])
	gotLast := m.entryIDForLine(rows[len(rows)-1])
	refFirst, refLast := parseRefEnds(t, buildReference(m))
	if gotFirst != refFirst || gotLast != refLast {
		t.Fatalf("%s parity: Y ends (%s..%s) != y ref ends (%s..%s)",
			ctx, gotFirst, gotLast, refFirst, refLast)
	}
}

// parseRefEnds extracts (first,last) entry ids from a "line:X" or "range:A..B".
func parseRefEnds(t *testing.T, ref string) (string, string) {
	t.Helper()
	switch {
	case strings.HasPrefix(ref, "line:"):
		id := strings.TrimPrefix(ref, "line:")
		return id, id
	case strings.HasPrefix(ref, "range:"):
		body := strings.TrimPrefix(ref, "range:")
		parts := strings.SplitN(body, "..", 2)
		if len(parts) != 2 {
			t.Fatalf("bad range ref %q", ref)
		}
		return parts[0], parts[1]
	default:
		t.Fatalf("unexpected ref %q", ref)
		return "", ""
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tui/ -run 'TestSelectionText|TestCopyTextParity' -v`
Expected: FAIL — `buildSelectionText`, `copySelectionText`, `selectedRows` undefined (compile error).

- [ ] **Step 3: Implement `copytext.go`**

Create `internal/tui/copytext.go`:

```go
package tui

import "strings"

// entryRowSpan returns the inclusive absolute m.lines index span [start,end] of
// the entry that owns row idx (ok=false if idx is out of range). Mirrors the
// accumulation walk in entryIDForLine.
func (m *model) entryRowSpan(idx int) (start, end int, ok bool) {
	if idx < 0 {
		return 0, 0, false
	}
	off := 0
	for _, e := range m.entries {
		n := len(e.lines)
		if idx < off+n {
			return off, off + n - 1, true
		}
		off += n
	}
	return 0, 0, false
}

// rangeSlice returns [lo, lo+1, ..., hi] (inclusive), or nil if hi < lo.
func rangeSlice(lo, hi int) []int {
	if hi < lo {
		return nil
	}
	out := make([]int, 0, hi-lo+1)
	for i := lo; i <= hi; i++ {
		out = append(out, i)
	}
	return out
}

// selectedRows returns the absolute m.lines indices that Y copies, mirroring
// buildReference's precedence EXACTLY:
//  1. search active + hit → the whole owning entry's rows
//  2. explicitly focused block → focusedBlockRange()
//  3. else → the visible viewport (collectVisible)
func (m *model) selectedRows() []int {
	if m.searchTerm != "" && m.searchHit >= 0 {
		if s, e, ok := m.entryRowSpan(m.searchHit); ok {
			return rangeSlice(s, e)
		}
	}
	if s, e, ok := m.focusedBlockRange(); ok {
		return rangeSlice(s, e)
	}
	return m.collectVisible(m.contentHeight())
}

// textForRows renders the given absolute m.lines rows to plain (no-ANSI)
// displayed text, one per line, skipping out-of-range indices. Reuses
// plainExportLine so output is byte-identical to the save-to-file format.
func (m *model) textForRows(idxs []int) string {
	lines := make([]string, 0, len(idxs))
	for _, i := range idxs {
		if i < 0 || i >= len(m.lines) {
			continue
		}
		lines = append(lines, plainExportLine(m.lines[i]))
	}
	return strings.Join(lines, "\n")
}

// buildSelectionText is the pure seam: the displayed text of the current
// (normal-mode) selection, or "" if nothing resolves.
func buildSelectionText(m *model) string {
	return m.textForRows(m.selectedRows())
}

// copySelectionText OSC-52-copies the normal-mode selection text and returns
// (text, lineCount). Returns ("",0) when there's nothing to copy.
func (m *model) copySelectionText() (string, int) {
	txt := buildSelectionText(m)
	if txt == "" {
		return "", 0
	}
	osc52Copy(txt)
	return txt, strings.Count(txt, "\n") + 1
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tui/ -run 'TestSelectionText|TestCopyTextParity' -v`
Expected: PASS (all). If the unused scaffolding (`first`, `m_idAt`) causes a
compile error, delete those lines per the Step 1 note and re-run.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/copytext.go internal/tui/copytext_test.go
git commit -m "feat(tui): copytext selection→plain-text helpers (mirrors y precedence)"
```

---

## Task 3: visual-mode text + `Y` dispatch

**Files:**
- Modify: `internal/tui/visual.go` (add `buildVisualText`, `copyVisualText`)
- Modify: `internal/tui/app.go` (`ActionCopyText` case; visual keymap route in `handleVisualKey`)
- Test: `internal/tui/visual_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `internal/tui/visual_test.go` (it already imports `strings`, `testing`, `tea`, `render`; add a `keyY` var near the other key vars):

```go
var keyY = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Y'}}

func TestVisualTextSpan(t *testing.T) {
	m := newVisualModel(t, "a", "b", "c", "d")
	m.visualAnchor = 1
	m.visualCursor = 2
	got := buildVisualText(m)
	want := joinPlain(m.lines[1:3]) // rows 1,2
	if got != want {
		t.Fatalf("visual span text:\n got %q\nwant %q", got, want)
	}
}

func TestVisualTextNoAnchorIsCaretRow(t *testing.T) {
	m := newVisualModel(t, "a", "b", "c")
	m.visualAnchor = -1
	m.visualCursor = 1
	got := buildVisualText(m)
	want := plainExportLine(m.lines[1])
	if got != want {
		t.Fatalf("no-anchor visual text = %q, want %q", got, want)
	}
}

func TestVisualCapitalYCopiesTextAndExits(t *testing.T) {
	m := newVisualModel(t, "a", "b", "c", "d")
	m.tailMode = false
	m.streamTop = 0
	m = key(m, keyV)     // enter visual, cursor at row 0
	m = key(m, keyJ)     // row 1
	m = key(m, keySpace) // anchor = 1
	m = key(m, keyJ)     // cursor → row 2
	m = key(m, keyY)     // copy text rows 1..2, exit
	if m.visualMode {
		t.Error("Y should exit visual mode")
	}
	if m.flash != "copied 2 lines" {
		t.Fatalf("flash = %q, want \"copied 2 lines\"", m.flash)
	}
}

func TestNormalCapitalYCopiesViewportText(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 6})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	seedIDs(m, "a", "b", "c")
	m.tailMode = false
	m.streamTop = 0
	want := fmt.Sprintf("copied %d lines", len(m.snapshotViewport()))
	m = key(m, keyY)
	if m.flash != want {
		t.Fatalf("flash = %q, want %q", m.flash, want)
	}
}
```

This test uses `fmt.Sprintf`, so add `"fmt"` to `visual_test.go`'s import block
(it currently imports `strings`, `testing`, `tea`, `render`).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tui/ -run 'TestVisualText|TestVisualCapitalY|TestNormalCapitalY' -v`
Expected: FAIL — `buildVisualText`/`copyVisualText` undefined and `Y` not wired (compile error, then assertion failures).

- [ ] **Step 3: Add visual-mode text helpers**

In `internal/tui/visual.go`, add (the file already imports `fmt`; add `strings`):

```go
// buildVisualText renders the inclusive visual span [min(anchor,cursor),max] to
// plain displayed text. With no anchor (visualAnchor < 0) it is just the caret
// row. "" if the span resolves to nothing.
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

// copyVisualText copies the visual span's text (OSC 52) and flashes a count.
func (m *model) copyVisualText() {
	txt := buildVisualText(m)
	if txt == "" {
		return
	}
	osc52Copy(txt)
	m.flash = fmt.Sprintf("copied %d lines", strings.Count(txt, "\n")+1)
}
```

- [ ] **Step 4: Route `Y` inside `handleVisualKey`**

In `internal/tui/visual.go`, in `handleVisualKey`, BEFORE the `switch msg.String()`, add a keymap-resolved route so `Y` stays remappable (visual mode otherwise bypasses the keymap):

```go
func (m *model) handleVisualKey(msg tea.KeyMsg) *model {
	if act, ok := m.km.Lookup(msg.String()); ok && act == keymap.ActionCopyText {
		m.copyVisualText()
		m.exitVisual()
		return m
	}
	switch msg.String() {
	// ... existing up/down/space/esc cases unchanged ...
	}
	return m
}
```

Add the import `"github.com/homeend/log-listener/internal/keymap"` to
`visual.go` if not already present.

- [ ] **Step 5: Add the normal-mode dispatch case**

In `internal/tui/app.go`, in the main `Update` action switch, immediately after the `case keymap.ActionCopyReference:` block, add:

```go
		case keymap.ActionCopyText:
			if _, n := m.copySelectionText(); n > 0 {
				m.flash = fmt.Sprintf("copied %d lines", n)
			} else {
				m.flash = "nothing to copy"
			}
```

(`fmt` is already imported in `app.go`.)

- [ ] **Step 6: Run the new tests + full tui package**

Run: `go test ./internal/tui/ -run 'TestVisualText|TestVisualCapitalY|TestNormalCapitalY' -v`
Expected: PASS.
Run: `go test ./internal/tui/`
Expected: PASS (no regression in existing visual/copyref tests — confirms `y` and the two-`space` flow still work).

- [ ] **Step 7: Build, full test, race**

Run: `go build ./... && go test ./... && go test -race ./...`
Expected: all PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/tui/visual.go internal/tui/app.go internal/tui/visual_test.go
git commit -m "feat(tui): Y copies selection text (normal + visual)"
```

---

## Task 4: Documentation

**Files:**
- Modify: `README.md`
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Locate the key docs in README**

Run: `grep -n "copy\|Copy\|\`y\`\|reference\|visual\|OSC 52\|clipboard" README.md`
Expected: find where the `y` copy-reference / visual mode are described.

- [ ] **Step 2: Document `Y` in README**

Near the `y` / copy description, add (match surrounding format), conveying:

> `Y` (capital) copies the selected **text** as displayed (no color), instead of
> the id reference `y` copies. It mirrors `y`'s context — the search line, the
> focused block, the viewport, or the visual-mode selection — joined by
> newlines, via OSC 52. Very large selections may hit the terminal's OSC 52
> size limit; use `s`/`S` (save to file) for big captures.

If a key table lists `y`, add a `Y` row in the same format.

- [ ] **Step 3: Add a CHANGELOG entry**

Under the top/unreleased section, in the existing style:

```markdown
- TUI: `Y` (capital) copies the selected text as displayed (no color) via
  OSC 52 — mirroring `y`'s context (search line / focused block / viewport /
  visual selection). `y` still copies the id reference.
```

- [ ] **Step 4: Verify**

Run: `go test ./...`
Expected: PASS (README/CHANGELOG are not generated-doc-guarded; `KEYBINDINGS.md` was already regenerated in Task 1).

- [ ] **Step 5: Commit**

```bash
git add README.md CHANGELOG.md
git commit -m "docs: document Y copy-text key"
```

---

## Final verification

- [ ] `go test ./...` — all green
- [ ] `go vet ./...` — clean
- [ ] `go test -race ./...` — clean
- [ ] `git grep -n "ActionCopyText"` — appears in actions.go, defaults.go, copytext usage, app.go, visual.go, KEYBINDINGS.md
- [ ] Manual smoke (optional): `./build.sh build`, run on a log, press `Y` (viewport), `]` then `Y` (block), `/term` then `Y` (search line), `v`→`space`→move→`Y` (visual). Paste each elsewhere to confirm text (not an id) landed.

Then use **superpowers:finishing-a-development-branch**.
