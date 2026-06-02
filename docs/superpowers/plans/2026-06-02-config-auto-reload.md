# Config Auto-Reload Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remember the YAML config file that was loaded at startup, watch it for changes, and live-re-apply groups + renderers (rebuilding the watcher and swapping the pipeline) in both TUI and stdout modes — without restarting the process.

**Architecture:** A small config-agnostic fsnotify wrapper (`internal/configwatch`) emits a debounced "changed" signal. On that signal, both run loops re-run `config.Load`, rebuild the renderer pipeline (stored behind an `atomic.Pointer`), build a **fresh** `watch.Watcher` over the new config, and `Close()` the old one — so files that no longer match are dropped for free (no `Remove` API needed). The TUI additionally reseeds its renderer/group/file panels and re-renders existing scrollback under the new renderers. Bad reloads are dropped silently; output settings (SSE/color/scrollback) are never re-applied.

**Tech Stack:** Go 1.26, `fsnotify`, `bubbletea`/`lipgloss`. Module path: `log-listener`.

**Design spec:** `docs/superpowers/specs/2026-06-02-config-auto-reload-design.md`

---

## File structure

| File | Responsibility | Change |
|------|----------------|--------|
| `internal/config/cli.go` | `Config` struct | Modify: add `SourcePath` field |
| `internal/config/yaml.go` | `loadWithFS` | Modify: set `cfg.SourcePath = yamlPath` |
| `internal/config/yaml_test.go` | config tests | Modify: assert `SourcePath` |
| `internal/configwatch/configwatch.go` | debounced single-file change notifier | Create |
| `internal/configwatch/configwatch_test.go` | configwatch tests | Create |
| `cmd/log-listener/main.go` | wiring: pipeline pointer, `loadRuntime`/`buildWatcher` helpers, reload branches | Modify |
| `cmd/log-listener/main_test.go` | `loadRuntime` test | Modify |
| `internal/tui/app.go` | `App.Reload` + `ReloadMsg` panel reseed | Modify |
| `internal/tui/app_test.go` | reload reseed test | Modify |
| `README.md`, `CHANGELOG.md` | docs | Modify |

---

## Task 1: Surface the resolved config path on `Config`

**Files:**
- Modify: `internal/config/cli.go` (struct around line 19-37)
- Modify: `internal/config/yaml.go` (`loadWithFS`, lines 114-142)
- Test: `internal/config/yaml_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/config/yaml_test.go`:

```go
func TestLoadSetsSourcePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log-listener.yml")
	if err := os.WriteFile(path, []byte("directories:\n  - id: a\n    paths: ["+strconv.Quote(dir)+"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load([]string{"--config", path}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SourcePath != path {
		t.Fatalf("SourcePath = %q, want %q", cfg.SourcePath, path)
	}
}

func TestLoadNoYAMLHasEmptySourcePath(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load([]string{"-d", dir}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SourcePath != "" {
		t.Fatalf("SourcePath = %q, want empty", cfg.SourcePath)
	}
}
```

Ensure the test file imports `os`, `path/filepath`, `strconv`, `time` (add any missing).

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -run TestLoadSetsSourcePath ./internal/config/`
Expected: FAIL — `cfg.SourcePath undefined (type *Config has no field or method SourcePath)`.

- [ ] **Step 3: Add the field**

In `internal/config/cli.go`, inside `type Config struct`, after the `ConfigFile string` line (line 27):

```go
	ConfigFile string

	// SourcePath is the absolute/relative path of the YAML file that was
	// actually loaded (resolved from --config or the default lookup), or ""
	// if no YAML was loaded. Used by the config-reload watcher.
	SourcePath string
```

- [ ] **Step 4: Set it in `loadWithFS`**

In `internal/config/yaml.go`, in `loadWithFS`, set the path right after it's resolved. Replace lines 120-129:

```go
	yamlPath, err := resolveYAMLPath(cfg.ConfigFile, homeDir)
	if err != nil {
		return nil, err
	}
	cfg.SourcePath = yamlPath
	if yamlPath == "" {
		if err := cfg.Validate(); err != nil {
			return nil, err
		}
		return cfg, nil
	}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/config/`
Expected: PASS (all config tests).

- [ ] **Step 6: Commit**

```bash
git add internal/config/cli.go internal/config/yaml.go internal/config/yaml_test.go
git commit -m "config: surface resolved YAML path as Config.SourcePath"
```

---

## Task 2: `internal/configwatch` — debounced single-file change notifier

**Files:**
- Create: `internal/configwatch/configwatch.go`
- Test: `internal/configwatch/configwatch_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/configwatch/configwatch_test.go`:

```go
package configwatch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// waitSignal blocks for up to d for one signal on ch, returning true if one
// arrived.
func waitSignal(ch <-chan struct{}, d time.Duration) bool {
	select {
	case <-ch:
		return true
	case <-time.After(d):
		return false
	}
}

func TestNotifiesOnChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yml")
	if err := os.WriteFile(path, []byte("a: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := New(path, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if err := os.WriteFile(path, []byte("a: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !waitSignal(w.Changes(), 2*time.Second) {
		t.Fatal("expected a change signal after writing the file")
	}
}

func TestCoalescesBurst(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yml")
	if err := os.WriteFile(path, []byte("a: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := New(path, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	for i := 0; i < 5; i++ {
		if err := os.WriteFile(path, []byte("a: x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !waitSignal(w.Changes(), 2*time.Second) {
		t.Fatal("expected one coalesced signal")
	}
	// The burst must not produce a second prompt signal right after.
	if waitSignal(w.Changes(), 300*time.Millisecond) {
		t.Fatal("burst should coalesce into a single signal")
	}
}

func TestIgnoresSiblingFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yml")
	if err := os.WriteFile(path, []byte("a: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := New(path, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// Writing a different file in the same directory must not signal.
	if err := os.WriteFile(filepath.Join(dir, "other.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if waitSignal(w.Changes(), 400*time.Millisecond) {
		t.Fatal("sibling file change must not signal")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/configwatch/`
Expected: FAIL — build error, `New` / package not found.

- [ ] **Step 3: Write the implementation**

Create `internal/configwatch/configwatch.go`:

```go
// Package configwatch watches a single config file for changes and emits a
// debounced "changed" signal. It is intentionally config-agnostic: it knows
// nothing about the YAML schema, so the reload caller owns parsing. Keeping
// fsnotify isolated here lets reload-orchestration logic be tested without
// real filesystem-event timing.
package configwatch

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher reports when the target file settles after a change.
type Watcher struct {
	fs        *fsnotify.Watcher
	target    string // absolute path of the watched file
	debounce  time.Duration
	changes   chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

// New starts watching the file at path. It watches the PARENT directory and
// filters by the file's absolute path, so editor "write temp then rename over
// the target" saves are still detected (a watch on the file inode itself goes
// deaf after such a save). Bursts of events from a single save are coalesced
// into one signal using the debounce window.
func New(path string, debounce time.Duration) (*Watcher, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := fw.Add(filepath.Dir(abs)); err != nil {
		fw.Close()
		return nil, err
	}
	w := &Watcher{
		fs:       fw,
		target:   abs,
		debounce: debounce,
		changes:  make(chan struct{}, 1),
		done:     make(chan struct{}),
	}
	go w.loop()
	return w, nil
}

// Changes delivers a signal each time the target file settles after a change.
// The channel has capacity 1; signals are dropped (not blocked) if the
// consumer hasn't drained the previous one, since a pending reload already
// covers any change.
func (w *Watcher) Changes() <-chan struct{} { return w.changes }

// Close stops the watcher and releases resources.
func (w *Watcher) Close() error {
	var err error
	w.closeOnce.Do(func() {
		close(w.done)
		err = w.fs.Close()
	})
	return err
}

func (w *Watcher) loop() {
	var timer *time.Timer
	var timerC <-chan time.Time
	arm := func() {
		if timer == nil {
			timer = time.NewTimer(w.debounce)
		} else {
			timer.Stop()
			timer.Reset(w.debounce)
		}
		timerC = timer.C
	}
	for {
		select {
		case <-w.done:
			if timer != nil {
				timer.Stop()
			}
			return
		case ev, ok := <-w.fs.Events:
			if !ok {
				return
			}
			abs, err := filepath.Abs(ev.Name)
			if err != nil || abs != w.target {
				continue
			}
			arm()
		case _, ok := <-w.fs.Errors:
			if !ok {
				return
			}
			// Best-effort: ignore watcher errors. A reload is not critical
			// enough to surface noise here.
		case <-timerC:
			timerC = nil
			// Confirm the file still exists before signaling; a transient
			// rename mid-save can briefly remove it.
			if _, err := os.Stat(w.target); err != nil {
				continue
			}
			select {
			case w.changes <- struct{}{}:
			default:
			}
		}
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/configwatch/`
Expected: PASS (all three tests).

- [ ] **Step 5: Run the race detector on the package**

Run: `go test -race ./internal/configwatch/`
Expected: PASS, no data races.

- [ ] **Step 6: Commit**

```bash
git add internal/configwatch/
git commit -m "configwatch: debounced single-file change notifier"
```

---

## Task 3: `loadRuntime` + `buildWatcher` helpers in main

This extracts the duplicated watcher-setup in `runWatch`/`runWatchTUI` into one
`buildWatcher`, and adds a pure `loadRuntime` (load → assign → compile pipeline)
that both startup and reload use. `loadRuntime` is the unit-testable seam.

**Files:**
- Modify: `cmd/log-listener/main.go`
- Test: `cmd/log-listener/main_test.go`

- [ ] **Step 1: Write the failing test**

Add to `cmd/log-listener/main_test.go`:

```go
func TestLoadRuntime(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "app.log")
	if err := os.WriteFile(logPath, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	yml := "files:\n  - id: app\n    paths: [" + strconv.Quote(logPath) + "]\n" +
		"renderers:\n  - name: r1\n    line_regex: \".*\"\n    template: \"{{.0}}\"\n"
	cfgPath := filepath.Join(dir, "log-listener.yml")
	if err := os.WriteFile(cfgPath, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}

	rt, err := loadRuntime([]string{"--config", cfgPath}, false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if rt.pipeline.RendererCount() != 1 {
		t.Fatalf("RendererCount = %d, want 1", rt.pipeline.RendererCount())
	}
	if len(rt.assignments) != 1 || rt.assignments[0].Path != logPath {
		t.Fatalf("assignments = %+v, want one for %s", rt.assignments, logPath)
	}
	if rt.cfg.SourcePath != cfgPath {
		t.Fatalf("SourcePath = %q, want %q", rt.cfg.SourcePath, cfgPath)
	}
}
```

Ensure `main_test.go` imports `os`, `path/filepath`, `strconv`, `time` (add any missing). Confirm the renderer template syntax `{{.0}}` matches the project DSL — if `ParseTemplate` rejects it, use a literal template like `"X"` instead (the test only checks the renderer count, not output).

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -run TestLoadRuntime ./cmd/log-listener/`
Expected: FAIL — `undefined: loadRuntime`.

- [ ] **Step 3: Add the `runtime` type and `loadRuntime`**

In `cmd/log-listener/main.go`, add near the top-level funcs (after `run`):

```go
// runtime bundles the per-config-load derived state: the parsed config, the
// compiled renderer pipeline, and the file→group assignments. Built once at
// startup and rebuilt on every config reload.
type runtime struct {
	cfg         *config.Config
	pipeline    *render.Pipeline
	assignments []discover.Assignment
}

// loadRuntime parses args (re-reading the YAML file), assigns files to groups,
// and compiles the renderer pipeline. dropUnmatched is passed explicitly so a
// reload keeps the STARTUP drop setting rather than the reloaded file's value
// (output settings are out of scope for reload). Pure and side-effect-free —
// the unit-testable seam for reload.
func loadRuntime(args []string, dropUnmatched bool, now time.Time) (*runtime, error) {
	cfg, err := config.Load(args, now)
	if err != nil {
		return nil, err
	}
	assignments, err := discover.Assign(cfg.Groups, cfg.GlobalFilter)
	if err != nil {
		return nil, err
	}
	pipeline, err := render.NewPipeline(cfg.RendererSpecs, dropUnmatched)
	if err != nil {
		return nil, err
	}
	return &runtime{cfg: cfg, pipeline: pipeline, assignments: assignments}, nil
}

// buildWatcher constructs a fresh watch.Watcher wired with matcher closures
// over cfg, registers every assignment as a tailer (fromStart=false → start at
// EOF, so a reload does not replay file history), and adds directory watches.
// Per-file/dir failures are logged to stderr but do not abort.
func buildWatcher(cfg *config.Config, assignments []discover.Assignment, stderr io.Writer) (*watch.Watcher, error) {
	w, err := watch.New(makeNewFileMatcher(cfg), 2*time.Second)
	if err != nil {
		return nil, err
	}
	w.SetDirMatcher(makeNewDirMatcher(cfg))
	for _, a := range assignments {
		if err := w.Add(a.Path, a.GroupID, false); err != nil {
			fmt.Fprintf(stderr, "log-listener: cannot tail %s: %v\n", a.Path, err)
		}
	}
	for _, d := range dirsToWatch(cfg) {
		if err := w.WatchDir(d); err != nil {
			fmt.Fprintf(stderr, "log-listener: cannot watch %s: %v\n", d, err)
		}
	}
	return w, nil
}
```

- [ ] **Step 4: Refactor `runWatch` and `runWatchTUI` to use `buildWatcher`**

In `runWatch`, replace the watcher construction block (current lines 129-145, from `w, err := watch.New(...)` through the `for _, d := range dirsToWatch(cfg)` loop) with:

```go
	w, err := buildWatcher(cfg, assignments, stderr)
	if err != nil {
		return err
	}
	defer w.Close()
```

In `runWatchTUI`, replace the identical block (current lines 203-219) with the same four lines.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./cmd/log-listener/`
Expected: PASS — `TestLoadRuntime` plus all existing e2e tests (the refactor must not change behavior).

- [ ] **Step 6: Commit**

```bash
git add cmd/log-listener/main.go cmd/log-listener/main_test.go
git commit -m "main: extract loadRuntime + buildWatcher helpers"
```

---

## Task 4: Pipeline behind `atomic.Pointer` so reload can swap it

Routes every renderer access in the live paths through an `atomic.Pointer[render.Pipeline]`,
so a reload can atomically replace the pipeline and all readers (stdout fanout,
SSE, TUI pump, TUI toggle callbacks) see the new one. No behavior change yet —
this is pure wiring, verified by the existing tests still passing.

**Files:**
- Modify: `cmd/log-listener/main.go`

- [ ] **Step 1: Add the `sync/atomic` import**

In `cmd/log-listener/main.go` import block, add `"sync/atomic"` (keep imports grouped/sorted with the existing stdlib group).

- [ ] **Step 2: Create the holder in `run` and thread it through**

In `run`, after the pipeline is built (current lines 48-52), add:

```go
	pipeline, err := render.NewPipeline(cfg.RendererSpecs, cfg.DropUnmatched)
	if err != nil {
		fmt.Fprintln(stderr, "log-listener:", err)
		return 2
	}
	var pipePtr atomic.Pointer[render.Pipeline]
	pipePtr.Store(pipeline)
```

- [ ] **Step 3: Change `runWatch`/`runWatchTUI`/`emit` signatures to take the pointer**

Change `emit` to read the current pipeline:

```go
func emit(pipePtr *atomic.Pointer[render.Pipeline], stdoutSink *sink.Stdout, sseHub *sink.SSEHub, group, path, line string) {
	ev, ok := pipePtr.Load().Render(time.Now(), group, path, line)
	if !ok {
		return
	}
	stdoutSink.Emit(ev)
	if sseHub != nil {
		sseHub.Emit(ev)
	}
}
```

Update the two `runWatch`/`runWatchTUI` call sites and `runOnce`:
- `runOnce` keeps using the concrete `pipeline` (no reload in --once). Leave its `emit` calls — but since `emit` now takes a pointer, give `runOnce` a local pointer: at the top of `runOnce`, the simplest is to keep `runOnce` calling `pipeline.Render` directly. Replace `runOnce`'s `emit(...)` call (current line 117) with an inline render to avoid threading the pointer:

```go
		for s.Scan() {
			ev, ok := pipeline.Render(time.Now(), a.GroupID, a.Path, s.Text())
			if ok {
				stdoutSink.Emit(ev)
				if sseHub != nil {
					sseHub.Emit(ev)
				}
			}
		}
```

(Keep `runOnce`'s signature taking `pipeline *render.Pipeline`.)

- [ ] **Step 4: Update `run` to pass `&pipePtr` into the watch paths**

Change the calls in `run`:
- `runWatchTUI(cfg, assignments, &pipePtr, sseHub, stderr)`
- `runWatch(cfg, assignments, &pipePtr, stdoutSink, sseHub, stderr)`

And their signatures:

```go
func runWatch(cfg *config.Config, assignments []discover.Assignment, pipePtr *atomic.Pointer[render.Pipeline], stdoutSink *sink.Stdout, sseHub *sink.SSEHub, stderr io.Writer) error
```

```go
func runWatchTUI(cfg *config.Config, assignments []discover.Assignment, pipePtr *atomic.Pointer[render.Pipeline], sseHub *sink.SSEHub, stderr io.Writer) error
```

In `runWatch`'s event loop, change the emit call (current line 166 and the two drain calls at 174) to:

```go
			emit(pipePtr, stdoutSink, sseHub, ev.Group, ev.Path, ev.Line)
```

- [ ] **Step 5: Route the TUI pump and callbacks through the pointer**

In `runWatchTUI`, the `tui.Options` callbacks and the pump goroutine must read `pipePtr.Load()`:

```go
	app := tui.New(tui.Options{
		Scrollback:    cfg.TUIScrollback,
		InitialFiles:  initial,
		Groups:        groups,
		Renderers:     renderers,
		SetRendererOn: func(i int, on bool) { pipePtr.Load().SetRendererEnabled(i, on) },
		RenderFn: func(group, file, raw string) (render.Event, bool) {
			return pipePtr.Load().Render(time.Now(), group, file, raw)
		},
	})
```

The `renderers` seed slice is still built from the startup `pipeline`. Replace the
`pipeline.RendererCount()` / `pipeline.RendererName(i)` / `pipeline.IsEnabled(i)`
calls (current lines 233-239) with `pipePtr.Load()`:

```go
	p0 := pipePtr.Load()
	renderers := make([]tui.RendererInfo, p0.RendererCount())
	for i := range renderers {
		renderers[i] = tui.RendererInfo{
			Name:     p0.RendererName(i),
			StartOff: !p0.IsEnabled(i),
		}
	}
```

In the pump goroutine, change `pipeline.Render(...)` (current line 268) to `pipePtr.Load().Render(...)`.

- [ ] **Step 6: Build and run the full suite**

Run: `go build ./... && go test ./... && go vet ./...`
Expected: PASS — no behavior change; all existing e2e/TUI tests still green.

- [ ] **Step 7: Run the race detector**

Run: `go test -race ./cmd/log-listener/ ./internal/tui/`
Expected: PASS, no data races.

- [ ] **Step 8: Commit**

```bash
git add cmd/log-listener/main.go
git commit -m "main: hold renderer pipeline behind atomic.Pointer"
```

---

## Task 5: `App.Reload` — reseed TUI panels + re-render scrollback

Adds the TUI-side apply: a `ReloadMsg` that replaces the groups/renderers/files
panels, resets toggle state to the new config defaults, and re-renders existing
scrollback under the new renderers (reusing `reRenderAll`). Runs on the
bubbletea goroutine via `prog.Send`.

**Files:**
- Modify: `internal/tui/app.go`
- Test: `internal/tui/app_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/tui/app_test.go`:

```go
func TestReloadMsgReseedsPanelsAndState(t *testing.T) {
	m := newModel(1000)
	// Seed an initial config: one group "old", one renderer "r_old" (on).
	m.groupOrder = []string{"old"}
	m.groupEnabled = map[string]bool{"old": true}
	m.rendererOrder = []string{"r_old"}
	m.rendererEnabled = []bool{true}
	// renderFn echoes raw as a single text line so reRenderAll has something
	// to rebuild from.
	m.renderFn = func(group, file, raw string) (render.Event, bool) {
		return render.Event{Group: group, File: file, Raw: raw,
			Rendered: []render.Part{{Type: "text", Value: raw}}}, true
	}
	m.appendStored(scrollbackEvent{group: "old", file: "f", raw: "line1"})

	// Reload to a new config: group "new", renderer "r_new" starting off.
	newM, _ := m.Update(ReloadMsg{
		Groups:    []GroupInfo{{ID: "new", StartOff: false}},
		Renderers: []RendererInfo{{Name: "r_new", StartOff: true}},
		Files:     []FileEntry{{Path: "/x/new.log", Group: "new"}},
	})
	m = newM.(*model)

	if len(m.groupOrder) != 1 || m.groupOrder[0] != "new" {
		t.Fatalf("groupOrder = %v, want [new]", m.groupOrder)
	}
	if _, ok := m.groupEnabled["old"]; ok {
		t.Fatal("stale group 'old' should be gone after reload")
	}
	if len(m.rendererOrder) != 1 || m.rendererOrder[0] != "r_new" {
		t.Fatalf("rendererOrder = %v, want [r_new]", m.rendererOrder)
	}
	if m.rendererEnabled[0] != false {
		t.Fatal("renderer r_new has off:true, should seed disabled")
	}
	if len(m.files) != 1 || m.files[0].Path != "/x/new.log" {
		t.Fatalf("files = %+v, want one /x/new.log", m.files)
	}
	// Scrollback content is preserved (one source entry survives).
	if len(m.entries) != 1 || m.entries[0].raw != "line1" {
		t.Fatalf("entries = %+v, want preserved line1", m.entries)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -run TestReloadMsgReseedsPanelsAndState ./internal/tui/`
Expected: FAIL — `undefined: ReloadMsg`.

- [ ] **Step 3: Define `ReloadMsg` and the `App.Reload` sender**

In `internal/tui/app.go`, after the `QuitMsg` declaration (line 98):

```go
// ReloadMsg replaces the renderer/group/file panels after a live config
// reload. Toggle state is reset to the supplied StartOff defaults and existing
// scrollback is re-rendered under the new renderers.
type ReloadMsg struct {
	Groups    []GroupInfo
	Renderers []RendererInfo
	Files     []FileEntry
}
```

After the `SetFiles` method (line 208), add:

```go
// Reload reseeds the panels and re-renders scrollback after a config reload.
// Safe from any goroutine.
func (a *App) Reload(groups []GroupInfo, renderers []RendererInfo, files []FileEntry) {
	a.mu.Lock()
	if a.done {
		a.mu.Unlock()
		return
	}
	prog := a.prog
	a.mu.Unlock()
	prog.Send(ReloadMsg{Groups: groups, Renderers: renderers, Files: files})
}
```

- [ ] **Step 4: Handle `ReloadMsg` in `Update`**

In `internal/tui/app.go`, in the `Update` method's outer `switch msg := msg.(type)`, add a case alongside `EventMsg`/`FileListMsg`/`QuitMsg` (around line 627):

```go
	case ReloadMsg:
		m.applyReload(msg)
```

Then add the method near `appendEvent`:

```go
// applyReload swaps in the new config's panels and toggle state, then
// re-renders existing scrollback through renderFn (which now reads the
// reloaded pipeline). Scrollback source entries are preserved; only their
// rendered lines are rebuilt. Toggle state is reset to the new config's
// StartOff defaults — the renderer set may have changed, so preserving old
// indices would be ambiguous.
func (m *model) applyReload(msg ReloadMsg) {
	m.groupOrder = m.groupOrder[:0]
	m.groupEnabled = map[string]bool{}
	for _, g := range msg.Groups {
		m.groupOrder = append(m.groupOrder, g.ID)
		m.groupEnabled[g.ID] = !g.StartOff
	}
	m.rendererOrder = m.rendererOrder[:0]
	m.rendererEnabled = m.rendererEnabled[:0]
	for _, r := range msg.Renderers {
		m.rendererOrder = append(m.rendererOrder, r.Name)
		m.rendererEnabled = append(m.rendererEnabled, !r.StartOff)
	}
	m.files = msg.Files
	if m.filesScroll >= len(m.files) {
		m.filesScroll = 0
	}
	if m.groupsScroll >= len(m.groupOrder) {
		m.groupsScroll = 0
	}
	if m.renderersScroll >= len(m.rendererOrder) {
		m.renderersScroll = 0
	}
	m.reRenderAll()
}
```

Note: `reRenderAll` already clamps `streamTop`/`searchHit` to the rebuilt line
count (lines 720-728), so no extra anchor handling is needed.

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test -run TestReloadMsgReseedsPanelsAndState ./internal/tui/`
Expected: PASS.

- [ ] **Step 6: Run the full TUI suite + race**

Run: `go test ./internal/tui/ && go test -race ./internal/tui/`
Expected: PASS, no data races.

- [ ] **Step 7: Commit**

```bash
git add internal/tui/app.go internal/tui/app_test.go
git commit -m "tui: App.Reload reseeds panels and re-renders scrollback"
```

---

## Task 6: Wire config-reload into stdout mode (`runWatch`)

**Files:**
- Modify: `cmd/log-listener/main.go` (`run` passes `args`; `runWatch` adds the reload branch)

- [ ] **Step 1: Thread `args` and the startup-drop value into `runWatch`**

`runWatch` needs the original CLI `args` (to re-run `config.Load`) and the
startup `drop_unmatched` (to keep output settings stable). Change its signature:

```go
func runWatch(cfg *config.Config, args []string, dropUnmatched bool, assignments []discover.Assignment, pipePtr *atomic.Pointer[render.Pipeline], stdoutSink *sink.Stdout, sseHub *sink.SSEHub, stderr io.Writer) error
```

In `run`, update the call:

```go
	if err := runWatch(cfg, args, cfg.DropUnmatched, assignments, &pipePtr, stdoutSink, sseHub, stderr); err != nil {
```

- [ ] **Step 2: Start the config watcher inside `runWatch`**

After `defer w.Close()` and before the `ctx, cancel := ...` block, add:

```go
	var cfgChanges <-chan struct{}
	if cfg.SourcePath != "" {
		cw, err := configwatch.New(cfg.SourcePath, 300*time.Millisecond)
		if err != nil {
			fmt.Fprintf(stderr, "log-listener: config watch disabled: %v\n", err)
		} else {
			defer cw.Close()
			cfgChanges = cw.Changes()
		}
	}
```

Add `"log-listener/internal/configwatch"` to the import block.

- [ ] **Step 3: Add the reload branch to the select loop**

In `runWatch`'s main `for { select { ... } }` (the non-drain one), add a case
alongside `case ev := <-w.Events():`:

```go
		case <-cfgChanges:
			rt, err := loadRuntime(args, dropUnmatched, time.Now())
			if err != nil {
				continue // silent: keep the last-good config running
			}
			newW, err := buildWatcher(rt.cfg, rt.assignments, stderr)
			if err != nil {
				continue
			}
			pipePtr.Store(rt.pipeline)
			w.Close()
			w = newW
```

Because `w` is a loop-local reassigned here, the `case ev := <-w.Events()` and
`case e := <-w.Errors()` arms read the new watcher on the next iteration. The
`defer w.Close()` still fires on the final `w` at return (closing the old one
here prevents a leak).

- [ ] **Step 4: Build and smoke-test the suite**

Run: `go build ./... && go test ./cmd/log-listener/ && go vet ./...`
Expected: PASS — existing e2e tests unaffected (they don't write a config file mid-run).

- [ ] **Step 5: Run the race detector**

Run: `go test -race ./cmd/log-listener/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/log-listener/main.go
git commit -m "main: live config reload in stdout streaming mode"
```

---

## Task 7: Wire config-reload into TUI mode (`runWatchTUI`)

**Files:**
- Modify: `cmd/log-listener/main.go` (`run` passes `args`; `runWatchTUI` pump adds the reload branch + `app.Reload`)

- [ ] **Step 1: Thread `args` and startup-drop into `runWatchTUI`**

Change its signature:

```go
func runWatchTUI(cfg *config.Config, args []string, dropUnmatched bool, assignments []discover.Assignment, pipePtr *atomic.Pointer[render.Pipeline], sseHub *sink.SSEHub, stderr io.Writer) error
```

In `run`, update the call:

```go
		if err := runWatchTUI(cfg, args, cfg.DropUnmatched, assignments, &pipePtr, sseHub, stderr); err != nil {
```

- [ ] **Step 2: Start the config watcher**

In `runWatchTUI`, after `defer w.Close()`, add the same watcher-start block as Task 6 Step 2:

```go
	var cfgChanges <-chan struct{}
	if cfg.SourcePath != "" {
		cw, err := configwatch.New(cfg.SourcePath, 300*time.Millisecond)
		if err != nil {
			fmt.Fprintf(stderr, "log-listener: config watch disabled: %v\n", err)
		} else {
			defer cw.Close()
			cfgChanges = cw.Changes()
		}
	}
```

- [ ] **Step 3: Add the reload branch to the pump goroutine's select**

In the pump goroutine (`go func() { for { select { ... } } }()`), add a case
alongside `case ev := <-w.Events():`:

```go
			case <-cfgChanges:
				rt, err := loadRuntime(args, dropUnmatched, time.Now())
				if err != nil {
					continue // silent
				}
				newW, err := buildWatcher(rt.cfg, rt.assignments, stderr)
				if err != nil {
					continue
				}
				pipePtr.Store(rt.pipeline)
				w.Close()
				w = newW

				// Reseed the TUI panels + re-render scrollback under the new
				// renderers. Toggle state resets to the new config defaults.
				p := rt.pipeline
				newGroups := make([]tui.GroupInfo, 0, len(rt.cfg.Groups))
				for _, g := range rt.cfg.Groups {
					newGroups = append(newGroups, tui.GroupInfo{ID: g.ID, StartOff: g.StartOff})
				}
				newRenderers := make([]tui.RendererInfo, p.RendererCount())
				for i := range newRenderers {
					newRenderers[i] = tui.RendererInfo{Name: p.RendererName(i), StartOff: !p.IsEnabled(i)}
				}
				newFiles := make([]tui.FileEntry, 0, len(rt.assignments))
				for _, a := range rt.assignments {
					newFiles = append(newFiles, tui.FileEntry{Path: a.Path, Group: a.GroupID})
				}
				app.Reload(newGroups, newRenderers, newFiles)
```

The pump goroutine already has `w` as the loop-relevant watcher; confirm `w` is
in scope there (it is — declared in `runWatchTUI` before the goroutine). Since
the goroutine reassigns `w`, and no other goroutine reads `w`, this is safe
(the bubbletea goroutine only touches `app`/`pipePtr`).

- [ ] **Step 4: Build and run the suite**

Run: `go build ./... && go test ./cmd/log-listener/ && go vet ./...`
Expected: PASS.

- [ ] **Step 5: Race detector across the touched packages**

Run: `go test -race ./cmd/log-listener/ ./internal/tui/ ./internal/configwatch/`
Expected: PASS, no data races.

- [ ] **Step 6: Manual verification**

```bash
make build
# In one terminal, create a config and run (TTY → TUI):
cat > /tmp/ll.yml <<'YAML'
files:
  - id: app
    paths: [/tmp/ll-demo.log]
renderers:
  - name: plain
    line_regex: ".*"
    template: "{{.0}}"
YAML
: > /tmp/ll-demo.log
./bin/log-listener --config /tmp/ll.yml
# In a second terminal: append lines, then EDIT /tmp/ll.yml (e.g. add/rename a
# renderer or add a second file path) and save.
echo "hello $(date)" >> /tmp/ll-demo.log
```
Expected: lines appear; after saving an edit to `/tmp/ll.yml`, the renderers/groups
panels (Ctrl+E / digit panels) reflect the new config and existing scrollback
re-renders. A deliberately broken YAML edit leaves the session running unchanged.

- [ ] **Step 7: Commit**

```bash
git add cmd/log-listener/main.go
git commit -m "main: live config reload in TUI mode"
```

---

## Task 8: Documentation + full verification

**Files:**
- Modify: `README.md`, `CHANGELOG.md`

- [ ] **Step 1: Document the feature in README**

Add a section (near the config/YAML docs) describing config auto-reload:

```markdown
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
```

- [ ] **Step 2: Add a CHANGELOG entry**

Add under the unreleased/top section:

```markdown
- **Config auto-reload**: the loaded YAML config file is now watched; edits
  re-apply groups and renderers live (rebuilding the file watcher and swapping
  the renderer pipeline) in both TUI and stdout modes. Output settings are not
  re-applied; invalid edits are ignored silently.
```

- [ ] **Step 3: Full green-bar verification**

Run:
```bash
go build ./... && go test ./... && go vet ./... && go test -race ./...
```
Expected: ALL PASS.

- [ ] **Step 4: Commit**

```bash
git add README.md CHANGELOG.md
git commit -m "docs: document config auto-reload"
```

---

## Self-review notes (verify during execution)

- **Spec coverage:** SourcePath (T1), configwatch debounce + rename-over-save + sibling-ignore (T2), rebuild-not-mutate watcher + loadRuntime (T3), atomic pipeline swap (T4), TUI panel reseed + scrollback re-render + toggle reset (T5), stdout reload (T6), TUI reload (T7), silent-on-error (T6/T7 `continue`), output-settings-not-reapplied (T3 `dropUnmatched` param + ignoring reloaded output fields), docs incl. known limitations (T8). All spec sections mapped.
- **Type consistency:** `runtime{cfg,pipeline,assignments}`, `loadRuntime(args,dropUnmatched,now)`, `buildWatcher(cfg,assignments,stderr)`, `ReloadMsg{Groups,Renderers,Files}`, `App.Reload(groups,renderers,files)`, `applyReload(msg)` — names used identically across tasks.
- **Template DSL caveat (T3 test):** if `{{.0}}` is not valid in this project's template parser, substitute a literal template string; the test asserts renderer count, not rendered output. Verify against `internal/render/template.go` when writing the test.
- **`w` reassignment:** both run loops keep `w` loop-local and reassign in the reload branch; `defer w.Close()` closes the final watcher, the branch closes the superseded one. No other goroutine reads `w`.
