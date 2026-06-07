# Output-File Passthrough (`-o`) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `-o <file>` / `--output <file>` so every rendered line is also written, in plain (no-color) stdout format, to a file in all run modes.

**Architecture:** A new `sink.FileSink` composes the existing `sink.Stdout` (color off) so on-disk format is byte-identical to non-TTY stdout. `main.go` opens it once (truncating), threads it (nil when unset) into `runOnce`/`runWatch`/`runWatchTUI` and `emit()`, and emits preload events to it so the capture matches what the user sees.

**Tech Stack:** Go 1.26, standard library only (`os`, `bufio` already in use). Tests via `go test ./...`.

**Reference spec:** `docs/superpowers/specs/2026-06-07-output-file-passthrough-design.md`

---

## File Structure

- `internal/config/cli.go` — add `Config.OutputFile`; parse `-o`/`--output`.
- `internal/config/cli_test.go` — parse tests.
- `internal/sink/file.go` (new) — `FileSink` + `OpenFile`.
- `internal/sink/file_test.go` (new) — unit tests (reuses `makeEvent` from `stdout_test.go`, same package).
- `main.go` — open `FileSink`; add param to `emit`, `runOnce`, `runWatch`, `runWatchTUI`; emit preloads to it.
- `main_test.go` — integration test through `run(...)`.
- `README.md`, `CHANGELOG.md` — document the flag.

---

## Task 1: CLI flag `-o` / `--output`

**Files:**
- Modify: `internal/config/cli.go` (Config struct ~line 20-40; `ParseArgs` switch, after the `--config` case ~line 67)
- Test: `internal/config/cli_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `internal/config/cli_test.go` (`refNow` already defined in this file):

```go
func TestParseArgsOutputFile(t *testing.T) {
	cfg, err := ParseArgs([]string{"-d", "/a", "-o", "out.log"}, refNow)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OutputFile != "out.log" {
		t.Fatalf("OutputFile = %q, want out.log", cfg.OutputFile)
	}
	cfg2, err := ParseArgs([]string{"-d", "/a", "--output", "out2.log"}, refNow)
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.OutputFile != "out2.log" {
		t.Fatalf("OutputFile = %q, want out2.log", cfg2.OutputFile)
	}
}

func TestParseArgsOutputRequiresValue(t *testing.T) {
	if _, err := ParseArgs([]string{"-o"}, refNow); err == nil {
		t.Fatal("expected error for -o with no value")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/ -run TestParseArgsOutput -v`
Expected: FAIL — `cfg.OutputFile` undefined (compile error).

- [ ] **Step 3: Add the `OutputFile` field**

In `internal/config/cli.go`, in the `Config` struct, add next to the other CLI scalar fields (e.g. after `MCPAddr string`):

```go
	OutputFile string // -o/--output; "" = no file capture. CLI-only (no YAML).
```

- [ ] **Step 4: Add the parse cases**

In `ParseArgs`, immediately after the existing `case a == "--config":` block (which ends with `i = next`), add:

```go
		case a == "-o" || a == "--output":
			v, next, err := requireValue(args, i, a)
			if err != nil {
				return nil, err
			}
			cfg.OutputFile = v
			cfg.cliExplicit["output"] = true
			i = next
```

(`requireValue(args, i, name)` is the existing helper at `cli.go:172`; passing `a` gives the correct flag name in its error message.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/config/ -run TestParseArgsOutput -v`
Expected: PASS (both tests).

- [ ] **Step 6: Commit**

```bash
git add internal/config/cli.go internal/config/cli_test.go
git commit -m "feat(config): add -o/--output flag → Config.OutputFile"
```

---

## Task 2: `sink.FileSink`

**Files:**
- Create: `internal/sink/file.go`
- Create: `internal/sink/file_test.go` (reuses `makeEvent` from `stdout_test.go`)

- [ ] **Step 1: Write the failing tests**

Create `internal/sink/file_test.go`:

```go
package sink

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/homeend/log-listener/internal/render"
)

func TestOpenFileTruncates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "o.txt")
	if err := os.WriteFile(path, []byte("OLD CONTENT"), 0o644); err != nil {
		t.Fatal(err)
	}
	fs, err := OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected truncated empty file, got %q", got)
	}
}

func TestFileSinkMatchesStdout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "o.txt")
	fs, err := OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ev := makeEvent(render.Part{Type: "text", Value: "hello world"})
	fs.Emit(ev)
	if err := fs.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var buf strings.Builder
	NewStdout(&buf, false).Emit(ev)
	if string(got) != buf.String() {
		t.Fatalf("file %q != non-TTY stdout %q", got, buf.String())
	}
}

func TestFileSinkJSONBlockNoANSI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "o.txt")
	fs, err := OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ev := makeEvent(
		render.Part{Type: "text", Value: "evt"},
		render.Part{Type: "json", Value: map[string]any{"k": "v"}},
	)
	fs.Emit(ev)
	if err := fs.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, "[d1] app.log: evt\n") {
		t.Fatalf("text line missing: %q", s)
	}
	if !strings.Contains(s, `"k": "v"`) {
		t.Fatalf("json block missing: %q", s)
	}
	if strings.Contains(s, "\x1b[") {
		t.Fatalf("ANSI escape leaked into file: %q", s)
	}
}
```

(`makeEvent` is defined in `stdout_test.go`, same package `sink`; it sets `File:"/var/log/app.log"`, `Group:"d1"`, so the rendered prefix is `[d1] app.log: `.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sink/ -run 'TestOpenFileTruncates|TestFileSink' -v`
Expected: FAIL — `OpenFile` undefined (compile error).

- [ ] **Step 3: Implement `FileSink`**

Create `internal/sink/file.go`:

```go
package sink

import (
	"os"

	"github.com/homeend/log-listener/internal/render"
)

// FileSink writes rendered events to a file in the same plain-text format as a
// non-TTY Stdout sink ([group] basename: text + indented JSON/XML blocks, no
// ANSI color). It owns the underlying file and is the only sink that closes its
// writer. Not safe for concurrent Emit; callers fan out from a single goroutine.
type FileSink struct {
	f     *os.File
	inner *Stdout
}

// OpenFile creates (or truncates) path and returns a FileSink writing to it.
// Opened O_CREATE|O_WRONLY|O_TRUNC (0o644): each run starts with a fresh file.
func OpenFile(path string) (*FileSink, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	return &FileSink{f: f, inner: NewStdout(f, false)}, nil
}

// Emit writes the event in plain (no-color) format. Delegates to the embedded
// Stdout so the on-disk format always matches non-TTY stdout exactly.
func (s *FileSink) Emit(ev render.Event) { s.inner.Emit(ev) }

// Close closes the underlying file (FileSink owns it, unlike Stdout).
func (s *FileSink) Close() error { return s.f.Close() }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sink/ -run 'TestOpenFileTruncates|TestFileSink' -v`
Expected: PASS (all three).

- [ ] **Step 5: Commit**

```bash
git add internal/sink/file.go internal/sink/file_test.go
git commit -m "feat(sink): FileSink writes plain stdout-format to a file"
```

---

## Task 3: Wire `FileSink` into `main.go` (all modes)

**Files:**
- Modify: `main.go` — `run()` (open, ~after line 154); `emit()` (line 432); `runOnce()` (line 308); `runWatch()` (line 340, call sites 388/418, preload loop 351); `runWatchTUI()` (line 448, pump ~512-516, preload before `tui.New` ~471)
- Test: `main_test.go`

- [ ] **Step 1: Write the failing integration test**

Add to `main_test.go` (it already imports `bytes`; add `os`, `path/filepath`, `strings` to its import block if not present):

```go
func TestRunOnceWritesOutputFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.log"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Output file lives in a SEPARATE temp dir so it isn't itself discovered
	// and tailed by -d.
	out := filepath.Join(t.TempDir(), "capture.txt")
	if err := os.WriteFile(out, []byte("STALE\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"-d", dir, "--once", "--no-color", "-o", out}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d, stderr=%q", code, stderr.String())
	}

	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if strings.Contains(got, "STALE") {
		t.Fatalf("output file was not truncated: %q", got)
	}
	for _, want := range []string{"app.log: hello", "app.log: world"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in output file: %q", want, got)
		}
	}
	if strings.Contains(got, "\x1b[") {
		t.Fatalf("ANSI escape leaked into output file: %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test . -run TestRunOnceWritesOutputFile -v`
Expected: FAIL — `run` ignores `-o`; file still contains `STALE` / lacks the lines (the flag is parsed but no sink writes yet).

- [ ] **Step 3: Open the FileSink in `run()`**

In `main.go`, after the `sseHub` block (the `}` closing `if cfg.SSEAddr != "" { ... defer sseHub.Close() }`, ~line 154) and BEFORE `if cfg.Once {` (~line 156), insert:

```go
	var fileSink *sink.FileSink
	if cfg.OutputFile != "" {
		fileSink, err = sink.OpenFile(cfg.OutputFile)
		if err != nil {
			fmt.Fprintln(stderr, "log-listener: output:", err)
			return 1
		}
		defer fileSink.Close()
	}
```

(`err` is already declared earlier in `run()`; reuse it with `=`. If the compiler reports `err` is not in scope at this point, use `fileSink, ferr := sink.OpenFile(...)` and test `ferr`.)

- [ ] **Step 4: Thread `fileSink` into the three dispatch calls**

Change the three call sites in `run()`:

- Line ~157: `runOnce(preloadEvents, assignments, pipeline, stdoutSink, sseHub)` → add `, fileSink`:
  ```go
  if err := runOnce(preloadEvents, assignments, pipeline, stdoutSink, sseHub, fileSink); err != nil {
  ```
- Line ~185: `runWatchTUI(cfg, args, cfg.DropUnmatched, assignments, &pipePtr, buf, sseHub, km, preloadEvents, stderr)` → add `fileSink` before `km`:
  ```go
  if err := runWatchTUI(cfg, args, cfg.DropUnmatched, assignments, &pipePtr, buf, sseHub, fileSink, km, preloadEvents, stderr); err != nil {
  ```
- Line ~192: `runWatch(cfg, args, cfg.DropUnmatched, assignments, &pipePtr, buf, stdoutSink, sseHub, preloadEvents, stderr)` → add `fileSink` after `sseHub`:
  ```go
  if err := runWatch(cfg, args, cfg.DropUnmatched, assignments, &pipePtr, buf, stdoutSink, sseHub, fileSink, preloadEvents, stderr); err != nil {
  ```

- [ ] **Step 5: Update `runOnce` to write to `fileSink`**

Change the signature (line 308) to add `fileSink *sink.FileSink` after `sseHub`:

```go
func runOnce(preloadEvents []render.Event, assignments []discover.Assignment, pipeline *render.Pipeline, stdoutSink *sink.Stdout, sseHub *sink.SSEHub, fileSink *sink.FileSink) error {
```

In its preload loop, after `stdoutSink.Emit(ev)`:

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

In its scan loop, inside `if ok {` after `stdoutSink.Emit(ev)`:

```go
			if ok {
				stdoutSink.Emit(ev)
				if sseHub != nil {
					sseHub.Emit(ev)
				}
				if fileSink != nil {
					fileSink.Emit(ev)
				}
			}
```

- [ ] **Step 6: Update `emit()` and `runWatch`**

Change `emit()` signature (line 432) to add `fileSink *sink.FileSink` after `sseHub`:

```go
func emit(pipePtr *atomic.Pointer[render.Pipeline], buf *linebuf.Buffer, stdoutSink *sink.Stdout, sseHub *sink.SSEHub, fileSink *sink.FileSink, group, path, line string) {
```

In `emit()` body, after `stdoutSink.Emit(ev)`:

```go
	ev.ID = buf.Append(ev)
	stdoutSink.Emit(ev)
	if sseHub != nil {
		sseHub.Emit(ev)
	}
	if fileSink != nil {
		fileSink.Emit(ev)
	}
```

Change `runWatch()` signature (line 340) to add `fileSink *sink.FileSink` after `sseHub`:

```go
func runWatch(cfg *config.Config, args []string, dropUnmatched bool, assignments []discover.Assignment, pipePtr *atomic.Pointer[render.Pipeline], buf *linebuf.Buffer, stdoutSink *sink.Stdout, sseHub *sink.SSEHub, fileSink *sink.FileSink, preloadEvents []render.Event, stderr io.Writer) error {
```

In `runWatch`'s preload loop, after `stdoutSink.Emit(ev)`:

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

Update BOTH `emit(...)` call sites in `runWatch` (lines ~388 and ~418) to pass `fileSink` after `sseHub`:

```go
				emit(pipePtr, buf, stdoutSink, sseHub, fileSink, ev.Group, ev.Path, ev.Line)
```

- [ ] **Step 7: Update `runWatchTUI`**

Change the signature (line 448) to add `fileSink *sink.FileSink` after `sseHub`:

```go
func runWatchTUI(cfg *config.Config, args []string, dropUnmatched bool, assignments []discover.Assignment, pipePtr *atomic.Pointer[render.Pipeline], buf *linebuf.Buffer, sseHub *sink.SSEHub, fileSink *sink.FileSink, km *keymap.Keymap, preloadEvents []render.Event, stderr io.Writer) error {
```

Write preload events to the file BEFORE `app := tui.New(...)` (the preload lines seed the on-screen scrollback but never pass through the pump). Insert just before the `app := tui.New(tui.Options{` line (~471):

```go
	if fileSink != nil {
		for _, ev := range preloadEvents {
			fileSink.Emit(ev)
		}
	}
```

In the pump goroutine, after `sseHub.Emit(rev)` inside the `case ev := <-w.Events():` block (~512-516):

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

- [ ] **Step 8: Run the integration test + full build**

Run: `go test . -run TestRunOnceWritesOutputFile -v`
Expected: PASS.

Run: `go build ./... && go test ./...`
Expected: build clean; all packages PASS (confirms every `emit`/`runOnce`/`runWatch`/`runWatchTUI` call site updated).

- [ ] **Step 9: Race check**

Run: `go test -race ./...`
Expected: PASS, no race warnings.

- [ ] **Step 10: Commit**

```bash
git add main.go main_test.go
git commit -m "feat: wire -o output file through all run modes"
```

---

## Task 4: Documentation

**Files:**
- Modify: `README.md` (flags/usage section)
- Modify: `CHANGELOG.md` (top unreleased section)

- [ ] **Step 1: Find the flags section in README**

Run: `grep -n "\-\-no-tui\|--no-color\|--mcp\|## Usage\|Flags\|Options" README.md`
Expected: locate the CLI flags list/table.

- [ ] **Step 2: Document `-o` in README**

Add an entry alongside the other flags (match the surrounding format — table row or bullet). Content to convey:

> `-o, --output <file>` — Also write every displayed line to `<file>` in plain
> (no-color) text, in all modes (`--once`, `--no-tui`, and the TUI). The file is
> truncated at startup. Tip: keep `<file>` outside any watched directory, or it
> will be discovered and tailed.

- [ ] **Step 3: Add a CHANGELOG entry**

Add under the top/unreleased heading in `CHANGELOG.md`, matching existing entry style:

```markdown
- `-o`/`--output <file>`: write all displayed lines to a file in plain
  stdout format, in every mode (truncates on start).
```

- [ ] **Step 4: Verify build/docs unaffected**

Run: `go test ./...`
Expected: PASS (no generated-docs guard touches README/CHANGELOG; `KEYBINDINGS.md` is unrelated to this feature).

- [ ] **Step 5: Commit**

```bash
git add README.md CHANGELOG.md
git commit -m "docs: document -o/--output flag"
```

---

## Final verification

- [ ] `go test ./...` — all green
- [ ] `go vet ./...` — clean
- [ ] `go test -race ./...` — clean
- [ ] Manual smoke (optional): `./build.sh build && printf 'a\nb\n' > /tmp/x.log && ./log-listener -f /tmp/x.log --once --no-color -o /tmp/cap.txt && cat /tmp/cap.txt` shows the two plain lines.

Then use **superpowers:finishing-a-development-branch**.
