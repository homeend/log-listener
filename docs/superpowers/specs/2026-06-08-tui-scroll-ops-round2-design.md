# TUI Scroll Operations, Round 2 (filesScroll + horizScroll) — Design

**Date:** 2026-06-08
**Status:** Draft — continuation of the viewport-operations layer
(`2026-06-08-tui-viewport-operations-design.md`); pending user review.
**Scope:** `internal/tui` — extract the remaining scattered scroll-clamp
arithmetic for the file overlay and horizontal pan into two intent-level
methods. No behavior change.

## Context

The first operations slice (5-3) extracted `scrollBy`/`selectionBounds`/
`moveVisualCursor`. It deliberately left the `showFiles` branches of the six
scroll actions untouched, plus the horizontal-pan handlers. This round finishes
the job for the two view-state values that still carry inline clamp math.

## What the inventory found

- **`filesScroll` (file overlay) — 6 action sites, one operation.** The six
  scroll actions each have a `showFiles` branch that moves `filesScroll` and
  clamps to `[0, len(m.files)-1]`. ScrollUp/ScrollDown use guard-and-skip
  (`if filesScroll > 0 { filesScroll-- }`, `if < len-1 { ++ }`);
  Page/Fast Up/Down use delta + clamp-both-ends. All six unify under a single
  `scrollFiles(delta)` that adds `delta` then clamps to `[0, len-1]` — identical
  to guard-and-skip for ±1 (the same equivalence used by `moveVisualCursor`).
- **`horizScroll` (horizontal pan) — 4 action sites, one operation.**
  `ActionScrollLeft/FastLeft` do `horizScroll -= step; clamp≥0`;
  `ActionScrollRight/FastRight` do `horizScroll += step` (no upper clamp — the
  renderer clips). A single `panBy(delta)` adds `delta` then clamps at 0.

**Deliberately NOT changed (policy mismatch or no operation):**
- `filesScroll` reset-to-0-on-out-of-range sites (`app.go:762–763, 801–802`:
  `if filesScroll >= len(files) { filesScroll = 0 }`) — this is reset-to-top, a
  *different* policy than clamp-to-last. Forcing it into `scrollFiles` would be
  wrong. Leave as-is. Same for the `filesScroll = 0` resets on overlay open.
- `horizScroll = 0` resets (`ActionResetHoriz`, clear) and reads
  (footer/`clipLine`/`adjustHorizToHit`) — single sites, not arithmetic.
- **`groupsScroll` / `renderersScroll`** — these are NEVER delta-scrolled (the
  group/renderer panels don't respond to scroll keys). Their only sites are
  `= 0` resets, reset-to-0-on-out-of-range clamps, and one read each. There is
  no operation to extract; a "generic list-scroll helper" would have a single
  consumer (`filesScroll`), so it is not built. Left entirely untouched.

This keeps the round honest: exactly two operations, each collapsing a real
repetition cluster (6 sites and 4 sites), measured at the call sites.

## The operations

Both are methods on `*model` in `internal/tui/viewport.go` (alongside
`scrollBy`), behavior byte-equivalent to the inline code they replace.

### `scrollFiles(delta int)`

Byte-equivalent to the six inline branches. The old PageDown/FastDown branches
clamp **high then low** (`if > len-1 { = len-1 }` then `if < 0 { = 0 }`); that
order is what makes the empty-list edge (`len-1 == -1`) resolve to 0 rather than
-1. `scrollFiles` reproduces that order exactly, so it also subsumes
ScrollUp/Down's guard-and-skip (identical for ±1) and PageUp/FastUp's
low-only clamp (the high clamp never fires on a decrement):

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

Call sites (the inner `showFiles` branch of each action; the stream branch —
now `scrollBy` — and any `matcher` branch stay put):
- `ActionScrollUp`   showFiles branch → `m.scrollFiles(-1)`
- `ActionScrollDown` showFiles branch → `m.scrollFiles(1)`
- `ActionPageUp`     showFiles branch → `m.scrollFiles(-page)`
- `ActionPageDown`   showFiles branch → `m.scrollFiles(page)`
- `ActionFastUp`     showFiles branch → `m.scrollFiles(-vertFastStep)`
- `ActionFastDown`   showFiles branch → `m.scrollFiles(vertFastStep)`

(The overlay is only shown when files exist, so the empty case is unreachable in
practice; the clamp order keeps `filesScroll ≥ 0` regardless, documenting it.)

**Out of scope — `ActionTop`/`ActionBottom` file branches:** these are *jumps*
(`filesScroll = 0` and `filesScroll = len-1; clamp≥0`), not delta arithmetic, so
they stay inline. Not worth a method (single-purpose, already minimal).

### `panBy(delta int)`

```go
// panBy pans the horizontal view by delta columns, clamped at the left edge
// (horizScroll ≥ 0). There is no right-edge clamp: the renderer clips overlong
// lines, so over-panning right simply shows blank — matching the old inline
// ActionScrollRight/FastRight behavior. Centralizes the four pan handlers.
func (m *model) panBy(delta int) {
	m.horizScroll += delta
	if m.horizScroll < 0 {
		m.horizScroll = 0
	}
}
```

Call sites:
- `ActionScrollLeft`  → `m.panBy(-horizStep)`
- `ActionScrollRight` → `m.panBy(horizStep)`
- `ActionFastLeft`    → `m.panBy(-horizFastStep)`
- `ActionFastRight`   → `m.panBy(horizFastStep)`

## Non-goals

- No behavior change. Scroll/pan are pixel-identical.
- No change to `groupsScroll`/`renderersScroll`, the `filesScroll` reset-to-0
  sites, `horizScroll` resets/reads, or any rendering.
- No generic multi-field list-scroll helper (single consumer — YAGNI).
- Not the app.go file split (that is the separate Effort B).

## Testing strategy

Existing file-overlay and horizontal-scroll tests must stay green unchanged.
New unit tests:
- `scrollFiles`: clamps at 0 (up past top), clamps at `len-1` (down past
  bottom), moves within range, multi-row (page) delta.
- `panBy`: clamps at 0 (left past edge), moves right (no upper clamp), fast
  steps.

`go test ./...`, `go vet ./...`, `go test -race ./internal/tui/`, and tagged
builds (`-tags nomcp`, `-tags nosse`) green.

## Success criteria

- `scrollFiles(delta)` and `panBy(delta)` exist in `internal/tui/viewport.go`.
- The six `showFiles` scroll branches are each one `scrollFiles(...)` call; the
  four pan handlers are each one `panBy(...)` call.
- `grep` shows no remaining `filesScroll`/`horizScroll` delta-arithmetic in the
  action handlers (only the reset-to-0 / reset / read sites remain, by design).
- All existing TUI tests pass unchanged; new unit tests pass; full suite + vet +
  race + tagged builds green.
