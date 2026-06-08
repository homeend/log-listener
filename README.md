# log-listener

Real-time multi-source log tailer with regex-based file discovery, a
configurable rendering pipeline (JSON/XML pretty-printing of embedded
payloads), an interactive TUI, and a localhost SSE stream for web clients.

Single static Go binary. Linux-first; macOS works modulo the "fully static"
linker flag.

---

## Capabilities at a glance

- **Watch many sources at once.** Mix recursive directory globs and explicit
  file paths in the same invocation. New files appearing in a watched
  directory are picked up automatically.
- **Regex-driven file selection.** Filter discovered files by basename regex,
  exclusion regex, and modification time (`older` / `younger`) — both
  absolute (`2026-01-01`, `2026-01-01T10:30:00Z`) and relative
  (`30s`, `15m`, `1h`, `2d`, `1w`).
- **Rotation-safe tailing.** Detects rename-rotation (inode change) and
  truncation (size decrease), drains the old descriptor, flushes any
  partial line, and reopens by path.
- **First-match-wins file ownership.** When the same file matches multiple
  configured groups (CLI or YAML), the first one in declaration order owns
  it. No duplicate lines.
- **Renderer pipeline with a small DSL.** Regex-match a line, then template
  the output: literal text + `$N` capture references + `json($N)` /
  `xml($N)` calls that pretty-print embedded payloads.
- **Multiple output destinations, in parallel.** A colorized stdout sink, an
  interactive bubbletea TUI, an HTTP/SSE broadcast, and an embedded MCP server
  — any combination. Output downgrades automatically when stdout isn't a TTY
  (no TUI, no color).
- **One-shot mode.** `--once` scans existing content (renderers still apply
  — useful for prettifying JSON inside historical logs) and exits.
- **YAML configuration with CLI precedence.** Anything you can do on the CLI
  you can do in `log-listener.yml`; CLI flags win on conflict.
- **OS-aware, customizable keybindings.** The TUI renders native key glyphs on
  macOS (`⌃ ⌥ ⇧`) and Ctrl/Shift text on Linux/Windows, and every shortcut is
  remappable per-OS via a `keybindings:` block. Full per-OS reference:
  [`KEYBINDINGS.md`](KEYBINDINGS.md).
- **Embedded MCP server for agent hand-off.** `--mcp [addr]` starts a
  Streamable-HTTP MCP server (default `127.0.0.1:7777`) sharing the live log
  buffer. An AI agent can call seven read tools against it; press `y` in the TUI
  to copy a paste-ready reference to the clipboard (OSC 52) that the agent
  resolves immediately. Local dev aid only — no authentication.
- **Static binary, CGO-free.** No CGO; `build-static` produces a fully static
  binary. Runtime deps: `fsnotify`, `yaml.v3`, `bubbletea`, `lipgloss`,
  `go-runewidth`, and the official Go MCP SDK
  (`github.com/modelcontextprotocol/go-sdk`).

---

## Install

Requires Go 1.26+.

Install the binary straight from the module path:

```bash
go install github.com/homeend/log-listener@latest   # → $GOBIN/log-listener
```

Or build from a clone:

```bash
git clone https://github.com/homeend/log-listener && cd log-listener
./build.sh build         # ./log-listener
./build.sh build-static  # CGO_ENABLED=0, stripped
```

On Linux, `build-static` produces a fully static binary
(`-extldflags "-static"`). On macOS the static linker flag is a no-op but
the resulting binary is still CGO-free and reproducible. (`build.cmd` is the
Windows equivalent of `build.sh`.)

### Build variants

The default build (`go build`, `go install …@latest`, `./build.sh build`) includes
everything. To produce leaner binaries, use build tags:

| Command | Result |
|---------|--------|
| `go build -tags nomcp` (`./build.sh build-nomcp`) | No MCP server; **drops the `modelcontextprotocol/go-sdk` dependency**. |
| `go build -tags nosse` (`./build.sh build-nosse`) | No SSE server. |
| `go build -tags "nomcp nosse"` (`./build.sh build-minimal`) | Neither MCP nor SSE. |

A stripped binary still recognizes the corresponding flag, but asking for the
removed feature (`--mcp`, `--sse`, or an `output.sse` YAML block) is a hard error.

---

## Quick start with templates

Generate a config for known applications instead of hand-writing one:

```bash
log-listener init goland junie          # write ./log-listener.yml
log-listener init jetbrains -o dev.yml  # a whole product family
log-listener init goland -o -           # print to stdout
log-listener init --list                # show available apps and bundles
```

`init` resolves each app's log locations for your OS, keeps the directories that
actually exist on disk, attaches sensible renderers, and writes a
`log-listener.yml` that the normal `log-listener` run consumes (and
live-reloads). Apps compose: selecting `junie` pulls in its own logs **plus**
the Junie↔IDE bridge logs that live inside each JetBrains product.

On an interactive run it offers to fetch a newer template catalog from GitHub
(`https://raw.githubusercontent.com/homeend/log-listener/main/internal/catalog/catalog.yml`);
`--offline` skips the check, `--online` forces it, and `--url <url>` fetches from
a catalog you host yourself (implies `--online`). Any network failure silently
falls back to the catalog bundled into the binary. An existing output file is
left untouched unless you confirm at the `[o]verwrite / [m]erge / [c]ancel`
prompt, or pass `--force` (optionally with `--merge` to append only new entries)
for non-interactive use.

---

## Core concepts

### Directory groups vs. file groups

A **directory group** (`-d`, `-dN`, or `directories:` in YAML) is a set of
paths that are walked (recursively by default) and filtered. Files within
that group are then watched for appends. New files created later inside the
watched directories are picked up if they pass the same filter.

A **file group** (`-f`, `-fN`, or `files:` in YAML) is a set of file paths
or globs that are watched directly. File groups are **always unfiltered**:
the filter syntax only applies to directory groups.

The `default` group id is what `-d` / `-r` / `-f` (no number suffix) refer
to. Numbered ids (`-d1`, `-r1`, `-d2`, …) let you keep multiple groups with
distinct rules in a single invocation. A rule flag (`-rN`) pairs with the
directory flag of the same number (`-dN`).

### Pattern paths

Both `-d` (directory groups) and `-f` (file groups) accept glob patterns
in their paths (`*`, `?`, `[abc]` — `path.Match` semantics). Patterns
are evaluated both at startup AND at runtime:

- **At startup**, each pattern is expanded to all currently-matching
  paths.
- **At runtime**, the watcher monitors each pattern's *literal prefix*
  (the part before the first `*`/`?`/`[`). Any newly-created directory
  that could plausibly lead to a pattern match is automatically watched
  and recursively scanned for matching files. This covers multi-hop
  patterns where the parent of the wildcard appears first, then the
  suffix directory, then the file — all three Create events are
  cascaded through.

Example: `-d '/tmp/acp-logs-*/sub' -r 'name:\.log$'` watches every
`.log` file in `/tmp/acp-logs-*/sub`, and when a brand-new
`/tmp/acp-logs-XYZ/sub/` directory appears later, its files are tailed
as soon as they're created. Same for files: `-f '/tmp/session-*/out.log'`
picks up `out.log` inside any new `session-*` directory.

### First-match-wins

Two places, same rule:

1. **File → group assignment.** Each discovered file is assigned to the
   first group (in declaration order) whose filter accepts it. A file
   never appears under two groups; its log lines are never duplicated.
2. **Line → renderer.** Each line is offered to renderers in declaration
   order. The first one whose `applies_to` scope matches the file *and*
   whose `line_regex` matches the line wins; the rest are skipped.

### Lines that no renderer matches

Pass through as a single text part by default. Set
`output.drop_unmatched: true` (YAML) to discard them silently — useful when
you want the renderer pipeline to act as a filter as well as a formatter.

A renderer matches only if its `line_regex` matches **and** its `json()` /
`xml()` render-calls actually parse. If a render-call can't parse its capture
(e.g. a line ending in `{KEY=value}`, which isn't JSON), the renderer is
treated as a non-match and the line falls through to the next renderer — and
ultimately renders as the original raw line (or is dropped under
`output.drop_unmatched`). So a `{…}` that only *looks* like JSON is never
split or mangled.

### `applies_to` semantics

`groups` and `paths` are AND-combined; empty means vacuously true. Path
globs are tried against the full path and the basename, so both
`*.app.log` and `/var/log/foo/*.app.log` work.

### Matchers and mute

A **matcher** is a reusable named predicate over three dimensions of an
event. For each dimension set **either** the literal key **or** the regex
key — never both — and set **at least one** dimension. All set dimensions
must match (**AND**).

| Dimension | Literal key | Regex key    | Target                |
|-----------|-------------|--------------|-----------------------|
| Content   | `line`      | `line_regex` | the raw log line      |
| File name | `name`      | `name_regex` | the file basename     |
| File path | `path`      | `path_regex` | the full file path    |

Literals match **exactly** (`name` equals the basename, `path` equals the
full path, `line` equals the whole line) and skip the regex engine — use a
literal when you don't need a pattern. Regex keys use Go `regexp` (RE2).

Define matchers once under `matchers:` and reference them by name, or write
the same fields inline where a single matcher is needed.

**`mute:`** drops matching lines *before* they reach any sink — stdout, the
SSE broadcast, and the TUI. A mute entry sets exactly one of `matcher:` (a
named reference) or inline matcher fields, an optional `id:` (used in error
messages), and an optional `applies_to:` (group ids + path globs, AND) to
scope it. Mute is checked first, ahead of every renderer and of
`output.drop_unmatched`. A mute whose matcher sets only `name`/`path` drops
*all* lines from the matching files.

**Renderers** may reference a matcher with `matcher:` instead of an inline
`line_regex`. The matcher's `line_regex` supplies the template captures
(`$1`, `$2`, …) and any `name`/`path` criteria additionally gate the
renderer. A renderer must set exactly one of `line_regex` or `matcher`, and
a matcher used by a renderer must carry a `line_regex` (otherwise there is
nothing to capture). All of these are validated at startup.

---

## CLI

### Flags

| Flag                              | Effect                                                                |
|-----------------------------------|-----------------------------------------------------------------------|
| `-d <paths…>` / `-dN <paths…>`    | Directory group `default` / group `N`. Multi-arg until next flag.     |
| `-r <tokens…>` / `-rN <tokens…>`  | File-filter rules for the matching directory group.                   |
| `-R <tokens…>`                    | Global file-filter applied to every directory group.                  |
| `-f <paths/globs…>` / `-fN …`     | File group `default` / `N`. Globs are expanded with `filepath.Glob`.  |
| `--config <path>`                 | Explicit YAML config path.                                            |
| `--once`                          | Scan existing content (renderers run) and exit. No tailing.           |
| `--no-tui`                        | Disable the interactive TUI even when stdout is a TTY.                |
| `--no-color`                      | Disable ANSI color in stdout.                                         |
| `-o <file>` / `--output <file>`   | Also write every displayed line to `<file>` in plain text (no ANSI), in all modes; truncates on startup. Tip: keep outside watched directories. |
| `--sse <addr>`                    | Enable the SSE broadcast on `addr` (e.g. `127.0.0.1:8080`).           |
| `--mcp [addr]`                    | Start the embedded MCP server (default `127.0.0.1:7777`). Optional value: bare `--mcp` uses the default; `--mcp host:port` overrides. Not active in `--once` mode. No authentication — local dev only. |
| `--preload <[group=]path>`        | Seed the buffer from a file before tailing (auto-detect raw vs capture). Repeatable. |
| `--preload-raw <[group=]path>`    | Force raw mode: run the file's lines through the pipeline under a synthetic group.    |
| `--preload-capture <path>`        | Force capture mode: reconstruct a saved `screen-log-listener-*` export.               |

### Rule tokens

Used inside `-r`, `-rN`, `-R`:

| Token              | Meaning                                                              |
|--------------------|----------------------------------------------------------------------|
| `name:<regex>`     | File **basename** must match. Go `regexp` syntax (RE2).              |
| `exclude:<regex>`  | File basename matching this is rejected even if `name` matched.      |
| `older:<when>`     | File mtime must be **before** `<when>`.                              |
| `younger:<when>`   | File mtime must be **after** `<when>`.                               |

`<when>` is one of:

- ISO date: `2026-01-15`
- ISO datetime: `2026-01-15T10:30:00`, `2026-01-15T10:30:00Z`, RFC3339
- Relative duration: `30s`, `15m`, `1h`, `2d`, `1w` — interpreted as
  *now minus that duration*.

### Examples

```bash
# Tail every .log under /var/log/app, recursively, real-time:
log-listener -d /var/log/app -r 'name:\.log$'

# Two groups with distinct rules:
log-listener \
    -d  /var/log/app-a    -r  'name:\.log$' 'younger:1h'   \
    -d1 /var/log/app-b    -r1 'name:panic-' 'exclude:\.gz$'

# Specific files, globbed:
log-listener -f '/tmp/run-*.log' '/var/log/system.log'

# Existing content only, no tailing — handy for pretty-printing historical JSON:
log-listener -d /var/log/app -r 'name:\.log$' --once

# Same as live mode but also broadcast to a web client:
log-listener -d /var/log/app -r 'name:\.log$' --sse 127.0.0.1:8080

# Disable everything fancy — just plain text on stdout:
log-listener -d /var/log/app -r 'name:\.log$' --no-tui --no-color
```

---

## YAML configuration

Resolution order (first match wins):

1. `--config <path>` — must exist or it's an error.
2. `./log-listener.yml` — current working directory.
3. `~/.log-listener.yml` — user home.

A complete annotated config:

```yaml
# directories — corresponds to -d / -r
directories:
  - id: default
    paths:
      - /var/log/app-a
      - /var/log/app-b
    recursive: true                 # default true
    file_filter:
      name_regex: '\.log$'
      exclude_regex: 'archive|\.gz$'
      older: 2026-01-01
      younger: 24h
  - id: 1                           # corresponds to -d1 / -r1
    paths: [/var/log/special]
    file_filter:
      name_regex: 'panic-.*\.log'
  - id: noisy
    paths: [/var/log/very-noisy]
    off: true                       # loaded but hidden in TUI on start
  - id: archived
    paths: [/var/log/old]
    disabled: true                  # not loaded at all — ignored entirely

# files — corresponds to -f / -fN; always unfiltered
files:
  - id: default
    paths: ['/tmp/run-*.log']       # glob-expanded
  - id: 1
    paths: ['/var/log/system.log']

# Applied to every directory group above. CLI -R wins entirely if present.
global_file_filter:
  younger: 7d

# Matchers — a reusable library of named predicates. See "Matchers and mute".
matchers:
  health-noise: { line_regex: 'GET /health' }   # regex over the log line
  idea-file:    { name: idea.log }              # exact basename literal
  app-json:     { name_regex: '\.app\.log$', line_regex: '(\{.*\})' }

# Mute — drop matching lines before any sink (stdout / SSE / TUI).
mute:
  - id: drop-health
    matcher: health-noise           # reference a named matcher
  - id: silence-debug
    line_regex: 'DEBUG'             # OR inline matcher fields
    applies_to: { groups: [1] }     # optional scope: group ids + path globs

# Renderers — see "Renderer pipeline" below for the template DSL
renderers:
  - name: app-json
    line_regex: '(\d{4}-\d{2}-\d{2}) \[(\w{4,5})\] (\{.*\})'
    template: '$1 $2\njson($3)'
    applies_to:
      groups: [1]
      paths: ['*.app.log']
  - name: idea-json                 # use a named matcher instead of line_regex
    matcher: app-json               # the matcher's line_regex feeds $1
    template: 'json($1)'
  - name: pretty-xml                # registered but starts off
    line_regex: '.+'
    template: 'xml($0)'
    off: true
  - name: legacy                    # ignored entirely
    line_regex: '.+'
    template: '$0'
    disabled: true

output:
  color: true                       # ignored when stdout isn't a TTY
  drop_unmatched: false             # true → drop lines no renderer matched
  sse:
    enabled: true
    addr: '127.0.0.1:8080'          # default if enabled:true and addr unset

tui:
  enabled: true                     # ignored when stdout isn't a TTY
  scrollback: 10000                 # bounded ring buffer of display lines
```

YAML is strict — unknown keys (e.g. `directorys:` typo) are an error.
Duplicate group ids within `directories:` or `files:` are an error.

### `disabled:` vs `off:` on entries

Every directory group, file group, and renderer accepts two booleans:

- **`disabled: true`** — *hard* disable. The entry is filtered out at
  load time and never reaches the pipeline / watcher / TUI. The
  keyboard cannot bring it back. Use this to mothball a config block
  without deleting it.
- **`off: true`** — *soft* disable. The entry is loaded normally, but
  its TUI toggle starts in the off position (group hidden in stream
  view; renderer skipped in first-match-wins dispatch). The user can
  toggle it back on with the digit / shifted-digit key or the panel.

If both are set on the same entry, `disabled` wins and `off` is
ignored.

### CLI ↔ YAML precedence

- Scalar fields (`--no-color`, `--no-tui`, `--sse`, etc.): CLI wins if
  explicitly set; otherwise YAML applies.
- Groups: same `(kind, id)` → CLI's version replaces YAML's. Different
  ids are merged (CLI groups first, YAML-only groups appended).
- Renderers: only YAML defines them.
- `-R` rules: if any `-R` token is on the CLI, YAML's
  `global_file_filter` is dropped entirely.

### Config auto-reload

When `log-listener` loads a YAML config (via `--config` or the default
`./log-listener.yml` / `~/.log-listener.yml` lookup), it watches that file and
re-applies changes live — no restart needed. On save it re-reads the file and
rebuilds the **groups/file discovery** and **renderers**:

- Newly-matching files and directories start being tailed; files that no longer
  match are dropped.
- The renderer pipeline is rebuilt; in the TUI the renderer/group/file panels
  reseed and existing scrollback re-renders under the new renderers (renderer
  toggle state resets to the file's `disabled`/`off` defaults).
- **Output settings are not re-applied** — SSE address, color, and scrollback
  size keep their startup values.
- An invalid edit (parse/validation error) is ignored silently; the last good
  config keeps running.

Works in both the interactive TUI and plain stdout streaming. Not active in
`--once` mode. A brief gap (lines appended during the rebuild) may be missed.

---

## Renderer pipeline

A renderer matches a regex against each line and templates the output. The
template DSL is small:

| Construct       | Meaning                                                       |
|-----------------|---------------------------------------------------------------|
| literal text    | Emitted as-is.                                                |
| `$N`            | Replaced with regex capture group N (`$0` = full match).      |
| `json($N)`      | Parse capture N as JSON, emit a pretty-printed JSON block.    |
| `xml($N)`       | Parse capture N as XML, emit a pretty-printed XML block.      |
| `\n` `\t` `\r`  | Literal newline / tab / carriage return.                      |
| `\\`            | Literal backslash.                                            |
| `$$`            | Literal `$`.                                                  |

Out-of-range `$N` references expand to the empty string (won't fail
loudly).

JSON/XML pretty-print blocks always start on their own line in stdout
output. If a `json(...)` or `xml(...)` input is malformed, the renderer
falls back to a plain text part containing the raw input — output is never
silently dropped.

### Example

Line:

```
2026-05-28 [ERROR] {"user":"bob","action":"login"}
```

Renderer:

```yaml
renderers:
  - name: app-json
    line_regex: '(\d{4}-\d{2}-\d{2}) \[(\w{4,5})\] (\{.*\})'
    template: '$1 $2\njson($3)'
```

Stdout:

```
[default] app.log: 2026-05-28 ERROR
{
  "action": "login",
  "user": "bob"
}
```

SSE payload (one line):

```json
{
  "ts":"2026-05-28T12:34:56Z",
  "file":"/var/log/app.log","group":"default","raw":"2026-05-28 [ERROR] {\"user\":\"bob\",\"action\":\"login\"}",
  "renderer":"app-json",
  "captures":["2026-05-28 [ERROR] {…}","2026-05-28","ERROR","{\"user\":\"bob\",…}"],
  "rendered":[
    {"type":"text","value":"2026-05-28 ERROR\n"},
    {"type":"json","value":{"action":"login","user":"bob"}}
  ]
}
```

### Scoping renderers (`applies_to`)

```yaml
renderers:
  - name: app-json
    line_regex: '…'
    template: '…'
    applies_to:
      groups: [errors, 1]      # only files in these groups
      paths: ['*.app.log']     # AND filename glob
```

Both lists are matched with AND semantics. Empty means vacuously true.

---

## Output destinations

The three sinks run in parallel. None of them blocks the others.

### Stdout

Format:

```
[<group>] <basename>: <text-part>
<pretty JSON block, if any>
<pretty XML block, if any>
```

ANSI color (on a TTY): group id in cyan, basename in blue, JSON/XML blocks
dimmed. Auto-disabled when stdout is piped or redirected, or when
`--no-color` is passed.

### SSE broadcast

```bash
curl -N http://127.0.0.1:8080/stream
```

Each event is one SSE `data:` line carrying a JSON object (the
`render.Event` schema shown above). Bind only to localhost by default.

Hub behavior:

- One HTTP server, multiple concurrent clients.
- Per-client 256-event buffer. Slow clients see drops (their events go
  silently nowhere); fast clients are unaffected.
- 15-second keepalive comments (`: keepalive\n\n`) defeat intermediary
  timeouts.
- `GET /` returns a one-line plaintext hint pointing at `/stream`.

### MCP server (`--mcp`)

```bash
--mcp                   # bind to 127.0.0.1:7777 (default)
--mcp 127.0.0.1:9100    # override address
```

Starts a Streamable-HTTP MCP server that shares the same in-memory log buffer
the TUI is watching. An AI agent connects and calls six read-only tools:

| Tool                           | What it returns                                                         |
|--------------------------------|-------------------------------------------------------------------------|
| `get_line(id)`                 | The single record with that ID.                                         |
| `get_range(from, to)`          | All records from `from` to `to` (inclusive, either order).              |
| `get_context(id, before, after)` | The record at `id` plus `before` records before and `after` after it. |
| `get_scrollback(limit, offset)` | The most recent records, newest-first, paginated by `limit`/`offset`. |
| `search(query, regex, limit)`  | Records whose text contains `query` (or matches `regex`), newest-first. |
| `list_exceptions()`            | All exception-annotated blocks detected in the buffer.                  |
| `get_viewport()`               | The TUI's current on-screen entry range and entries — exactly what the user sees and what `y` copies as the fallback viewport range. Returns an error when no TUI is attached (headless / `--no-tui`) — use `get_scrollback` instead. |

**Record IDs** are stable opaque strings (`L0`, `L1`, `L2`, … base-36) assigned
at ingest and never reused. IDs in the buffer can be evicted as the ring fills,
but those that remain are permanent.

**`y` — Copy reference (TUI).** Press `y` in the TUI to copy a paste-ready
reference to the clipboard via OSC 52. A cyan `│` in the left margin marks the
multi-line block the cursor is currently on — that is the block `y` will copy as
a `range:`; the indicator disappears when the cursor is not on a multi-line block
(or while tailing), so you can always tell whether `y` will produce a block range
or the fallback viewport range. Which reference is produced depends on
context:

| Context                           | Copied reference                              |
|-----------------------------------|-----------------------------------------------|
| Search active, hit selected       | `line:<hitEntryId>`                           |
| Cursor inside a multi-line block  | `range:<headId>..<endId>`                     |
| Otherwise (default)               | `range:<firstVisibleId>..<lastVisibleId>` (viewport) |

Paste the reference to an agent. It resolves `line:<id>` via `get_line` and
`range:<a>..<b>` via `get_range`.

**`Y` — Copy text as displayed (TUI).** Press `Y` (capital) to copy the selected
text as displayed — same contexts as `y` (search hit, focused block, or viewport),
but the **text itself** instead of a reference. Output is plain text with no ANSI
color codes, with `[group] filename:` prefixes, and pretty-printed JSON/XML blocks
kept as formatted. Text is copied to the clipboard via OSC 52. Very large
selections may exceed the terminal's OSC 52 size limit; use `s` (save viewport) or
`S` (save full buffer) as the escape hatch.

The MCP server runs alongside stdout/SSE/TUI and is **not** started in
`--once` mode. No authentication — bind to loopback only.

### TUI

Auto-selected when stdout is a TTY and `--no-tui` was not passed.
`--once` mode never uses the TUI.

### Per-group toggling and prefix columns

The TUI footer shows the current state: `events: 1234 · tail · col: 0 ·
groups: 3 (1 off) · files: 14`. Disabled-group event count goes up but
those events are hidden from the stream view (the watcher is still
tailing them, and stdout / SSE consumers still see them — filtering is
TUI-only).

Open the **Ctrl+G** "groups" panel to see all defined groups, their
enable state (`ON` / `OFF`), the digit key that toggles each, and a
count of files currently assigned to each. Digit keys `1`–`9` toggle
groups directly from the stream view too; only the first nine groups
are keyboard-addressable, the rest stay always-on.

**Ctrl+P** hides/shows the `[group]` prefix column, **Ctrl+L** hides/
shows the `basename:` column. With both off you get just the log body —
useful when you only care about content. Toggles are instant; the
scrollback isn't rebuilt (the prefix is composed at render time, so the
toggle has near-zero overhead per event).

### Renderer toggling

Renderers can be toggled at runtime via the shifted-digit keys (`!`
`@` `#` `$` `%` `^` `&` `*` `(`) — the same idea as `1`–`9` for
groups, but for the renderers list. **Toggling re-renders the entire
scrollback live**: a JSON pretty-print block turns back into the raw
log line, and toggling on regenerates the pretty-print from the same
raw source. The pipeline is updated atomically so future events also
honor the new state. Stdout and SSE consumers see the change for new
events only — they don't have a scrollback to re-render.

`Ctrl+E` opens the **Renderers** overlay, mirroring the Groups panel:
each renderer is listed with its toggle key, `ON`/`OFF` state, and
name. The footer status line shows `rend: N (M off)`.

Only the first 9 renderers are keyboard-addressable; beyond that they
stay always-on (use `disabled:` or `off:` in YAML to start one disabled).

### Collapsing multiline entries

Press **`m`** to switch the stream view into **collapsed** mode: any
row whose body starts with whitespace (Python tracebacks, indented
log continuations, etc.) is hidden, and the row above it gets a dim
`[...]` suffix to signal there's more. JSON/XML pretty-print blocks
also collapse — the head that triggered the renderer keeps showing,
the block lines vanish behind the marker.

Press `m` again to expand. The toggle is TUI-only; stdout and SSE
consumers always see the full content. Search and group filtering
respect the collapsed state — a hit inside a hidden continuation is
not surfaced, mirroring how a hit in a disabled group isn't surfaced.

### Search

Press **`/`** to enter search mode. Type the term — the footer shows
what's being typed — and press **Enter** to find the first hit, or
**Esc** to cancel. The match is case-insensitive substring matching
(no regex), runs against the rendered line body, and respects the
group enable/disable toggles (hits in disabled groups are skipped).

Once committed, every visible occurrence is highlighted with a yellow
background; the **current hit** is marked with a brighter red
background. The viewport jumps to (and centers) the current hit row.

Use **`n`** to walk forward through hits and **`p`** to walk
backward. While a search term is active, **↑/↓** (and **`k`/`j`**) also
jump to the previous/next hit — `PgUp`/`PgDn` and `Ctrl/Shift+arrows`
still scroll, as an escape hatch. When there are no further hits in the
requested direction, the footer asks
`No more hits — wrap to top/bottom? (y/n)`. Press **y** to wrap, **n**
or **Esc** to dismiss without moving. Jumping to a hit also pans the
view horizontally when the matched term is off-screen, so the term is
always brought into view.

Press **`t`** to toggle **filter mode**: the stream collapses to only
the entries that contain the term. Filtering is whole-entry — if the
match is inside a rendered JSON/XML block, the entire block is shown
alongside its source line, so matched structured content is never
truncated. The footer shows a `filter` tag while it's on.

Pressing **`/`** then **Enter** with nothing typed re-runs the last
search term; the last term is remembered even after you clear the
active search.

Press **Esc** with no overlay open to clear the active search (term
goes away, highlights vanish, filter mode turns off). Pressing **End**
/ **G** to return to tail mode keeps the search term active, so the
next **n** continues walking forward.

### Tail mode vs browsing

On launch the TUI is in **tail mode**: the viewport is pinned to the
bottom and new events appear as they arrive. The moment you start
scrolling up (Up, PgUp, Ctrl+Up, Shift+Up, Home, `g`), you leave tail
mode and the viewport is **locked at the absolute position you're
looking at** — new events continue to be collected but the screen does
not move. Press **End** (or `G`) to re-stick to the latest. Scrolling
down past the bottom also re-sticks automatically.

The status footer shows `tail` when pinned, or `@<top>/<total>` while
browsing, so you can see at a glance whether the view is live.

### Keybindings

The table below lists the **Linux/Windows** defaults. On **macOS** the same
keys are shown with native glyphs (`⌃` Ctrl, `⌥` Option, `⇧` Shift, `⎋` Esc,
`⇥` Tab), and fast scrolling advertises `Shift`+Arrow as its primary binding —
`Ctrl`+Arrow is intercepted by macOS Mission Control / Spaces before a terminal
app can see it (it stays bound as a fallback; PgUp/PgDn always page). A terminal
can't capture the ⌘ key, so no shortcut uses Cmd.

Every binding is an *action* you can remap — see **Customizing keybindings**
below. The authoritative per-OS reference is generated from the code into
[`KEYBINDINGS.md`](KEYBINDINGS.md) (regenerate with
`./build.sh keybindings-docs`, or print it with `log-listener --keybindings-doc`).

| Key                 | Action                                                |
|---------------------|-------------------------------------------------------|
| `q` / Ctrl+C        | Quit.                                                 |
| Tab / Ctrl+I        | Toggle the "watched files" overlay.                   |
| **Ctrl+G**          | **Toggle the "groups" overlay (enable/disable list).**|
| Esc                 | Close any open overlay.                               |
| **Ctrl+P**          | **Toggle the `[group]` prefix column.**               |
| **Ctrl+L**          | **Toggle the `basename:` prefix column.**             |
| **`1` … `9`**       | **Toggle the N-th group on/off in the stream.**       |
| **Ctrl+R**          | **Clear the TUI scrollback (watcher / SSE keep running).** |
| **`/`**             | **Start a search. Type the term, Enter to find, Esc to cancel.** |
| **`n`**             | **Jump to the next hit (prompts to wrap if none below).** |
| **`p`**             | **Jump to the previous hit (prompts to wrap if none above).** |
| **`t`**             | **Toggle filter mode: show only entries containing the search term (whole JSON/XML blocks kept).** |
| ↑ / `k`             | Prev hit when searching; otherwise scroll one line up (unsticks tail). |
| ↓ / `j`             | Next hit when searching; otherwise scroll one line down. |
| Ctrl+↑ / Shift+↑    | Scroll up 10 lines.                                   |
| Ctrl+↓ / Shift+↓    | Scroll down 10 lines.                                 |
| PgUp / Ctrl+B       | Scroll up by one screen.                              |
| PgDn / Ctrl+F / Spc | Scroll down by one screen.                            |
| **Home** / `g`      | **Jump to the first (oldest) line.**                  |
| **End** / `G`       | **Jump to the latest line and re-stick to tail mode.**|
| ← / `h`             | Pan view left 10 columns.                             |
| → / `l`             | Pan view right 10 columns.                            |
| Ctrl+← / Shift+←    | Pan view left 50 columns.                             |
| Ctrl+→ / Shift+→    | Pan view right 50 columns.                            |
| `0`                 | Jump back to column 0 (leftmost).                     |
| **`Ctrl+E`**        | **Toggle the "renderers" overlay.**                   |
| **`!` `@` `#` `$` `%` `^` `&` `*` `(`** | **Toggle renderer 1–9 on/off (shifted digits).** |
| **`m`**             | **Collapse multiline entries to one line + `[...]`.** |
| **`s`**             | **Save the visible viewport to a `screen-log-listener-*.txt` file (cwd).** |
| **`S`**             | **Save the full scrollback buffer to a `screen-log-listener-*.txt` file (cwd).** |
| **`]`** / **`[`**   | **Jump to the next / previous multi-line block.**     |
| **`}`** / **`{`**   | **Jump to the next / previous processor-matched block (e.g. exception).** |
| **`e`**             | **Toggle the exception left-bar marker.**             |
| **`y`**             | **Copy reference to clipboard (OSC 52) — `line:<id>`, block `range`, or viewport `range` depending on context (see MCP server section).** |
| **`Y`**             | **Copy selected text as displayed (no ANSI color, with prefixes, OSC 52) — same contexts as `y`; large selections may hit the OSC 52 size limit, use `s`/`S` to save instead.** |
| **`v`**             | **Enter visual line-selection mode: `space` sets the selection start; move with ↑/↓ or `j`/`k`; press `y` to copy the reference and exit, or `Y` to copy the text and exit; `esc` cancels.** |

When you pan horizontally (`←` / `→`), the visible window is clipped from
the left while ANSI styling (colors and the search highlight) is preserved
across the scrolled portion.

The stream view is a bounded ring buffer of pre-rendered display lines
(default 10000, configurable via `tui.scrollback` in YAML). Older lines
roll off when the buffer fills.

(Tab and Ctrl+I are bound to the same action because terminals transmit
the same byte — 0x09 — for both.)

### Customizing keybindings

Any action can be rebound from your `log-listener.yml`. Add a `keybindings:`
block whose sub-sections are an OS name (`darwin` / `linux` / `windows`) or
`default` (every OS). Each maps an **action name** to the list of keys that
trigger it; the list **replaces** the built-in default for that action.

```yaml
keybindings:
  default:                 # applies on every OS
    search: ["/"]
    quit:   ["q", "ctrl+c"]
  darwin:                  # macOS-only overrides
    fast_down: ["shift+down"]
  linux:
    fast_down: ["ctrl+down"]
```

Resolution is per-action: a current-OS section wins over `default`, which wins
over the built-in default. Giving an action a shorter key list is how you
*drop* one of its default keys. Loading fails fast on an unknown action name, an
unrecognized key token, a key bound to two actions, or a key that clashes with
the positional `1`–`9` / `!@#…` toggles — so a typo never silently does nothing.
Run `log-listener --keybindings-doc` for every action name and its defaults.

---

## Behavior details

### Log rotation

For every tailed file, the watcher tracks `(inode, position)`. On every
fsnotify event for the file's parent directory (plus a 500ms poll
safety-net), the tailer re-stats the path:

- `inode` changed → **rename rotation**. The remaining bytes on the old
  descriptor are drained, any partial line is flushed as a final line for
  the old context, the old fd is closed, the file is reopened at offset 0
  by path, and any bytes already present are emitted.
- `size < position` → **truncation**. The fd is rewound to offset 0, the
  partial-line buffer is flushed, and reading resumes.
- Neither → just read new bytes.

### Newly-created files

When fsnotify reports a `Create` event in a watched directory, the watcher
runs the same matching logic used for initial discovery (group filters,
plus the global filter). If a group accepts the file, a new tailer is
started **from offset 0** — Create events arrive before Write events, so
starting from EOF would miss initial content.

### Initial scan

At startup, every directory group is walked once; existing files that pass
the filter get a tailer that starts **at EOF** (matching `tail -f`
semantics). The full content of existing files is only emitted by `--once`
mode.

### Shutdown

First SIGINT (or SIGTERM):
- The watcher's context is cancelled.
- The main loop drains queued events for up to 200ms.
- The process exits with code 0.

Any subsequent signal hard-exits with code 130. The signal handler stays
attached for the lifetime of the process, so Ctrl+C never becomes a
no-op (a hazard with `signal.Notify` if the handler exits while the
default action is still suppressed).

### Pipe-mode auto-detection

If `os.Stdout.Stat().Mode() & os.ModeCharDevice == 0`, the output is piped
or redirected. In that case the TUI is skipped and color is forced off.
The renderer pipeline and SSE broadcast are unaffected.

### Group ordering across CLI and YAML

CLI groups (in declaration order) come first, then YAML groups whose
`(kind, id)` doesn't collide. This ordering determines first-match-wins,
both for file → group and for renderer dispatch.

---

## Performance characteristics

Built for tens-to-hundreds of log lines per second across dozens of files.
The watcher's events channel is bounded at 1024 entries; back-pressure on
slow consumers (TUI, SSE) is handled by per-channel buffers, not by
blocking the main pipeline. JSON marshaling for SSE happens once per
event; the SSE hub fans the same byte slice to all clients.

Lines can be large (the renderer reads up to 32 KiB per `Read`) and
templates can call `json(...)` on captures that are themselves large JSON
payloads — both are tested.

---

## Limitations and non-goals

- **Linux first.** Built for fsnotify on `inotify`. macOS works (fsnotify
  supports kqueue) but is less exercised. Windows is a future-milestone
  goal — not currently supported.
- **Recursive subdirectory creation IS handled** — see the "Pattern
  paths" section above. New subdirectories appearing inside a recursive
  group (or inside a watched glob expansion) at runtime get an fsnotify
  watch + a recursive scan, and their files are tailed as they appear.
- **`$10` ambiguity.** Multi-digit captures parse greedily —
  `$10` is capture 10, not "$1 followed by 0". Use a literal escape if
  you need that.
- **SSE addr default is localhost-only.** Binding to a non-loopback
  address is not blocked (you can do `--sse 0.0.0.0:8080`), but there's
  no built-in auth — don't expose the stream on a public interface.
- **TUI tests are partial.** The model's state transitions are unit-tested
  but a real terminal is required to exercise the rendering paths.
- **Startup delay in terminals that don't auto-answer OSC 11.** Bubbletea
  v1.3 calls `lipgloss.HasDarkBackground()` from its own `init()`, which
  blocks up to **termenv.OSCTimeout (5 s)** waiting for the terminal to
  report its background color. In a real terminal (xterm, iTerm2,
  Windows Terminal, etc.) this returns in milliseconds; under some pty
  wrappers, certain IDE-embedded terminals, or tmux without passthrough,
  it can take up to 5 s for `log-listener` to start up. After that one-
  time delay everything (SSE, TUI, stdout) works normally. Removed in
  bubbletea v2; track upstream for the fix.

---

## Development

There is no Makefile — `build.sh` (Unix) / `build.cmd` (Windows) wrap the
common `go` commands:

```bash
./build.sh test              # go test ./...
./build.sh vet               # go vet ./...
./build.sh race              # go test -race ./...
./build.sh build             # local binary
./build.sh build-static      # CGO_ENABLED=0 stripped binary
./build.sh keybindings-docs  # regenerate KEYBINDINGS.md from the keymap
```

Plain `go test ./...` works too; a single package is `go test ./internal/<pkg>/`
and a single test is `go test -run TestName ./internal/<pkg>/`.

Layout:

```
main.go, init.go           entry point + signal handling + sink/TUI wiring (package main, repo root)
internal/timeparse/        ISO 8601 + relative duration parser
internal/discover/         directory walk + file filter + group assignment
internal/watch/            fsnotify watcher + rotation-safe tailer
internal/config/           CLI parser + YAML loader + CLI/YAML merge
internal/render/           template DSL + JSON/XML + first-match pipeline
internal/sink/             colorized stdout + SSE broadcast hub
internal/tui/              bubbletea app with bounded scrollback + Ctrl+I overlay
internal/linebuf/          concurrency-safe ring of log records with stable IDs + block maps
internal/mcp/              embedded MCP server (Streamable HTTP) exposing read tools over linebuf
```

`PLAN.md` is the authoritative architecture document. `CHANGELOG.md` is
the human-readable per-phase change log. `CLAUDE.md` is the short guide
for Claude Code sessions in this repo.

---

## License

(Not yet declared.)
