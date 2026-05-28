# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to phased delivery per `PLAN.md`.

## [Unreleased]

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
