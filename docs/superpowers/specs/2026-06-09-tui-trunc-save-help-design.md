# TUI usability batch: filename truncation, save-selection, help panel

**Status:** Approved (design)
**Date:** 2026-06-09
**Scope:** Three light, independent TUI features sharing keymap/overlay/save
infrastructure. Word wrap (the fourth, structurally heavier feature) is
deliberately deferred to its own spec/cycle.

## Goal

Add three TUI conveniences:

1. **Filename truncation** — middle-ellipsis long filenames in the inline file
   column, toggleable at runtime and via config.
2. **Save selection** — save the current visual selection to a file with `s`,
   parallel to the existing `y`/`Y` copy.
3. **Help panel** — a searchable overlay (`?`) listing every keybinding for the
   current OS.

## Non-goals

- Word wrap (separate cycle).
- Truncating the group column, the files overlay, or exported files (truncation
  is display-only on the inline file column).
- A dedicated config field or key for save-selection (it reuses the existing
  `save_viewport` action, reinterpreted in visual mode).

---

## Feature 1: Filename truncation (middle-ellipsis)

### Behavior

When enabled, a filename whose **display width** exceeds the configured limit is
shortened to `head + "..." + tail`, fitted to exactly the limit in display
columns. Disabled by default; full names otherwise. Display-only: the export
path (`plainExportLine`) and the files overlay always show full names, so no
data is ever lost — only on-screen width changes.

### `truncateMiddle` helper (`internal/tui/width.go`)

```go
// truncateMiddle shortens s to at most maxCols DISPLAY columns by replacing the
// middle with "...", measured with dispWidth so wide/CJK names never overflow.
// s unchanged if it already fits. Degenerate cases: maxCols <= 0 -> ""; maxCols
// <= 3 (no room for "..." plus content) -> the first maxCols columns of s with
// no ellipsis.
func truncateMiddle(s string, maxCols int) string
```

Rules (must be spelled out so the implementer doesn't guess):
- `dispWidth(s) <= maxCols` → return `s` verbatim.
- `maxCols <= 0` → `""`.
- `maxCols <= 3` → first `maxCols` display columns of `s`, no ellipsis.
- otherwise: `avail := maxCols - 3`; `left := (avail + 1) / 2`; `right := avail - left`.
  Take the first `left` display columns and the last `right` display columns,
  join with `"..."`. Column-accurate slicing (not byte/rune slicing) using the
  same `go-runewidth` accounting as `dispWidth`.

### Wiring into the render path (the one correctness trap)

In `renderDisplayLineCore` (`render.go:99-103`), the file column writes the name
**and** accumulates its width:

```go
if m.showFile {
    name := dl.file
    if m.truncateFiles {
        name = truncateMiddle(dl.file, m.effFilenameWidth())
    }
    sb.WriteString(fileStyle.Render(name))
    sb.WriteString(": ")
    visW += dispWidth(name) + 2 // ": "
}
```

Compute `name` **once** and feed it to *both* `fileStyle.Render` and the `visW`
accumulator. Feeding only the `Render` call leaves `visW` measuring the full
name, and `clipLine` then pads/clips by the wrong amount — a subtle off-by-N
that short-name tests miss. A test MUST assert the rendered row's reported width
with a long, truncated name.

`effFilenameWidth()` returns `m.filenameWidth` if `> 0`, else `defaultFilenameWidth`
(16) — mirroring how `Scrollback` treats 0 as "use the default".

### Model fields (`app.go`)

```go
truncateFiles bool // toggle: middle-ellipsis long filenames in the file column
filenameWidth int  // max display cols before truncation; <=0 => defaultFilenameWidth
```

`const defaultFilenameWidth = 16` alongside the other TUI constants.

### Runtime toggle (keymap)

- New action `ActionToggleFilenameTrunc Action = "toggle_filename_trunc"` in
  `keymap/actions.go`, with an `AllActions` entry:
  `{ActionToggleFilenameTrunc, "Toggle filename truncation", "Shorten long filenames in the file column with a middle ellipsis.", "main"}`.
- Default key `f` in `keymap/defaults.go`: `ActionToggleFilenameTrunc: {"f"}` (`f`
  is currently free; single rune, no `glyphs.go` change needed).
- Update case in `update.go`: `case keymap.ActionToggleFilenameTrunc: m.truncateFiles = !m.truncateFiles`.
  No cache rebuild — truncation is applied at render time, so the toggle is a
  pure redraw (same property as the column toggles).

### Config

- Raw `config.TUI` struct (`yaml.go`) gains:
  ```go
  TruncateFilenames *bool `yaml:"truncate_filenames,omitempty"`
  FilenameWidth     *int  `yaml:"filename_width,omitempty"`
  ```
- Resolved `config.Config` (`cli.go`) gains:
  ```go
  TUITruncateFilenames bool // tui.truncate_filenames; default false
  TUIFilenameWidth     int  // tui.filename_width; 0 => default 16
  ```
- Flatten in `mergeYAMLInto` (`yaml.go`, the `if yc.TUI != nil` block ~line 370):
  ```go
  if t.TruncateFilenames != nil {
      cfg.TUITruncateFilenames = *t.TruncateFilenames
  }
  if t.FilenameWidth != nil {
      cfg.TUIFilenameWidth = *t.FilenameWidth
  }
  ```
- `tui.Options` (`app.go`) gains `TruncateFiles bool` and `FilenameWidth int`;
  `tui.New` sets `m.truncateFiles = opts.TruncateFiles` and
  `m.filenameWidth = opts.FilenameWidth`.
- `main.go` (`tui.New(tui.Options{...})`, ~line 470) passes
  `TruncateFiles: cfg.TUITruncateFilenames, FilenameWidth: cfg.TUIFilenameWidth`.
- The emitted annotated example (`emit.go` TUI block) documents both fields;
  regenerate `log-listener.example.yml` and update the golden test.

---

## Feature 2: Save selection (`s` in visual mode)

### Behavior

In visual mode (after `v` + optional `space` anchor), `s` writes the selected
rows to `screen-log-listener-<timestamp>.txt` and exits visual mode, exactly
mirroring how `y`/`Y` copy and exit. Normal-mode `s` (save viewport) is
unchanged — the reinterpretation is scoped to visual mode, the same way `y`/`Y`
are reinterpreted there.

### Snapshot source (`save.go`)

```go
// snapshotSelection returns the visual selection's rows as plain export text
// (full prefixes, styling stripped) via plainExportLine, in display order.
func (m *model) snapshotSelection() []string {
    lo, hi := m.selectionBounds()
    out := make([]string, 0, hi-lo+1)
    for i := lo; i <= hi; i++ {
        out = append(out, plainExportLine(m.lines[i]))
    }
    return out
}
```

`selectionBounds` already clamps to a valid `[lo, hi]` (visual mode guarantees
`len(m.lines) > 0`).

### Cmd threading (the one integration change)

`handleVisualKey` currently returns `*model`; the save path needs to emit a
`tea.Cmd`. Change the signature to return `(tea.Model, tea.Cmd)` and update the
single call site in `update.go:24` from `return m.handleVisualKey(msg), nil` to
`return m.handleVisualKey(msg)`. Existing cases return `m, nil`; the new case:

```go
case keymap.ActionSaveViewport:
    lines := m.snapshotSelection()
    m.exitVisual()
    return m, m.saveCmd(lines)
```

placed in the keymap-resolved switch in `handleVisualKey` alongside the existing
`ActionCopyReference` / `ActionCopyText` cases. The async write yields the
existing `saveResultMsg`, so the `saved N lines to <path>` flash already works.

### Documentation

Extend the `ActionVisualSelect` description in `actions.go` to mention `s`:
"… `y` copies the range, `Y` the text, `s` saves it to a file, all exit; `esc`
cancels."

---

## Feature 3: Help panel (`?`, searchable)

### Behavior

`?` opens a modal overlay listing every action — its current-OS resolved keys,
Title, and Desc — grouped by Context (`main`, then any others), in `AllActions`
order. While open it is **fully modal**: printable keys filter the list,
backspace deletes, `j`/`k`/arrows scroll, `esc` or `?` closes. Opening it closes
the files/groups/renderers overlays (mutually exclusive, like `enterVisual`).

### Key source — reuse, don't reformat

The panel renders each action's keys via `m.resolvedKM().Display(action)` — the
**same `(*keymap.Keymap).Display` method** that `doc.go:26` uses to generate
`KEYBINDINGS.md`. This guarantees the panel and the doc can't drift, and gives
current-OS keys for free (the resolved keymap carries its `goos`).

### Model fields (`app.go`)

```go
showHelp   bool   // help overlay open
helpQuery  string // live filter for the help list (independent of searchQuery)
helpScroll int    // first visible help row (mirrors filesScroll)
```

`helpQuery` is a **separate field** from `searchQuery` — they must not share
state.

### Input handling (`internal/tui/help.go`)

- In `update.go`, alongside the other modal guards (after `visualMode`,
  `searchInput`, `wrapPrompt`):
  ```go
  if m.showHelp {
      return m.handleHelpKey(msg), nil
  }
  ```
- `handleHelpKey(msg tea.KeyMsg) *model`:
  - `esc` or `?` → `m.showHelp = false`, clear `helpQuery`, return.
  - `up`/`k` → `helpScroll--` (clamp ≥ 0); `down`/`j` → `helpScroll++` (clamp to
    last filtered row). Use the same clamp idiom as `scrollFiles`.
  - `backspace` → trim last rune of `helpQuery`, reset `helpScroll = 0`.
  - any printable single rune → append to `helpQuery`, reset `helpScroll = 0`.
  - everything else ignored (stays modal).

Note: `j`/`k` scroll rather than filter, matching the files overlay; printable
*other* letters filter. Document this in the panel footer ("type to filter,
j/k scroll, esc close").

### Open action (`update.go`)

- New action `ActionHelp Action = "help"` in `actions.go`, `AllActions` entry:
  `{ActionHelp, "Help", "Show the searchable keybindings panel.", "main"}`.
- Default key `?` in `defaults.go`: `ActionHelp: {"?"}` (free; single rune, no
  `glyphs.go` change).
- Update case: `case keymap.ActionHelp: m.showHelp = true; m.helpQuery = ""; m.helpScroll = 0; m.showFiles = false; m.showGroupsPanel = false; m.showRenderersPanel = false`.

### Filtering + rendering (`help.go`)

- `helpRows() []helpRow` builds the displayed list: for each `keymap.AllActions`
  entry, a row `{keys: m.resolvedKM().Display(d.Action), title: d.Title, desc: d.Desc, context: d.Context}`.
  When `helpQuery != ""`, keep only rows whose lower-cased `keys+title+desc`
  contains the lower-cased query.
- `renderHelp() string` renders the overlay panel mirroring the files/groups
  overlay (`view.go`): a bordered box, a title (`Help — type to filter`), the
  filtered rows windowed by `helpScroll` to the content height, the current
  `helpQuery` shown in the footer, and the hint line. Wire it into `view.go`
  where the other overlays are dispatched (help takes precedence when
  `m.showHelp`).

---

## Cross-cutting

### Files

| File | Change |
|------|--------|
| `internal/tui/width.go` | `truncateMiddle` + `effFilenameWidth` helper. |
| `internal/tui/render.go` | Truncate `dl.file` in `renderDisplayLineCore`. |
| `internal/tui/save.go` | `snapshotSelection`. |
| `internal/tui/visual.go` | `handleVisualKey` → `(tea.Model, tea.Cmd)`; save case. |
| `internal/tui/help.go` (new) | Help overlay model + input + render. |
| `internal/tui/app.go` | New model fields, `Options` fields, `tui.New` wiring, constant. |
| `internal/tui/update.go` | Visual call-site signature; truncation + help cases; help modal guard. |
| `internal/tui/view.go` | Dispatch `renderHelp`. |
| `internal/keymap/actions.go` | Two new actions + `AllActions` entries; `ActionVisualSelect` desc. |
| `internal/keymap/defaults.go` | `f` and `?` defaults. |
| `internal/config/yaml.go` | `TUI` raw fields + flatten block. |
| `internal/config/cli.go` | Resolved `TUITruncate*` fields. |
| `internal/config/emit.go` | Example TUI block. |

### Generated / doc artifacts (must stay green)

- Regenerate `KEYBINDINGS.md` via `./build.sh keybindings-docs` (two new
  actions; guarded by `TestDocsUpToDate`).
- Regenerate `log-listener.example.yml`; update its golden test.
- Update `README.md` (key table / features) and `CHANGELOG.md`.

### Testing

- `truncateMiddle`: fits-unchanged; long ASCII middle-ellipsis exact width;
  wide/CJK name never exceeds `maxCols`; degenerate `maxCols <= 3` and `<= 0`.
- Render width: a long filename with truncation on → `renderDisplayLineCore`
  reports the correct `visW` (the off-by-N guard).
- Save selection: visual span → `snapshotSelection` returns full-prefix plain
  lines; `handleVisualKey` on `s` returns a non-nil `tea.Cmd` and exits visual.
- Help: `?` opens and closes overlays; `helpRows` filters by query across
  key+title+desc; `helpScroll` clamps; keys reflect the resolved current-OS
  keymap (assert a known action's `Display` string appears).
- Config: `tui.truncate_filenames` / `tui.filename_width` parse into the
  resolved fields; `0` width resolves to 16 at consumption.

### Verification gates

Every commit leaves `go test ./...`, `go vet ./...`, `go test -race ./...` green.

## Locked design rules touched

- **Keybindings flow through `internal/keymap`**: both new actions get per-OS
  defaults and `AllActions` entries; the help panel reads `Display` (no
  re-formatting), so `KEYBINDINGS.md` stays the single source of truth.
- **Truncation is display-only** — exports and the files overlay keep full
  names; the cache is untouched so toggles are pure redraws.
