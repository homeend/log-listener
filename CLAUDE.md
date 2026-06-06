# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build, test, run

```bash
make build         # local binary
make build-static  # CGO_ENABLED=0 static binary
make test          # go test ./...
make vet           # go vet ./...
make race          # go test -race ./...
```

Run a single package's tests: `go test ./internal/<pkg>/...`
Run a single test: `go test -run TestName ./internal/<pkg>/`

## Architecture (big picture)

`log-listener` is a Go (1.26) CLI tool that tails multiple log sources in
real time with a renderer pipeline, an SSE broadcast, and an optional TUI.
Authoritative design + per-phase history lives in `PLAN.md` and `CHANGELOG.md`.

### Module map

| Package                    | Role                                                          |
|----------------------------|---------------------------------------------------------------|
| `internal/timeparse`       | ISO 8601 + relative duration → `time.Time`.                   |
| `internal/discover`        | Directory walk + glob, FileFilter, first-match-wins assign.   |
| `internal/watch`           | fsnotify Watcher + per-file Tailer (rotation/truncation).     |
| `internal/config`          | CLI + YAML parser, `Config.Load`, CLI-precedence merge.       |
| `internal/render`          | Template DSL parser, JSON/XML, Pipeline (first-match-wins).   |
| `internal/sink`            | Colorized stdout + SSE hub.                                   |
| `internal/tui`             | bubbletea app: streaming view + Ctrl+I file overlay.          |
| `cmd/log-listener`         | Entry point; wires config → discover → watch → pipeline → sinks/TUI. |

### Data flow

`config.Load` → `discover.Assign` (first-match-wins file → group) →
`watch.Watcher` (fsnotify → per-file Tailer) → raw line +
`render.Pipeline.Render` (first-match-wins renderer) → `render.Event` →
fanout to `sink.Stdout` and/or `sink.SSEHub`, OR to `tui.App.Push` when TUI
is active.

### Locked design rules

- **First-match-wins, everywhere**: file → group assignment, and
  line → renderer matching, both use declaration order. A renderer matches
  only if its `line_regex` matches AND its `json()`/`xml()` render-calls
  actually parse; a parse failure makes the renderer fall through to the next
  one (or to raw / drop), so non-JSON `{…}` is never mangled.
- **Directory `regex` matches filenames, not log lines**. Line content
  matching is the renderer's job.
- **Lines that match no renderer** are emitted as-is unless
  `output.drop_unmatched: true`.
- **`applies_to` is AND** of `groups` and `paths` (empty = vacuously true).
- **Render-function output starts on its own line** (JSON/XML blocks always
  emitted as separate output blocks).
- **TUI off when stdout isn't a TTY**; same for color.
- **Single static binary** — only deps: `fsnotify`, `yaml.v3`, `bubbletea`,
  `lipgloss`.

## Conventions

- Each phase from `PLAN.md` ends with two commits: `phase N: <desc>` for the
  implementation and `phase N review fixes` for the post-commit code review
  fixes. Both must leave `go test ./...`, `go vet ./...`, `go test -race ./...`
  green.
- `.claude/settings.local.json` is intentionally tracked — do NOT add
  `.claude/` to `.gitignore`. The file persists permission grants between
  sessions.
- `internal/`-only code; no exported library surface yet.
