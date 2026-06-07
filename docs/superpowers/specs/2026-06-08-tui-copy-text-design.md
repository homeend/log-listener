# TUI Copy-Text (`Y`) — Design

**Date:** 2026-06-08
**Status:** Approved (pending spec review)
**Branch:** new feature branch off `main`.

## Summary

Add a capital `Y` key that copies the **displayed text** of the current
selection to the terminal clipboard (OSC 52), as a complement to the existing
lowercase `y`, which copies a paste-ready **id reference**. `Y` mirrors `y`'s
context precedence exactly — search hit, focused block, viewport, and the
visual-mode span — but emits the rendered, no-color text instead of an
`line:<id>` / `range:<id>..<id>` reference. Lowercase `y` is unchanged.

## Goals / Non-goals

**Goals:**
- New action `ActionCopyText` bound to `Y` (all OSes), remappable via the
  `keybindings:` YAML layer like every other action.
- `Y` copies the **same selection** `y` references, as displayed text:
  - search hit active → the whole owning entry's rows;
  - explicitly focused block → the block's rows;
  - else → the visible viewport rows;
  - visual mode → the highlighted `anchor..cursor` span (just the caret row if
    no anchor set), then exit visual mode.
- Text form = displayed/rendered minus ANSI: `[group] file: ` prefix on head
  rows, pretty-printed JSON/XML continuation rows kept, styling stripped —
  identical to what the save-to-file feature writes (`plainExportLine`).
- Multi-row selections joined by `\n`.
- A flash shows a **count** (`copied N lines`), never the dumped text.
- `y` (lowercase / `ActionCopyReference`) is **completely unchanged**.
- A parity test guarantees `y` and `Y` resolve the same entries per context.

**Non-goals (YAGNI):**
- No refactor of `buildReference` / `copyref.go` into a shared resolver
  (load-bearing, just-merged, with explicit-focus + single-entry-block
  special-casing). `Y` mirrors its precedence in a separate function.
- No change to the two-`space` visual flow (still copies the range reference).
- No lowercase-`y`-in-visual-mode behavior.
- No working around terminal OSC 52 payload size caps — very large
  viewport/scrollback copies may be truncated by the terminal; save-to-file
  (`s`/`S`) remains the escape hatch for big content. Documented, not solved.

## Current baseline

- `internal/tui/copyref.go`: `buildReference(m)` resolves, by precedence:
  1. `m.searchTerm != "" && m.searchHit >= 0` → `line:<entryIDForLine(searchHit)>`;
  2. `focusedBlockRange()` (explicit block focus) → `line:<id>` (single-entry)
     or `range:<head>..<end>`;
  3. else → `range:<first visible>..<last visible>` over `collectVisible(contentHeight())`.
  `copyReference(m)` OSC-52-copies it; `osc52Copy(ref)` writes the escape to
  **stderr**. `entryIDForLine(idx)` maps an absolute `m.lines` index → owning
  entry id.
- `internal/tui/save.go`: `plainExportLine(dl displayLine) string` →
  `stripANSI(dl.body)` for block rows, else `"[" + dl.group + "] " + dl.file +
  ": " + stripANSI(dl.body)`. `snapshotViewport()` maps `collectVisible(...)`
  rows through it.
- `internal/tui/visual.go`: visual mode tracks `visualAnchor`/`visualCursor`
  (absolute `m.lines` indices; anchor `-1` = unset). `handleVisualKey` handles
  up/k, down/j, space (sets anchor, then copies range ref + exits), esc.
  `buildVisualRef(m)` → `range:<entryID(lo)>..<entryID(hi)>`.
- `internal/tui/app.go`: `km.Lookup(key) (Action, bool)` resolves keys to
  actions in the main `Update` switch. Visual mode intercepts **before** that
  switch (`if m.visualMode { return m.handleVisualKey(msg), nil }`), so visual
  keys currently bypass the keymap.
- `internal/keymap`: actions live in `actions.go` (`Action` const + an
  `AllActions` `ActionDef` doc entry); default per-OS keys in `defaults.go`.
  `KEYBINDINGS.md` is generated (`--keybindings-doc` / `./build.sh
  keybindings-docs`) and guarded by `TestDocsUpToDate`.
- OSC 52 lib `github.com/aymanbagabas/go-osc52/v2` uses
  `base64.StdEncoding.EncodeToString` — **unwrapped** base64, so multi-line
  (`\n`-containing) payloads encode into one clean blob and round-trip on paste.

## Component 1: keymap action

`internal/keymap/actions.go`:
- Add const `ActionCopyText Action = "copy_text"` (next to `ActionCopyReference`).
- Add to `AllActions` (immediately after the `ActionCopyReference` entry):
  ```go
  {ActionCopyText, "Copy text", "Copy the selected text (search line, block, viewport, or visual selection) as displayed.", "main"},
  ```

`internal/keymap/defaults.go`: add to the bindings map (near `ActionCopyReference`):
```go
		ActionCopyText:             {"Y"},
```

Regenerate the doc: `./build.sh keybindings-docs` (updates `KEYBINDINGS.md`;
`TestDocsUpToDate` then passes).

## Component 2: text resolution (`internal/tui/copytext.go`, new)

```go
package tui

import "strings"

// entryRowSpan returns the inclusive absolute m.lines index span [start,end] of
// the entry that owns row idx, and ok=false if idx is out of range. Mirrors the
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

// textForRows renders the given absolute m.lines rows to plain (no-ANSI)
// displayed text, one per line, skipping out-of-range indices. Reuses
// plainExportLine so the output is byte-identical to the save-to-file format.
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

## Component 3: visual-mode text (`internal/tui/visual.go`)

Add a pure builder + copy helper paralleling `buildVisualRef`/`copyVisualSelection`:

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
(Adds `fmt`/`strings` imports to `visual.go` as needed — `fmt` is already
imported.)

## Component 4: dispatch (`internal/tui/app.go`)

**Normal mode** — add a case next to `ActionCopyReference`:
```go
		case keymap.ActionCopyText:
			if _, n := m.copySelectionText(); n > 0 {
				m.flash = fmt.Sprintf("copied %d lines", n)
			} else {
				m.flash = "nothing to copy"
			}
```

**Visual mode** — in `handleVisualKey`, route the keymap-bound copy-text key so
`Y` stays remappable (visual mode otherwise bypasses the keymap). Add, before
the existing `switch msg.String()` hardcoded cases, a keymap check:
```go
	if act, ok := m.km.Lookup(msg.String()); ok && act == keymap.ActionCopyText {
		m.copyVisualText()
		m.exitVisual()
		return m
	}
```
The two-`space` reference flow and esc/j/k handling are untouched.

## Data flow

`Y` keypress → (visual mode?) `handleVisualKey` keymap check → `copyVisualText`
→ `osc52Copy` + flash, `exitVisual`. Otherwise main `Update` switch →
`ActionCopyText` → `copySelectionText` → `selectedRows` (mirrors
`buildReference`) → `textForRows` → `plainExportLine` per row → `osc52Copy` +
flash.

## Edge cases

- Empty buffer / nothing visible → `selectedRows` empty → `buildSelectionText`
  "" → `nothing to copy` flash, no clipboard write.
- Visual mode, no anchor → single caret row copied.
- Search hit whose entry spans multiple rows → all rows of that entry (head +
  continuations), matching `y` copying `line:<id>`.
- Out-of-range row indices defensively skipped in `textForRows`.
- Large selections may exceed terminal OSC 52 caps (inherent; documented).

## Architecture / files

- `internal/keymap/actions.go` — `ActionCopyText` const + `AllActions` entry.
- `internal/keymap/defaults.go` — `ActionCopyText: {"Y"}`.
- `KEYBINDINGS.md` — regenerated.
- `internal/tui/copytext.go` (new) — `entryRowSpan`, `selectedRows`,
  `rangeSlice`, `textForRows`, `buildSelectionText`, `copySelectionText`.
- `internal/tui/visual.go` — `buildVisualText`, `copyVisualText`.
- `internal/tui/app.go` — `ActionCopyText` dispatch + visual-mode keymap route.
- `internal/tui/copytext_test.go` (new) — unit + parity tests.
- `README.md`, `CHANGELOG.md` — document `Y`.

## Testing strategy

**Unit (`internal/tui`)** — seed a model with known entries (reuse existing TUI
test helpers / `push`), then assert `buildSelectionText`:
- **viewport** (no search, no block focus): equals `strings.Join(snapshotViewport(), "\n")`.
- **search hit**: with a search term + `searchHit` set to a multi-row entry,
  the text is exactly that entry's rows (head + continuations) via
  `plainExportLine`.
- **focused block**: with `blockFocused` + `streamTop` on a multi-entry block,
  the text equals that block's rows.
- **multi-line block content**: an entry with a JSON/XML render block — assert
  the continuation rows appear (proves displayed/rendered form, not just raw).
- **visual span**: set `visualAnchor`/`visualCursor` → `buildVisualText` equals
  those rows; with `visualAnchor = -1` → just the caret row.
- **empty**: empty model → `buildSelectionText` == "" and `copySelectionText`
  returns `("",0)`.

**Parity (`internal/tui`)** — the y/Y anti-drift guard: for each context
(search / block / viewport / visual), assert the **entry ids** at the ends of
`Y`'s selection match the ids encoded in `buildReference`/`buildVisualRef`.
Concretely: parse the `line:`/`range:` ref, and compare to
`entryIDForLine(firstRow)` / `entryIDForLine(lastRow)` of `selectedRows()` (or
the visual span). They must be equal — if `selectedRows` ever reorders its
precedence, this fails.

**Docs** — `TestDocsUpToDate` confirms `KEYBINDINGS.md` regenerated.

Each phase commit leaves `go test ./...`, `go vet ./...`, `go test -race ./...`
green.

## Conventions

Phase commits per repo convention. Regenerate `KEYBINDINGS.md` with
`./build.sh keybindings-docs`. Update `README.md` + `CHANGELOG.md` on delivery.
