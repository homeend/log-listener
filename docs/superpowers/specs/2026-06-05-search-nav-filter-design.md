# TUI Search: Filter, Hit Navigation, Auto-Scroll, Repeat — Design

**Date:** 2026-06-05
**Status:** Approved (pending spec review)

## Summary

Four enhancements to the TUI search experience:

1. **`t` — filter to matches.** When a search is active, `t` toggles a mode
   that shows only entries containing the term. Matching is whole-entry: an
   entry's head line plus all of its rendered JSON/XML block lines are shown
   together, so matched JSON is never shown truncated.
2. **Up/Down navigate hits.** While a search term is active, Up/Down (and vim
   `k`/`j`) jump to the previous/next hit instead of scrolling one line.
3. **Horizontal auto-scroll to the hit.** Jumping to a hit whose match is off
   the left/right edge pans the view so the matched term is visible.
4. **`/` then Enter repeats the last term.** Pressing `/` and Enter with an
   empty query re-runs the last committed search term, which persists across
   `Esc`/clear.

## Current Behavior (baseline)

- `/` sets `searchInput=true`, `searchQuery=""`; typing appends; Enter calls
  `commitSearch` → sets `searchTerm` (lowercase substring), jumps to the first
  hit, exits tail. `n`/`p` call `searchNext`/`searchPrev`; `Esc` clears.
- `collectVisible(rows)` returns absolute `m.lines` indices, filtered only by
  `lineEnabled` (group enable/disable + collapse-multiline). No search filter.
- `jumpToHit(idx)` centers vertically (`streamTop = idx - rows/2`), exits tail.
  It never adjusts `horizScroll`.
- Data model: `m.entries []scrollbackEvent` is the source of truth (one per
  pipeline emission, each holding `lines []displayLine`); `m.lines` is the flat
  concatenation. An entry's lines are contiguous in `m.lines`.

## New Model State

Add to `model`:

- `filterMode bool` — whether the `t` filter is active.
- `lastQuery string` — the last committed query, persisted across clears for
  `/`+Enter repeat.

## Feature 1: `t` filter to matches (whole-entry)

- `t` is a no-op unless `searchTerm != ""`. Otherwise it toggles `filterMode`.
- Turning the filter ON sets `tailMode=false` (you are browsing matches).
- When `filterMode` is on, `collectVisible` is driven by a new helper
  `filteredIndices()`:
  - Walk `m.entries`, tracking a running offset into `m.lines` (entry `e`
    occupies `[off, off+len(e.lines))`).
  - For each entry, test whether **any** of its lines contains the term using
    the same predicate as `findHit` (lowercase `Contains`; `stripANSI` first
    for block lines).
  - If matched, append the absolute indices of **all** that entry's lines that
    pass group enable/disable. (Collapse-multiline is ignored for shown
    entries — the explicit goal is to show full matched content, including the
    whole JSON/XML block.)
  - Result: ordered absolute indices of every line in every matching entry.
- Highlighting of matches is unchanged.
- Clearing the search (`Esc`/`clearSearch`) resets `filterMode=false`.

## Feature 2: Up/Down navigate hits when a term is active

In the key handlers for `up`/`k` and `down`/`j`:

- If the files overlay is open, keep the existing files-scroll behavior.
- Else if `searchTerm != ""`: `up`/`k` → `searchPrev()`, `down`/`j` →
  `searchNext()`. Running past the ends sets the existing wrap prompt, exactly
  like `n`/`p`.
- Else: the existing line-scroll behavior.

`PgUp`/`PgDn`, `Ctrl/Shift+Up/Down`, and `Home`/`End` remain scrolling at all
times (the escape hatch for reading around matches). `n`/`p` continue to work.

## Feature 3: Horizontal auto-scroll to the hit

Extend `jumpToHit(idx)`:

- After vertical positioning, compute the on-screen column of the first match
  on the hit line via a helper `hitColumn(idx) int`:
  - Prefix width for head lines = `len("["+group+"] ")` when `showGroup` +
    `len(file+": ")` when `showFile`, in runes; block lines have no prefix.
  - Body match offset = rune index of the (lowercased) term within the line's
    body text (`stripANSI` body for block lines).
  - On-screen column = prefix runes + body rune offset. Returns -1 if the term
    is not on that line (defensive; shouldn't happen for a real hit).
- Let `start = hitColumn(idx)`, `end = start + runeLen(term)`. If
  `start < horizScroll` or `end > horizScroll + width`, set
  `horizScroll = max(0, start - margin)` (small left margin, e.g. `horizStep/2`).
  If the match already lies within `[horizScroll, horizScroll+width)`, leave
  `horizScroll` unchanged. Skip entirely when `width <= 0`.

Vertical positioning in filter mode: when `filterMode` is on, `jumpToHit`
recenters against the filtered list — find the hit's position `pos` in
`filteredIndices()`, set the top to `clamp(pos - rows/2, 0, len(fil)-1)`, and
`streamTop = fil[top]`. In non-filter mode, the existing absolute centering is
kept.

## Feature 4: `/` then Enter repeats the last term

- `commitSearch`: if the trimmed query is empty:
  - if `lastQuery != ""`, treat the search as that query (re-run it: set
    `searchTerm` from `lastQuery` and jump to a hit); do **not** clear.
  - else, `clearSearch()` (unchanged behavior when there is no prior term).
- On a non-empty commit, set `lastQuery` to the committed query before jumping.
- `clearSearch` continues to wipe `searchTerm`/`searchQuery`/`searchHit`/
  `wrapPrompt`/`filterMode`, but must **not** touch `lastQuery`.
- `/` still opens an empty prompt (no prefill).

## Feature 5: UX indicators

- `renderFooter`: when `searchTerm != ""`, the existing `/term` suffix gains a
  ` filter` tag while `filterMode` is on (e.g. `· /userId filter`).
- The help header gains a `t filter` hint.

## Viewport / scroll model (filter mode)

`collectVisible(rows)`:
- Non-filter: unchanged.
- Filter: build `fil = filteredIndices()`; the top row is the first element of
  `fil` whose absolute index is `>= streamTop`; return up to `rows` elements
  from there. `PgUp`/`PgDn` adjust `streamTop` and the mapping re-resolves the
  nearest filtered row, so paging still works. `jumpToHit` sets `streamTop` to
  a filtered anchor as described above.

This reuses the existing `streamTop` anchor — no parallel filtered-scroll state
to keep in sync with appends / trims / re-renders.

## Architecture / Files

All changes are in `internal/tui`:

- `app.go` — new model fields (`filterMode`, `lastQuery`); `t` key; up/down
  delegation to `searchPrev`/`searchNext`; `collectVisible` filter branch;
  `filteredIndices` helper; `hitColumn` helper; footer + header hints.
- `search.go` — `commitSearch` last-query fallback; `clearSearch` preserves
  `lastQuery`, resets `filterMode`; `jumpToHit` horizontal auto-scroll +
  filter-aware vertical centering.

No changes outside `internal/tui`.

## Testing (model-level, no TTY)

- `filteredIndices` returns whole matching entries including all JSON/XML block
  lines, and excludes non-matching entries; respects group disable.
- `t` is a no-op when no term is active; toggles `filterMode` when one is.
- Up/Down call `searchPrev`/`searchNext` only when a term is active; scroll
  otherwise; `n`/`p` unaffected.
- `jumpToHit` sets `horizScroll` when the match is off-screen (left and right
  cases) and leaves it unchanged when the match is already visible; prefix
  width is accounted for on head lines.
- `commitSearch` with empty query re-runs `lastQuery`; `lastQuery` survives
  `Esc`/`clearSearch`; a fresh non-empty search updates `lastQuery`.
- Footer shows the `filter` tag only when `filterMode` is on.

## Out of Scope (YAGNI)

Regex search, case-sensitive toggle, persisting search across restarts.

## Conventions

Phase commits per repo convention (`phase N: <desc>` + review fixes), each
leaving `go test ./...`, `go vet ./...`, `go test -race ./...` green.
