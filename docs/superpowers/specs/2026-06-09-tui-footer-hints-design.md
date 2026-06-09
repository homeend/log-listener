# Context-Sensitive Footer Hints — Design

**Date:** 2026-06-09
**Component:** `internal/tui`
**Status:** Approved for planning

## Goal

Make the TUI's bottom status bar surface the keys most useful *at the current
moment* instead of relying solely on the static hint list in the top header.
When the user is searching, show match-navigation keys; when a block is
focused, show block-jump keys; when scrolled away from the tail, show
jump-back keys; and so on. This generalizes the context-hint pattern that
already exists for visual-selection mode.

## Background (current state)

The TUI paints two bars (see `internal/tui/view.go`):

- **Top header** (`View`, lines ~53-68): a *static* list of global action
  hints (`^C quit · Tab files · ^G groups · … · ? help`). **Unchanged by this
  feature.**
- **Bottom footer** (`renderFooter`): status counters
  (`events · pos · col · groups · rend · files · /term`) in the normal case,
  but it already early-returns three full-width takeover bars and one
  context-hint bar:
  1. `visualMode` → `VISUAL  ↑↓ move · space anchor · y ref · Y text · s save · esc cancel`
  2. `searchInput` → `/<typed>_`
  3. `wrapPrompt` → `No more hits — wrap to top|bottom? (y/n)`
  4. `flash` → transient message (e.g. `copied 3 lines`)

This feature keeps bars 2-4 (search-input, wrap-prompt, flash) as full-width
takeovers checked *first*, and replaces the remaining cases (including the
existing visual-mode bar) with a single composed **hints-left + compact-status-
right** bar driven by the current context.

All hint keys are rendered through the existing `m.keyDisplay(action)` helper,
so per-OS key forms and YAML overrides are reflected automatically.

## Layout

The composed bottom bar is one terminal row:

```
[LABEL] hint · hint · hint …                    ev N · @pos · /term
└────────────── left (context hints) ───────────┘└──── right (status) ────┘
```

- **Left:** an optional short uppercase mode label, then the context's ordered
  hint list joined with ` · `. Default (tail) context has no label.
- **Right:** a compact status tail — the same data as today's footer,
  abbreviated: `ev <len(m.lines)>`, position (`tail` or `@<streamTopRow>/<len>`),
  and `/<searchQuery>` when a search term is committed.
- The two are separated by padding so status is right-aligned to `m.width`.

## Contexts, precedence, and hint sets

Exactly one context is active per frame. Precedence is highest-first; the first
matching state wins. (The search-input / wrap-prompt / flash takeovers are
resolved *before* any of these.)

| Pri | Context (model state)        | Label             | Hint actions (in order)                                                |
|-----|------------------------------|-------------------|------------------------------------------------------------------------|
| 1   | `m.visualMode`               | `VISUAL`          | space anchor · `CopyReference` ref · `CopyText` text · `SaveViewport` save · `CloseOverlay` cancel |
| 2   | `m.blockFocused`             | `BLOCK`           | `NextBlock`·`PrevBlock` next·prev · `NextMarkedBlock`·`PrevMarkedBlock` marked · `ToggleExceptionMarks` marks · `CopyReference` copy · `CloseOverlay` esc |
| 3   | `m.matcher != nil`           | `SEARCH`/`FILTER` | `NextMatch`·`PrevMatch` next·prev · `Filter` filter · `NextBlock`·`PrevBlock` blocks · `CloseOverlay` clear |
| 4   | `!m.tailMode`                | `BROWSE`          | `Bottom` tail · `Top` top · `ScrollUp`·`ScrollDown` scroll · `PageUp`·`PageDown` page · `VisualSelect` select |
| 5   | default (tail)               | *(none)*          | `Search` search · `VisualSelect` select · `NextBlock`·`PrevBlock` blocks · `CollapseAll` collapse · `Help` help |

Notes:

- **Visual mode** (pri 1) is folded into this system: its hint set is today's
  set, but it now also shows the compact status on the right (it showed none
  before).
- **`space anchor`** in the VISUAL set is a literal label — `space` is not a
  keymap action (it is one of the keys bound to `PageDown`, repurposed inside
  visual mode), so it is hard-coded rather than resolved via `keyDisplay`.
- **SEARCH vs FILTER:** when `m.filterMode` is true the label is `FILTER` and
  the `Filter` hint word is `unfilter`; otherwise the label is `SEARCH` and the
  word is `filter`.
- Block-jump keys appear in the SEARCH set as well as the BLOCK set (a teaser
  while searching; the fuller BLOCK set takes over once a block is focused).

## Width fitting

When `dispWidth(left) + dispWidth(right) + 1` exceeds `m.width`, drop hint
entries from the **right end** of the left list (lowest priority within the
context) one at a time and append a trailing `…`, until it fits. The status
tail is preserved (it is short and carries live state). If even the label +
status cannot fit, the status is right-truncated by the existing single-row
clamp (`MaxHeight(1)` / width clamp) — no special handling beyond what the
current footer already does.

Measurement reuses `dispWidth` (ANSI-aware) from `internal/tui/width.go`.

## Components / files

- **Create `internal/tui/footerhints.go`:**
  - `func (m *model) contextHints() (label string, hints []string)` — pure
    selection of label + ordered hint strings per the precedence table. Each
    hint string is built with a small helper that pairs `m.keyDisplay(action)`
    (or a literal) with a label word.
  - `func (m *model) compactStatus() string` — the abbreviated right-hand
    status tail.
  - `func (m *model) composeFooterBar(label string, hints []string) string` —
    joins label + hints, right-aligns `compactStatus`, applies width fitting.
- **Modify `internal/tui/view.go` (`renderFooter`):** after the
  search-input / wrap-prompt / flash early returns, replace both the
  `visualMode` early return and the normal status block with a single call that
  renders `composeFooterBar(m.contextHints())`. Styling: hints/label use the
  existing `headerBg` for mode contexts (matching today's visual bar) and the
  `dimStyle` look for the default tail context, to keep visual parity.

No changes to `internal/keymap`, config, or any other package. No new
keybindings, no new config fields.

## Testing

`internal/tui/footerhints_test.go`:

1. **Per-context content** — construct a model in each state (visual, block,
   search, filter, browse, tail) and assert `contextHints()` returns the
   expected label and that the joined hints contain the expected key glyphs and
   words.
2. **Precedence** — set overlapping states (e.g. `visualMode` +
   `blockFocused` + `matcher`) and assert visual wins; then block beats search;
   then search beats browse; then browse beats tail.
3. **FILTER variant** — `filterMode` true flips the label to `FILTER` and the
   word to `unfilter`.
4. **Width truncation** — narrow `m.width` drops low-priority hints and appends
   `…`, while the compact status is still present and right-aligned.
5. **Override reflection** — remap a hinted action's key via a keymap override
   and assert the rendered hint shows the new glyph (proves `keyDisplay` wiring).

All existing TUI tests must stay green (`go test ./...`, `go vet ./...`,
`go test -race ./...`).

## Out of scope

- The top header static hint list (unchanged).
- Any new keybindings or config fields.
- The search-input, wrap-prompt, and flash takeover bars (unchanged).
- README/CHANGELOG updates are handled at delivery time, not part of this
  design's scope decisions.
