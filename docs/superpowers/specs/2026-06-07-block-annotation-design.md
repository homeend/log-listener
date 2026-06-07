# Block Annotation + Render Plugins — Design

**Date:** 2026-06-07
**Status:** Approved (pending spec review)

## Summary

Give the TUI a notion of **blocks** (multi-line log units — stack traces,
pretty-printed JSON, indented continuations), let pluggable **processors**
annotate a block with metadata, and let toggleable **render plugins** decorate
annotated blocks. v1 ships exactly one processor (**exception detection**, with
per-language signatures) and one render plugin (**`renderException`**, a colored
left bar). Plus block navigation: two key sets, one over all blocks and one over
"processed" blocks (those a processor matched).

Segmentation + processors live in a neutral, dependency-free package
(`internal/blocks`) so feature #1 (the MCP server) can reuse them for a
`list_exceptions` tool. Render plugins stay TUI-side.

This is feature #3 of the streaming→agent roadmap
(`docs/superpowers/specs/2026-06-07-streaming-agent-features-roadmap.md`).
Per-line / per-block **IDs, a cursor, and clipboard copy are explicitly out of
scope** — they are deferred to feature #1, where an agent consumes them.

## Goals / Non-goals

**Goals:** detect exception/stack-trace blocks across common languages; mark
them with a left bar; jump between blocks (all, and processor-matched); expose a
clean processor/render-plugin seam so future analyses slot in without touching
the segmenter.

**Non-goals (YAGNI):** line/block IDs, cursor selection, OSC 52 clipboard
(feature #1); a config-driven plugin registry; more than one processor or render
plugin; folding behavior beyond the existing `m` collapse; perfect single-block
grouping for every language (see Limitations).

## Current Behavior (baseline)

- `model.lines []displayLine` is the flat row cache. `displayLine{ group, file,
  body string; bodyWidth int; isBlock bool }`. Head rows have `isBlock=false`
  and render with the `[group] file:` prefix; continuation rows (embedded
  newline / JSON / XML) have `isBlock=true` and render **body-only, no prefix**.
- `isContinuation(dl)` returns true for `isBlock` rows or rows whose `body`
  starts with a space/tab. It drives the `m` collapse-multiline toggle and the
  `[...]` marker.
- `renderDisplayLineCore(dl, isCurrent)` returns `(styledString, visW)` where
  `visW` is the unstyled visual column width. `clipLine(line, visW)` pads to
  `m.width` (or clips under horizontal scroll) using `visW`. **If `visW`
  understates the real width, the row overflows `m.width`, wraps, and scrolls
  the header off-screen** — a class of bug this codebase has already fixed
  twice. Any added glyph MUST be accounted for in `visW`.
- `collectVisible(rows)` yields the visible absolute `m.lines` indices, honoring
  tail/browse position, group enable/disable, collapse, and search-filter mode.
- `m.lines` is mutated by: `appendEvent` (append), `trimToCap` (drop from
  front), `reRenderAll` (renderer toggle → full rebuild), `applyReload` (config
  reload → full rebuild).
- Search-hit navigation (`searchNext`/`searchPrev`/`jumpToHit`) is the existing
  pattern for "move the viewport to an absolute index, exit tail, recenter."

## Component 1: the `internal/blocks` package (neutral, no TUI deps)

Types:

```go
package blocks

// Line is the neutral input: the plain (ANSI-stripped) text of one row, plus
// whether it is a render-block row (pretty-printed JSON/XML), which is always
// a continuation regardless of leading whitespace.
type Line struct {
	Text          string
	IsRenderBlock bool
}

// ExceptionInfo is the exception processor's annotation.
type ExceptionInfo struct {
	Language string // best-guess language id ("java", "python", …) or "" if unsure
}

// Block is a contiguous run of lines [Start, End] (inclusive indices into the
// Line slice it was segmented from), plus processor annotations.
type Block struct {
	Start, End int
	Exception  *ExceptionInfo
}

// Processed reports whether any processor matched this block. v1: exception only.
func (b Block) Processed() bool { return b.Exception != nil }
```

Functions:

```go
// IsContinuation reports whether ln continues the preceding block: a render-block
// row, leading whitespace, OR a continuation signature (see below). This is the
// segmenter's predicate — distinct from the TUI's whitespace-only isContinuation.
func IsContinuation(ln Line) bool

// Segment groups lines into blocks (a block = a head row followed by zero or
// more continuation rows) and runs every processor to annotate them. Pure /
// full recompute.
func Segment(lines []Line) []Block

// Annotate runs the processors on a single (possibly still-growing) block.
// Used by the TUI's incremental path; Segment calls it internally.
func Annotate(b *Block, lines []Line)
```

Processor seam:

```go
// Processor annotates a block in place. Processors MUST NOT change block
// boundaries (Start/End) — segmentation is a separate, single concern.
type Processor interface {
	Process(b *Block, lines []Line)
}

// processors is the fixed v1 set. No config-driven registry.
var processors = []Processor{exceptionProcessor{}}
```

### Segmentation rules

- A **head** is any line for which `IsContinuation` is false. A block runs from a
  head through all immediately-following continuation lines.
- **Continuation = whitespace OR signature.** The whitespace half is the shared
  primitive (see "Two continuations" below). The signature half matches a small,
  anchored set of language-neutral patterns, finalized by research during
  planning. Provisional set (deliberately conservative to avoid false positives):
  - `Caused by:` (line start) — Java/Kotlin chained causes
  - `... ` + digits + ` more` — Java elided frames
  - `#` + digits + ` ` (line start) — PHP stack frames
  - `goroutine ` (line start) — Go traces
  - **NOT** bare `at ` — Java frames are tab-indented (already whitespace
    continuations); as a non-indented signature it is redundant and a
    false-positive magnet ("at 10:00 the server started…").
- A leading continuation line with no preceding head (e.g. the very first line in
  the buffer starts with a space) forms a degenerate single-line block.

### Exception processor (provisional signatures)

`exceptionProcessor.Process` inspects a block's lines for per-language markers
and, on a match, sets `b.Exception = &ExceptionInfo{Language: …}`. The concrete
per-language signature table (Python `Traceback (most recent call last):`, Java
`…Exception` + `\tat `, Go `panic:` / `goroutine`, Node `at <fn> (file:line)`,
Rust `thread '…' panicked at`, PHP `#0 `, C/C++ `#<n> 0x…` / AddressSanitizer,
Kotlin `Exception` + `\tat `, TS same as JS) is **provisional**: the
`gsd-phase-researcher`-style research pass during planning produces the verified
table and states, per language, what detection and grouping actually achieve.

## Component 2: the block cache in the model

The TUI holds `m.blocks []blocks.Block` plus `m.blocksDirty bool`.

- **One recompute path:** `m.ensureBlocks()` rebuilds `m.blocks =
  blocks.Segment(m.blockLines())` when `blocksDirty`, then clears the flag.
  `blockLines()` adapts `m.lines` → `[]blocks.Line{ Text: stripANSI(dl.body),
  IsRenderBlock: dl.isBlock }`.
- **Dirty is set by every `m.lines` mutator:** `appendEvent`, `trimToCap`,
  `reRenderAll`, `applyReload`. Missing one renders stale bars / jumps to dead
  targets, so all four set it.
- `ensureBlocks()` is called at the top of `renderStream` and before any block
  navigation, so the cache is current wherever it is read.
- **Performance:** `Segment` is a single O(n) pass over `m.lines`. Recompute
  happens at most once per Update cycle (dirty-gated), not per glyph. If
  high-throughput streaming makes the full rebuild hot, incremental
  append-time segmentation is the documented future optimization — correctness
  (one rebuild path) is preferred for v1.

### Two definitions of "continuation" (deliberate)

`isContinuation` (TUI, whitespace-only) drives `m` collapse and the `[...]`
marker. `blocks.IsContinuation` (whitespace **+** signatures) drives
segmentation and the bar. They diverge only on signature-only lines (e.g.
`Caused by:`): collapse treats it as a head and shows it; the block bar treats
it as a continuation. This is intentional. To keep them honest, the
whitespace test is a **single shared primitive** (`blocks.IsWhitespaceCont`)
that both call; signatures are layered only inside `blocks.IsContinuation`. The
spec does **not** change collapse — it documents the seam.

## Component 3: `renderException` render plugin (left bar)

When enabled (default on) and a visible row belongs to a block with
`Exception != nil`, prepend a **column-0 colored bar** to the row.

- **Glyph:** a styled `▌` (left half-block) in an alert color, followed by one
  space — rendered at the very left edge (column 0) on **every** row of the
  block, so the bar reads as one contiguous left edge. Column 0 is the only
  position consistent across the row types (head rows carry a `[group] file:`
  prefix, `isBlock` rows do not), per the baseline. Non-bar rows of an
  unflagged block, and all rows when the toggle is off, render unchanged.
- **Width accounting (critical):** the bar prefix `▌ ` is exactly **2 display
  columns**. `renderDisplayLineCore` (or a wrapper used by `renderStream`) MUST
  add 2 to the returned `visW` for a barred row, so `clipLine` pads/clips
  against the true width. The bar is a width-bearing prefix, not a cosmetic
  string prepend. Under horizontal scroll the bar clips with the rest of the
  left edge (consistent with the existing prefix behavior).
- **Toggle:** a new action (`e`, default on). Off → no bars; blocks/meta/nav are
  unaffected.
- The language guess is stored in meta but **not** rendered in v1 (reserved for
  MCP / a future header-tag plugin).

The render-plugin seam for v1 is a single function the stream renderer consults
(`exceptionBar(idx) (glyph string, width int, ok bool)`); a general registry is
out of scope.

## Component 4: navigation (two key sets)

Four new keymap actions, all overridable, dispatched like the existing search-hit
nav (`unstickFromTail`, set `streamTop` to the target block's first **visible**
line, recenter):

- **Multi-line blocks:** `]` → next, `[` → previous. Targets only blocks
  spanning more than one row (`End > Start`); single-line log entries (each a
  degenerate block) are skipped, so this hops between the multi-row structures
  (stack traces, indented dumps, JSON/XML), exceptions or not.
- **Processed blocks:** `}` → next `Processed()` block, `{` → previous.

Both skip heads that are hidden by **group-disable or collapse**
(`lineEnabled`). Navigation **during an active search filter is best-effort**:
nav jumps `streamTop` to the block head, and `collectVisible` then clamps the
view to the nearest filtered row — so the landing may sit near, not exactly on,
a head that the filter would hide. This degrades gracefully (no crash, no layout
damage); a precise filter-aware nav is out of scope for v1. Wrap behavior
mirrors search nav (stop at the ends; no wrap-prompt for blocks — just clamp).
Navigation reads `m.blocks` via `ensureBlocks()`.

## Component 5: keymap actions + doc

Add to `internal/keymap` (all OS-independent defaults, all overridable):

| Action               | Name                  | Default | Title / Desc |
|----------------------|-----------------------|---------|--------------|
| `ActionNextBlock`    | `next_block`          | `]`     | "Next block" / "Jump to the next multi-line block." |
| `ActionPrevBlock`    | `prev_block`          | `[`     | "Previous block" / "Jump to the previous multi-line block." |
| `ActionNextMarkedBlock` | `next_marked_block` | `}`    | "Next marked block" / "Jump to the next processor-matched block." |
| `ActionPrevMarkedBlock` | `prev_marked_block` | `{`    | "Previous marked block" / "Jump to the previous processor-matched block." |
| `ActionToggleExceptionMarks` | `toggle_exception_marks` | `e` | "Toggle exception marks" / "Show/hide the exception left-bar." |

`AllActions` grows by 5; `actions_test.go` exact-count assertion updated;
`KEYBINDINGS.md` regenerated; `TestDocsUpToDate` guards it.

## Architecture / Files

- `internal/blocks/` (new package): `blocks.go` (types, `Segment`, `Annotate`,
  `IsContinuation`, `IsWhitespaceCont`, processor seam), `exception.go`
  (`exceptionProcessor` + per-language signature table), plus `_test.go` files.
- `internal/tui/app.go`: `m.blocks`, `m.blocksDirty`, `m.showExceptionMarks`
  fields; set dirty in the four mutators; `ensureBlocks`, `blockLines`,
  `exceptionBar`; dispatch the five actions; bar width folded into the stream
  render path.
- `internal/tui/blocks.go` (new, TUI side): block-nav helpers + the
  `exceptionBar`/width integration, to keep `app.go` from growing unwieldy.
- `internal/keymap/{actions,defaults}.go` + tests; `KEYBINDINGS.md`.
- `README.md`, `CHANGELOG.md`.

## Limitations (v1, stated honestly)

- **Multi-block exceptions still fragment** for some languages. Signatures fix
  Java `Caused by:` / `… more` and PHP `#0`, but a **Go panic interleaves
  non-indented function lines (`main.main()`) with indented `file:line` lines**,
  so it splits into ≥2 blocks regardless of the signature set. The per-language
  research states, per language, what grouping is actually achieved; the spec
  does not promise clean single-block grouping for all ten.
- Detection is heuristic (signature-based), not a parser — false
  positives/negatives are possible; the conservative signature set favors
  precision over recall.
- No IDs/copy/cursor (feature #1).

## Testing

- `internal/blocks` (pure, no TTY): `Segment` groups whitespace continuations;
  signature continuations (`Caused by:`, `#0 `, `goroutine `, `… more`) extend a
  block; bare `at ` does NOT (non-indented); a leading-whitespace first line is a
  degenerate block; the exception processor flags representative snippets per
  language and sets `Language`; `Processed()` reflects the annotation;
  processors never alter `Start`/`End`.
- `internal/tui`: `blockLines` adapts `m.lines` (ANSI stripped, `IsRenderBlock`
  set); `ensureBlocks` recomputes after each mutator (append/trim/reRender/
  reload) and not when clean; `exceptionBar` returns a width that, added to
  `visW`, keeps the rendered row ≤ `m.width` (regression guard against the
  wrap/ghost-row bug — assert no visible row exceeds `m.width`); the toggle
  hides bars; `]`/`[`/`}`/`{` move `streamTop` to the right block and skip
  group-disabled / filtered targets.
- Keymap: the five actions have the specified defaults; `TestDocsUpToDate`
  passes after regeneration.

## Conventions

Phase commits per repo convention, each leaving `go test ./...`, `go vet ./...`,
`go test -race ./...` green. Update `README.md` + `CHANGELOG.md` and regenerate
`KEYBINDINGS.md` on delivery. Planning begins with a per-language exception
research pass that finalizes the signature table.
