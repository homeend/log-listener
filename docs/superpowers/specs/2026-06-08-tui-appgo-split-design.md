# Split `internal/tui/app.go` into focused files — Design

**Date:** 2026-06-08
**Status:** Draft — granularity ("moderate, by responsibility") approved in
brainstorming; pending user review of this written spec.
**Scope:** `internal/tui` — purely organizational. Move functions/types/vars
between files within `package tui`. No behavior change, no signature change, no
new logic.

## Context

`internal/tui/app.go` has grown to ~1678 lines doing many unrelated jobs: the
public `App` facade, the bubbletea message types, the `model` struct + lifecycle,
the ~290-line `Update` key-dispatch switch, reconcile/linebuf integration,
display-line rendering, the `View`/footer/panels chrome, and stream/viewport
rendering. That's hard to hold in context and hard to navigate. This slice
splits it into focused files by responsibility.

**Why this is low-risk:** all moves stay in `package tui`. Go resolves symbols
across files in the same package, so there are no import changes and no call-site
edits — only relocation. The compiler catches a missed or duplicated declaration
immediately, and the full TUI test suite is unchanged and must stay green. There
is **no** behavioral change possible from a same-package file split done
correctly.

**Non-goals:**
- No function bodies change; no renames; no signature changes; no new tests
  (the existing suite is the safety net — it must pass unchanged).
- No splitting of `search.go`, `visual.go`, `copyref.go`, `copytext.go`,
  `blocks.go`, `save.go`, `focusbar.go`, `viewport.go` beyond *receiving* the
  few helpers noted below.
- Not the aggressive split (no separate `panels.go`/`streamrender.go`/`model.go`).

## Target file layout

After the split, `internal/tui/` non-test `.go` files (new files in **bold**):

| File | Responsibility | Approx lines |
|------|----------------|--------------|
| `app.go` | The public `App` facade + bubbletea message types + the `model` struct and its constructor/lifecycle. "What the TUI *is*." | ~280 |
| **`update.go`** | The `Update` key-dispatch switch + `applyReload`. "How input drives state." | ~310 |
| **`reconcile.go`** | Buffer→view reconciliation and append path. | ~210 |
| **`render.go`** | Display-line decomposition and per-line rendering + line-visibility predicates. | ~200 |
| **`view.go`** | `View`, footer, group/renderer panels, and stream/viewport rendering (the screen output). | ~470 |
| **`width.go`** | Shared ANSI-stripping + display-width helpers (used across the package). | ~25 |
| `viewport.go` (existing) | Scroll/pan ops + the viewport helpers + movement consts. | ~110 |

## Exact function / declaration assignment

**`width.go` (new) — shared text helpers:**
- `var ansiRE`, `stripANSI`, `runeLen`, `dispWidth`, `runeWidth`.

**`viewport.go` (existing — receives):**
- Move in: `unstickFromTail`, `maybeReStick`, `contentHeight` (viewport-position
  helpers that belong next to `scrollBy`/`scrollFiles`/`panBy`).
- Move in: the movement `const` block (`horizStep`, `horizFastStep`,
  `vertFastStep`, `hitMargin`) — scroll/pan step sizes. (`hitMargin` is also used
  by `search.go`; same package, no change there.)

**`app.go` (keeps) — facade + model shape:**
- Types: `FileEntry`, `displayLine`, `EventMsg`, `FileListMsg`, `QuitMsg`,
  `ReloadMsg`, `App`, `GroupInfo`, `RendererInfo`, `Options`, `scrollbackEvent`,
  `model`, `RenderFunc`; `const defaultScrollback` (the only `const` left after
  the movement block at ~401 moves to `viewport.go`).
- Funcs: `New`, `(a *App) Run`, `(a *App) Push`, `(a *App) SetFiles`,
  `(a *App) Reload`, `(a *App) Quit`, `newModel`, `(m *model) Init`.

**`update.go` (new) — input → state:**
- `(m *model) Update`, `(m *model) applyReload`.

**`reconcile.go` (new) — buffer→view:**
- `tuiDecompose`, `(m *model) appendEvent`, `(m *model) appendStored`,
  `displayLinesFromEntry`, `(m *model) reconcile`, `(m *model) dragViewStateDown`,
  `(m *model) visibleEntries`, `(m *model) reRenderAll`.

**`render.go` (new) — display-line rendering + predicates:**
- `decomposeEvent`, `(m *model) renderDisplayLine`, `renderDisplayLineAt`,
  `renderDisplayLineCore`, `(m *model) groupEnabledLine`, `(m *model) lineEnabled`,
  `(m *model) filteredIndices`, `isContinuation`.

**`view.go` (new) — screen output:**
- The style `var` block (`groupStyle`, `fileStyle`, `dimStyle`, `headerBg`,
  `matchStyle`, `currentMatchStyle`) — its principal consumer is `View`;
  `render.go` references `matchStyle`/`currentMatchStyle` across-file (same
  package, fine).
- Funcs: `(m *model) View`, `renderFooter`, `disabledGroupCount`,
  `disabledRendererCount`, `toggleRenderer`, `renderGroupsPanel`,
  `rendererShiftChar`, `renderRenderersPanel`, `padRow`, `pluralS`,
  `collectVisible`, `publishViewport`, `renderStream`, `blankRow`, `blankRows`,
  `clipLine`, `clipANSIWindow`, `renderFiles`, `hint`, `resolvedKM`, `keyDisplay`.

(Every current top-level declaration in `app.go` appears exactly once above —
the implementation plan will verify the union is complete and disjoint.)

## Execution strategy (for the plan)

One new file per commit, each green:
1. `width.go` (smallest, most shared — flush it first).
2. `viewport.go` receives the helpers + consts.
3. `reconcile.go`.
4. `render.go`.
5. `update.go`.
6. `view.go`.
After each move: `gofmt -l` clean, `go build ./...`, `go test ./internal/tui/`
green. `app.go` is whatever remains (the facade + model). Final pass: full
suite + `go vet ./...` + `-race` + tagged builds; confirm `app.go` is ~280 lines
and each new file has a clear single responsibility.

Because each step is a pure relocation, a regression can only manifest as a
compile error (missed/duplicate decl) — caught immediately, before the commit.

## Testing strategy

No new tests. The existing `internal/tui` suite (Update/scroll/search/visual/
copy/reconcile/render) is the contract: it must pass **unchanged** after every
move. Plus `go vet ./...`, `go test -race ./internal/tui/`, and tagged builds
(`-tags nomcp`, `-tags nosse`) green at the end.

## Success criteria

- `app.go` drops from ~1678 to ~280 lines (facade + model only).
- The six target files exist with the assignments above; each new file has one
  clear responsibility.
- No declaration is lost or duplicated (every former `app.go` top-level decl
  lives in exactly one file).
- `gofmt -l internal/tui/` clean; `go build ./...`, `go test ./...`,
  `go vet ./...`, `go test -race ./internal/tui/`, and `-tags nomcp`/`nosse`
  builds all green — with the test suite unchanged.
