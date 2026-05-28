# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to phased delivery per `PLAN.md`.

## [Unreleased]

### Phase 2 review fixes
- YAML decoding is now strict (`yaml.Decoder.KnownFields(true)`) so typos in
  YAML keys produce errors instead of silently being ignored.
- Duplicate group ids inside YAML (`directories`/`files`) are now an error.
- `output.sse.enabled: true` without an explicit `addr` defaults to
  `127.0.0.1:8080` per PLAN.md.
- Removed dead `cliExplicit["global_filter"]` write; merge already
  branches on `GlobalFilter == nil`.

### Phase 2 — YAML config + merge
- `internal/config/yaml.go`: full YAML schema (directories, files,
  global_file_filter, renderers, output, tui) with `gopkg.in/yaml.v3`.
- `internal/config.Load`: resolves YAML path (`--config` > `./log-listener.yml`
  > `~/.log-listener.yml`), parses it, merges into the CLI Config with
  CLI-precedence semantics, and validates the result.
- `Config.Validate`: extracted from the old `validate()` so CLI parsing no
  longer fails on "no groups" — that check now runs after the YAML merge.
- `Config.cliExplicit`: tracks which scalar fields the CLI set so YAML
  doesn't clobber them. Group merge: same `(kind, id)` → CLI wins; YAML
  groups with unique IDs are appended in YAML declaration order.
- `Config` gains `DropUnmatched`, `TUIScrollback`, and `RendererSpecs`
  fields. `RendererSpecs` is only carried through for now; Phase 3 will
  compile them into the rendering pipeline.
- `cmd/log-listener/main.go`: now calls `config.Load` instead of
  `config.ParseArgs` directly.
- Adds dependency: `gopkg.in/yaml.v3 v3.0.1`.

### Phase 1 review fixes
- `cmd/log-listener`: signal handler now keeps listening for SIGINT
  indefinitely so a second Ctrl+C always hard-exits (previously the
  goroutine returned after 2s, leaving the process unkillable via Ctrl+C
  since `signal.Notify` had suppressed the default handler).
- `cmd/log-listener`: shutdown drain loop now also reads from
  `Watcher.Errors()` so late errors aren't dropped.
- `internal/watch`: `tickAll` snapshots the tailer map under the lock and
  ticks outside the lock, so a slow consumer on `Events()` can no longer
  stall `Add`/`WatchDir`/`Close`.
- Workflow: `PLAN.md` now documents the review-after-each-phase loop.

### Phase 1 — Core CLI + raw tailing
- `internal/timeparse`: parses ISO 8601 dates and relative durations
  (`30s`, `15m`, `1h`, `2d`, `1w`) into a `time.Time` anchor.
- `internal/discover`: directory walk + glob expansion + `FileFilter`
  (name regex, exclude regex, mtime older/younger) + first-match-wins
  group assignment.
- `internal/watch`: per-file `Tailer` with line-buffer + rotation/truncation
  detection (inode change + size decrease); `Watcher` wraps fsnotify and
  dispatches Tailer ticks on directory events; matches newly-created files
  via a user-supplied `NewFileMatcher`.
- `internal/config`: CLI parser for `-d`/`-dN`, `-r`/`-rN`, `-R`, `-f`/`-fN`,
  `--once`, `--no-tui`, `--no-color`, `--sse`, `--config`. Numbered flags
  pair by ID; `-r1` configures the filter for `-d1`'s directory group.
- `cmd/log-listener`: wires config → discover → watch → stdout. Output
  format: `[<group>] <basename>: <line>`. `--once` mode reads existing
  files via `bufio.Scanner` and exits. Live mode uses fsnotify for
  real-time tailing with a 500ms poll safety net. Graceful SIGINT with
  200ms drain window; double-SIGINT hard exit (130).
- Added dependency: `github.com/fsnotify/fsnotify v1.10.1`.

### Phase 0 — Project scaffolding
- Initial `PLAN.md` with locked design decisions (first-match-wins, AND
  semantics for `applies_to`, renderers run in `--once`, 10k default TUI
  scrollback).
- `CLAUDE.md` for future Claude Code sessions.
- `.gitignore` and `CHANGELOG.md`.
