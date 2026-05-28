# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to phased delivery per `PLAN.md`.

## [Unreleased]

### Phase 6 review fixes
- `Makefile` `run` target replaced with `demo`, which creates a tempdir
  and seeds it with a JSON-tail line so `make demo` is a self-contained
  smoke test (the old `run` pointed at `/var/log/app-a` which doesn't
  exist on dev boxes).
- README clarifies that `-extldflags "-static"` is Linux-only — on
  macOS the binary is CGO-free but not "fully static."

## [Unreleased]

### TUI: per-group toggling, column hide/show, groups panel
- Digit keys `1`–`9` toggle the N-th declared group on/off in the
  stream view. Disabled groups' events stay in scrollback (and are
  still emitted to stdout / SSE — filtering is TUI-only), but are
  skipped while painting. Toggle again to re-show them in their
  original positions.
- **Ctrl+P** toggles the `[group]` prefix column; **Ctrl+L** toggles
  the `basename:` column. Both default on; either or both can be off.
  Toggles are instant — the prefix is composed at render time, the
  pre-rendered scrollback never rebuilds.
- **Ctrl+G** opens a new "Groups" overlay (mirrors Tab/Ctrl+I "Files"):
  lists every defined group with its digit key, `ON`/`OFF` state, and
  count of files currently assigned. Esc closes either overlay; opening
  one closes the other.
- Footer status now reports `groups: N (M off)` and `-G`/`-F` markers
  when columns are hidden.

Internals: `m.events` is now `[]displayLine` (group + file + body +
isBlock), built from `render.Event` via the new `decomposeEvent`. The
styled prefix is added on the fly in `renderDisplayLine`. The visible-
window walk skips disabled-group lines so a long run of hidden events
doesn't leave gaps. `tui.New` gained a `groupIDs []string` argument,
passed from `cfg.Groups` in `cmd/log-listener/main.go`.

### Performance — four hot-path fixes to keep the CPU quiet
Reported symptom: 13 watched files at ~10–30 lines/sec was spinning up
laptop fans. Root cause was mostly GC pressure from idle polling.

- **`Tailer.readAvailable` no longer allocates 32 KiB per call.**
  The read buffer now lives on the `Tailer` and is allocated once in
  `NewTailer`. Benchmark `BenchmarkTailerIdleTick`: 32 KiB / 3 allocs →
  272 B / 2 allocs (~120× less heap traffic). At 13 tailers × 2 polls
  /sec that's 832 KiB/sec → 7 KiB/sec saved on the idle path alone.
- **Default poll interval bumped 500 ms → 2 s.** The poll is only a
  safety net for fsnotify dropping an event; Linux inotify is reliable
  enough that 2 s wide is plenty. 4× fewer no-op `Stat`+`Read` syscalls
  per second across all tailers.
- **`SSEHub.Emit` skips `json.Marshal` when no clients are connected.**
  Process configured with `--sse` but no browser tab attached used to
  marshal every event for nobody — now it bails after a cheap
  `len(clients) == 0` check.
- **TUI `clipLine` fast-path at `horizScroll == 0`** — returns the
  styled line as-is and lets the terminal wrap. Removes the
  `stripANSI` regex from the common per-render hot path (~900
  matches/sec at 30 visible lines × 30 events/sec).

### Pattern-based directory matching with runtime new-dir detection
- `-d` and `directories: paths:` now accept glob patterns (`*`, `?`,
  `[abc]`) in any path segment — not just `-f`.
- At startup, each pattern is expanded to all currently-matching
  directories. Missing matches are not an error (literal-path typos
  still are).
- At runtime, the watcher monitors each pattern's *literal prefix* and
  any directory the pattern could lead to. New directories are picked
  up via fsnotify Create events and recursively scanned; matching files
  inside them start tailing immediately. Multi-hop create chains are
  cascaded (e.g. `/tmp/acp-*/sub`: new `acp-NEW/`, then `sub/`, then
  `file.log` — all three trigger).
- Same dynamic behaviour applies to `-f` glob paths whose parent
  directory doesn't exist yet (e.g. `-f /tmp/session-*/out.log`).
- New API: `watch.NewDirMatcher` + `Watcher.SetDirMatcher`. The matcher
  decides whether a newly-created directory is interesting enough to
  watch + scan.
- New helpers in `internal/discover`: `HasMeta`, `LiteralPrefix`,
  `MatchesPath`, `PrefixMatchesPattern`.
- README: removed the "no recursive subdir creation handling"
  limitation; added a "Pattern paths" section under Concepts.

### TUI tail mode + browse mode
- Replaced the offset-from-end `streamScroll` with a proper tail/browse
  state machine: `tailMode bool` + `streamTop int` (absolute index).
  When the user scrolls up, the TUI leaves tail mode and **locks the
  viewport at the absolute lines they're looking at**. New incoming
  events are still collected but do not shift the screen. Pressing
  **End** / `G` re-sticks to the tail; scrolling down past the bottom
  also re-sticks automatically.
- **Home / `g`** now jumps to the FIRST (oldest) line — it used to be
  the horizontal column-0 reset. Column 0 reset is now `0` only.
- **End / `G`** jumps to the latest line and re-enters tail mode.
- Footer status shows `tail` when pinned, or `@<top>/<total>` while
  browsing, so the mode is always visible.
- Scrollback ring buffer eviction now decrements `streamTop` so the
  browsing user's anchor stays valid even when oldest events get
  trimmed.

### TUI keybindings + horizontal scroll
- New bindings in the TUI:
  - **PgUp / Ctrl+B**, **PgDn / Ctrl+F / Space** — scroll one screen.
  - **← / h**, **→ / l** — pan view horizontally by 10 columns.
  - **Home / 0** — jump to column 0.
  - **End / $** — jump right to expose the widest line's tail.
- Horizontal pan strips ANSI styling on the visible window (single-pass
  regex strip) so scrolled long lines render as plain text without
  corrupted escape sequences.
- Documented the bubbletea-v1.3 startup delay (up to termenv.OSCTimeout
  = 5 s) in terminals that don't auto-respond to OSC 11. This was the
  root cause of "SSE isn't working" reports — SSE server starts only
  after main() runs, which happens after that init probe times out.

## [0.1.0] — 2026-05-28

First end-to-end release. Implements all six phases of `PLAN.md`. See
the per-phase entries below for details.

### Phase 6 — Polish
- `README.md`: usage, CLI reference, YAML schema, renderer DSL, SSE
  consumer example, TUI shortcuts.
- `log-listener.example.yml`: annotated example config covering every
  YAML key.
- `Makefile` with `build`, `build-static` (CGO_ENABLED=0, stripped),
  `test`, `vet`, `race`, `cover`, `clean`, `run` targets.
- `cmd/log-listener` top-of-file doc comment refreshed (no longer
  claims "Phase 1 surface").
- `CLAUDE.md` rewritten with the full module map and architecture so
  future Claude Code sessions can start productively.

### Phase 5 review fixes
- `App.Push` / `App.SetFiles` / `App.Quit` now release the mutex before
  calling `prog.Send()`. Previously a slow `Send` would hold the lock
  long enough to serialize other Push calls. The mutex now only guards
  the `done` check.

### Phase 5 — TUI
- `internal/tui`: bubbletea-based interactive UI. Streaming log view with
  a bounded ring-buffer scrollback (default 10k, configurable via
  `tui.scrollback` in YAML). Tab / Ctrl+I toggles the watched-files
  panel (both keys bound since terminals send byte 0x09 for both).
  Arrow keys / `j` / `k` scroll. `q` / Ctrl+C quit. Esc closes the
  files overlay.
- `cmd/log-listener` now picks TUI mode automatically when stdout is a
  TTY and `--no-tui` was not passed. In TUI mode the watcher events are
  pumped through the renderer pipeline by a background goroutine and
  delivered to the TUI via `app.Push`; SSE still runs in parallel.
- New dependencies: `github.com/charmbracelet/bubbletea`,
  `github.com/charmbracelet/lipgloss`.

### Phase 4 review fixes
- Simplified the color-detection block in `cmd/log-listener/main.go`:
  one boolean (`useColor`) is set per the `--no-color` flag and then
  forced off if stdout isn't a real TTY. The previous if/else-if had
  a redundant `!cfg.NoColor` re-check.

### Phase 4 — Color stdout + SSE
- `internal/sink/stdout.go`: colorized terminal output using bare ANSI SGR
  codes (no `fatih/color` dep). Color auto-disables when stdout isn't a
  TTY (`(*os.File).Stat().Mode() & os.ModeCharDevice`) or when the user
  passes `--no-color`.
- `internal/sink/sse.go`: HTTP/SSE hub. Single `GET /stream` endpoint
  serves the full `render.Event` as JSON per SSE message. Slow clients
  see drops (per-client 256-event buffer); the hub never blocks the Emit
  caller. 15-second keepalive comments defeat intermediary timeouts.
- `cmd/log-listener` now creates a `Stdout` sink and an `SSEHub` (if
  `cfg.SSEAddr != ""`), and routes every rendered event to both. The
  inline formatter in `main.go` is gone — its logic moved into
  `sink.Stdout.Emit`.
- `render.Event` and `render.Part` now carry JSON tags so the SSE
  payload matches the PLAN.md schema (`ts`, `file`, `group`, `raw`,
  `renderer`, `captures`, `rendered`).

### Phase 3 review fixes
- `emit()` no longer adds a second newline when the template already ends
  with `\n`. Output now has exactly one line break between the prefix line
  and the first JSON/XML block.
- `TestCaptureOutOfRange` was passing trivially because of a short-circuit
  bug — fixed to actually assert that out-of-range captures expand to
  empty string inside a literal context.
- Added `TestPipelineRendererScopedByAppliesTo` covering group-only,
  path-only, and fallback selection paths through the pipeline.

### Phase 3 — Renderer pipeline
- `internal/render`: template DSL (`literals + $N + json($N) + xml($N)`),
  escapes (`\n`, `\t`, `\r`, `\\`, `$$`), out-of-range `$N` expands to
  empty.
- `internal/render.Pipeline`: first-match-wins over compiled renderers,
  `applies_to` semantics enforced as AND (`groups` ∧ `paths`), `paths`
  matched against both full path and basename (`filepath.Match`).
- Invalid JSON/XML inputs to `json(...)` / `xml(...)` fall back to a
  text part so output is never lost.
- `cmd/log-listener`: emits text parts on the prefix line, JSON
  pretty-printed (2-space indent) and XML pretty-printed each on their
  own lines below. Honors `drop_unmatched`.

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
