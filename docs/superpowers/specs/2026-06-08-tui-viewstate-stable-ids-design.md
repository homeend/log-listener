# TUI View-State as Stable IDs (slice 5-3) — Design

**Date:** 2026-06-08
**Status:** Draft — design agreed in brainstorming; pending user review of this written spec.
**Scope:** `internal/tui` (view-state representation + ~108 call sites across ~7 files).

## Context

Slice **5-3** of cycle #5 (TUI on the shared model — see
[[plugin-architecture-roadmap]]). 5-1 put the TUI on the shared `linebuf` record
store; 5-2 unified search. The TUI's view-state (`streamTop`, `searchHit`,
`visualCursor`, `visualAnchor`) is still stored as **absolute `m.lines` row
indices**, which drift on eviction and must be re-adjusted each reconcile by
`dragViewStateDown`. This slice re-expresses view-state in **stable entry IDs**
so it is eviction-proof and the index-drag machinery can be removed.

### Honest ROI note (the user chose to proceed with this in view)

This is a large, low-direct-ROI refactor: ~108 call sites (with ~70 in `app.go`)
to remove one small, working, tested function (`dragViewStateDown`, ~30 lines).
The drift-class bug that motivated "IDs are safer" (the 5-1 TOCTOU) is already
fixed. The value is conceptual robustness/cleanliness, not a current bug fix.
The user reviewed this trade-off explicitly and chose to proceed, staged.

## Strategy: wrap-first / flip-last accessor seam (makes every batch green)

The danger ("you can't half-convert `streamTop`") only exists if you convert the
*storage* first. Invert it: convert storage **last**.

- **Stage 0 — accessor seam (green floor, zero behavior change).** Add accessors
  that wrap the existing fields verbatim, e.g.:
  ```go
  func (m *model) streamTopRow() int     { return m.streamTop }
  func (m *model) setStreamTopRow(i int) { m.streamTop = i }
  ```
  The field stays authoritative. No behavior change. Commit.
- **Stages 1..N — migrate call sites in batches (mechanical, always green).**
  Replace `m.streamTop` reads → `m.streamTopRow()` and writes →
  `m.setStreamTopRow(...)`, a batch at a time, testing between. Every site —
  migrated or not — still touches the *same field*, so divergence is impossible
  and each batch is green. This is the bulk (~100 sites); safe find-replace.
- **Final flip per value — the real work (one focused commit each).** Once `grep`
  confirms no direct field references remain, change the storage representation
  (e.g. `streamTop int` → an anchor `(entryID string, rowOffset int)`), rewrite
  **only the accessor internals** to resolve/anchor against `m.window` +
  `displayCache`, and delete the now-unneeded drag for that value. The compiler
  enforces completeness: removing the field fails the build at any missed site
  before the flip lands.

This concentrates the genuine risk into a few small resolver flips rather than
spreading it across 108 sites.

## Decomposition — by view-state value, not by file

Three independently-shippable flip-units (each: accessor seam → migrate sites →
flip storage → keep regression tests green):

1. **`streamTop`** — the hard one. Its anchor is `(entryID, rowOffset)` because a
   row index can point mid-entry (a continuation row of a tall JSON/XML block).
   Resolver maps `(entryID, rowOffset) ↔ absolute m.lines index` using `m.window`
   order + `displayCache[id]` row counts. Eviction of the anchor entry must
   reproduce today's clamp-to-0 behavior.
2. **`searchHit`** — anchor is `(entryID, rowOffset)` of the matched row; on
   eviction, reproduce today's "scrolled off → unset to -1" behavior.
3. **`visualCursor` + `visualAnchor`** (as a pair) — each an `(entryID,
   rowOffset)` anchor; reproduce today's clamp (cursor → 0) and unset (anchor →
   −1) eviction semantics.

`dragViewStateDown` is deleted once all four values have flipped to anchors (its
job — adjusting indices on eviction — is then handled by the resolvers returning
clamped/unset results for evicted anchors).

## Resolver (the core of each flip)

Two helpers over the current window (computed once per reconcile, cached):

- `rowForAnchor(id string, off int) (idx int, ok bool)` — absolute `m.lines`
  index for `(entryID, rowOffset)` in the current window; `ok=false` if the entry
  is no longer visible (evicted or scrolled out of the window). `off` is clamped
  to the entry's current row count (a re-render can change an entry's row count).
- `anchorForRow(idx int) (id string, off int, ok bool)` — inverse: the
  `(entryID, rowOffset)` owning absolute row `idx`.

Both walk `m.window` accumulating `len(displayCache[id])` — the same accumulation
`visibleEntries`/`entryIDForLine` already do (5-1), so this is a known-correct
pattern. The accessors (`streamTopRow`/`setStreamTopRow`, etc.) call these.

**Evicted-anchor semantics (must match `dragViewStateDown` exactly):**
- `streamTop`: evicted anchor → clamp to row 0 (top of the now-shorter window).
- `searchHit`: evicted → unset (-1).
- `visualCursor`: evicted → clamp to 0. `visualAnchor`: evicted → unset (-1).

## Regression net (behavior preservation is the contract)

The existing eviction tests are the proof the resolvers reproduce today's
behavior — they must stay green **unchanged**:
- `TestVisualIndicesClampOnEviction`
- `TestReconcileEvictionDragsViewState`
Plus the full TUI suite (scroll/page/visual/search/copy), `go vet`, `go test
-race`. Each stage commit must be green.

## Non-goals

- No change to display formatting, rendering, search semantics (5-2), or the
  shared record model (5-1).
- No change to MCP.
- No new view-state *features* — purely a representation change.

## Success criteria

- `streamTop`/`searchHit`/`visualCursor`/`visualAnchor` are stored as stable
  `(entryID, rowOffset)` anchors; no absolute-index view-state fields remain.
- `dragViewStateDown` is deleted; eviction behavior is preserved (resolvers
  return clamped/unset results for evicted anchors).
- The full TUI suite + the two eviction regression tests stay green unchanged;
  `go test ./...`, `go vet ./...`, `go test -race ./...` green.
- Each stage landed as its own green commit (floor-then-flip), so the work can
  pause/resume at any stage boundary.
