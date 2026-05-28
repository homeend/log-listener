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
- **Three output destinations, in parallel.** A colorized stdout sink, an
  interactive bubbletea TUI, and an HTTP/SSE broadcast — any combination.
  Output downgrades automatically when stdout isn't a TTY (no TUI, no
  color).
- **One-shot mode.** `--once` scans existing content (renderers still apply
  — useful for prettifying JSON inside historical logs) and exits.
- **YAML configuration with CLI precedence.** Anything you can do on the CLI
  you can do in `log-listener.yml`; CLI flags win on conflict.
- **Static binary, four runtime dependencies.** `fsnotify`, `yaml.v3`,
  `bubbletea`, `lipgloss`. No CGO.

---

## Install

Requires Go 1.26+.

```bash
git clone <this-repo> && cd log-listener
make build              # ./log-listener
make build-static       # CGO_ENABLED=0, stripped
```

On Linux, `make build-static` produces a fully static binary
(`-extldflags "-static"`). On macOS the static linker flag is a no-op but
the resulting binary is still CGO-free and reproducible.

A self-contained smoke test:

```bash
make demo               # creates a tempdir, seeds a log line, tails it
```

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

### `applies_to` semantics

`groups` and `paths` are AND-combined; empty means vacuously true. Path
globs are tried against the full path and the basename, so both
`*.app.log` and `/var/log/foo/*.app.log` work.

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
| `--sse <addr>`                    | Enable the SSE broadcast on `addr` (e.g. `127.0.0.1:8080`).           |

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

# files — corresponds to -f / -fN; always unfiltered
files:
  - id: default
    paths: ['/tmp/run-*.log']       # glob-expanded
  - id: 1
    paths: ['/var/log/system.log']

# Applied to every directory group above. CLI -R wins entirely if present.
global_file_filter:
  younger: 7d

# Renderers — see "Renderer pipeline" below for the template DSL
renderers:
  - name: app-json
    line_regex: '(\d{4}-\d{2}-\d{2}) \[(\w{4,5})\] (\{.*\})'
    template: '$1 $2\njson($3)'
    applies_to:
      groups: [1]
      paths: ['*.app.log']

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

### CLI ↔ YAML precedence

- Scalar fields (`--no-color`, `--no-tui`, `--sse`, etc.): CLI wins if
  explicitly set; otherwise YAML applies.
- Groups: same `(kind, id)` → CLI's version replaces YAML's. Different
  ids are merged (CLI groups first, YAML-only groups appended).
- Renderers: only YAML defines them.
- `-R` rules: if any `-R` token is on the CLI, YAML's
  `global_file_filter` is dropped entirely.

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

### TUI

Auto-selected when stdout is a TTY and `--no-tui` was not passed.
`--once` mode never uses the TUI.

| Key                 | Action                                     |
|---------------------|--------------------------------------------|
| `q` / Ctrl+C        | Quit.                                      |
| Tab / Ctrl+I        | Toggle the "watched files" overlay.        |
| Esc                 | Close the files overlay.                   |
| ↑ / `k`             | Scroll one line up.                        |
| ↓ / `j`             | Scroll one line down.                      |
| Ctrl+↑ / Shift+↑    | Scroll up 10 lines.                        |
| Ctrl+↓ / Shift+↓    | Scroll down 10 lines.                      |
| PgUp / Ctrl+B       | Scroll up by one screen.                   |
| PgDn / Ctrl+F / Spc | Scroll down by one screen.                 |
| ← / `h`             | Pan view left 10 columns.                  |
| → / `l`             | Pan view right 10 columns.                 |
| Ctrl+← / Shift+←    | Pan view left 50 columns.                  |
| Ctrl+→ / Shift+→    | Pan view right 50 columns.                 |
| Home / `0`          | Jump back to column 0 (leftmost).          |
| End / `$`           | Jump right to reveal the end of widest line.|
| `g`                 | Jump to top of stream / files list.        |
| `G`                 | Jump to bottom (pin to live) / list end.   |

When you pan horizontally (`←` / `→`), the visible window is clipped from
the left and ANSI styling is dropped for the scrolled portion — that's a
tradeoff to keep the implementation simple and reliable.

The stream view is a bounded ring buffer of pre-rendered display lines
(default 10000, configurable via `tui.scrollback` in YAML). Older lines
roll off when the buffer fills.

(Tab and Ctrl+I are bound to the same action because terminals transmit
the same byte — 0x09 — for both.)

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
- **No recursive subdirectory creation handling.** If a *new* subdirectory
  appears under a recursive group at runtime, files created inside it
  aren't observed (the parent directory watches at startup don't extend
  to subdirs created later). Files created in **existing** subdirs are
  fine.
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

```bash
make test          # unit tests (8 packages)
make vet           # go vet
make race          # go test -race
make build         # local binary
make build-static  # CGO_ENABLED=0 stripped binary
make demo          # self-contained tail of a tempdir
```

Layout:

```
cmd/log-listener/          entry point + signal handling + sink/TUI wiring
internal/timeparse/        ISO 8601 + relative duration parser
internal/discover/         directory walk + file filter + group assignment
internal/watch/            fsnotify watcher + rotation-safe tailer
internal/config/           CLI parser + YAML loader + CLI/YAML merge
internal/render/           template DSL + JSON/XML + first-match pipeline
internal/sink/             colorized stdout + SSE broadcast hub
internal/tui/              bubbletea app with bounded scrollback + Ctrl+I overlay
```

`PLAN.md` is the authoritative architecture document. `CHANGELOG.md` is
the human-readable per-phase change log. `CLAUDE.md` is the short guide
for Claude Code sessions in this repo.

---

## License

(Not yet declared.)
