# Word Wrap for the TUI — Design

**Date:** 2026-06-09
**Status:** Approved
**Branch:** `feat/tui-word-wrap`

## Goal

Add a toggleable word-wrap mode to the streaming TUI: when ON, long lines wrap
to multiple display rows instead of being clipped by horizontal pan. Wrap is the
deferred fourth feature split out of the trunc/save/help cycle as the
structurally heavy one.

## Locked decisions (from brainstorm)

1. **Whole-line vertical scroll.** With wrap ON, vertical scroll lands on whole
   logical-line boundaries: `↓` moves past *all* of a line's wrapped rows at
   once, not one terminal row at a time. This keeps the stable-ID viewstate
   anchors completely unchanged. The single accepted cost: a logical line taller
   than the viewport cannot have its tail scrolled into view independently
   (rare — minified JSON / base64 blobs).
2. **Toggle key `w`** (currently unbound; `f`/`m`/`e` are taken).
3. **YAML config default `tui.word_wrap`** mirroring the `tui.truncate_filenames`
   plumbing: key toggles at runtime, YAML sets the startup default.

## Approaches considered

- **A — Render-time expansion (CHOSEN).** `m.lines` / `displayCache` stay
  width-independent exactly as today; wrapping happens only in the view layer at
  paint time. One logical line → N terminal rows when drawn.
- **B — Decompose-time baking (REJECTED).** Bake wrap segments into
  `displayLine`s. Rejected because `displayCache` is deliberately
  width-independent (CLAUDE.md "display-only transform, no cache rebuild" rule),
  `render.DecomposeLines` is shared with the MCP buffer, and baking would shift
  `m.lines` indices — dragging the anchor `off` into counting wrap segments and
  forcing a full cache rebuild on every terminal resize. Fights the grain
  everywhere.

## Core mechanism

A single helper, reusing the existing ANSI-aware window primitive
(`clipANSIWindow` in `view.go`):

```go
// wrapLine splits one fully-styled row (prefix + gutter + body, visible
// width = visW) into ceil(visW/width) terminal rows, each exactly `width`
// display columns. Reuses clipANSIWindow so style state, search highlight,
// and wide-rune straddle are all handled for free.
func wrapLine(line string, visW, width int) []string {
    if width <= 0 || visW <= width {
        return []string{clipANSIWindow(line, 0, width)} // == today's single row
    }
    n := (visW + width - 1) / width
    out := make([]string, 0, n)
    for i := 0; i < n; i++ {
        out = append(out, clipANSIWindow(line, i*width, width))
    }
    return out
}
```

`clipANSIWindow` already preserves style state across the skip boundary
(escapes before `skip` are emitted verbatim) and replaces a straddling wide rune
with a filler space, so colors and the search-term highlight survive wrapping
with no extra work. The prefix (`[g] file:`) and the gutter bar land on row 1;
continuation rows are pure body windows starting at column 0.

## What changes

All new behavior is gated on `m.wordWrap`. **The wrap-OFF path stays
byte-identical to today** — wrap is a clean conditional branch, not a rewrite of
the render path.

1. **`renderStream` (view.go):** for each visible line, expand via `wrapLine`
   instead of a single `clipLine`, then fill / truncate to exactly `rows`
   terminal rows. Tail mode fills from the bottom (the topmost line may show only
   its trailing wrap rows); browse mode fills from `streamTop` downward (the
   bottom line may show only its leading wrap rows). This is standard pager
   behavior.

2. **`collectVisible` (view.go) becomes height-aware:** collect logical lines
   until the **sum** of their wrapped heights (`ceil(visW/width)`) reaches `rows`
   terminal rows — not until it has `rows` lines. This is the load-bearing
   correctness fix: without it the viewport overflows by several rows and
   re-triggers the exact vanishing-header glitch `clipLine` exists to prevent.
   The per-line height MUST be measured from the *same* rendered visible width
   `renderStream` paints with (prefix + gutter + collapse-marker contributions),
   so there is one source of width truth and no drift. Both the tail-mode
   backward walk and the browse-mode forward walk are affected.

3. **`viewport.go` / page handlers (update.go):** `scrollBy(±1)` is unchanged —
   it already moves one logical line, which *is* whole-line scroll. Only Page and
   Fast deltas overshoot when wrapping (they advance `contentHeight` /
   `vertFastStep` logical lines, each now several terminal rows). When wrap is
   ON, Page/Fast deltas are translated through terminal-row space: advance enough
   logical lines to cover ~one screen (`contentHeight`) of terminal rows, so
   PageDown ≈ one screen rather than several.

4. **wrap ⊥ pan:** toggling wrap ON resets `horizScroll = 0`; the horizontal pan
   keys (`ScrollLeft/Right`, `FastLeft/Right`) no-op while wrapping; the footer
   shows `wrap` in place of `col: N` so it never implies panning is active.

## State, keybinding, config (mirrors `truncate_filenames` plumbing)

- `model.wordWrap bool`; `Options.WordWrap`; `New` sets `m.wordWrap = opts.WordWrap`.
- `keymap.ActionToggleWordWrap` + `AllActions` entry (action-count guard
  38 → 39); default key `w` in `defaults.go`; `KEYBINDINGS.md` regenerated and
  guarded by `TestDocsUpToDate`.
- `update.go`: `case keymap.ActionToggleWordWrap` → flip `m.wordWrap`, reset
  `m.horizScroll = 0`.
- Config: `yaml.go` `TUI.WordWrap *bool` (yaml `word_wrap`) + flatten block in
  `mergeYAMLInto`; `cli.go` resolved `Config.TUIWordWrap bool`; `main.go` wires
  `Options.WordWrap: cfg.TUIWordWrap`; `log-listener.example.yml` documents
  `word_wrap: false` under `tui:`.

## Untouched (the headline)

**`rowAnchor` and all four consumers — `streamTop`, `searchHit`, `visualCursor`,
`visualAnchor` — are unchanged.** Keeping the anchor at logical-line granularity
is *correct* for the selection/search/copy consumers (a partial-wrapped-row
selection is meaningless), not a compromise. Wrap lives entirely in the paint
layer; no `viewanchor.go` change.

## Documented v1 limitation

- A single logical line taller than the viewport cannot have its tail scrolled
  into view independently. Same root cause as whole-line scroll; clean YAGNI
  boundary. A `streamTop`-only sub-row offset is a coherent v2 if anyone needs it.
- The gutter / selection bar (visual mode, exception marks, focus bar) appears on
  a wrapped line's **first** terminal row only; continuation rows carry body
  content alone.

## Interactions verified against existing behavior

- **Search highlight:** survives via `clipANSIWindow` ANSI preservation; height is
  unaffected because `highlightMatches` adds zero-width ANSI only.
- **Collapse-multiline (`m`):** orthogonal — wrap applies to whatever rows are
  visible after collapse filtering. The `[...]` collapse marker's width is part of
  the rendered width used for height accounting.
- **Reload / eviction:** unaffected — `displayCache` and anchors are untouched,
  so reconcile behaves exactly as today.

## Testing strategy

- `wrapLine` unit tests: short line (1 row, == old clip output), exact-width line
  (1 row), overflow (N rows each exactly `width` cols), wide-rune (CJK) straddle
  at the wrap boundary, ANSI/highlight span crossing a boundary.
- `collectVisible` height-aware tests: mixed tall/short lines fill exactly `rows`
  terminal rows in both tail and browse mode; no overflow past the viewport.
- Page/Fast translation: PageDown with wrapped lines advances ≈ one screen of
  terminal rows, not several.
- wrap ⊥ pan: toggling wrap ON zeroes `horizScroll`; pan keys are no-ops while
  wrapping; footer shows `wrap`.
- Config plumbing: `word_wrap: true` in YAML starts the model wrapped.
- Anchor invariance: a visual selection across wrapped lines still yanks/saves the
  whole logical lines (seed via `seedSearch` + `reconcile`, per the stable-ID
  anchor test discipline).
- `TestDocsUpToDate` stays green after `KEYBINDINGS.md` regen; action-count guard
  updated 38 → 39.

All gates green throughout: `go test ./...`, `go vet ./...`, `go test -race ./...`.
