# log-listener FAQ

Task- and problem-oriented answers. For the full reference see
[`README.md`](README.md); for every key see [`KEYBINDINGS.md`](KEYBINDINGS.md).

---

## Getting started

### How do I just watch a directory and see new lines as they arrive?

```bash
log-listener -d /var/log/app -r 'name:\.log$'
```

`-d` is the directory, `-r` is the filter (`name:` is a basename regex). The
TUI opens automatically when stdout is a terminal; new lines stream in at the
bottom (tail mode).

### I ran it and nothing shows up. Why?

By design, existing files are tailed **from their end** (`tail -f` semantics) —
you only see lines written *after* startup. Three things to check:

1. **No new lines yet.** Append a line (`echo hi >> /var/log/app/x.log`) and
   confirm it appears.
2. **Your filter rejected the files.** `-r 'name:\.log$'` only keeps `*.log`.
   Open the **Tab** ("watched files") overlay in the TUI to see exactly which
   files were picked up.
3. **You wanted the *existing* content.** Use `--once` (below).

### How do I see what's *already* in the files, not just new lines?

```bash
log-listener -d /var/log/app -r 'name:\.log$' --once
```

`--once` scans existing content (renderers still run, so it's great for
pretty-printing historical JSON) and exits. It never opens the TUI.

### Is there a quick way to get a config for a known app?

Yes — `init` generates one:

```bash
log-listener init goland junie       # writes ./log-listener.yml
log-listener init --list             # show available apps/bundles
log-listener init jetbrains -o -     # print to stdout instead of writing
```

It resolves each app's log locations for your OS, keeps the directories that
actually exist, and attaches sensible renderers.

---

## Discovering & watching files

### How do I watch several places with different rules at once?

Use numbered groups. Each `-dN` pairs with its own `-rN`:

```bash
log-listener \
    -d  /var/log/app-a -r  'name:\.log$' 'younger:1h' \
    -d1 /var/log/app-b -r1 'name:panic-' 'exclude:\.gz$'
```

`-d`/`-r` with no number is the `default` group; `-d1`/`-r1`, `-d2`/`-r2`, … are
distinct groups with their own rules.

### A file matches two groups — will I see duplicate lines?

No. **First-match-wins**: each file is owned by the first group (in declaration
order) whose filter accepts it. It never appears under two groups, and its lines
are never duplicated.

### I want to tail specific files, not a whole directory.

Use a file group (`-f`), which accepts globs and is **always unfiltered**:

```bash
log-listener -f '/tmp/run-*.log' '/var/log/system.log'
```

### The directory/file I want doesn't exist yet — it's created at runtime. Will it be picked up?

Yes. Both `-d` and `-f` accept glob patterns evaluated **at startup and at
runtime**. A brand-new directory that could lead to a pattern match is watched
and scanned automatically:

```bash
log-listener -d '/tmp/acp-logs-*/sub' -r 'name:\.log$'
# when /tmp/acp-logs-XYZ/sub/ appears later, its .log files start tailing
```

New files inside an already-watched directory are started from offset 0 (so you
don't miss their first lines).

### How do I filter by age — only recent logs, or only old ones?

Rule tokens `younger:` / `older:` take an absolute date/time or a relative
duration (`30s`, `15m`, `1h`, `2d`, `1w`, meaning *now minus that*):

```bash
log-listener -d /var/log/app -r 'name:\.log$' 'younger:2d'   # last 2 days
```

### My log file gets rotated/truncated. Will tailing survive it?

Yes. The watcher detects rename-rotation (inode change) and truncation (size
decrease): it drains the old descriptor, flushes any partial line, and reopens
by path. No restart needed.

---

## Rendering — making logs readable

### My logs have JSON blobs crammed on one line. How do I pretty-print them?

Add a renderer that captures the JSON and runs it through `$json(...)`. In
`log-listener.yml`:

```yaml
renderers:
  - name: app-json
    line_regex: '(\d{4}-\d{2}-\d{2}) \[(\w{4,5})\] (\{.*\})'
    template: '$1 $2\n$json($3)'
```

`$N` inserts capture group N; `$json($N)` / `$xml($N)` parse and indent it. The
block always starts on its own line. `--once` is handy for prettifying existing
logs this way.

### What if a line *looks* like JSON but isn't (e.g. `{KEY=value}`)?

It's left alone. A renderer only matches if its `line_regex` matches **and** its
`json()`/`xml()` calls actually parse. A parse failure makes the renderer fall
through to the next one (or to the raw line) — malformed `{…}` is never split or
mangled, and output is never silently dropped.

### I have several renderers — which one wins for a given line?

First-match-wins again: each line is offered to renderers in declaration order;
the first whose `applies_to` scope and `line_regex` match wins. Scope a renderer
to specific groups/files with `applies_to` (`groups` and `paths`, AND-combined).

### Can I flip a renderer (e.g. raw vs. pretty JSON) without restarting?

Yes, live in the TUI. Shifted-digit keys `!@#$%^&*(` toggle renderers 1–9;
**toggling re-renders the entire scrollback** — a JSON block turns back into the
raw line and vice-versa. **Ctrl+E** opens the Renderers overlay showing each
renderer's key and ON/OFF state.

---

## Cutting noise

### How do I drop noisy lines entirely (health checks, debug spam)?

Use `mute:` in YAML — it drops matching lines **before** any sink (stdout, SSE,
TUI):

```yaml
matchers:
  health-noise: { line_regex: 'GET /health' }
mute:
  - matcher: health-noise
  - line_regex: 'DEBUG'
    applies_to: { groups: [1] }   # optional scope
```

### How do I keep only lines a renderer matched, discarding the rest?

```yaml
output:
  drop_unmatched: true
```

Unmatched lines are discarded silently, so the renderer pipeline acts as a
filter too. (Default is `false`: unmatched lines pass through as raw text.)

### How do I hide a group's noise on screen but still feed it to stdout/SSE?

Press the group's digit key (`1`–`9`) in the TUI, or **Ctrl+G** for the groups
panel. Disabling a group hides it from the stream view only — the watcher keeps
tailing it and stdout/SSE consumers still receive it. Filtering is TUI-only.

### What's the difference between `off:` and `disabled:` on a group/renderer?

- **`off: true`** — *soft*: loaded normally but its TUI toggle starts in the off
  position. You can turn it back on with the key/panel.
- **`disabled: true`** — *hard*: filtered out at load time, never reaches the
  pipeline/TUI. The keyboard can't bring it back. Use it to mothball a config
  block without deleting it. If both are set, `disabled` wins.

---

## Navigating the TUI

### How do I scroll back through history? It keeps jumping to the bottom.

The TUI launches in **tail mode** (pinned to the newest line). The moment you
scroll up (Up/PgUp/Home/`g`), it locks to where you're looking and stops
following new events. Press **End** (or `G`) to re-stick to the latest; scrolling
down past the bottom re-sticks automatically. The footer shows `tail` when live
or `@<top>/<total>` while browsing.

### How do I search?

Press **`/`**, type the term, **Enter** to jump to the first hit (**Esc**
cancels). Search is **smart-case**: case-insensitive unless your query has an
uppercase letter. Then:

- **`n`** / **`p`** — next / previous hit (also **↑/↓** while a search is active).
- **`t`** — filter mode: collapse the stream to only entries containing the term
  (whole JSON/XML blocks kept).
- **Ctrl+R** *while the search box is open* — toggle regex mode (footer shows
  `/(regex) `; prefix with `(?i)` for case-insensitive).
- **Esc** with no overlay open — clear the search.

### My lines are too long and run off the right edge.

Two options:

- **Pan horizontally** with ←/→ (10 cols) or Ctrl/Shift+←/→ (50 cols); `0`
  returns to the left edge.
- **Word wrap** with **`w`** — long lines wrap to multiple rows instead of being
  clipped. (Horizontal pan is disabled while wrapping; footer shows `wrap`.)

### Multi-line entries (stack traces, JSON blocks) clutter the view. Can I collapse them?

Press **`m`**. Continuation rows (anything starting with whitespace, plus
JSON/XML blocks) are hidden and the head row gets a dim `[...]` marker. Press
**`m`** again to expand. It's TUI-only — stdout/SSE always see full content.

### How do I jump between blocks or exceptions?

- **`]`** / **`[`** — next / previous multi-line block.
- **`}`** / **`{`** — next / previous processor-matched block (e.g. exception).
- **`e`** — toggle the exception left-bar marker.

### The screen froze while scrolling up through a huge log — the counter moved but nothing else.

This was a bug where scrolling stepped *raw* line indices through a long run of
hidden/disabled lines (a disabled group, or collapsed continuations), so the
position counter advanced while the visible window stayed put until it crossed
the run. It's **fixed** — up/down scrolling now counts only visible lines and
steps over disabled runs in one move. If you still see it, capture the moment
with **Ctrl+D** (diagnostic dump) and report it.

### The prefix columns (`[group] file:`) take up room I'd rather give to the log.

- **Ctrl+P** — hide/show the `[group]` column.
- **Ctrl+L** — hide/show the `basename:` column.
- **`f`** — middle-ellipsis-truncate long filenames (configurable via
  `tui.truncate_filenames` / `tui.filename_width`).

Toggles are instant — the scrollback isn't rebuilt.

### Where do I find every key without leaving the app?

Press **`?`** for the searchable help overlay (type to filter, `j`/`k` to
scroll, `esc`/`?` to close). It reflects your current OS and any keybinding
overrides.

---

## Saving & handing off to an AI agent

### How do I save what's on screen (or the whole buffer) to a file?

- **`s`** — save the visible viewport to `screen-log-listener-*.txt` (cwd). In
  visual mode it saves the current selection.
- **`S`** — save the full scrollback buffer.
- `-o <file>` / `--output <file>` — continuously mirror every displayed line to
  a plain-text file in all modes (keep it outside watched directories).

### How do I copy a precise selection?

Press **`v`** for visual line-selection mode: **space** sets the start, move with
↑/↓ (or `j`/`k`), then **`y`** copies a reference, **`Y`** copies the text, or
**`s`** saves the selection. **Esc** cancels.

### How do I hand a log range to an AI agent?

Run with `--mcp` to start the embedded MCP server (loopback, no auth — local dev
only):

```bash
log-listener -d /var/log/app -r 'name:\.log$' --mcp   # 127.0.0.1:7777
```

Then in the TUI press **`y`** to copy a paste-ready **reference** (via OSC 52) —
a `line:<id>`, a block `range:`, or the viewport `range:` depending on context.
Paste it to the agent; it resolves the reference through the MCP read tools
(`get_line`, `get_range`, `search`, `get_viewport`, …). Press **`Y`** instead to
copy the **text itself** (no ANSI, with prefixes). Very large selections may
exceed the terminal's OSC 52 limit — use `s`/`S` to save instead.

---

## Output destinations

### How do I stream logs to a web client / browser?

Enable the SSE broadcast and consume the stream:

```bash
log-listener -d /var/log/app -r 'name:\.log$' --sse 127.0.0.1:8080
curl -N http://127.0.0.1:8080/stream
```

Each event is one JSON object. Bind to loopback — there's no built-in auth.

### Can I run stdout, TUI, SSE, and MCP at the same time?

Yes — the sinks run in parallel and don't block each other. Output downgrades
automatically when stdout isn't a TTY (no TUI, no color).

### I'm piping the output to a file/another command and the TUI is in the way.

When stdout isn't a terminal, the TUI and color are skipped automatically. To
force plain output even on a TTY:

```bash
log-listener -d /var/log/app -r 'name:\.log$' --no-tui --no-color
```

### Can I make a smaller binary without MCP/SSE?

Yes, via build tags:

| Build | Result |
|-------|--------|
| `./build.sh build-nomcp` | No MCP server; drops the MCP SDK dependency. |
| `./build.sh build-nosse` | No SSE server. |
| `./build.sh build-minimal` | Neither MCP nor SSE. |

A stripped binary still recognizes the flag but errors if you ask for the
removed feature.

---

## Configuration

### Where does it look for a config file?

First match wins: `--config <path>` (must exist) → `./log-listener.yml` →
`~/.log-listener.yml`. Anything you can do on the CLI you can do in YAML; CLI
flags win on conflict. YAML is strict — unknown keys and duplicate group ids are
errors.

### Do I have to restart after editing the config?

No. When a YAML config is loaded, the file is watched and changes re-apply live
— groups/file discovery and the renderer pipeline rebuild on save. The file
watcher is only rebuilt when the *watch-set* actually changes, so a
renderer-only edit doesn't drop in-flight lines. **Output settings (SSE addr,
color, scrollback size) keep their startup values.** An invalid edit is ignored
silently and the last good config keeps running. (Not active in `--once` mode.)

### How do I remap a keybinding?

Add a `keybindings:` block keyed by OS (`darwin`/`linux`/`windows`) or `default`.
Each action maps to the list of keys that trigger it; the list **replaces** the
default:

```yaml
keybindings:
  default:
    quit: ["q", "ctrl+c"]
  linux:
    fast_down: ["ctrl+down"]
  darwin:
    fast_down: ["shift+down"]
```

Giving an action a shorter list is how you *drop* a default key. Loading fails
fast on an unknown action, an unrecognized key, or a clash. Run
`log-listener --keybindings-doc` for every action name and its defaults.

---

## Troubleshooting

### It takes ~5 seconds to start in my IDE's embedded terminal.

A known upstream quirk: bubbletea v1.3 queries the terminal's background color on
init and waits up to 5 s for a reply. Real terminals answer in milliseconds; some
pty wrappers, IDE-embedded terminals, or tmux-without-passthrough don't, so it
hits the timeout once at startup. Everything works normally afterward. Fixed in
bubbletea v2 (upstream).

### Something glitched on screen — how do I capture it for a bug report?

Press **Ctrl+D** while the glitch is visible. It dumps a diagnostic snapshot to
`debug-log-listener-*.txt` (cwd): a duplicate-content scan of the buffer, the
current view state, and the recent watch/reload event ring. For a persistent
on-disk trail of watch/reload events, run with `--debug-log <path>`.

### `log-listener --version` says "unknown flag." How do I check the version?

There's no `--version` flag yet. If you installed via
`go install …@latest` against a tagged release, the Go toolchain records the
version in the binary's build info; otherwise the version isn't surfaced by the
app itself. (A `--version` flag is on the roadmap.)

### `go install github.com/homeend/log-listener@latest` didn't get the newest tag.

`@latest` resolves to the highest **semver git tag** (`vX.Y.Z`). If a tag was
just pushed, the module proxy may not have it yet — bypass the cache with:

```bash
GOPROXY=direct go install github.com/homeend/log-listener@latest
```

Note `go install` takes a **module path**, not an `https://` URL.

### Does it work on macOS / Windows?

Linux-first (built on `inotify`). **macOS** works (kqueue via fsnotify) but is
less exercised; `build-static`'s fully-static linker flag is a no-op there but
the binary is still CGO-free. **Windows** is a future milestone — not currently
supported.
