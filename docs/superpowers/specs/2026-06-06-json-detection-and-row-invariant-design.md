# Validity-Based JSON/XML Detection + Row Invariant — Design

**Date:** 2026-06-06
**Status:** Approved (conversationally); pending spec review

## Problem

The bundled `idea-trailing-json` renderer

```yaml
line_regex: '^(.*?\s)(\{.+\})\s*$'
template:   '$1\njson($2)'
```

"detects JSON" by a regex seeing `{…}` at end of line — it never checks the
braces are valid JSON. IntelliJ logs lines like
`… Saved path macros: {DB_ARTIFACTS_BUNDLE=C:\…\artifacts}` (a single physical
line; `{KEY=value}` is **not** JSON). The renderer matches anyway, the template
forces a `\n`, `json($2)` fails to parse and **silently falls back to a text
part**. The result is two text parts `"$1\n"` + `"{…}"`, which collapse into one
`displayLine.body` carrying an embedded `\n`.

That single body then spans multiple terminal rows, violating the TUI invariant
"**one `displayLine` = one terminal row**". Observed symptoms (all the same root
cause):
- header/top bar scrolls off (vertical overflow — `collectVisible` row count and
  `contentHeight` windowing are wrong),
- the line wraps at `horizScroll=0` (`clipLine` emits the `\n`),
- the extra line disappears when scrolled right (`clipANSIWindow` drops the `\n`
  once it's left of the window — verified),
- exception lines that end in `{…}` hit the identical path ("additional new
  lines").

## Two independent fixes

### Fix A — validity-based detection (renderer falls through on parse failure)

You cannot validate JSON with a regex, so detection must be by actual parse
success. **When a renderer's `json($N)`/`xml($N)` render-call cannot parse its
capture, the renderer does not match** — the pipeline continues to the next
renderer, and ultimately to raw passthrough. Genuine JSON still renders as a
block; `{KEY=value}` and brace-ending exception messages render as the original
single line.

Changes:
- `render.renderJSON` / `render.renderXML` return `(Part, bool)` — `ok=false`
  on parse failure (empty input is `ok=true` with an empty text part).
- `Template.Execute` returns `([]Part, bool)`. It returns `false` as soon as any
  `partRenderJSON`/`partRenderXML` call reports `ok=false`; otherwise `true`.
  Plain text/capture templates always return `true`.
- `Pipeline.Render`: after `caps := r.Match(path, raw)` succeeds,
  `parts, ok := r.template.Execute(caps); if !ok { continue }`. Only on `ok`
  does the renderer win (`ev.Rendered = parts`).

Behavioral consequences:
- A line whose regex matched a renderer but whose render-call failed now counts
  as **unmatched**: it is emitted raw, or dropped when `output.drop_unmatched`
  is true. (Previously it showed a fallback text part inside the "winning"
  renderer.) Output is still never silently mangled — it shows as the source
  line.
- This refines the LOCKED rule "first-match-wins": a renderer matches only if
  its `line_regex` matches **and** its render-calls parse. `CLAUDE.md` and the
  README renderer docs are updated to state this.

### Fix B — row invariant (decompose stores a list of lines)

Independently, `decomposeEvent` must never leave a `\n` inside a
`displayLine.body`, so even a user-authored multi-line text template (e.g.
`'$1\n$2'`) can't break the grid. A multi-line render is **stored as a list of
lines (a block/array)**, not a text blob — which is what the TUI's
`scrollbackEvent.lines []displayLine` model is for.

Change (in `decomposeEvent`): after building the head `text`
(`TrimRight(…, "\n")`), split it on `\n`:
- segment 0 → the **head** `displayLine` (carries the `[group] file:` prefix),
- segments 1..n → **block** `displayLine`s (`isBlock: true`, dim-styled, no
  prefix) — identical storage/rendering to JSON/XML block lines, so a
  `'$1\n$2'` template stores as `["$1" (head), "$2" (block)]`.

`Split` returns one element when there is no `\n`, so this is a **no-op for every
normal line**; it only changes behavior when a body would otherwise contain a
newline.

**Why decompose, not the `render.Part` layer:** `render.Event` is serialized
verbatim into the SSE JSON stream, so changing `Part.Value` from `string` to
`[]string` would break the SSE wire schema and its clients. stdout/SSE are
streams (a `\n` renders fine there), so the grid-specific "list of lines"
storage belongs in the TUI's `decomposeEvent`; the render layer keeps emitting
parts unchanged.

## How A and B interact

A removes the catalog's only source of embedded `\n` (non-JSON braces now fall
through instead of producing fallback-text). B is defense-in-depth for
user-authored templates that use a literal `\n`. Both are small and independent;
B can land first (its failing test already exists).

## Files

- `internal/render/json.go`, `internal/render/xml.go` — `(Part, bool)` returns.
- `internal/render/template.go` — `Execute` returns `([]Part, bool)`.
- `internal/render/pipeline.go` — `Render` skips a renderer when `Execute`
  returns `ok=false`.
- `internal/render/render_test.go` — update `Execute` call sites to the
  two-value form; the "invalid json/xml falls back to text" unit tests become
  "Execute reports ok=false", plus a pipeline test that a regex-matching but
  unparseable render-call falls through to raw.
- `internal/tui/app.go` — `decomposeEvent` splits the head text on `\n`.
- `internal/tui/multiline_test.go` — `TestDecomposeNeverLeavesEmbeddedNewline`
  (already written) + a `View()` symptom test (exactly `height` rows, header
  present).
- `cmd/log-listener/e2e_test.go` — e2e: a non-JSON `{…}` line renders as one raw
  line (no extra row), and a valid trailing-JSON line still pretty-prints.
- `CLAUDE.md`, `README.md`, `CHANGELOG.md` — document the refined match
  semantics and the fixes.

## Testing

- **render**: `renderJSON`/`renderXML` ok flag; `Execute` ok=false on
  unparseable json/xml call, ok=true otherwise; `Pipeline.Render` falls through
  to the next renderer / raw when a render-call fails; valid JSON still wins and
  renders a block; `drop_unmatched=true` drops a regex-matched-but-unparseable
  line.
- **tui**: no `displayLine.body` contains `\n`; `View()` is exactly `height`
  rows with the header present for an embedded-`\n` event; normal lines
  unaffected.
- **e2e**: the IntelliJ macro line renders as the single source line.

## Out of Scope

- The parked horizontal "center the hit" tweak (separate, resume after).
- Changing the SSE wire schema / `render.Part` representation.

## Conventions

Phase commits (`phase N: <desc>` + review fixes), each leaving
`go test ./...`, `go vet ./...`, `go test -race ./...` green.
