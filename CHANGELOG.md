# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to phased delivery per `PLAN.md`.

## [Unreleased]

### Changed (BREAKING): render-call DSL syntax is now `$`-prefixed
- Template render-calls change from `json($N)`/`xml($N)` to `$json($N)`/`$xml($N)`.
  Render-calls now live behind the `$` sigil (like `$N` captures), so literal
  text can never be mistaken for a call, and an unknown `$name(...)` is a parse
  error instead of silent literal output. Update any custom config templates by
  prefixing `json(`/`xml(` with `$`.

### Internal: pluggable `renderFunc` interface
- `json` and `xml` are unified under one `renderFunc` interface (`Name`/`Parse`/
  `Lines`) with a name-keyed registry; `ParseTemplate`, `Execute`, and
  `DecomposeLines` dispatch generically. A new render type is one self-
  registering file — no edits to the dispatch sites. Exception detection is
  unchanged (a different abstraction, already behind its own `Processor` interface).

### Internal: TUI view-state as stable anchors
- The TUI's four view-state values (`streamTop`, `searchHit`, `visualCursor`,
  `visualAnchor`) are now stored as stable `(entryID, rowOffset)` anchors instead
  of absolute `m.lines` row indices. Anchors resolve against the current reconcile
  window via shared resolvers (`internal/tui/viewanchor.go`), so view-state
  survives head eviction and renderer-toggle re-renders on its own — the
  `dragViewStateDown` index-drag and `reRenderAll`'s post-reconcile clamp block
  are both removed. A past-end anchor clamps to the last row so over-scrolling
  down still re-sticks to tail (guarded by a new regression test). No
  user-visible behavior change.

### Internal: split `internal/tui/app.go` into focused files
- The 1678-line `internal/tui/app.go` is split (same `package tui`, **zero
  behavior change**) into responsibility-focused files: `app.go` keeps the `App`
  facade + bubbletea messages + the `model` struct/lifecycle; `update.go` holds
  the `Update` key-dispatch; `reconcile.go` the buffer→view reconcile/append
  path; `render.go` display-line rendering; `view.go` `View`/footer/panels/stream
  rendering; `width.go` the shared ANSI/width helpers; and `viewport.go` gains
  the viewport-position helpers + scroll-step consts. Declaration set is
  identical before/after (pure relocation); the test suite is unchanged.

### Internal: TUI viewport/selection operations layer
- Scattered view-state index arithmetic in `internal/tui` is collapsed into
  five intent-level methods, so call sites compose verbs instead of repeating
  low-level clamp math. **Behavior-preserving refactor — no user-facing change.**
- **`scrollBy(delta)`** (`viewport.go`) owns the up/down scroll asymmetry that
  was duplicated across the six scroll actions (line/page/fast, each direction):
  up unsticks tail and clamps at the top; down is a no-op while tailing, else
  moves and re-sticks on catch-up.
- **`scrollFiles(delta)`** (`viewport.go`) centralizes the file-overlay cursor
  move (clamp to `[0, len(files)-1]`), collapsing the six `showFiles` branches of
  the scroll actions.
- **`panBy(delta)`** (`viewport.go`) centralizes horizontal panning (clamp at the
  left edge; no right clamp — the renderer clips), collapsing the four pan
  handlers.
- **`selectionBounds()`** (`visual.go`) centralizes the "order the
  (anchor, cursor) pair, fall back to the caret row" idiom that was copied
  verbatim three times (`visualBar`, `buildVisualText`, `buildVisualRef`).
- **`moveVisualCursor(delta)`** (`visual.go`) centralizes the up/down
  visual-caret move (clamp to the line range + keep on screen).
- The four view-state values stay plain indices and the eviction index-drag
  (`dragViewStateDown`) is unchanged; this was an explicit non-goal after a
  call-site inventory showed an ID-based representation would relocate
  complexity rather than reduce it.

### Search: smart-case shared predicate + `Ctrl+R` regex toggle
- TUI search now uses the same **smart-case** `searchmatch.Matcher` as the MCP
  `search` tool — an all-lowercase query matches case-insensitively; any
  uppercase letter makes the match case-sensitive. Agent and human now search
  against an identical predicate.
- **`Ctrl+R`** toggles regex mode while the search box is open. The footer
  prefix changes to `/(regex) ` when active. An invalid regex keeps the input
  box open and flashes an error instead of committing a broken matcher.
- `clearSearch` now also resets the `searchRegex` flag, so opening a fresh
  search always starts in substring mode.

### Build variants: `nomcp` / `nosse` tags
- **`go build -tags nomcp`** compiles a binary without the embedded MCP server,
  dropping the `modelcontextprotocol/go-sdk` dependency entirely. **`-tags nosse`**
  drops the SSE broadcast server. Tags compose (`-tags "nomcp nosse"`). The default
  build is unchanged (full-featured), so `go install …@latest` is unaffected.
- Asking a stripped binary for the removed feature (`--mcp`, `--sse`, or a YAML
  `output.sse` block) is a hard error with a clear message and non-zero exit.
- `./build.sh` gains `build-nomcp`, `build-nosse`, `build-minimal`, and
  `test-nomcp` / `test-nosse` / `test-minimal`.

### Internal: sink fan-out via `sink.Fanout` registry
- **`sink.Sink` interface + `sink.Fanout` registry**: the stdout, SSE, and
  output-file sinks now implement a common `Sink` interface and are dispatched
  through a single ordered `Fanout` instead of a hardcoded, per-call nil-guarded
  fan-out in `main.go`. `Fanout` skips nil sinks (including typed-nil pointers),
  the seam a future build-tagged constructor uses to compile a sink in or out.
  Behavior-preserving refactor — output is byte-identical.
- **TUI-mode preload now also broadcasts to SSE**: previously, preloaded lines
  (`--preload`) in TUI mode reached only the TUI and the output file, not SSE
  clients, even though live lines did. They now reach SSE too, matching non-TUI
  mode. (Live behavior and stdout/file output are unchanged.)

### TUI: focused-block indicator + visual selection mode + copy-text key
- **Focused-block `│` indicator**: a cyan `│` in the TUI left margin marks the
  multi-line block the cursor is currently on — the live preview of what `y` will
  copy as a `range:` reference. The indicator appears when navigating onto a
  multi-line block (e.g. via `]`/`[`/`}`/`{` or scrolling) and disappears when
  the cursor is not on a block or while tailing, so you can always tell whether
  `y` will produce a block range or the fallback viewport range.
- **`Y` — copy text as displayed**: press `Y` (capital) to copy the selected text
  (no ANSI color codes) as shown in the TUI, with `[group] file:` prefixes and
  pretty-printed JSON/XML blocks intact. Mirrors `y`'s context-aware selection:
  search hit, focused block, or viewport. Text is copied to the clipboard via
  OSC 52; very large selections may exceed the terminal's OSC 52 size limit —
  use `s` (save viewport) or `S` (save full buffer) instead.
- **`v` — unified visual line-selection mode**: a vim-style modal selection layer.
  Press `v` to enter; move the cursor with ↑/↓ (`j`/`k`); press `space` to anchor
  the start of the selection, move to the end, then press `y` to copy a
  `range:<id>..<id>` reference or `Y` to copy the selected text, both via OSC 52
  and exit the mode. `esc` cancels at any point.

### Embedded MCP server + agent hand-off (`--mcp`, `y`)
- **`--mcp [addr]`** starts an embedded Streamable-HTTP MCP server (default
  `127.0.0.1:7777`) that shares the live in-memory log buffer alongside the
  TUI / stdout / SSE sinks. Optional value: bare `--mcp` uses the default;
  `--mcp host:port` overrides. Not active in `--once` mode. No authentication
  — local dev aid only. CLI flag only (no YAML `output.mcp` field this cycle).
- **Seven read-only MCP tools**: `get_line(id)`, `get_range(from,to)`,
  `get_context(id,before,after)`, `get_scrollback(limit,offset)`,
  `search(query,regex,limit)`, `list_exceptions()`, and the new
  **`get_viewport()`** — returns the TUI's current on-screen entry range and
  entries (exactly what the user sees / what `y` copies as the fallback viewport
  range); returns an error when no TUI is attached (headless / `--no-tui`) — use
  `get_scrollback` instead. All operate on the shared `linebuf.Buffer` and return
  JSON. Implemented in the new `internal/mcp` package using the official Go MCP
  SDK (`github.com/modelcontextprotocol/go-sdk`).
- **End-to-end MCP tests**: integration tests drive the real embedded server with
  the Go MCP client against a preloaded fixture, exercising `get_viewport`,
  `search`, `list_exceptions`, and `get_range` end-to-end (no mocking).
- **Stable per-record IDs**: every log record is assigned a permanent opaque ID
  (`L0`, `L1`, … base-36) at fan-out ingest by the new `internal/linebuf`
  concurrency-safe ring buffer. IDs are stable for the lifetime of the record
  in the buffer; evicted records are gone but survivors never change ID.
- **`y` — Copy reference** (TUI): copies a paste-ready reference to the
  clipboard via OSC 52, context-sensitively: `line:<id>` when a search hit is
  selected; `range:<headId>..<endId>` when the cursor is on a multi-line block;
  `range:<firstVisibleId>..<lastVisibleId>` (viewport) otherwise. Paste to an
  agent, which resolves it via `get_line` / `get_range`.

### Preload the buffer from a file
- **`--preload <[group=]path>`** (repeatable) seeds the buffer before tailing so
  the TUI can be driven with canned data — no live logs needed. **Raw** files run
  through the renderer pipeline under a synthetic group; **capture** files (a
  saved `screen-log-listener-*` export) are reconstructed faithfully, recovering
  the original groups/files so block annotation, exception marks, navigation, and
  save all work on the restored buffer. Mode auto-detects by filename; force it
  with **`--preload-raw`** / **`--preload-capture`**. `--preload x --once` prints
  the seeded content and exits (a headless inspection path).

### Block annotation + exception marks
- Multi-line log units (stack traces, pretty-printed JSON/XML, indented
  continuations) are grouped into **blocks** by a neutral `internal/blocks`
  package: indentation plus a small signature set (`Caused by:`, `goroutine `,
  PHP `#<n>`) so multi-part traces group together.
- An **exception processor** flags blocks that look like stack traces and
  guesses the language (Python/Java/Kotlin/Go/JS/TS/Rust/C-C++/PHP). Detection
  is heuristic; Go panics may still split into multiple blocks.
- **`e`** toggles a red left-bar (`▌`) drawn on exception blocks. **`]`/`[`**
  jump between multi-line blocks (single-line entries are skipped); **`}`/`{`**
  jump between processor-matched (exception) blocks. All keys are remappable via
  the `keybindings:` block. IDs/clipboard for agent hand-off arrive with the
  MCP server.

### Installable via `go install`
- The module path is now `github.com/homeend/log-listener` (was the bare
  `log-listener`), and the `package main` entry point moved from
  `cmd/log-listener/` to the **repo root**, so
  `go install github.com/homeend/log-listener@latest` produces the
  `log-listener` binary. All internal imports were rewritten to the new module
  path; `build.sh`/`build.cmd` now build the root package (`.`).
- E2E tests are isolated from ambient config discovery (the spawned binary runs
  from a throwaway dir with `HOME`/`USERPROFILE` redirected), so a developer's
  gitignored `./log-listener.yml` no longer perturbs assertions.

### Save view to a text file
- **`s`** writes the currently visible rows, and **`S`** writes the entire
  scrollback buffer, to a timestamped `screen-log-listener-<ts>.txt` file in the
  working directory (numeric suffix on same-second collisions). Output is plain
  text: ANSI stripped, full `[group] file:` prefixes kept regardless of column
  toggles. A footer message confirms the path (or reports a write error) until
  the next keypress. Both keys are remappable via the `keybindings:` block.

### Output log to file (`-o` / `--output`)
- **`-o <file>` / `--output <file>`** writes every displayed line to `<file>`
  in plain text (ANSI color stripped), in all modes (`--once`, `--no-tui`, and
  the interactive TUI). The file is truncated at startup. Format: one line per
  log entry, same as stdout: `[group] basename: text`, with indented JSON/XML
  blocks on separate lines. Note: keep the output file outside any watched
  directory, or it will be discovered and tailed (infinite loop risk).

### OS-aware keybindings (translation + override layer)
- **`internal/keymap`** is now the single source of truth for TUI keys: every
  function is a named *action* mapped to a per-OS list of keys, so behavior and
  on-screen help both derive from one table. The TUI dispatches by action
  instead of hard-coded key strings.
- **macOS-native display**: on `darwin` the header/overlay hints and the
  reference doc render keys with Mac glyphs (`⌃ ⌥ ⇧ ⎋ ⇥`) instead of
  `Ctrl/Alt/Shift/Esc/Tab`. Display is case-accurate (a binding on `g` shows
  `g`, not `G`). Terminals can't see the ⌘ key, so no shortcut uses Cmd.
- **macOS fast-scroll remap**: because `Ctrl`+Arrow is captured by macOS
  Mission Control / Spaces before a terminal sees it, the macOS defaults
  advertise `Shift`+Arrow first for fast scrolling (Ctrl+Arrow stays bound;
  PgUp/PgDn remain a safety net). *(Shift+Arrow forwarding is not yet verified
  on every macOS terminal.)*
- **User overrides via YAML**: a new `keybindings:` block remaps any action.
  Resolution is per-action with replace-semantics and precedence
  current-OS section → `default` section → built-in default. Unknown action
  names, unmappable key tokens, key collisions, and bindings that shadow the
  positional `1`–`9` / `!@#…` toggles are all rejected at load time — no silent
  no-fire.
- **Generated reference**: `log-listener --keybindings-doc` prints a Markdown
  table of every action's keys per OS; the committed `KEYBINDINGS.md` is
  produced from it (`./build.sh keybindings-docs`) and guarded by
  `TestDocsUpToDate` so it can never drift from the code.

### TUI display-width fixes
- **Tabs** in log lines (e.g. Java stack-trace frames, `\tat …`) are expanded to
  8-column tab stops, and **wide/CJK characters** are measured at their true
  cell width (2 columns) instead of one. Both previously made the width math
  underestimate, so the row overflowed and wrapped — pushing the header
  off-screen and leaving stale fragments bleeding through. Rows are now clamped
  to the terminal's display width, including ANSI- and wide-char-aware
  horizontal scrolling.

### Renderer validity & multi-line rendering fixes
- **JSON/XML detection is validity-based**: a renderer matches only when its
  `json()`/`xml()` call actually parses. Lines that match a renderer's regex
  but carry non-JSON braces (e.g. IntelliJ's `{KEY=value}` macro dumps, or
  exception messages ending in `{…}`) now fall through and render as the
  original single line instead of being split/mangled.
- **TUI row invariant**: multi-line rendered text is stored as a list of rows,
  so an embedded newline can no longer wrap a row, push the header off-screen,
  or corrupt horizontal scrolling. Header/footer lines are also clamped to a
  single row so they can't wrap at narrow terminal widths.

### TUI search: filter, hit navigation, auto-scroll, repeat
- **`t` filter**: show only entries containing the search term; a match inside
  a rendered JSON/XML block shows the whole block alongside its source line
  (whole-entry filtering). A `filter` tag appears in the footer while active.
- **Up/Down navigate hits**: while a term is active, Up/Down (and `k`/`j`) jump
  to the previous/next hit; PgUp/PgDn and Ctrl+arrows still scroll.
- **Horizontal auto-scroll**: jumping to a hit pans the view so an off-screen
  matched term becomes visible.
- **Repeat search**: `/` then Enter re-runs the last committed term, which is
  remembered across clears.

### Matchers and mute
- **Reusable matchers**: a global `matchers:` library of named predicates over
  a log line's content, the source file's basename, and its full path. Each
  dimension matches by an exact literal (`line`/`name`/`path`) or a regex
  (`line_regex`/`name_regex`/`path_regex`); at least one dimension is required
  and all set dimensions are AND-combined.
- **`mute:`**: drop matching lines before any sink (stdout / SSE / TUI). Each
  entry references a named matcher or sets inline matcher fields, with an
  optional `id` and `applies_to` (group ids + path globs) scope. Mute is
  applied ahead of every renderer and of `output.drop_unmatched`.
- **Renderer `matcher:`**: renderers may reference a named matcher instead of
  an inline `line_regex`; the matcher's `line_regex` supplies the template
  captures and any name/path criteria additionally gate the renderer. Matcher
  references, exactly-one-of constraints, and capture availability are
  validated at startup. New `internal/match` package.

### Template auto-configuration
- **`log-listener init <apps...>`**: generate a `log-listener.yml` from an
  embedded, OS-aware catalog of application log templates (JetBrains family +
  Junie). Templates compose via reusable fragments (shared family discovery and
  cross-app bridge logs), resolve `{product}`/OS path tokens for the current OS,
  and probe-and-pick the directories that exist. Supports `-o <path|->`,
  `--list`, an interactive overwrite/merge/cancel prompt, `--force`/`--merge`,
  and optional online catalog updates (`--online`/`--offline`) with a bundled
  fallback on any failure.

### Config auto-reload
- **Config auto-reload**: the loaded YAML config file is now watched; edits
  re-apply groups and renderers live (rebuilding the file watcher and swapping
  the renderer pipeline) in both TUI and stdout modes. Output settings are not
  re-applied; invalid edits are ignored silently.

### Phase 6 review fixes
- `Makefile` `run` target replaced with `demo`, which creates a tempdir
  and seeds it with a JSON-tail line so `make demo` is a self-contained
  smoke test (the old `run` pointed at `/var/log/app-a` which doesn't
  exist on dev boxes).
- README clarifies that `-extldflags "-static"` is Linux-only — on
  macOS the binary is CGO-free but not "fully static."

## [Unreleased]

### TUI: collapse multiline entries with `m`
- New keybinding **`m`** toggles a "collapsed multiline" view: any
  row whose body starts with whitespace (Python tracebacks, indented
  continuations) is hidden, and the preceding head row gets a dim
  `[...]` suffix to flag the hidden content. JSON/XML pretty-print
  blocks collapse the same way — head row stays, block rows vanish
  behind the marker.
- The collapse extends `lineEnabled`, so the existing tail/browse,
  group-filter, and search paths automatically respect it (a hit in
  a hidden continuation is not surfaced — mirrors disabled-group
  behavior).
- TUI-only; stdout and SSE consumers continue to receive full content.

### Runtime renderer toggles + YAML `disabled` / `off`
- **Renderer toggles** — `!` `@` `#` `$` `%` `^` `&` `*` `(` (shifted
  digits 1–9) toggle renderers on/off in the TUI, mirroring the
  digit-key toggles for groups. **The entire scrollback re-renders
  live**: existing JSON-pretty-print blocks turn back into the raw log
  line, and toggling back on regenerates them from the same source.
  Future events also honor the new state.
- **`Ctrl+E`** opens the new **Renderers** overlay (mirror of Ctrl+G
  Groups): lists every loaded renderer with its toggle key, `ON`/`OFF`
  state, and name.
- Footer status grows a new `rend: N (M off)` segment.
- **`disabled: true`** (hard) — works on dir groups, file groups, and
  renderers. The entry is filtered out at YAML load time and never
  reaches the pipeline / watcher / TUI; keyboard cannot bring it back.
- **`off: true`** (soft) — same three entry kinds. The entry is loaded
  normally, but its TUI toggle starts in the off position. The user
  can flip it on with the digit / shifted-digit key or the panel. For
  renderers, the pipeline's atomic enable flag is initialized to false.
  If both `disabled` and `off` are set, `disabled` wins.
- Removed the `$` "jump-to-widest-line" horizontal-scroll binding —
  `$` is now the renderer-#4 toggle. Right-arrow still pans.

Internals: TUI scrollback restructured. `m.events` was a flat
`[]displayLine`; it's now a pair of fields: `m.entries []scrollbackEvent`
(source of truth — group, file, raw, lines) and `m.lines []displayLine`
(derived flat cache used by every hot-path reader, kept in sync by
`appendEvent` / `trimToCap` / `reRenderAll`). Scrollback cap stays
line-count-based; trim evicts whole entries from the head and shifts
`streamTop` / `searchHit` by lines-dropped. `tui.New` now takes an
`Options` struct instead of positional args (callers: main.go +
TestNewSeedsInitialFiles). `render.Pipeline` gained `sync/atomic`-based
per-renderer enable flags (`SetRendererEnabled` / `IsEnabled` /
`RendererCount` / `RendererName`), so `Render` does a lock-free atomic
load on the hot path and toggle calls from the bubbletea goroutine
need no mutex.

### TUI: in-line search (`/`, `n`, `p`)
- **`/`** opens a search prompt at the bottom of the screen. Type the
  term (case-insensitive substring), press **Enter** to commit, or
  **Esc** to cancel. The footer mirrors what's being typed.
- After commit, the viewport jumps to (and centers) the first hit and
  exits tail mode. Every visible match is highlighted (yellow bg); the
  active hit gets a brighter style (red bg) so n/p navigation is
  unambiguous.
- **`n`** advances to the next hit; **`p`** steps back. When no hit
  exists in the requested direction, the footer asks "wrap to
  top/bottom? (y/n)" — **y** wraps, **n** / **Esc** dismisses without
  moving.
- Search respects group toggles (hits in disabled groups are skipped),
  searches block lines (JSON/XML pretty-prints) in addition to head
  lines, and stays case-insensitive throughout.
- **Esc** with no overlay open clears the active search.

Internals: new `search.go` file with `findHit`, `jumpToHit`,
`commitSearch`, `searchNext`, `searchPrev`, and `highlightMatches`.
`Model.Update` gained two modal pre-dispatch branches
(`handleSearchInputKey`, `handleWrapPromptKey`) that run before the
normal key switch so search-input keys and the y/n prompt can't be
intercepted by other bindings. `collectVisible` now returns absolute
event indices so the renderer can tell which row holds the active hit.

### TUI: Ctrl+R clears the scrollback
- New keybinding: **Ctrl+R** empties the TUI's in-memory event list,
  resets `streamTop` / `horizScroll`, and re-enters tail mode. The
  watcher, stdout sink, and SSE hub keep running — this only resets
  the viewer's view of history. Useful when you want a clean screen
  before triggering a specific action you're debugging.

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
