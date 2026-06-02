# Config auto-reload — design

**Date:** 2026-06-02
**Status:** Approved, ready for implementation plan
**Feature:** Remember the loaded YAML config file, watch it for changes, and
re-apply groups + renderers live without restarting the process.

## Goal

When the user edits the YAML config file that `log-listener` loaded at startup,
the running process picks up the change and re-applies it — without losing the
session. Applies in both interactive TUI mode and plain stdout streaming mode.

## Scope decisions (locked)

| Decision | Choice |
|----------|--------|
| What re-applies on reload | **Renderers** and **groups / file discovery** |
| Output settings on reload | **Not re-applied** — startup values for `drop_unmatched`, color, SSE addr, scrollback, tui-enabled are kept |
| Invalid config on reload | **Keep running, silent** — discard the bad reload, keep last-good config, no error surfaced |
| Run modes | **Both** TUI and stdout streaming (`runWatch` + `runWatchTUI`). `--once` excluded (it reads and exits) |
| TUI toggle state after reload | **Reset to config defaults** (new file's `disabled`/`off`) |
| Existing scrollback after reload | **Re-render history** under the new renderers |

## Core idea: rebuild, don't mutate

The watcher has no `Remove`, its matcher closures capture the startup `cfg`, and
the pipeline has no swap method. Rather than add mutation APIs to each, **rebuild
the watcher and pipeline on reload**:

- `watch.Watcher.Close()` already closes every per-file tailer. Building a fresh
  watcher with new matcher closures (over the new `cfg`) and `Add`-ing only the
  now-matching files makes **removals fall out for free** — files that no longer
  match are simply never re-added. No new `Remove`/`SetMatcher` API.
- New matcher closures capture the new `cfg`, so matcher staleness is solved for
  free.
- Both run loops hold `w` as a **local**; `case ev := <-w.Events()` re-evaluates
  each loop iteration, so reassigning `w` inside the reload branch swaps the
  watcher with no extra machinery.

## Components

### 1. `internal/config` — surface the resolved path

- Add `Config.SourcePath string`. Set in `loadWithFS` to the resolved
  `yamlPath` (empty when no YAML file is loaded).
- Reload is just `config.Load(args, time.Now())` with the original CLI args, so
  CLI-precedence is re-applied on every reload. The reload consumer reads only
  `cfg.Groups` and `cfg.RendererSpecs` from the result; output fields are
  ignored (see scope).
- No YAML file (`SourcePath == ""`) → the feature is inert; no config watcher is
  started.

### 2. `internal/configwatch` — new package (the only new package)

A small, **config-agnostic** fsnotify wrapper. Watches a single file path and
emits a debounced "changed" signal. Keeps fsnotify at the edge so reload
orchestration is testable without it.

```go
package configwatch

type Watcher struct { /* ... */ }

// New watches the file at path. It watches the PARENT directory and filters by
// basename so editor "rename-over-the-file" saves are still detected. Events
// are coalesced with the given debounce window. A best-effort re-Stat confirms
// the file still exists before signaling.
func New(path string, debounce time.Duration) (*Watcher, error)

// Changes delivers a signal each time the file settles after a change.
func (w *Watcher) Changes() <-chan struct{}

func (w *Watcher) Close() error
```

- Watch the **parent dir**, filter the **basename**, re-stat on each fsnotify
  event (same lesson `watch.Watcher` already encodes for rotation).
- Debounce ~**300ms**: coalesce the burst of write/rename/chmod events a single
  save produces into one `Changes()` signal.
- Emits `struct{}` (not `*Config`) to stay decoupled from `config`; the reload
  caller does the `config.Load` so the load path is testable independently and
  fsnotify never appears in reload-logic tests.

### 3. Pipeline as `atomic.Pointer[render.Pipeline]`

- Replace the single `*render.Pipeline` value threaded through `main` with an
  `atomic.Pointer[render.Pipeline]` holder.
- `emit`, the TUI `RenderFn`, and `SetRendererOn` read the current pipeline via
  `.Load()` on each call. Reload stores the rebuilt pipeline.
- Matches the existing per-renderer `atomic.Bool` design and is needed anyway:
  in TUI mode the pump goroutine and the bubbletea goroutine both touch the
  pipeline.
- The rebuilt pipeline uses the **startup** `drop_unmatched` (output settings are
  out of scope), not the reloaded file's value.

### 4. Reload-apply orchestration (`main`)

Shared steps on a reload signal, in both modes:

1. `newCfg, err := config.Load(args, time.Now())`; on `err`, **drop silently**
   and keep running.
2. Build the new pipeline: `render.NewPipeline(newCfg.RendererSpecs, startupDrop)`.
   On compile error, drop silently.
3. Build new matcher closures (`makeNewFileMatcher(newCfg)`,
   `makeNewDirMatcher(newCfg)`) and a fresh `watch.Watcher`.
4. `discover.Assign(newCfg.Groups, newCfg.GlobalFilter)`; `Add` each assignment
   with `fromStart=false` (start at EOF — no history replay); `WatchDir` each of
   `dirsToWatch(newCfg)`.
5. Atomically swap the pipeline pointer.
6. `Close()` the old watcher; reassign the loop-local `w` to the new one.

**stdout (`runWatch`):** add `configwatch.Changes()` to the existing `select`.
The reload branch runs steps 1–6 inline.

**TUI (`runWatchTUI`):** the pump goroutine's `select` gains the
`configwatch.Changes()` case. After steps 1–6 it calls `app.Reload(...)` (below).

### 5. TUI panel reseed — `app.Reload` (largest chunk)

The renderer/group/file panels are seeded once in `tui.New`, and shift+digit maps
to a pipeline index. A reload that changes the renderer set makes the panels and
key mapping stale — a real bug, not polish.

Add an `App.Reload(groups []GroupInfo, renderers []RendererInfo, files []FileEntry)`
method that posts a `tea.Msg` (so it runs on the bubbletea goroutine) which:

- Replaces the groups, renderers, and files panels.
- Remaps shift+digit toggles to the new pipeline indices.
- Resets toggle state to config defaults (`StartOff` from the new specs/groups).
- Re-renders existing scrollback under the new renderers, reusing the existing
  scrollback re-render path from the renderer-toggle feature (dual-array
  `scrollbackEvent` + `RenderFn`).
- Preserves scrollback **content** (lines are not dropped).

## Data flow (reload)

```
config file edited
  → configwatch (parent-dir fsnotify, basename filter, 300ms debounce)
  → Changes() signal
  → reload branch in select loop / pump goroutine:
      config.Load(args) ──err──▶ drop silently, keep running
        │ ok
        ├─ NewPipeline(newSpecs, startupDrop) ──err──▶ drop silently
        ├─ new watch.Watcher + matchers(newCfg)
        ├─ discover.Assign(newCfg) → Add(fromStart=false) + WatchDir
        ├─ pipelinePtr.Store(newPipeline)
        ├─ oldWatcher.Close(); w = newWatcher
        └─ (TUI only) app.Reload(groups, renderers, files)
```

## Known limitations (documented, accepted)

- **Reload gap:** lines appended to a still-matching file during the rebuild
  window (old watcher closed → new watcher `Add`-ing at EOF) can be missed.
- **Silent bad reloads:** by choice, a parse/validation failure gives no TUI/stderr
  feedback that the edit didn't take.
- **Deleting the watched file:** reload re-runs `config.Load`, which may fall back
  to CLI-only / no-YAML. If the result fails validation, the old config is kept.

## Testing plan

- `internal/configwatch`: debounce coalescing (burst of events → single signal),
  rename-over-save detection — via temp files, with bounded waits (no reliance on
  real-time fsnotify latency in assertions where avoidable).
- `internal/render`: `atomic.Pointer[Pipeline]` swap visibility — readers see the
  new pipeline after `Store`.
- Reload-apply: exercise the load path (`config.Load` with a changed file →
  expected groups/specs) directly; orchestration verified by existing watch/render
  package tests plus a manual run.
- Project convention: every commit leaves `go test ./...`, `go vet ./...`, and
  `go test -race ./...` green.

## Out of scope

- Re-applying output settings (SSE addr, color, scrollback size, tui-enabled).
- Live reload in `--once` mode.
- Surfacing reload errors/status in the UI (a future one-line TUI status cell
  could revisit the silent-on-error choice without changing this architecture).
