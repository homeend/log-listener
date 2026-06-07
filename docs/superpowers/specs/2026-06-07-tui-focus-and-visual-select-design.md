# TUI Focus Indicator + Visual Selection — Design

**Date:** 2026-06-07
**Status:** Approved (pending spec review)
**Branch:** extends `feat/embedded-mcp-server` (cursor + `copy_reference` work).

## Summary

Two TUI enhancements to the copy-reference UX built with the MCP server:

1. **Focused-block column** — a `│` left-margin bar (cyan) on the rows of the
   *currently focused block*, i.e. exactly the block `y` would copy. It appears
   when the cursor sits in a multi-line block and disappears when it doesn't, so
   the user always sees whether `y` will copy a block or the whole viewport.
2. **Visual selection mode (`v`)** — a vim-style modal mode: move a cursor, press
   `space` to set the selection start, move, press `space` again to copy the
   `range:<id>..<id>` reference (OSC 52) and exit; `esc` cancels.

Both reuse the existing left-margin bar rendering pattern (`exceptionBar`) and
the `entryIDForLine` + OSC 52 copy path, and both land on the current branch.

## Goals / Non-goals

**Goals:** make the copy target visible before pressing `y`; let the user select
an arbitrary line range visually and copy it as an agent-resolvable reference;
reuse existing rendering/copy machinery; keep the careful row-width invariants
intact.

**Non-goals (YAGNI):** a persistent always-on cursor outside visual mode (Feature
A uses the derived `cursorIndex()`); full-row background highlighting (unsafe with
embedded ANSI — use margin bars); multi-register/named selections; line-wise vs
char-wise distinction (always whole-line / whole-entry); mouse selection;
selecting across a config reload (visual mode is transient).

## Current baseline (the seams reused)

- **Left-margin bar pattern** (`internal/tui/blocks.go`, `app.go renderStream`):
  `renderStream` builds each visible row as
  `styled, visW := renderDisplayLineAt(idx)`, then
  `if bar, ok := m.exceptionBar(idx); ok { styled = bar + styled; visW += exceptionBarWidth }`,
  then `clipLine(styled, visW)`. `exceptionBar(idx) (string, bool)` returns a
  styled `▌ ` (red) when marks are on and idx is in an exception block;
  `exceptionBarWidth = dispWidth("▌ ")` (measured). This is the exact pattern both
  new margins follow.
- **`cursorIndex() int`** (`app.go`): the focused line — `searchHit` when
  searching, else `streamTop` when browsing (`!tailMode`), else -1.
- **`buildReference(m)`** (`internal/tui/copyref.go`): rule 2 copies the
  multi-line block containing `cursorIndex()` as `range:headId..endId`;
  `entryIDForLine(idx)` maps an absolute `m.lines` index → its owning entry's id;
  `copyReference(m)` writes the ref to stderr via OSC 52 (`osc52.New(ref).WriteTo`).
- **Modal key precedence** (`app.go Update`): the very top of the `tea.KeyMsg`
  branch clears `m.flash` then routes modal sub-modes first:
  `if m.searchInput { return m.handleSearchInputKey(msg), nil }`, then
  `if m.wrapPrompt != 0 { ... }`, then positional toggles, then
  `km.Lookup(key)` → action `switch`. New modal `visualMode` slots in here.
- **`blocks`** (`m.blocks []blocks.Block{Start, End int; Exception *…}`),
  recomputed by `ensureBlocks()`; `m.lineEnabled`, `collectVisible(rows)`,
  `clipLine`, `blankRow` already exist.

## Feature A — Focused-block column

A new margin bar rendered **to the left of** the exception bar.

```go
// focusBarStyle: cyan (lipgloss color "6" or "14"), distinct from the red
// exception bar (color "9").
var focusBarStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
var focusBarWidth = dispWidth("│ ") // measured, like exceptionBarWidth

// focusBar returns the styled "│ " prefix and true when the row at idx belongs
// to the focused block — the multi-line block (End > Start) containing
// cursorIndex(). Suppressed during visual mode (the selection margin owns the
// gutter then). Returns ("", false) otherwise.
func (m *model) focusBar(idx int) (string, bool)
```

`focusBar` logic:
- If `m.visualMode` → `"", false` (visual selection owns the margin).
- `cur := m.cursorIndex()`; if `cur < 0` → `"", false`.
- `m.ensureBlocks()`; find the block `b` with `cur >= b.Start && cur <= b.End && b.End > b.Start`; if none → `"", false`.
- If `idx >= b.Start && idx <= b.End` → return `focusBarStyle.Render("│") + " ", true`.
- Else `"", false`.

This is the same predicate `buildReference` rule 2 uses, so the column is a
faithful live preview of the block `y` copies.

**Composition in `renderStream`:** prepend focus first, then exception, summing
widths so `clipLine` pads/clips against the true visible width:
```go
styled, visW := m.renderDisplayLineAt(idx)
if fb, ok := m.focusBar(idx); ok { styled = fb + styled; visW += focusBarWidth }
if eb, ok := m.exceptionBar(idx); ok { styled = eb + styled; visW += exceptionBarWidth }
rendered = append(rendered, m.clipLine(styled, visW))
```
Order: focus is outermost (leftmost). A focused exception row shows `│ ▌ body`;
focused non-exception shows `│ body`; unfocused exception shows `▌ body`
(unchanged). Always on — no toggle.

Width safety: `focusBarWidth` is measured with `dispWidth` (the `│` U+2502 box
char, like the exception `▌`, is East-Asian ambiguous), so a barred row's
accounting stays exact and the no-overflow/no-wrap row invariant holds.

## Feature B — Visual selection mode

### State (new model fields)
```go
visualMode   bool // in visual selection mode
visualCursor int  // absolute m.lines index of the moving cursor
visualAnchor int  // absolute m.lines index of the selection start; -1 until set
```
Initialised in `newModel`: `visualAnchor: -1` (the others zero/false).

### Entering — `ActionVisualSelect` (key `v`)
In the action `switch`:
```go
case keymap.ActionVisualSelect:
    if len(m.lines) == 0 { break }
    m.visualMode = true
    m.visualAnchor = -1
    m.unstickFromTail(); m.tailMode = false
    // start at the top visible row, or streamTop
    vis := m.collectVisible(m.contentHeight())
    if len(vis) > 0 { m.visualCursor = vis[0] } else { m.visualCursor = m.streamTop }
```

### Modal key handling
At the top of the `tea.KeyMsg` branch, after the `m.flash = ""` line and before
`searchInput`:
```go
if m.visualMode { return m.handleVisualKey(msg), nil }
```
`handleVisualKey(msg tea.KeyMsg) *model` (new, in `internal/tui/visual.go`):
- **Up** (`tea.KeyUp` or `k`): `m.visualCursor--` clamped to `>= 0`; then
  `m.ensureVisualVisible()`.
- **Down** (`tea.KeyDown` or `j`): `m.visualCursor++` clamped to
  `<= len(m.lines)-1`; then `ensureVisualVisible()`.
  (Fixed arrows + `j`/`k`; visual mode does not consult the configurable scroll
  keymap — keep it simple and predictable.)
- **`space`** (`" "`):
  - if `m.visualAnchor < 0` → `m.visualAnchor = m.visualCursor` (set start).
  - else → `m.copyVisualSelection()` then exit (`m.exitVisual()`).
- **`esc`** (`tea.KeyEsc`) → `m.exitVisual()` (no copy).
- any other key → ignored (no-op), staying in visual mode.

Helpers:
```go
// ensureVisualVisible scrolls streamTop so visualCursor stays on screen.
func (m *model) ensureVisualVisible()
// exitVisual leaves visual mode (visualMode=false, visualAnchor=-1).
func (m *model) exitVisual()
// buildVisualRef is the PURE seam (mirrors buildReference): it returns
// range:<entryID(min)>..<entryID(max)> over the inclusive [min(anchor,cursor),
// max] line span, or "" if either endpoint can't be resolved. Tested directly.
func buildVisualRef(m *model) string
// copyVisualSelection = buildVisualRef + osc52Copy + flash (the side-effecting
// wrapper).
func (m *model) copyVisualSelection()
```
```go
func buildVisualRef(m *model) string {
    lo, hi := m.visualAnchor, m.visualCursor
    if lo > hi { lo, hi = hi, lo }
    a, b := m.entryIDForLine(lo), m.entryIDForLine(hi)
    if a == "" || b == "" { return "" }
    return fmt.Sprintf("range:%s..%s", a, b)
}
func (m *model) copyVisualSelection() {
    ref := buildVisualRef(m)
    if ref == "" { return }
    osc52Copy(ref)        // shared helper extracted from copyReference
    m.flash = "copied " + ref
}
```
`osc52Copy(ref string)` is extracted from `copyReference`'s raw
`osc52.New(ref).WriteTo(os.Stderr)` write so both copy paths share it.

### Rendering the selection (margin bars, not full-row background)
Full-row background is unsafe (embedded ANSI resets clear it mid-line), so the
selection uses the margin-bar pattern. A new
`visualBar(idx) (string, bool)`:
- only active when `m.visualMode`;
- `idx == m.visualCursor` → a caret `▶ ` in a bright style (cursor row);
- else, when anchored and `idx` within `[min(anchor,cursor), max]` → a selection
  bar `┃ ` in a selection style;
- else `"", false`.
`visualBarWidth = dispWidth("▶ ")` (the caret and bar must render to the same
width; if `┃` and `▶` differ in measured width, pad to a common
`visualBarWidth`, asserting equality at init). In `renderStream`, `visualBar`
takes the gutter slot (it and `focusBar` are mutually exclusive — `focusBar`
already returns false in visual mode); prepend it like the others and add its
width.

### Footer hint
While `m.visualMode`, the footer/flash line shows
`VISUAL  ↑↓ move · space set/copy · esc cancel` (reuse the existing footer/flash
render path; e.g. set it where the header/footer is composed when `visualMode`).

### Interactions
- Entering visual mode leaves tail mode (so the cursor is stable).
- Config reload / new events while in visual mode: `trimToCap`/`reRenderAll`
  already adjust `streamTop`/`searchHit` on eviction; `visualCursor`/`visualAnchor`
  must be clamped the same way (decrement by evicted lines, clamp to range; if an
  anchor scrolls off, treat as if unset → `-1`). Keep this minimal: on any line
  eviction while `visualMode`, clamp both indices to `[0, len(lines)-1]` and reset
  a now-invalid anchor to `-1`.
- `]`/`[`/`}`/`{`, `/`, `y` are not handled in visual mode (only up/down/space/esc
  act); the user exits with `esc` or completes with the second `space`.

## Keymap + docs

- New action `ActionVisualSelect = "visual_select"` in
  `internal/keymap/actions.go` (registry row: `{ActionVisualSelect, "Visual
  select", "Enter visual line-selection mode (space sets/copies a range, esc
  cancels).", "main"}`), default key `{"v"}` in `defaults.go` (verify `v` is
  free). The in-mode keys (space/esc/up/down) are modal, not registered actions —
  same as search typing. Regenerate `KEYBINDINGS.md` (`./build.sh
  keybindings-docs`); `TestDocsUpToDate` guards it. Bump the hardcoded action
  count in `TestAllActionsUniqueAndNonEmpty` if present.

## Files

- `internal/tui/app.go` — model fields (`visualMode`, `visualCursor`,
  `visualAnchor`); `newModel` init (`visualAnchor: -1`); `renderStream` prepend of
  `focusBar`/`visualBar`; modal route `if m.visualMode { … }`; the
  `ActionVisualSelect` dispatch case; footer hint.
- `internal/tui/focusbar.go` (new) — `focusBar`, `focusBarStyle`, `focusBarWidth`.
- `internal/tui/visual.go` (new) — `handleVisualKey`, `ensureVisualVisible`,
  `exitVisual`, `copyVisualSelection`, `visualBar`, styles/widths.
- `internal/tui/copyref.go` — extract `osc52Copy(ref string)` shared helper.
- `internal/keymap/actions.go`, `defaults.go` — `ActionVisualSelect`.
- `internal/tui/focusbar_test.go`, `internal/tui/visual_test.go` (new).
- `KEYBINDINGS.md` (regenerated), `README.md`, `CHANGELOG.md`.

## Testing

**Feature A (`focusbar_test.go`):**
- Seed a single-line entry, then a multi-line block (e.g. `panic:\n  at x`),
  then another single line. With `streamTop` on the block head (`!tailMode`),
  assert `focusBar(idx)` true for every block row and false for the single-line
  rows. Move `streamTop` off the block → assert `focusBar` false everywhere.
- In tail mode, assert `focusBar` false (cursorIndex == -1).
- During `visualMode`, assert `focusBar` returns false.
- Width safety: render via `renderStream` at a narrow width with a focused
  exception block; assert every row's `dispWidth` is exactly `m.width` (the
  existing exception-bar width test is the template) — proves `│ ▌` stacking
  keeps the row-width invariant.

**Feature B (`visual_test.go`):**
- Enter visual mode (`v`), assert `visualMode` and `visualAnchor == -1`, cursor at
  top visible row, tail mode off.
- Move down N (`j`/down), `space` (anchor set == cursor), move down M, `space` →
  assert the model OSC-copied / flashed `range:<id@lo>..<id@hi>` with the correct
  endpoint entry IDs (seed events with explicit IDs `L0..`), and `visualMode`
  false afterwards. Test via the pure `copyVisualSelection`/`buildVisualRef` seam
  (assert the ref string), not by scraping stderr.
- Order independence: anchor below cursor then move up → range normalises
  (min..max).
- `esc` after anchoring → `visualMode` false, no flash/copy (assert `m.flash`
  unchanged / empty).
- Eviction clamp: in visual mode with an anchor, append past cap → assert
  `visualCursor`/`visualAnchor` stay in `[0, len-1]` (or anchor reset to -1).

## Conventions

Two phase commits per repo convention. Each leaves `go test ./...`, `go vet
./...`, `go test -race ./internal/tui/` green. Update `README.md` + `CHANGELOG.md`
(new `v` key) and regenerate `KEYBINDINGS.md`.
