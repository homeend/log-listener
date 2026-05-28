# log-listener

A real-time multi-source log tailer with regex-based file discovery,
configurable line renderers, an interactive TUI, and an SSE stream for web
clients. Single static Go binary, Linux-first.

## Why

You run an application that produces many log files per run (often new files,
sometimes rotated). You want to:

- Watch a set of directories / files in real time.
- Filter which files get watched by filename regex and modification time.
- Pretty-print embedded JSON/XML inside log lines.
- Optionally stream the same events to a web UI via SSE.
- Optionally drive an interactive terminal UI alongside the stream.

## Install

Requires Go 1.26+:

```bash
git clone <this-repo> && cd log-listener
make build
./log-listener -d /var/log/myapp -r 'name:\.log$'
```

Or build a fully-static binary:

```bash
make build-static
```

## Quick start — CLI

```bash
# Tail every .log file under /var/log/app, recursively:
log-listener -d /var/log/app -r 'name:\.log$'

# Two directory groups with different rules:
log-listener \
    -d  /var/log/app-a    -r  'name:\.log$' 'younger:1h' \
    -d1 /var/log/app-b    -r1 'name:panic'

# A specific file (no filter — file groups are always watched as-is):
log-listener -f /tmp/output.log

# One-shot scan of existing content (no tailing):
log-listener -d /var/log/app --once
```

### CLI reference

| Flag                              | Effect                                                  |
|-----------------------------------|---------------------------------------------------------|
| `-d <paths…>` / `-dN <paths…>`    | Directory group `default` / group `N`.                  |
| `-r <tokens…>` / `-rN <tokens…>`  | File-filter for the matching directory group.           |
| `-R <tokens…>`                    | Global file-filter applied to all directory groups.     |
| `-f <paths/globs…>` / `-fN …`     | File group `default` / `N` (unfiltered, glob-expanded). |
| `--config <path>`                 | Explicit YAML config path.                              |
| `--once`                          | Scan existing content and exit; do not tail.            |
| `--no-tui`                        | Disable the interactive TUI even on a TTY.              |
| `--no-color`                      | Disable ANSI color in stdout.                           |
| `--sse <addr>`                    | Enable SSE broadcast on `addr` (e.g. `127.0.0.1:8080`). |

Rule tokens (used with `-r`, `-rN`, `-R`):

| Token              | Meaning                                                 |
|--------------------|---------------------------------------------------------|
| `name:<regex>`     | Filename (basename) must match.                         |
| `exclude:<regex>`  | Filename matching this is excluded.                     |
| `older:<when>`     | File mtime must be **before** `<when>`.                 |
| `younger:<when>`   | File mtime must be **after** `<when>`.                  |

`<when>` is either an ISO 8601 date/datetime (`2026-01-15`,
`2026-01-15T10:30:00`, `2026-01-15T10:30:00Z`) or a relative duration:
`30s`, `15m`, `1h`, `2d`, `1w`.

## Quick start — YAML

Drop a `log-listener.yml` next to where you run from (or pass `--config`):

```yaml
directories:
  - id: default
    paths: [/var/log/app-a, /var/log/app-b]
    recursive: true
    file_filter:
      name_regex: '\.log$'
      exclude_regex: 'archive'
      younger: 24h
  - id: 1
    paths: [/var/log/special]
    file_filter:
      name_regex: 'panic-.*\.log'

files:
  - id: default
    paths: ['/tmp/output-*.log']

global_file_filter:
  younger: 7d

renderers:
  - name: app-json
    line_regex: '(\d{4}-\d{2}-\d{2}) \[(\w{4,5})\] (\{.*\})'
    template: '$1 $2\njson($3)'
    applies_to:
      groups: [1]
      paths: ['*.app.log']

output:
  color: true
  drop_unmatched: false
  sse:
    enabled: true
    addr: '127.0.0.1:8080'

tui:
  enabled: true
  scrollback: 10000
```

CLI flags override YAML values. CLI groups with the same `id` replace the
YAML version entirely.

Config file resolution order (first match wins):

1. `--config <path>` (must exist or it's an error)
2. `./log-listener.yml`
3. `~/.log-listener.yml`

## Renderer template DSL

```
line_regex: '(\d{4}-\d{2}-\d{2}) \[(\w{4,5})\] (\{.*\})'
template:   '$1 $2\njson($3)'
```

- Literal text, `$N` (regex capture group N; `$0` = full match).
- Renderer functions: `json(...)`, `xml(...)`.
- Escapes: `\n`, `\t`, `\r`, `\\`. `$$` produces a literal `$`.
- One renderer per line, first match wins by YAML declaration order.
- `applies_to.groups` AND `applies_to.paths` — both must match if set;
  empty means vacuously true for that dimension. Path globs are tested
  against both full path and basename.
- Invalid input to `json(...)` / `xml(...)` falls back to a text part so
  output is never dropped.

## SSE consumer

```bash
curl -N http://127.0.0.1:8080/stream
```

Each event arrives as one SSE message:

```
data: {"ts":"2026-05-28T12:34:56Z","file":"/var/log/a.log","group":"d1",
       "raw":"...", "renderer":"app-json","captures":["...","..."],
       "rendered":[{"type":"text","value":"..."},{"type":"json","value":{...}}]}
```

Slow clients see their events drop silently (per-client buffer = 256
events); other clients are unaffected.

## TUI shortcuts

| Key            | Action                                  |
|----------------|-----------------------------------------|
| `q` / Ctrl+C   | Quit.                                   |
| Tab / Ctrl+I   | Toggle the "watched files" overlay.     |
| Esc            | Close the files overlay.                |
| ↑ / `k`        | Scroll up.                              |
| ↓ / `j`        | Scroll down.                            |
| `g` / `G`      | Jump to top / bottom.                   |

The TUI auto-disables when stdout isn't a TTY (e.g. piped to another
command). SSE and the renderer pipeline still run.

## Development

```bash
make test       # unit tests
make vet        # go vet
make race       # tests with -race
make build      # local binary
make build-static  # CGO_ENABLED=0 static binary
```

See `PLAN.md` for the architecture and the per-phase commit history;
`CHANGELOG.md` for the human-readable change log.
