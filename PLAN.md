# log-listener — Implementation Plan

A real-time multi-source log tailer with regex-based file discovery, configurable
line renderers, a TUI overlay, and an SSE stream for web clients.

## Design pillars

- **Single static Go binary**, Linux-first (Windows in a later milestone).
- **First-match-wins everywhere** — a file is assigned to the first directory/file
  group that matches it; a line is rendered by the first renderer that matches it.
- **Discovery filter ≠ line filter.** Directory-group rules pick *which files*
  to watch (name regex + mtime). Renderers match *line content* and transform it.
- **Lines flow through by default.** A line that no renderer matches is emitted
  as-is, unless `drop_unmatched: true`.
- **Pipe-aware.** When stdout is not a TTY, TUI and color auto-disable.
- **One terminal session, three concurrent outputs:** stdout, optional SSE, TUI.

## Module layout

```
log-listener/
├── cmd/log-listener/main.go         entry point, signal handling, wiring
├── internal/
│   ├── config/                      CLI + YAML config
│   │   ├── cli.go                   -d/-dN, -r/-rN, -R, -f/-fN, --config, --once
│   │   ├── yaml.go                  loader: --config → ./log-listener.yml → ~/.log-listener.yml
│   │   ├── merge.go                 CLI overrides YAML
│   │   └── validate.go              cross-field validation, friendly errors
│   ├── timeparse/parse.go           ISO 8601 + relative durations (1h, 2d, 30m)
│   ├── discover/
│   │   ├── walker.go                recursive walk, glob expansion
│   │   ├── filter.go                name_regex, exclude_regex, older, younger
│   │   └── assign.go                first-match-wins file → group
│   ├── watch/
│   │   ├── watcher.go               fsnotify; new files in watched dirs
│   │   ├── rotation.go              inode change + size-decrease detection
│   │   └── tailer.go                per-file line streamer; resumes after rotation
│   ├── render/
│   │   ├── matcher.go               line → first matching renderer
│   │   ├── template.go              DSL parser: literals, $N, json($N), xml($N)
│   │   ├── json.go                  pretty-print
│   │   └── xml.go                   pretty-print
│   ├── sink/
│   │   ├── event.go                 typed event model
│   │   ├── pipeline.go              fan-out: event → [stdout, sse, tui]
│   │   ├── stdout.go                colorized (or plain in pipe mode)
│   │   └── sse.go                   localhost HTTP/SSE server
│   └── tui/
│       ├── app.go                   bubbletea root
│       ├── stream.go                live log view + scrollback
│       └── files_panel.go           Ctrl+I overlay (effectively-watched files)
└── log-listener.yml                 example config
```

## Data flow

```
            ┌──────────────┐
  paths ──▶ │  discover    │ recursive walk + glob + file-filter
            └──────┬───────┘
                   │
            ┌──────▼───────┐
            │   assign     │ first directory/file group that matches owns the file
            └──────┬───────┘
                   │
            ┌──────▼───────┐
            │   watcher    │ fsnotify: new files, rotation, append
            └──────┬───────┘
                   │ raw line + source file
            ┌──────▼───────┐
            │   render     │ first renderer (scoped via applies_to) wins
            └──────┬───────┘
                   │ Event{file, ts, raw, captures, rendered_parts}
            ┌──────▼───────┐
            │   sink       │ fan-out
            └──┬────┬───┬──┘
               │    │   │
            stdout SSE TUI
```

## Event model (used by SSE & TUI)

```json
{
  "ts": "2026-05-28T12:34:56.123Z",
  "file": "/var/log/a/run-42.log",
  "group": "d1",
  "raw": "2026-05-28 ERROR {\"u\":\"bob\"}",
  "renderer": "app-json",
  "captures": ["2026-05-28", "ERROR", "{\"u\":\"bob\"}"],
  "rendered": [
    {"type":"text","value":"2026-05-28 ERROR\n"},
    {"type":"json","value":{"u":"bob"}}
  ]
}
```

When no renderer matched: `renderer: null`, `rendered: [{type:"text", value:<raw>}]`.

## Renderer template DSL

```
line_regex: '(\d{4}-\d{2}-\d{2}) \[(\w{4,5})\] (\{.*\})'
template:   '$1 $2\njson($3)'
```

- Literal text + `$N` (capture group) + renderer functions `json(...)`, `xml(...)`.
- `json(...)` and `xml(...)` always emit on a new line (per design decision).
- One renderer per line; first match wins.
- `applies_to: {groups: [d1, d2], paths: ['*.app.log']}` scopes a renderer to
  specific directory/file groups and/or path globs (omit = global).

## Example config

```yaml
directories:
  - id: default                   # -d
    paths: [/var/log/app-a, /var/log/app-b]
    recursive: true
    file_filter:
      name_regex: '\.log$'
      exclude_regex: '\.gz$|/archive/'
      older: 2026-01-01
      younger: 24h
  - id: 1                         # -d1
    paths: [/var/log/special]
    file_filter:
      name_regex: 'panic-.*\.log'

files:
  - id: default                   # -f
    paths: ['/tmp/output-*.log']
  - id: 1                         # -f1
    paths: ['/var/log/system.log']

global_file_filter:               # -R, applied to every -d group
  younger: 7d

renderers:
  - name: app-json
    line_regex: '(\d{4}-\d{2}-\d{2}) \[(\w{4,5})\] (\{.*\})'
    template: '$1 $2\njson($3)'
    applies_to:
      groups: [d1]
      paths: ['*.app.log']

output:
  color: true                     # ignored when not a TTY
  drop_unmatched: false
  sse:
    enabled: true
    addr: '127.0.0.1:8080'

tui:
  enabled: true                   # ignored when not a TTY
```

## CLI ↔ YAML mapping

| CLI flag                       | YAML field                                  |
|--------------------------------|---------------------------------------------|
| `-d a b`                       | `directories[id=default].paths = [a,b]`     |
| `-d1 c`                        | `directories[id=1].paths = [c]`             |
| `-r name:.log older:2026-01-01`| `directories[id=default].file_filter`       |
| `-r1 ...`                      | `directories[id=1].file_filter`             |
| `-R younger:7d`                | `global_file_filter`                        |
| `-f path1 path2`               | `files[id=default].paths`                   |
| `-fN path`                     | `files[id=N].paths`                         |
| `--config path`                | (selects which YAML to load)                |
| `--once`                       | one-shot scan, no watch                     |
| `--no-tui` / `--no-color`      | override `tui.enabled` / `output.color`     |
| `--sse 127.0.0.1:8080`         | `output.sse`                                |

Config file resolution order: `--config <path>` → `./log-listener.yml` →
`~/.log-listener.yml`. CLI flags override YAML.

## Key design decisions

1. **Rotation detection.** Per-file tailer tracks `(inode, size)`. Rotation =
   inode changed OR size decreased. On rotation: drain old fd (read remaining
   bytes), close, reopen by path, reset offset to 0. Deduplication is by
   `(inode, offset)` — never by content.
2. **Cross-group dedup.** Same path under multiple `-d` groups → first match
   wins in declaration order. Each file has exactly one owning group.
3. **Recursion excludes.** Same `exclude_regex` mechanism as include —
   no gitignore semantics.
4. **Shutdown.** First SIGINT → stop watchers, drain queued events, flush sinks,
   exit 0. Second SIGINT within 2s → hard exit 130.
5. **SSE security.** Binds `127.0.0.1` by default. No auth. User can override
   address in config, but we warn loudly if they bind to non-loopback.
6. **TUI buffer.** Bounded ring buffer (e.g., 10k lines) to keep memory steady
   under sustained throughput. Older lines age out.
7. **Empty captures.** A `$N` referencing a capture group that didn't participate
   in the match expands to the empty string.

## Per-phase workflow

Each phase ends with:

1. **Tests written** covering basic functionality of the modules touched
   (table-driven `_test.go` files alongside source).
2. **`go test ./...` passes** — no skipped or failing tests.
3. **`go vet ./...` passes**.
4. **Single git commit** scoped to that phase, with a message like
   `phase N: <short description>`.

If a phase introduces a public-ish API (e.g., the renderer DSL), include at
least one test that exercises the full happy path of that API.

## Phased delivery

### Phase 1 — Core CLI + raw tailing
**Scope:** prove the watcher + discovery + first-match-wins assignment.
- `internal/config/cli.go` for `-d/-dN`, `-r/-rN`, `-R`, `-f/-fN`, `--once`
- `internal/timeparse`, `internal/discover` (walker, filter, assign)
- `internal/watch` (watcher, rotation, tailer)
- Plain stdout writer: `<file>: <raw line>`
- Signal handling (graceful + hard exit)

**Done when:** running `log-listener -d /tmp/logs -r name:.log` prints new
appended lines from `*.log` files under `/tmp/logs`, picks up new files,
survives rotation, and exits cleanly on Ctrl+C.

### Phase 2 — YAML config
**Scope:** make CLI + YAML interchangeable.
- `internal/config/{yaml,merge,validate}.go`
- Config resolution: `--config` → CWD → `~/`
- Example `log-listener.yml`

**Done when:** any Phase 1 CLI invocation can be expressed in YAML with
identical behavior; CLI overrides YAML.

### Phase 3 — Renderer pipeline
**Scope:** the rendering DSL and JSON/XML built-ins.
- `internal/render/template.go` (parser + executor)
- `internal/render/{json,xml}.go`
- `internal/render/matcher.go` with `applies_to` scoping
- `drop_unmatched` option
- Sink emits typed `Event` (even if only stdout consumes it for now)

**Done when:** sample log with mixed JSON-tail lines renders prettified JSON
on a new line; non-matching lines flow through; `drop_unmatched` filters them.

### Phase 4 — Output sinks: color stdout + SSE
**Scope:** complete the non-TUI output story.
- `internal/sink/stdout.go` with color (auto-disabled when not a TTY)
- `internal/sink/sse.go`: minimal HTTP server, single endpoint `GET /stream`
  serving SSE; clients receive Event JSON
- `--sse` flag; `--no-color`

**Done when:** a browser tab connected to `http://127.0.0.1:8080/stream`
receives the same events shown on stdout, in real time.

### Phase 5 — TUI
**Scope:** the interactive overlay.
- `internal/tui/app.go` (bubbletea)
- Live log stream view with bounded scrollback
- `Ctrl+I` overlay: scrollable list of effectively-watched files (with group id)
- Esc/Ctrl+I again to dismiss
- Auto-disabled when not a TTY

**Done when:** running interactively shows live colorized logs; Ctrl+I opens
the file panel; second Ctrl+C exits cleanly.

### Phase 6 — Polish
- README with examples + screenshots
- Static-binary build (`CGO_ENABLED=0`)
- Smoke tests with temp dirs (file create / append / rotate / delete)
- Linux-only abstractions isolated behind interface for future Windows work

## Confirmed semantics

- **Renderer precedence:** first match wins by YAML declaration order
  (top-to-bottom).
- **`applies_to` with both `groups` and `paths`:** AND — a file must be in
  one of the listed groups *and* match one of the path globs.
- **One-shot (`--once`) mode:** renderers still run; the primary motivation
  for one-shot is pretty-printing JSON in existing files.
- **TUI scrollback:** default 10k lines, configurable via `tui.scrollback`.
