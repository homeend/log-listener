# TUI View-State as Stable IDs (slice 5-3) — Design

**Date:** 2026-06-08 (un-deferred / refreshed 2026-06-09)
**Status:** ACTIVE. Originally deferred 2026-06-08 in favor of the viewport
operations layer (now merged). Re-opened 2026-06-09 with the user accepting,
eyes open, that this flip *adds* net lines (it deletes the ~27-line
`dragViewStateDown` but adds resolver + accessor machinery across the call
surface) — the payoff is eviction-proof view-state and removal of the
index-drag machinery, i.e. structural robustness, **not** a line-count win and
**not** a live bug fix (the motivating 5-1 TOCTOU was already fixed). This runs
as the first half of a two-part effort; the second part (#4, unify the
renderers) is where actual code deletion is targeted.
**Scope:** `internal/tui` (view-state representation). Refreshed call-site
surface measured 2026-06-09: **101 production + 160 test ≈ 261** direct field
references (`streamTop` 48/92, `searchHit` 24/33, `visualCursor` 25/18,
`visualAnchor` 15/17). The earlier "~108/~248" numbers predate the 5-1/5-2
test additions and the app.go split. Most edits are mechanical getter/setter
swaps; the genuine risk is concentrated in the resolver + the four storage
flips. The viewport operations layer (`scrollBy`/`moveVisualCursor`) did **not**
shrink this surface — it concentrated a handful of *writes*; the dominant read
sites and the `searchHit`/`visualAnchor` writes are untouched by it.

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

- `rowForAnchor(a rowAnchor) (idx int, ok bool)` — absolute `m.lines`
  index for the anchor in the current window; `ok=false` if the anchor is the
  sentinel or its entry is no longer visible (evicted or scrolled out of the
  window). `off` is clamped to the entry's current row count (a re-render can
  change an entry's row count).
- `anchorForRow(idx int) rowAnchor` — inverse: the `(entryID, rowOffset)`
  owning absolute row `idx`. **Index-domain rule (refined 2026-06-09):**
  - `idx < 0` → **sentinel** (`rowAnchor{}`). Preserves the unset semantics of
    `searchHit`/`visualAnchor` (−1).
  - empty window (no visible entries) → **sentinel**.
  - `idx` in `[0, total)` → the exact owning anchor.
  - `idx >= total` in a **non-empty** window → clamp to the **last row** (a
    resolvable anchor on the last entry's last offset), **not** the sentinel.

The past-end clamp is load-bearing: `scrollBy(delta>0)` intentionally lets the
target row run past the end and relies on `maybeReStick()` to re-pin to tail.
If a past-end write collapsed to the sentinel, `streamTopRow()` would resolve to
**0** and `maybeReStick` would count every row from the top → stay browsing at
the top instead of re-sticking. Clamping past-end to the last row reproduces the
old `streamTop`-runs-past-end-then-re-sticks behavior. The asymmetry
(`idx<0`/empty → sentinel; past-end-nonempty → last row) is safe for all four
values: only `streamTop` ever receives a past-end index (via `scrollBy` down);
`searchHit`/`visualCursor`/`visualAnchor` are only ever set to in-range indices
or `-1`.

Both walk `m.window` accumulating `len(displayCache[id])` — the same accumulation
`visibleEntries`/`entryIDForLine` already do (5-1), so this is a known-correct
pattern. The accessors (`streamTopRow`/`setStreamTopRow`, etc.) call these, and
the viewport ops (`scrollBy`/`moveVisualCursor`) compose *on top of* the
accessors (they become ordinary call sites: `setStreamTopRow(streamTopRow()+delta)`).

**Evicted-anchor semantics (must match `dragViewStateDown` exactly):**
- `streamTop`: evicted anchor → clamp to row 0 (top of the now-shorter window).
- `searchHit`: evicted → unset (-1).
- `visualCursor`: evicted → clamp to 0. `visualAnchor`: evicted → unset (-1).

**Unresolvable-write rule (one rule per value, same as eviction).** A setter
called with a row index that `anchorForRow` maps to the sentinel — `idx < 0`,
or an empty window (e.g. `setStreamTopRow(0)` before the first reconcile) —
stores a sentinel anchor (`entryID == ""`) that the getter resolves to that
value's clamp result: `streamTopRow()→0`, `searchHitRow()→-1`,
`visualCursorRow()→0`, `visualAnchorRow()→-1`. This is the *same* outcome as an
evicted anchor, so there is one rule per value, not two. A **past-end** index in
a non-empty window is *not* unresolvable — it clamps to the last row (see the
index-domain rule above), so an intentional over-scroll re-sticks rather than
jumping to the top. The conditional drag (streamTop only when `!tailMode`; visual
only when `visualMode`) is preserved by the getters reading the same conditions,
not by the setters.

**`reRenderAll`'s post-reconcile clamp block is deleted (not migrated).**
`reRenderAll` currently re-clamps `streamTop`/`searchHit` against the new line
count after a toggle-driven re-render (`if m.streamTop > len(m.lines) { … }`,
`if m.searchHit >= len(m.lines) { … = -1 }`). That block exists *only* because
the values are stale-able ints; under anchors the resolver clamps the offset into
the (re-rendered) entry automatically, so the block becomes dead code and is
removed in the flip (the streamTop lines with Task 4, the searchHit lines with
Task 5) — exactly as `dragViewStateDown` is removed. This also eliminates the
second production past-end writer (`m.streamTop = len(m.lines)`), so it never
reaches a setter. The micro-improvement (a re-render while scrolled past a
collapsing block now resolves to a valid row instead of a past-end index) is
intended, not a regression.

## Regression net (behavior preservation is the contract)

The existing eviction tests are the proof the resolvers reproduce today's
behavior. Their **assertions and eviction semantics stay unchanged**; their
**field access is mechanically swapped to accessors** in the same commit that
flips that value's storage and removes the field (the field exists through the
seam + migration stages, so the tests compile untouched until then):
- `TestVisualIndicesClampOnEviction` — reads `m.visualCursor`/`m.visualAnchor`;
  swap reads to `m.visualCursorRow()`/`m.visualAnchorRow()`. Cursor/anchor are
  *set* by `keyV`/`keyJ`/`keySpace` through production handlers (which route
  through accessors after the flip), so no write edits here.
- `TestReconcileEvictionDragsViewState` — writes `m.streamTop = 0` (before any
  reconcile, **empty window**) and `m.streamTop = 2`, reads `m.streamTop`; swap
  to `m.setStreamTopRow(0)` / `m.setStreamTopRow(2)` / `m.streamTopRow()`.

After the swap these tests get **stronger**: they now prove the anchor
round-trips (write row 2 → evict a row → resolve back to 1), not just that an
int was decremented.

**New regression test (coverage gap found 2026-06-09):** the suite does **not**
currently assert that scrolling *down past the end* re-sticks to tail — the
existing re-stick test (`app_test.go`) uses `End` (a direct tail jump), which
never exercises `maybeReStick` via over-scroll. Because the past-end resolver
rule is exactly what preserves this path, add a test **first, against the current
`int` code** (so it passes as a baseline and is committed as part of the safety
net before any flip): drive the real scroll-down key (or `scrollBy(+big)`) from a
browsing state and assert `tailMode == true` afterward. Written this way it would
*fail* under a naive sentinel→0 translation, which is the proof the past-end
clamp is needed.

Plus the full TUI suite (scroll/page/visual/search/copy), `go vet`, `go test
-race`. Each stage commit must be green.

### Test surface (refreshed 2026-06-09)

Direct field references measured on the current tree (post app.go split,
post 5-1/5-2): **101 production + 160 test ≈ 261** total (`streamTop` 48/92,
`searchHit` 24/33, `visualCursor` 25/18, `visualAnchor` 15/17). The risk
surface is still the resolver + the 4 storage flips; the test references are
mostly mechanical getter/setter swaps, edited within each value's flip commit.
Production writes now span `blocks.go`, `reconcile.go`, `search.go`, `update.go`,
`viewport.go`, `visual.go` — so the migration is driven by `grep`, not by file,
and the ops methods (`scrollBy`, `moveVisualCursor`, `unstickFromTail`,
`maybeReStick`) are migrated as ordinary call sites.

## Non-goals

- No change to display formatting, rendering, search semantics (5-2), or the
  shared record model (5-1).
- No change to MCP.
- No new view-state *features* — purely a representation change.

## Success criteria

- `streamTop`/`searchHit`/`visualCursor`/`visualAnchor` are stored as stable
  `(entryID, rowOffset)` anchors; no absolute-index view-state fields remain.
- `dragViewStateDown` is deleted and `reRenderAll`'s post-reconcile clamp block
  is deleted; eviction and re-render behavior are preserved (resolvers return
  clamped/unset results for evicted anchors and clamp offsets into re-rendered
  entries).
- The new scroll-down-past-end → re-stick regression test is added first
  (green against the int baseline) and stays green across every flip.
- The full TUI suite + the two eviction regression tests stay green; the two
  eviction tests keep their assertions and semantics, with field access
  swapped to accessors (within each value's flip commit). `go test ./...`,
  `go vet ./...`, `go test -race ./...` green.
- Each stage landed as its own green commit (floor-then-flip), so the work can
  pause/resume at any stage boundary.
