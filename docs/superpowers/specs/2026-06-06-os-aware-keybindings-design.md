# OS-aware keybindings with a translation + override layer

**Date:** 2026-06-06
**Status:** Approved (design)

## Problem

Every TUI keybinding in `internal/tui/app.go` is hard-coded twice and
disconnected: once as a `switch msg.String()` case (lines ~447–662) that
dispatches behavior, and again as literal text inside header/footer/overlay
help strings (e.g. `app.go:1038` `" … Ctrl+G groups · Ctrl+E rend …"`). There
is no single source of truth.

This blocks three things the user wants:

1. **OS-appropriate display.** macOS users expect Mac glyphs (`⌃ ⌥ ⇧ ⎋ ⇥`)
   rather than the words `Ctrl/Alt/Shift/Esc/Tab`.
2. **A translation layer between an action ("system command") and the keys
   bound to it**, so behavior and display both derive from one mapping.
3. **User overrides** of default keys, per-OS, in the YAML config.

Plus: a keybindings reference doc that cannot drift from the code.

## Hard constraint (drives the whole design)

**A terminal TUI cannot capture the macOS Cmd (⌘) key.** macOS terminal
emulators intercept ⌘-shortcuts themselves and never forward them to the app;
bubbletea only ever receives Ctrl, Alt/Option (Meta), Shift, and plain keys.
"Mac-native" therefore means **glyph display** + **a small per-OS default
remap**, never ⌘-key handling.

## macOS conflict investigation (findings)

Every current binding was checked against macOS terminal behavior:

- **Control-letter bindings are safe.** `Ctrl+G/E/P/L/R/B/F` are Unix terminal
  control codes; bubbletea runs the terminal in **raw mode**, so the line
  discipline does not eat them — they reach the app identically on macOS and
  Linux. `Ctrl+C`/`q`, `Esc`, `Tab`, `/`, letters and digits are universal.
- **One real conflict: `Ctrl + Arrow`.** On macOS these are **system-wide
  defaults** owned by the window server before the terminal sees them:
  `Ctrl+←/→` switch Spaces, `Ctrl+↑` opens Mission Control, `Ctrl+↓` is App
  Exposé. Our fast-scroll bindings `ctrl+up/down/left/right` (`app.go:576–635`)
  are therefore swallowed for most Mac users.
  - **Safety net already present:** each of those cases is already aliased to a
    `shift+arrow` variant, and `Shift+Arrow` is forwarded to a TUI on macOS.
    The fix is to make the macOS keymap advertise the `shift+arrow` form as
    primary while keeping both bound.
- **Doc gotchas (not conflicts):** `Ctrl+I` and `Tab` are the same byte
  (`0x09`); and Option/⌥ bindings require "Use Option as Meta" in Terminal.app,
  so Option must not be used for defaults.

### Unverified assumption (must be honest in code + docs)

This design was authored on WSL2/Linux. **"Shift+Arrow reaches a bubbletea TUI
on macOS Terminal.app/iTerm2" is not verified here.** It is advertised as the
macOS fast-scroll primary but marked "requires verification on macOS" in the
generated doc. `ctrl+arrow` stays bound (works if the user disabled Mission
Control), and **PgUp/PgDn remain a guaranteed paging safety net**, so the app
is never bricked even if both arrow forms fail on a given Mac terminal.

## Architecture

### New package: `internal/keymap`

The single source of truth for actions, default keys, display, and resolution.

| File | Responsibility |
|------|----------------|
| `actions.go` | `Action` (string) type; ordered `AllActions []ActionDef` |
| `defaults.go` | built-in per-OS default keymaps |
| `glyphs.go` | per-OS key-token → display-label translation |
| `keymap.go` | `Keymap` type, `Resolve`, reverse lookup, token normalization |
| `doc.go` | `RenderMarkdownDoc` — generates `docs/KEYBINDINGS.md` content |

#### `actions.go`

```go
type Action string

type ActionDef struct {
    Action  Action
    Title   string   // human label, e.g. "Toggle file overlay"
    Desc    string   // one-line description for docs
    Context string   // "main" | "groups" | "renderers" | "files" — for grouping
}

var AllActions = []ActionDef{ /* ordered */ }
```

Named actions covered (every non-positional binding):

```
quit, toggle_files, toggle_groups, toggle_renderers, close_overlay,
search, next_match, prev_match, filter,
toggle_group_col, toggle_file_col, clear, collapse_all,
scroll_up, scroll_down, page_up, page_down, fast_up, fast_down,
top, bottom, scroll_left, scroll_right, fast_left, fast_right, reset_horiz
```

#### `defaults.go`

```go
var defaultLinux   Keymap // and defaultWindows == defaultLinux
var defaultDarwin  Keymap
```

Keys are stored as **ordered lists**; order = display priority, and the handler
matches *any* key in the list. The **only** darwin difference from linux is that
fast-scroll actions list the `shift+arrow` form first:

```
linux:  fast_down: ["ctrl+down", "shift+down"]
darwin: fast_down: ["shift+down", "ctrl+down"]
```

Both remain bound on every platform so nothing breaks.

#### `glyphs.go`

```go
func Display(keys []string, goos string) string
```

Renders an action's key list to a per-OS label. Token map:

| Token | darwin | linux / windows |
|-------|--------|-----------------|
| `ctrl+` | `⌃` | `Ctrl+` |
| `alt+`  | `⌥` | `Alt+` |
| `shift+`| `⇧` | `Shift+` |
| `esc`   | `⎋` | `Esc` |
| `tab`   | `⇥` | `Tab` |
| `enter` | `↩` | `Enter` |
| `up/down/left/right` | `↑↓←→` | `↑↓←→` |
| `pgup/pgdown` | `PgUp/PgDn` | `PgUp/PgDn` |
| `" "` (space) | `Space` | `Space` |

Unknown tokens display verbatim.

#### `keymap.go`

```go
type Keymap map[Action][]string

// Resolve merges user override layers over the built-in OS default, per-action
// (first defining layer wins, list replaces — no key-by-key merge), then
// validates for collisions. goos selects the app default.
func Resolve(goos string, userDefault, userOS map[Action][]string) (Keymap, error)

// Lookup maps a bubbletea key string to its action (built over the merged map).
func (k Keymap) Lookup(key string) (Action, bool)
```

**Precedence, per action (replace, first layer wins):**

1. user config, current-OS section (`darwin`/`linux`/`windows`) — highest
2. user config, `default` section (cross-OS)
3. app built-in OS-default keymap — lowest

List-replace semantics (not key-by-key merge) so a user can *clear* a default
by setting an explicit list.

**Three failure modes closed at `Resolve` time:**

1. **Collision detection** runs over the **fully merged** keymap (overrides +
   untouched defaults), not just the user layer. If one key resolves to two
   actions (e.g. user rebinds `clear`→`n` while `next_match` still owns `n`),
   `Resolve` returns an error **naming both actions and the key**. No silent
   last-wins. (Replace-list semantics let the user swap atomically to fix it.)
2. **Token normalization.** User-typed keys are normalized against the
   canonical token vocabulary (`Ctrl+I`→`ctrl+i`, `Esc`→`esc`, `Space`→`" "`,
   `Tab`→`tab`). A token that cannot be mapped is an **error**, never a silent
   no-fire.
3. **Unknown action name** in config → error listing the valid action names.

### Scope boundary: positional toggles

`1`–`9` (toggle group N), `!@#$%^&*(` (toggle renderer N) are **positional**,
not single functions. They stay as a **special-case branch handled before the
action switch**, documented but **not individually overridable**. (`reset_horiz`
bound to `0` *is* a named, overridable action.)

## Config integration (`internal/config`)

New YAML block; raw user layers are passed through to `keymap.Resolve`.

```yaml
keybindings:
  default:                       # cross-OS overrides
    search: ["/"]
  darwin:
    fast_down: ["shift+down"]
  linux:
    fast_down: ["ctrl+down"]
  windows:
    fast_down: ["ctrl+down"]
```

`cmd/log-listener` reads `runtime.GOOS` **once** and calls
`keymap.Resolve(goos, cfg.Keybindings.Default, cfg.Keybindings[goos])`. OS is an
injected parameter everywhere (`Resolve`, `Display`, `RenderMarkdownDoc`) so
tests drive all three platforms from Linux. CLI precedence rules for the rest of
the config are unchanged; `keybindings` is YAML-only (no CLI flags per action).

## TUI integration (`internal/tui/app.go`)

- `App` gains: the resolved `Keymap`, its reverse-lookup, and `goos` (for glyphs).
- The hard-coded `switch msg.String()` becomes: positional-toggle check →
  else `action, ok := km.Lookup(msg.String())` → `switch action`.
- **All help text is generated** from `AllActions` + `Display(...)`: header,
  footer (`renderFooter`), and the overlay panel headers (`app.go:1148/1205/1444`).
  No more literal `" Ctrl+G groups · … "` strings.
- The existing test suite (`app_test.go`, `search_test.go`, `multiline_test.go`)
  guards behavior through the refactor.

## App-generated docs

The doc is produced **by the app from its own default mappings**, so it tracks
the code automatically.

- `keymap.RenderMarkdownDoc(goos...)` builds the markdown from `AllActions` +
  the built-in default keymaps: an action table with columns *Action /
  Linux·Windows / macOS* (glyphs), grouped by `Context`, plus the YAML-override
  precedence section and the macOS Shift+Arrow verification caveat.
- `cmd/log-listener` exposes **`--keybindings-doc`**: prints the generated
  markdown to stdout and exits (no TUI, no watching).
- `make keybindings-docs` runs `log-listener --keybindings-doc > docs/KEYBINDINGS.md`.
- `TestDocsUpToDate` calls the same `RenderMarkdownDoc` and **fails if the
  committed `docs/KEYBINDINGS.md` is stale**, catching forgotten regenerations.

## Data flow

```
config.Load
  → cfg.Keybindings (raw user layers: default + per-OS)
  → keymap.Resolve(runtime.GOOS, default, perOS)   // merge + validate
  → keymap.Keymap
  → tui.App  (Lookup for dispatch; Display for all help text)

cmd --keybindings-doc → keymap.RenderMarkdownDoc → stdout → docs/KEYBINDINGS.md
```

## Error handling

- Unknown action name / unmappable key token / key collision → `Resolve`
  returns an error; `cmd/log-listener` prints it and exits non-zero (same as any
  config load failure). Fail fast, never start the TUI with a broken keymap.

## Testing

- `Resolve`: precedence (current-OS > default > app-default), list-replace,
  clear-a-default, collision error (names both actions + key), unknown-action
  error, token-normalization (accept canonical forms, error on unmappable).
- `Display`: per-OS glyph rendering for representative tokens (darwin vs linux).
- Dispatch: every default key in each OS keymap maps via `Lookup` to its
  expected action; positional `1-9`/`!@#…` still handled by the special branch.
- TUI behavior: a custom-override test (rebind `quit`→`x`, assert quit fires on
  `x` and not on the old key); existing suites remain green.
- Docs: `TestDocsUpToDate` (committed file == `RenderMarkdownDoc` output).
- `make test`, `make vet`, `make race` all green.

## Out of scope

- ⌘ (Cmd) key handling — impossible in a terminal TUI.
- Option/⌥-based default bindings (Terminal.app Meta caveat).
- Individual rebinding of positional `1-9` / `!@#…` toggles.
- Per-action CLI flags (overrides are YAML-only).
- A live in-app "edit keybindings" UI.
```
