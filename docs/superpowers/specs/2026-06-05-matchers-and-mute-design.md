# Matchers & Mute — Design

**Date:** 2026-06-05
**Status:** Approved (pending spec review)

## Summary

Add a reusable **matcher** concept to log-listener: a named predicate over a
log line's content, the source file's basename, and its full path. Matchers
power two new capabilities:

1. **`mute`** — a config section that drops matching log lines before they
   reach any sink (stdout / SSE / TUI).
2. **Renderer matcher reference** — a renderer may use a named matcher instead
   of an inline `line_regex`; the matcher's `line_regex` supplies the template
   captures.

Matchers are defined once in a global `matchers:` library and referenced by
name, or written inline where a single matcher is needed.

## Motivation

- Suppress known-noisy lines (health checks, debug spam) without disabling a
  whole file or renderer.
- Reuse the same match rule across mute and renderers ("predefined regex").
- Avoid forcing users to write regex for simple cases — explicit literal keys
  give exact, fast matching with no regex engine cost.

## Config Schema

```yaml
# Reusable named matchers (global library).
matchers:
  idea-file:      { name: idea.log }                  # exact basename
  json-line:      { line_regex: '^\s*(\{.*\})\s*$' }  # regex with capture
  health-noise:   { line_regex: 'GET /health' }
  jetbrains-path: { path_regex: 'JetBrains' }

# Drop matching lines before they reach any sink/TUI.
mute:
  - id: drop-health               # optional identity, used in diagnostic messages
    matcher: health-noise         # reference a named matcher
  - id: silence-debug
    line: DEBUG                   # OR inline matcher fields
    applies_to: { groups: [app] } # optional scope: group ids + path globs (AND)

renderers:
  - name: idea-json
    matcher: json-line            # matcher's line_regex feeds captures ($1...)
    template: 'json($1)'
  - name: legacy
    line_regex: '^(\d+) (.*)$'    # existing form is unchanged
    template: '$1 -> $2'
```

### Matcher fields

A matcher matches over three dimensions. For each dimension, set **either** the
literal key **or** the regex key (never both). **At least one** dimension must
be set.

| Dimension | Literal key | Regex key   | Target                     |
|-----------|-------------|-------------|----------------------------|
| Content   | `line`      | `line_regex`| the raw log line           |
| File name | `name`      | `name_regex`| `filepath.Base(path)`      |
| File path | `path`      | `path_regex`| the full file path         |

## Semantics

- **Literal = exact equality.** `name` matches when the basename equals the
  value exactly; `path` when the full path equals the value; `line` when the
  entire log line equals the value. (`line` literal is rarely useful — use
  `line_regex` for substring/pattern matching.)
- **Regex = `regexp` search** (`MatchString`; `FindStringSubmatch` when
  captures are needed).
- **Multiple criteria within one matcher = AND.** All set dimensions must
  match. Consistent with the existing `applies_to` rule.
- **`mute` drops the line before rendering.** A muted line never becomes an
  `Event`, so it reaches no sink and no TUI. Mute is checked first in
  `Pipeline.Render`, taking precedence over every renderer and over
  `output.drop_unmatched`.
- A mute rule whose matcher sets only `name`/`path` (no content criterion)
  drops **all** lines from matching files.
- **`mute.applies_to`** scopes a rule by group id (`groups`) and path glob
  (`paths`), AND-combined, identical to renderer `applies_to`. A rule's
  `applies_to` and its matcher are themselves AND-combined: the rule mutes a
  line only when `applies_to` admits it **and** the matcher matches.
- **Renderer `matcher`** — the referenced matcher's `line_regex` supplies the
  template captures (`$1`, `$2`, ...). Any `name`/`path` criteria on the
  matcher additionally gate the renderer (a content-aware `applies_to`).

## Compilation & Validation (fail at config load)

- Unknown `matcher:` reference → error.
- Duplicate matcher name → error.
- Matcher with zero criteria → error.
- Both literal and regex set for the same dimension → error.
- Invalid regex in any `*_regex` field → error (with field context).
- Renderer must set **exactly one** of `line_regex` or `matcher` (not both,
  not neither).
- A matcher referenced by a renderer **must** have a `line_regex` (nothing to
  capture otherwise) → error.
- `mute` entry must set **exactly one** of `matcher:` (named reference) or
  inline matcher fields.
- A `mute` entry's identity key is `id` (not `name`) so it does not collide
  with the matcher's inline `name` (file-basename) field. `id` is optional.

## Architecture

New package **`internal/match`** owns the matcher type and logic — it is
cross-cutting (file name/path like `discover`, line content like `render`) and
small enough to test in isolation.

```go
// internal/match
type Spec struct {
    Line, LineRegex string
    Name, NameRegex string
    Path, PathRegex string
}

type Matcher struct { /* compiled literals + *regexp.Regexp per dimension */ }

func Compile(s Spec) (*Matcher, error)        // validates: >=1 dim, no dup lit+regex
func (m *Matcher) HasLineRegex() bool         // for renderer-capture validation
func (m *Matcher) Match(path, line string) (caps []string, ok bool)
```

`Match` returns `ok` per AND semantics; `caps` is the `line_regex`
submatch slice when a line regex is set (else `nil`).

### `render` package changes

- `Renderer` gains a matcher-backed construction path. When built from a
  matcher, the matcher's line regex provides `Match` captures and the
  name/path criteria fold into `Applies`/`Match`.
- New `MuteRule` type: `{ id string; matcher *match.Matcher; groups
  map[string]bool; pathGlobs []string }` with
  `Mutes(group, path, line string) bool`.
- `Pipeline` holds `mutes []*MuteRule`, checked at the top of `Render`:
  ```go
  for _, mr := range p.mutes {
      if mr.Mutes(group, path, raw) { return Event{}, false }
  }
  ```
- `NewPipeline` signature extends to accept the global matcher library and the
  mute specs (in addition to renderer specs + drop flag).

### `config` package changes

- `File` gains `Matchers map[string]MatcherSpec` and `Mute []MuteSpec`.
- `Renderer` gains `Matcher string`.
- `RendererSpec` carries the resolved matcher reference/spec; `Config` carries
  the matcher library and mute specs through `mergeYAMLInto`.
- Strict YAML decoding already rejects unknown keys — new keys are added to the
  schema structs.

### `cmd` changes

Thread the matcher library + mute specs into both `render.NewPipeline` call
sites (`main.go` startup and the reload `buildRuntime` seam).

### Data flow (unchanged choke point)

`watch` -> `Pipeline.Render(now, group, path, raw)` -> **mute check** -> first
-match-wins renderer (matcher- or line_regex-backed) -> `Event` -> sinks/TUI.

## Testing

- **`match`**: literal vs regex per dimension; AND across dimensions; exact
  equality vs regex search; basename derivation from path; zero-criteria and
  dup-literal+regex errors; capture extraction.
- **`render`**: mute drops a line in `Render`; mute precedence over
  `drop_unmatched`; renderer-via-matcher produces correct captures; matcher
  name/path criteria gate a renderer; `applies_to` scoping of mute.
- **`config`**: parse + validate `matchers` and `mute`; every error case above;
  round-trip through `mergeYAMLInto` into `Config`.

## Out of Scope (YAGNI)

- TUI live-toggle of mute rules.
- `exclude_regex` / age (`older`/`younger`) fields on matchers — those remain
  on file filters (discovery-time selection).
- Catalog/`init` emission of `matchers`/`mute` sections (can follow later).

## Conventions

Implementation follows the repo's phase convention: each phase ends with a
`phase N: <desc>` commit and a `phase N review fixes` commit, both leaving
`go test ./...`, `go vet ./...`, and `go test -race ./...` green.
