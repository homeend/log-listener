# TUI Save: Viewport / Scrollback → Text File — Design

**Date:** 2026-06-07
**Status:** Approved (pending spec review)

## Summary

Two new TUI keybindings that export the streaming view to a plain-text file:

1. **`s` — save viewport.** Write the rows currently visible on screen to a
   timestamped `.txt` file in the working directory.
2. **`S` — save scrollback.** Write the entire in-memory scrollback buffer (not
   just the visible window) to a timestamped `.txt` file.

Both are confirmed with a transient footer message (`saved N lines to <path>`),
or a footer error if the write fails. This is the first of three "streaming →
agent" features (see the roadmap doc); it is fully self-contained.

## Goals / Non-goals

**Goals:** a one-keypress dump of what you're looking at (or everything you
have), as readable plain text, suitable for pasting into an issue or handing to
an agent. No prompts on the happy path.

**Non-goals (YAGNI):** choosing the filename interactively, choosing a
directory, embedding line IDs (those arrive with feature #1), styled/HTML/JSON
export, appending to an existing file, configurable formats.

## Current Behavior (baseline)

- The model owns `entries []scrollbackEvent` (one per pipeline emission) and the
  flat cache `lines []displayLine`. `collectVisible(rows)` returns the absolute
  `m.lines` indices currently on screen, already honoring tail/browse position,
  group enable/disable, collapse-multiline, and filter mode.
- `renderDisplayLine` / `renderDisplayLineCore` produce **styled** rows with
  ANSI; `stripANSI(s)` removes escape sequences.
- Keybindings flow through `internal/keymap`: one named `Action` per function,
  per-OS default keys, YAML override resolution, and `KEYBINDINGS.md` generated
  by `--keybindings-doc` and guarded by `TestDocsUpToDate`.
- `s` and `S` are currently unbound on every OS.

## Keymap: two new actions

Add to `internal/keymap`:

- `ActionSaveViewport Action = "save_viewport"`
- `ActionSaveScrollback Action = "save_scrollback"`

`actions.go` — append both to `AllActions` (context `"main"`):

- `{ActionSaveViewport, "Save viewport", "Write the visible rows to a text file.", "main"}`
- `{ActionSaveScrollback, "Save scrollback", "Write the full scrollback buffer to a text file.", "main"}`

`defaults.go` — same keys on every OS (added to the OS-independent `m` map):

- `ActionSaveViewport: {"s"}`
- `ActionSaveScrollback: {"S"}`

`KEYBINDINGS.md` is regenerated (`./build.sh keybindings-docs`); `TestDocsUpToDate`
keeps it honest. Both keys are overridable via YAML like every other action.

## Plain-text format

A single helper renders one `displayLine` to a plain (unstyled) export line:

```
plainExportLine(dl):
    head  → "[" + group + "] " + file + ": " + stripANSI(body)
    block → stripANSI(body)            // continuation / JSON / XML rows
```

Decisions (predictable over WYSIWYG):

- The export **always includes** the `[group] file:` prefix on head lines, even
  if the group/file columns are toggled off on screen. The file is a complete
  record; column toggles are a viewing convenience, not an export filter.
- Continuation/block rows keep their existing indentation (their `body` already
  carries it) with styling stripped.
- No horizontal clipping or width padding — export lines are full-length.
- Lines are joined with `\n` and the file ends with a trailing `\n`.

Two pure, unit-testable snapshot methods on `model` return `[]string`:

- `snapshotViewport() []string` — `plainExportLine` over `collectVisible(m.contentHeight())`.
  This is exactly what is on screen (honors tail/browse, group disable, collapse,
  filter), minus styling, plus full prefixes.
- `snapshotScrollback() []string` — `plainExportLine` over **all** of `m.lines`,
  in order, ignoring transient view toggles (collapse/filter) and group
  enable/disable. "Save scrollback" means the whole buffer, period.

**Forward reuse (feature #1):** "give me the viewport" and "give me the
scrollback" are exactly the request/response operations an agent wants, so these
two snapshots are direct material for MCP tools (`get_viewport` / `get_scrollback`).
Keep them as pure `model` methods returning `[]string` so the MCP layer can call
the same logic. Exposing the *live* viewport to MCP needs a thread-safe accessor
into the bubbletea model — that coupling is designed in the #1 spec, not here.

## File write (bubbletea-idiomatic side effect)

Writing happens off the model via a `tea.Cmd` so `Update` stays pure and the IO
is off the render path:

1. On the save key, `Update` calls the matching snapshot method **synchronously**
   (it reads `m.lines`, which is owned by the model goroutine) and captures the
   resulting `[]string`.
2. It returns a `tea.Cmd` closure that writes those lines and yields a result
   message: `saveResultMsg{ path string; n int; err error }`.
3. The next `Update` handles `saveResultMsg` by setting a transient `flash`
   string (see below).

File naming, in the write command:

- Base name: `screen-log-listener-YYYYMMDD-HHMMSS.txt` via
  `time.Now().Format("20060102-150405")`. The `screen-` prefix marks these as
  view dumps and groups them in a directory listing.
- Directory: `m.saveDir`, a new model field defaulting to `""` → current working
  directory (`os.Getwd`). The field is a **test seam** (tests set a `t.TempDir()`);
  production never sets it.
- Collision (two saves in the same second): if the target exists, append
  `-1`, `-2`, … before `.txt` until the name is free.
- Write atomically enough for this purpose: `os.WriteFile(path, []byte(strings.Join(lines,"\n")+"\n"), 0o644)`.

## Confirmation / errors (transient footer)

Add `flash string` to `model`. `renderFooter` shows it, when non-empty, in
place of the normal status line (it sits below the existing search-input and
wrap-prompt priority branches). It is cleared on the **next key event** (set
`m.flash = ""` at the top of the `tea.KeyMsg` path, before dispatch), so it
persists until the user does anything.

- Success: `flash = "saved 142 lines to /path/screen-log-listener-20260607-013355.txt"`.
- Empty buffer: still writes an (empty-bodied) file and reports `saved 0 lines…`;
  no special-case.
- Failure: `flash = "save failed: <err>"`.

## Architecture / Files

- `internal/keymap/actions.go` — two new action constants + `AllActions` entries.
- `internal/keymap/defaults.go` — `s` / `S` defaults (OS-independent).
- `KEYBINDINGS.md` — regenerated.
- `internal/tui/app.go` — `flash` + `saveDir` fields; clear `flash` on key
  events; dispatch `ActionSaveViewport` / `ActionSaveScrollback`; `saveResultMsg`
  handling in `Update`; `renderFooter` flash branch; optional header hint.
- `internal/tui/save.go` (new) — `plainExportLine`, `snapshotViewport`,
  `snapshotScrollback`, the file-naming + write command, `saveResultMsg`.

No changes outside `internal/keymap` and `internal/tui`. Stdout/SSE/MCP
unaffected; TUI-only feature.

## Testing (model-level, no TTY)

- `plainExportLine`: head line gets `[group] file: ` prefix + stripped body even
  with `showGroup`/`showFile` off; block line is stripped body only; ANSI fully
  removed.
- `snapshotViewport`: matches `collectVisible` selection — respects browse
  position, group disable, collapse, and filter mode.
- `snapshotScrollback`: returns every `m.lines` row in order regardless of
  toggles (collapse/filter/group-disable do not shrink it).
- File write: with `saveDir = t.TempDir()`, the command writes the expected
  bytes; a second save in the same second produces a `-1` suffixed file (no
  overwrite); the file ends with a trailing newline.
- Dispatch: `ActionSaveViewport` / `ActionSaveScrollback` produce a non-nil
  `tea.Cmd`; a `saveResultMsg` sets `flash`; a subsequent key clears `flash`.
- Keymap: defaults bind `s` / `save_viewport` and `S` / `save_scrollback`;
  `TestDocsUpToDate` passes after regeneration.

## Conventions

Phase commits per repo convention (`phase N: <desc>` + review fixes), each
leaving `go test ./...`, `go vet ./...`, `go test -race ./...` green. Update
`README.md` + `CHANGELOG.md` and regenerate `KEYBINDINGS.md` on delivery.
