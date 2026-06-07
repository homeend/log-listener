# Preload — Seed the Buffer From a File — Design

**Date:** 2026-06-07
**Status:** Approved (pending spec review)

## Summary

Add `--preload` (and forced variants `--preload-raw` / `--preload-capture`) to
seed log-listener's buffer from a file before it starts tailing, so the TUI can
be driven with canned data — no live logs required. Two source formats:

- **raw** — arbitrary log lines, run through the renderer pipeline (tests
  renderers / JSON detection / block annotation), under a synthetic group.
- **capture** — a `screen-log-listener-*.txt` file saved by the `S` export,
  reconstructed faithfully *without* the pipeline (it is already rendered),
  recovering the original groups/files from the `[group] file:` prefixes.

This is a testing/dev convenience slotted in before the MCP server, and gives a
headless verification path (`--preload-capture x --once`) for both the user and
the agent.

## Goals / Non-goals

**Goals:** drive the TUI from a file; round-trip the `S` capture format
faithfully (groups, files, multi-line blocks, exception marks, nav, save all
work on the restored buffer); work with zero config; be testable headlessly.

**Non-goals (YAGNI):** editing/filtering during preload; a verbatim no-pipeline
mode for raw input; preloading from stdin; live-reloading the preload file;
mixing preload with config-reload semantics.

## Current Behavior (baseline)

- `internal/config/cli.go` is a hand-rolled, left-to-right arg parser. Adding
  flags = new `case` arms; `requireValue(args, i, name)` consumes a flag value.
  Order across flags is preserved by construction.
- `render.Event{ Ts time.Time; File, Group, Raw string; Rendered []Part }`.
  `Part{ Type string; Value any }`; a text part is `{Type:"text", Value:string}`.
- `render.Pipeline.Render(now, group, path, line) (Event, bool)` renders one raw
  line.
- TUI: `tui.New(Options{...})` seeds files/groups/renderers before `Run`.
  `model.appendEvent(ev)` decomposes an event into display rows (splitting the
  text part on `\n` → head + dim block rows) and flags blocks dirty.
- `model.snapshotScrollback() []string` (from the save feature) renders the
  whole buffer as plain text: head rows → `[group] file: body`, block rows →
  `body` (no prefix). This is exactly the capture format the importer parses.
- `runOnce` emits events to the stdout sink (+SSE) and exits; `runWatch` emits
  then tails; `runWatchTUI` seeds panels and pumps the watcher into `app.Push`.

## Component 1: CLI flags

Three repeatable, order-preserving flags, parsed in `cli.go` into a new
`Config.Preloads []PreloadSpec`:

```go
type PreloadMode int
const (
	PreloadAuto PreloadMode = iota
	PreloadRaw
	PreloadCapture
)
type PreloadSpec struct {
	Group string // synthetic group for raw mode ("" → "preload"); ignored for capture
	Path  string
	Mode  PreloadMode
}
```

- `--preload <[group=]path>` → `PreloadAuto`
- `--preload-raw <[group=]path>` → `PreloadRaw`
- `--preload-capture <path>` → `PreloadCapture` (a `group=` prefix is ignored)

Each `case` calls `requireValue` then appends a `PreloadSpec`. Value parsing
(`parsePreloadValue`):

- Split on the **first** `=`. Treat the left side as a group name **only if** it
  is non-empty and contains none of `/ \ :` (so `C:\x` and `api=C:\x` both parse
  correctly: the former is a bare path, the latter is group `api` + path
  `C:\x`). Otherwise the whole value is the path and the group is `""`.

## Component 2: mode resolution (filename-based auto-detect)

`PreloadAuto` resolves by **filename**, not content (content detection misfires
on common bracket-prefixed logs like `[2026-06-07 10:45:03] INFO: msg`):

```go
// ResolveMode maps Auto to Raw/Capture by the basename. The S export always
// writes screen-log-listener-<ts>.txt, so that prefix is the reliable signal.
func ResolveMode(m PreloadMode, path string) PreloadMode {
	if m != PreloadAuto {
		return m
	}
	if strings.HasPrefix(filepath.Base(path), "screen-log-listener-") {
		return PreloadCapture
	}
	return PreloadRaw
}
```

`main.go` resolves each spec's mode, prints a one-line announcement to stderr
(`log-listener: preload <path> → raw` / `→ capture`), then loads it.

## Component 3: `internal/preload` package (loader + capture parser)

A new neutral package (depends only on `internal/render`, stdlib).

```go
package preload

// RenderFunc renders one raw line for raw-mode preload (a closure over the
// pipeline, supplied by main).
type RenderFunc func(group, file, line string) (render.Event, bool)

// Load reads spec.Path and returns the events to seed, using mode (already
// resolved to Raw or Capture). Raw lines go through renderFn; capture lines are
// reconstructed directly. A read error is returned (callers exit non-zero).
func Load(spec PreloadSpec, mode PreloadMode, renderFn RenderFunc) ([]render.Event, error)

// ParseCapture reconstructs events from saved-capture lines, exported for the
// round-trip test. No pipeline involved.
func ParseCapture(lines []string) []render.Event
```

**Raw mode:** read the file line by line; for each, `renderFn(group, base,
line)` where `group = spec.Group or "preload"`, `base = filepath.Base(spec.Path)`;
keep events where `ok`.

**Capture mode (`ParseCapture`):** walk the lines, rebuilding one event per head:

- A line matching `^\[([^\]]*)\] (.+?): (.*)$` is a **head**: flush any open
  event, then open a new event with that group/file and `text = body`.
- Any other line is a **continuation** (no-prefix block row): append `"\n"+line`
  to the open event's text. If no event is open (a leading orphan), open one
  with group `""`, file `""`, text = the line.
- Flush the final event.

Each event is `render.Event{ Group, File, Raw: <text>, Rendered:
[]Part{{Type:"text", Value:<text>}} }`. When seeded, `appendEvent` splits the
text on `\n` → head row + dim block rows, reproducing the saved layout. Heads
whose body starts with whitespace (e.g. `    at Foo(Bar.java:1)` stack frames,
saved as separate prefixed events) parse as separate heads — block segmentation
regroups them and the exception processor re-flags them, so the `▌` bar, `]`/`}`
nav, and `S` save all work on the restored buffer.

Non-greedy `(.+?): ` stops at the **first** `": "`, so a body containing `": "`
survives intact (the only non-idempotent input is a *file basename* containing
`": "`, which is effectively impossible). Pretty-printed JSON/XML rows (no
prefix) are folded back as embedded-newline block rows — visually identical,
though their original part-type (json/xml vs text) is not preserved (acceptable:
this is a display round-trip, not a data round-trip).

## Component 4: main.go wiring (routing by mode)

After the pipeline is built, build `renderFn := func(g,f,l string) (render.Event,
bool) { return pipeline.Render(time.Now(), g, f, l) }`, then for each spec:
resolve mode, announce, `preload.Load(...)` (a load error → print + `return 1`).
Concatenate into `preloadEvents []render.Event` (flag order preserved). Derive
the distinct preload **groups** (first-seen order) and **files** `(group, file)`
from the events themselves.

Routing (preloads always precede live data):

- **TUI** (`runWatchTUI`): pass `preloadEvents` via the new
  `tui.Options.InitialEvents`; merge the preload groups into the `Groups` slice
  (dedup against config groups, append new ones) and the preload files into
  `InitialFiles`, so the groups panel, digit toggles, group column, and files
  overlay include them.
- **`--no-tui`** (`runWatch`): emit each preload event to the stdout sink (and
  SSE hub if present) before entering the tail loop.
- **`--once`** (`runOnce`): emit preload events first, then the existing
  once-scan, then exit. This is the **headless verification path**.

Capture events carry `Group`/`File` but no styled parts; the stdout sink adds its
own `[group] file:` prefix from those fields — added **once**, correct (so
`--preload-capture x --once` prints the recovered content with prefixes).

## Component 5: TUI `InitialEvents`

`tui.Options` gains `InitialEvents []render.Event`. In `New`, after seeding
files/groups/renderers, `for _, ev := range opts.InitialEvents { m.appendEvent(ev) }`.
`appendEvent` already flags blocks dirty, so segmentation/exception detection run
on first render. No new model field is required.

## Architecture / Files

- `internal/config/cli.go` — three flag cases + `parsePreloadValue`; `Config`
  gains `Preloads []PreloadSpec` (+ the `PreloadSpec`/`PreloadMode` types, in a
  new `internal/config/preload.go` or alongside).
- `internal/preload/preload.go` (new) — `Load`, `ParseCapture`, `ResolveMode`.
- `internal/preload/preload_test.go` (new).
- `internal/tui/app.go` — `Options.InitialEvents`; `New` seeds them.
- `internal/tui/preload_test.go` (new) — the round-trip idempotency test.
- `main.go` — build `renderFn`, load preloads, route by mode, announce on stderr.
- `e2e_test.go` (root) — `--preload-capture … --once` and `--preload … --once`.
- `README.md`, `CHANGELOG.md`.

## Testing

**Centerpiece — capture round-trip idempotency (`internal/tui`):** seed a model
with representative events (a single-line entry, a multi-line JSON event, and a
Java stack trace spread over several prefixed events), take
`cap := m.snapshotScrollback()`, reconstruct `evs := preload.ParseCapture(cap)`,
seed a fresh model with `evs`, and assert
`m2.snapshotScrollback()` **equals** `cap` (fixed point). Then `m2.ensureBlocks()`
and assert a stack-frame row reports `inExceptionBlock(idx) == true` — proving
groups, leading whitespace, multi-line structure, and exception flagging all
survive the round trip.

**Unit (`internal/preload`):** `ParseCapture` splits heads vs continuations,
recovers multiple groups from one capture, folds no-prefix rows into the
preceding event, and handles a leading orphan; `ResolveMode` maps Auto by the
`screen-log-listener-` basename prefix; raw `Load` runs lines through `renderFn`.

**Unit (`internal/config`):** `parsePreloadValue` splits `api=path`, leaves
`C:\x` and `api=C:\x` correct, defaults the group to `""`; the three flags
append specs with the right `Mode` in command-line order.

**E2E (root):** `--preload <raw.log> --once` prints rendered lines and exits;
`--preload-capture <capture.txt> --once` prints the reconstructed lines and
exits (isolated from ambient config like the other e2e tests).

## Conventions

Phase commits per repo convention, each leaving `go test ./...`, `go vet ./...`,
`go test -race ./...` green. Update `README.md` + `CHANGELOG.md` on delivery
(this feature adds no keybindings, so `KEYBINDINGS.md` is unaffected).
