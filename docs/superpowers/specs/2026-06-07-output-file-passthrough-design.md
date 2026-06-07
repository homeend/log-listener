# Output-File Passthrough (`-o`) — Design

**Date:** 2026-06-07
**Status:** Approved (pending spec review)
**Branch:** new feature branch off `main`.

## Summary

Add an optional `-o <file>` / `--output <file>` flag. When set, every rendered
line is written to that file in the **same plain-text format `sink.Stdout`
produces when output is not a TTY** — `[<group>] <basename>: <text>` plus any
indented JSON / pre-formatted XML blocks, with no ANSI color. The file receives
these lines in **all run modes**: `--once`, headless `--no-tui`, and interactive
TUI. This lets a user watch logs live in the TUI while persisting a clean,
greppable capture to disk.

The capture is a *passthrough*: it carries exactly the lines that would be
displayed (the renderer pipeline's output, honoring `drop_unmatched`), including
the preload-seeded lines, so the file matches what the user sees on screen.

## Goals / Non-goals

**Goals:**
- `-o <file>` / `--output <file>` CLI flag → `Config.OutputFile`.
- A file sink that reuses the existing `Stdout` plain (no-color) formatting.
- Writes in every mode: `--once`, `--no-tui`, and TUI.
- Truncate (overwrite) the file at startup.
- Plain text only — never ANSI codes, regardless of `--no-color` / TTY state.
- Preload events are written too (they are displayed lines).
- Open failure is fatal (stderr message + exit code 1).
- Unit + integration tests.

**Non-goals (YAGNI):**
- No YAML `output.file` field — CLI-only, mirroring the `--mcp` precedent.
- No append mode, no rotation, no size cap, no timestamps-added-by-us
  (the rendered line already carries whatever the renderer emitted).
- No buffering layer / explicit flush tuning — `*os.File` writes go straight to
  the OS, which is what a `tail -f` reader needs.
- No color-in-file option.
- No generalized `[]Emitter` fanout refactor.

## Current baseline

- `sink.Stdout` (`internal/sink/stdout.go`) formats a `render.Event` as
  `[<group>] <basename>: <text>` followed by JSON (2-space indent) / XML blocks.
  With `color=false` it emits no ANSI codes. `Stdout.Close()` is a deliberate
  no-op ("doesn't own the writer").
- `run()` (`main.go`) builds `stdoutSink := sink.NewStdout(stdout, useColor)`
  and an optional `sseHub`, then dispatches to `runOnce` / `runWatch` /
  `runWatchTUI`.
- Events fan out from a **single goroutine** in every path:
  - `runOnce` — sequential.
  - `runWatch` — the `select` loop calls `emit(...)`.
  - `runWatchTUI` — the pump goroutine renders, `app.Push`es, and `sseHub.Emit`s.
  So a file sink wired alongside needs **no internal locking**.
- Preload events: in `runWatch` they are emitted to `stdoutSink` (and `sseHub`)
  before the loop. In `runWatchTUI` they seed the model via `InitialEvents`
  (not pushed through the pump). `runOnce` receives them as `preloadEvents`.

## Component 1: CLI flag

In `internal/config`:

- Add field to `Config`:
  ```go
  OutputFile string // -o/--output; "" = no file capture. CLI-only (no YAML).
  ```
- In `ParseArgs`, add two switch cases (value required, via the existing
  `requireValue` helper, same shape as `--config`):
  ```go
  case a == "-o" || a == "--output":
      v, next, err := requireValue(args, i, a)
      if err != nil {
          return nil, err
      }
      cfg.OutputFile = v
      cfg.cliExplicit["output"] = true
      i = next
  ```
  No `Validate` change — an unwritable/invalid path surfaces at open time as a
  fatal error (Component 3). No YAML merge entry (CLI-only by design).

## Component 2: `sink.FileSink`

New file `internal/sink/file.go`:

```go
// FileSink writes rendered events to a file in the same plain-text format as a
// non-TTY Stdout sink ([group] basename: text + indented JSON/XML blocks, no
// ANSI color). It owns the underlying file and is the only sink that closes its
// writer. Not safe for concurrent Emit; callers fan out from a single goroutine.
type FileSink struct {
    f     *os.File
    inner *Stdout
}

// OpenFile creates (or truncates) path and returns a FileSink writing to it.
// The file is opened O_CREATE|O_WRONLY|O_TRUNC (0o644): each run starts fresh.
func OpenFile(path string) (*FileSink, error) {
    f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
    if err != nil {
        return nil, err
    }
    return &FileSink{f: f, inner: NewStdout(f, false)}, nil
}

// Emit writes the event in plain (no-color) format. Delegates to the embedded
// Stdout so the on-disk format always matches non-TTY stdout exactly.
func (s *FileSink) Emit(ev render.Event) { s.inner.Emit(ev) }

// Close closes the underlying file (FileSink owns it, unlike Stdout).
func (s *FileSink) Close() error { return s.f.Close() }
```

Composition (not a new formatter) keeps the on-disk format DRY-identical to
non-TTY stdout: any future change to `Stdout.Emit` flows through automatically.

## Component 3: wiring in `main.go`

In `run()`, after the `sseHub` block and before the `cfg.Once` dispatch:

```go
var fileSink *sink.FileSink
if cfg.OutputFile != "" {
    fileSink, err = sink.OpenFile(cfg.OutputFile)
    if err != nil {
        fmt.Fprintln(stderr, "log-listener: output:", err)
        return 1
    }
    defer fileSink.Close()
}
```

Thread `fileSink` (possibly nil) into all three run paths:

- **`emit(...)`** gains a `fileSink *sink.FileSink` param; after `stdoutSink.Emit(ev)`:
  ```go
  if fileSink != nil {
      fileSink.Emit(ev)
  }
  ```
- **`runOnce`** gains `fileSink`; emits each event to it alongside `stdoutSink`.
- **`runWatch`** gains `fileSink`; passes it to `emit(...)`, and emits each
  **preload** event to it in the preload loop (alongside `stdoutSink`).
- **`runWatchTUI`** gains `fileSink`; in the pump goroutine, after
  `app.Push(rev)` / `sseHub.Emit(rev)`:
  ```go
  if fileSink != nil {
      fileSink.Emit(rev)
  }
  ```
  and, **before** `tui.New`/program start, writes the preload events to the file
  so the captured file includes the TUI's seeded scrollback:
  ```go
  if fileSink != nil {
      for _, ev := range preloadEvents {
          fileSink.Emit(ev)
      }
  }
  ```

Because `FileSink` is an independent sink, it fires in every mode without any
mode-specific conditional beyond the nil check.

### Why preload events go to the file in TUI mode

In TUI mode preload events are *displayed* (they seed the scrollback the user
scrolls through) but are not pushed through the pump. Writing them to the file
explicitly keeps the capture faithful to "all lines as displayed." Order: preload
lines first (as on screen, oldest-first), then live lines — matching `runWatch`,
where the preload loop already precedes live emission.

## Architecture / files

- `internal/config/cli.go` — `Config.OutputFile`; `-o`/`--output` parse cases.
- `internal/sink/file.go` (new) — `FileSink`, `OpenFile`.
- `internal/sink/file_test.go` (new) — unit tests.
- `main.go` — open `FileSink`; thread through `emit`, `runOnce`, `runWatch`,
  `runWatchTUI`; preload writes in `runWatch` (existing loop) + `runWatchTUI`.
- `main_test.go` (or existing CLI/integration test file) — integration coverage.
- `README.md`, `CHANGELOG.md` — document `-o`/`--output`.

## Testing strategy

**Unit (`internal/sink/file_test.go`):**
- `OpenFile` truncates an existing non-empty file (pre-write content, open,
  assert file is empty before any Emit).
- `Emit` writes the exact non-TTY format for a text-only event
  (`[grp] base.log: hello\n`).
- `Emit` writes a JSON block on its own line for an event with a `json` part
  (indented, no ANSI).
- `Close` closes the file (a subsequent `Emit` would now fail — assert `Close`
  returns nil and the bytes are flushed to disk).
- Cross-check: a `FileSink` and a `NewStdout(buf, false)` produce byte-identical
  output for the same event (guards the "matches non-TTY stdout" contract).

**CLI (`internal/config`):**
- `ParseArgs([]string{"-o", "out.log", ...})` sets `OutputFile == "out.log"`.
- `--output out.log` sets the same.
- `-o` with no following value returns the `requireValue` error.

**Integration (`main_test.go`-style, via `run(args, stdout, stderr)`):**
- Headless (`--no-tui --no-color -o <tmp> --once` over a fixture, or a short
  watch): after the run, the temp file contains the expected plain lines and no
  ANSI escapes.
- Truncation: pre-fill the target file, run, assert old content is gone.
- (If a TUI/PTY harness is convenient) a `-o` run under the existing non-TTY
  path already exercises the all-modes wiring; a PTY test is optional, not
  required, since the sink is mode-independent.

Each phase commit leaves `go test ./...`, `go vet ./...`, `go test -race ./...`
green.

## Conventions

Phase commits per repo convention. Update `README.md` + `CHANGELOG.md` on
delivery (and no generated docs are affected — `KEYBINDINGS.md` is unrelated).
