# Sink Interface + Fanout Registry Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `main.go`'s hardcoded output fan-out with a single `sink.Fanout` registry behind a `sink.Sink` interface, so output sinks are uniform and a future build-tag file can add/omit one.

**Architecture:** Add a `Sink` interface (`Emit` + `Close`) and a `Fanout` registry (ordered, nil-skipping, itself a `Sink`) to `internal/sink`. `Stdout`, `FileSink`, and `SSEHub` already satisfy the interface. Thread one `*sink.Fanout` through `emit`, `runOnce`, `runWatch`, `runWatchTUI`, and `run()`, replacing the three individual sink parameters and the per-call nil-guarded fan-out. Behavior is preserved except one documented micro-change: TUI-mode preload now also reaches SSE (see spec).

**Tech Stack:** Go 1.26, standard library (`errors`, `reflect`). No new dependencies.

**Spec:** `docs/superpowers/specs/2026-06-08-sink-interface-registry-design.md`

---

## File Structure

| File | Responsibility | Change |
|------|----------------|--------|
| `internal/sink/fanout.go` | `Sink` interface + `Fanout` registry | **Create** |
| `internal/sink/fanout_test.go` | Unit tests for `Fanout` | **Create** |
| `main.go` | Wire one `*sink.Fanout` through `emit`/`runOnce`/`runWatch`/`runWatchTUI`/`run` | **Modify** |

**Key facts for the implementer (verified against current code):**

- `Stdout`, `FileSink`, `SSEHub` each already have `Emit(render.Event)` and `Close() error`. No changes to those types are needed.
- In `main.go`, `sseHub` is a `*sink.SSEHub` that is **nil** when `cfg.SSEAddr == ""`, and `fileSink` is a `*sink.FileSink` that is **nil** when `cfg.OutputFile == ""`. Passing a nil concrete pointer through the `Sink` interface produces a *typed-nil* interface value (`s == nil` is **false**), so `Fanout` must detect typed-nil via reflection — otherwise `Emit` would call a method on a nil receiver and panic.
- `emit`, `runOnce`, `runWatch`, `runWatchTUI` are called **only** from `run()` in `main.go`. No test calls them directly (tests drive the `run()` entry point), so changing their signatures is internal.
- `main()` does `os.Exit(run(...))`, so every `return 1` in `run()` terminates the process. Collapsing the individual `defer sseHub.Close()` / `defer fileSink.Close()` into one `defer fanout.Close()` per branch is safe: error paths that return before building the fanout exit the process immediately, and `SSEHub.Close()` is idempotent.

---

## Task 1: `sink.Sink` interface + `sink.Fanout` registry

**Files:**
- Create: `internal/sink/fanout.go`
- Create: `internal/sink/fanout_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/sink/fanout_test.go`:

```go
package sink

import (
	"errors"
	"testing"

	"github.com/homeend/log-listener/internal/render"
)

// recSink records Emit/Close calls into a shared log for order assertions.
type recSink struct {
	name     string
	log      *[]string
	closeErr error
}

func (r *recSink) Emit(ev render.Event) { *r.log = append(*r.log, r.name+":emit:"+ev.ID) }
func (r *recSink) Close() error         { *r.log = append(*r.log, r.name+":close"); return r.closeErr }

func TestFanoutEmitsToAllInRegistrationOrder(t *testing.T) {
	var log []string
	a := &recSink{name: "a", log: &log}
	b := &recSink{name: "b", log: &log}
	f := NewFanout(a, b)

	f.Emit(render.Event{ID: "L1"})

	want := []string{"a:emit:L1", "b:emit:L1"}
	if len(log) != len(want) || log[0] != want[0] || log[1] != want[1] {
		t.Fatalf("emit order = %v, want %v", log, want)
	}
}

func TestNewFanoutSkipsUntypedNil(t *testing.T) {
	var log []string
	a := &recSink{name: "a", log: &log}
	f := NewFanout(a, nil) // untyped nil interface

	f.Emit(render.Event{ID: "L1"})

	if len(log) != 1 || log[0] != "a:emit:L1" {
		t.Fatalf("log = %v, want only a:emit:L1", log)
	}
}

func TestFanoutAddSkipsTypedNilPointer(t *testing.T) {
	f := NewFanout()
	// A nil *FileSink passed as a Sink is a typed-nil interface (s == nil is
	// false). Add must skip it so Emit never dereferences a nil receiver.
	var fs *FileSink
	f.Add(fs)

	// Must not panic and must emit to nothing.
	f.Emit(render.Event{ID: "L1"})
}

func TestFanoutCloseClosesAllAndJoinsErrors(t *testing.T) {
	var log []string
	errBoom := errors.New("boom")
	a := &recSink{name: "a", log: &log, closeErr: errBoom}
	b := &recSink{name: "b", log: &log}
	f := NewFanout(a, b)

	err := f.Close()

	if len(log) != 2 || log[0] != "a:close" || log[1] != "b:close" {
		t.Fatalf("close order = %v, want [a:close b:close]", log)
	}
	if !errors.Is(err, errBoom) {
		t.Fatalf("Close() err = %v, want it to wrap errBoom", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail (compile error)**

Run: `go test ./internal/sink/ -run TestFanout -v`
Expected: FAIL — build error, `undefined: NewFanout` / `undefined: Fanout`.

- [ ] **Step 3: Write the implementation**

Create `internal/sink/fanout.go`:

```go
package sink

import (
	"errors"
	"reflect"

	"github.com/homeend/log-listener/internal/render"
)

// Sink is a passive output destination that receives every emitted event.
// Stdout, FileSink, and SSEHub all satisfy it.
type Sink interface {
	Emit(render.Event)
	Close() error
}

// Fanout is the ordered registry of passive sinks. It emits to each sink in
// registration order and is itself a Sink, so it composes. nil sinks are
// skipped at registration, which lets a build-tagged constructor return nil
// when its sink is compiled out, and lets callers pass a nil *SSEHub/*FileSink
// without a guard.
type Fanout struct {
	sinks []Sink
}

// NewFanout builds a Fanout from the given sinks, skipping any nil entries
// (both untyped nil and typed-nil pointers).
func NewFanout(sinks ...Sink) *Fanout {
	f := &Fanout{}
	for _, s := range sinks {
		f.Add(s)
	}
	return f
}

// Add registers a sink, skipping nil. This is the plug-in point a future
// build-tagged constructor uses: fanout.Add(buildSSE(cfg)) is a no-op when the
// constructor returns nil.
func (f *Fanout) Add(s Sink) {
	if isNilSink(s) {
		return
	}
	f.sinks = append(f.sinks, s)
}

// Emit fans the event out to every registered sink in registration order.
func (f *Fanout) Emit(ev render.Event) {
	for _, s := range f.sinks {
		s.Emit(ev)
	}
}

// Close closes every registered sink, continuing past errors, and returns all
// errors joined.
func (f *Fanout) Close() error {
	var errs []error
	for _, s := range f.sinks {
		if err := s.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// isNilSink reports whether s is a nil interface or a typed-nil pointer. A nil
// *SSEHub or *FileSink passed as a Sink is a non-nil interface wrapping a nil
// pointer, so a plain s == nil check is insufficient.
func isNilSink(s Sink) bool {
	if s == nil {
		return true
	}
	v := reflect.ValueOf(s)
	return v.Kind() == reflect.Ptr && v.IsNil()
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/sink/ -run TestFanout -v`
Expected: PASS — all four tests pass.

- [ ] **Step 5: Run the full sink package + vet**

Run: `go test ./internal/sink/ && go vet ./internal/sink/`
Expected: PASS, no vet warnings.

- [ ] **Step 6: Commit**

```bash
git add internal/sink/fanout.go internal/sink/fanout_test.go
git commit -m "feat(sink): add Sink interface + Fanout registry"
```

---

## Task 2: Thread `*sink.Fanout` through `main.go`

This task is a behavior-preserving refactor (except the documented TUI-preload→SSE change). It has no new unit tests of its own; the existing test suite — stdout output, `-o` output-file passthrough, SSE broadcast, e2e — is the regression proof. After the change, those tests must pass unchanged.

**Files:**
- Modify: `main.go` (functions `emit`, `runOnce`, `runWatch`, `runWatchTUI`, and the `run()` wiring)

- [ ] **Step 1: Rewrite `emit` to take a `*sink.Fanout`**

Replace the `emit` function (currently at `main.go:452`) with:

```go
func emit(pipePtr *atomic.Pointer[render.Pipeline], buf *linebuf.Buffer, fanout *sink.Fanout, group, path, line string) {
	ev, ok := pipePtr.Load().Render(time.Now(), group, path, line)
	if !ok {
		return
	}
	ev.ID = buf.Append(ev)
	fanout.Emit(ev)
}
```

(Keep the existing doc comment above `emit`; it still describes routing a raw line through the pipeline then fanning out.)

- [ ] **Step 2: Rewrite `runOnce` to take a `*sink.Fanout`**

Replace the `runOnce` function (signature at `main.go:318`) with:

```go
func runOnce(preloadEvents []render.Event, assignments []discover.Assignment, pipeline *render.Pipeline, fanout *sink.Fanout) error {
	for _, ev := range preloadEvents {
		fanout.Emit(ev)
	}
	for _, a := range assignments {
		f, err := os.Open(a.Path)
		if err != nil {
			return fmt.Errorf("open %s: %w", a.Path, err)
		}
		s := bufio.NewScanner(f)
		s.Buffer(make([]byte, 64*1024), 16*1024*1024)
		for s.Scan() {
			ev, ok := pipeline.Render(time.Now(), a.GroupID, a.Path, s.Text())
			if ok {
				fanout.Emit(ev)
			}
		}
		if err := s.Err(); err != nil {
			f.Close()
			return fmt.Errorf("read %s: %w", a.Path, err)
		}
		f.Close()
	}
	return nil
}
```

(Keep the existing doc comment above `runOnce`.)

- [ ] **Step 3: Update `runWatch` signature, preload loop, and `emit` calls**

In `runWatch` (signature at `main.go:356`):

1. Change the signature — replace `stdoutSink *sink.Stdout, sseHub *sink.SSEHub, fileSink *sink.FileSink` with `fanout *sink.Fanout`:

```go
func runWatch(cfg *config.Config, args []string, dropUnmatched bool, assignments []discover.Assignment, pipePtr *atomic.Pointer[render.Pipeline], buf *linebuf.Buffer, fanout *sink.Fanout, preloadEvents []render.Event, stderr io.Writer) error {
```

2. Replace the preload loop:

```go
	for _, ev := range preloadEvents {
		stdoutSink.Emit(ev)
		if sseHub != nil {
			sseHub.Emit(ev)
		}
		if fileSink != nil {
			fileSink.Emit(ev)
		}
	}
```

with:

```go
	for _, ev := range preloadEvents {
		fanout.Emit(ev)
	}
```

3. Replace **both** `emit(...)` call sites (the live one in the main `select`, and the one in the `<-ctx.Done()` drain loop):

```go
emit(pipePtr, buf, stdoutSink, sseHub, fileSink, ev.Group, ev.Path, ev.Line)
```

with:

```go
emit(pipePtr, buf, fanout, ev.Group, ev.Path, ev.Line)
```

- [ ] **Step 4: Update `runWatchTUI` signature, preload loop, and pump fan-out**

In `runWatchTUI` (signature at `main.go:471`):

1. Change the signature — replace `sseHub *sink.SSEHub, fileSink *sink.FileSink` with `fanout *sink.Fanout`:

```go
func runWatchTUI(cfg *config.Config, args []string, dropUnmatched bool, assignments []discover.Assignment, pipePtr *atomic.Pointer[render.Pipeline], buf *linebuf.Buffer, fanout *sink.Fanout, km *keymap.Keymap, preloadEvents []render.Event, stderr io.Writer) error {
```

2. Replace the preload-to-file block:

```go
	if fileSink != nil {
		for _, ev := range preloadEvents {
			fileSink.Emit(ev)
		}
	}
```

with (this is the documented unify — preload now also reaches SSE):

```go
	for _, ev := range preloadEvents {
		fanout.Emit(ev)
	}
```

3. In the pump goroutine, replace:

```go
				rev.ID = buf.Append(rev)
				app.Push(rev)
				if sseHub != nil {
					sseHub.Emit(rev)
				}
				if fileSink != nil {
					fileSink.Emit(rev)
				}
```

with:

```go
				rev.ID = buf.Append(rev)
				app.Push(rev)
				fanout.Emit(rev)
```

- [ ] **Step 5: Update `run()` wiring — remove individual defers, build per-branch Fanout**

In `run()`:

1. Remove the line `defer sseHub.Close()` (inside the `if cfg.SSEAddr != ""` block, ~`main.go:154`).
2. Remove the line `defer fileSink.Close()` (inside the `if cfg.OutputFile != ""` block, ~`main.go:163`).

   Keep the construction of `stdoutSink`, `sseHub` (with `sseHub.Start()`), and `fileSink` (with `sink.OpenFile`) exactly as-is, including their `return 1` error paths.

3. Replace the `--once` branch:

```go
	if cfg.Once {
		if err := runOnce(preloadEvents, assignments, pipeline, stdoutSink, sseHub, fileSink); err != nil {
			fmt.Fprintln(stderr, "log-listener:", err)
			return 1
		}
		return 0
	}
```

with:

```go
	if cfg.Once {
		fanout := sink.NewFanout(stdoutSink, sseHub, fileSink)
		defer fanout.Close()
		if err := runOnce(preloadEvents, assignments, pipeline, fanout); err != nil {
			fmt.Fprintln(stderr, "log-listener:", err)
			return 1
		}
		return 0
	}
```

4. Replace the TUI branch:

```go
	if useTUI {
		if err := runWatchTUI(cfg, args, cfg.DropUnmatched, assignments, &pipePtr, buf, sseHub, fileSink, km, preloadEvents, stderr); err != nil {
			fmt.Fprintln(stderr, "log-listener:", err)
			return 1
		}
		return 0
	}
```

with (TUI fanout excludes `stdoutSink` — bubbletea owns the terminal):

```go
	if useTUI {
		fanout := sink.NewFanout(sseHub, fileSink)
		defer fanout.Close()
		if err := runWatchTUI(cfg, args, cfg.DropUnmatched, assignments, &pipePtr, buf, fanout, km, preloadEvents, stderr); err != nil {
			fmt.Fprintln(stderr, "log-listener:", err)
			return 1
		}
		return 0
	}
```

5. Replace the final non-TUI `runWatch` branch:

```go
	if err := runWatch(cfg, args, cfg.DropUnmatched, assignments, &pipePtr, buf, stdoutSink, sseHub, fileSink, preloadEvents, stderr); err != nil {
		fmt.Fprintln(stderr, "log-listener:", err)
		return 1
	}
	return 0
```

with:

```go
	fanout := sink.NewFanout(stdoutSink, sseHub, fileSink)
	defer fanout.Close()
	if err := runWatch(cfg, args, cfg.DropUnmatched, assignments, &pipePtr, buf, fanout, preloadEvents, stderr); err != nil {
		fmt.Fprintln(stderr, "log-listener:", err)
		return 1
	}
	return 0
```

- [ ] **Step 6: Build and verify no stale references remain**

Run: `go build ./... && grep -n "stdoutSink, sseHub, fileSink\|sseHub.Emit\|fileSink.Emit\|sseHub.Close\|fileSink.Close" main.go`
Expected: build succeeds; the grep prints **nothing** (all individual sink fan-out/close references are gone; `sseHub.Start()` and the `sink.NewSSEHub`/`sink.OpenFile` constructors remain and are fine — they are not matched by the grep).

- [ ] **Step 7: Run the full test suite, vet, and race**

Run: `go test ./... && go vet ./... && go test -race ./...`
Expected: PASS across all packages — stdout, `-o` file output, SSE, and e2e tests all green, proving behavior preservation.

- [ ] **Step 8: Commit**

```bash
git add main.go
git commit -m "refactor(main): fan out through sink.Fanout registry

Collapse the hardcoded stdout/sse/file fan-out into one *sink.Fanout
threaded through emit/runOnce/runWatch/runWatchTUI. TUI-mode preload now
also reaches SSE (documented in spec), matching non-TUI preload."
```

---

## Self-Review

**1. Spec coverage:**
- `Sink` interface + `Fanout` (NewFanout/Add/Emit/Close, nil-skipping, ordered) → Task 1. ✓
- `Stdout`/`SSEHub`/`FileSink` satisfy `Sink` → verified pre-existing (Key facts); no code needed. ✓
- `linebuf.Append` stays privileged, not a sink → preserved in `emit` (Step 1) and TUI pump (Step 4). ✓
- TUI `app` stays primary, not a sink → preserved (Step 4 keeps `app.Push`). ✓
- `emit`/`runOnce`/`runWatch`/`runWatchTUI` collapse three sink params to one `*sink.Fanout` → Task 2 Steps 1–4. ✓
- `run()` builds per-mode Fanout, `defer fanout.Close()` replaces individual defers → Task 2 Step 5. ✓
- Intentional behavior change (TUI preload → SSE) → Task 2 Step 4.2, matches spec. ✓
- Build-tag plug-in seam (`Add` skips nil) → Task 1 implementation + test. ✓
- Testing (Fanout unit tests; existing suite unchanged; vet/race green) → Task 1 Steps 1–5, Task 2 Steps 7. ✓

**2. Placeholder scan:** No TBD/TODO/"handle edge cases"/"similar to". All code shown in full. ✓

**3. Type consistency:** `Fanout`, `NewFanout`, `Add`, `Emit`, `Close`, `isNilSink`, `Sink` used identically across Task 1 and Task 2. `*sink.Fanout` is the single parameter type in every modified signature. ✓
