# Build-Tag-Gated MCP/SSE Binaries Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow compiling `log-listener` without MCP (`-tags nomcp`, which also drops the `modelcontextprotocol/go-sdk` dependency) and/or without SSE (`-tags nosse`), while the default build stays full-featured.

**Architecture:** Move the SSE and MCP wiring out of `main.go` into build-tagged file pairs (`feature_mcp.go`/`feature_nomcp.go`, `feature_sse.go`/`feature_nosse.go`), each defining one function with an identical signature. `main.run()` calls those functions tag-agnostically. The `internal/mcp` import lives only in `feature_mcp.go` (`//go:build !nomcp`), so a `nomcp` build never imports it → the SDK is excised. `internal/config` is unchanged (it parses flags into string fields; no feature import to gate). Asking a stripped binary for the missing feature is a hard error.

**Tech Stack:** Go 1.26 build tags (`//go:build`), standard library. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-06-08-build-tagged-binaries-design.md`

---

## File Structure

| File | Build tag | Responsibility | Change |
|------|-----------|----------------|--------|
| `feature_sse.go` | `!nosse` | `buildSSE(cfg) (sink.Sink, error)` — start SSEHub | **Create** |
| `feature_nosse.go` | `nosse` | `buildSSE` stub — hard error if SSE requested | **Create** |
| `feature_nosse_test.go` | `nosse` | assert `--sse` / `output.sse` hard-error | **Create** |
| `feature_mcp.go` | `!nomcp` | `startMCP(cfg, buf, stderr) (io.Closer, error)` — start server; **only importer of `internal/mcp`** | **Create** |
| `feature_nomcp.go` | `nomcp` | `startMCP` stub — hard error if `--mcp` requested | **Create** |
| `feature_nomcp_test.go` | `nomcp` | assert `--mcp` hard-error | **Create** |
| `main.go` | (none) | call `buildSSE`/`startMCP`; drop inline blocks + `internal/mcp` import | **Modify** |
| `e2e_mcp_test.go`, `e2e_mcp_tools_test.go`, `e2e_mcp_tui_test.go` | add `!nomcp` | keep MCP e2e out of `nomcp` builds | **Modify** |
| `e2e_sse_test.go` | `!nosse` | the moved `TestE2ESSEDeliversEvents` | **Create** (move) |
| `e2e_test.go` | (none) | remove `TestE2ESSEDeliversEvents` (moved out) | **Modify** |
| `build.sh` | — | `build-nomcp`/`build-nosse`/`build-minimal`/`test-minimal` targets | **Modify** |
| `README.md`, `CHANGELOG.md` | — | document build variants | **Modify** |

**Key facts (verified against current code):**

- `main.run()` currently constructs SSE inline (`main.go` ~146): `var sseHub *sink.SSEHub; if cfg.SSEAddr != "" { sseHub = sink.NewSSEHub(cfg.SSEAddr); if err := sseHub.Start(); err != nil { ...return 1 } }`. The three run-mode branches build `sink.NewFanout(..., sseHub, ...)`.
- MCP is constructed inline (`main.go` ~174) **after** the `--once` early-return: `var mcpServer *mcp.Server; if cfg.MCPAddr != "" { mcpServer = mcp.New(cfg.MCPAddr, buf); if err := mcpServer.Start(); err != nil {...return 1}; defer mcpServer.Close(); fmt.Fprintf(stderr, "log-listener: mcp on http://%s\n", mcpServer.Addr()) }`.
- `main.go` imports `"github.com/homeend/log-listener/internal/mcp"`. After Task 2 it must not.
- `mcp.Server` has `New(addr string, buf *linebuf.Buffer) *Server`, `Start() error`, `Addr() string`, `Close() error` — so `*mcp.Server` satisfies `io.Closer`.
- `sink.SSEHub` has `NewSSEHub(addr) *SSEHub`, `Start() error`, `Emit(render.Event)`, `Close() error` — so `*sink.SSEHub` satisfies `sink.Sink`.
- `run(args []string, stdout, stderr io.Writer) int` is the testable entry; `main()` does `os.Exit(run(...))`. `err` is already in scope in `run()` (from `config.Load`), so `sseSink, err := ...` / `mcpCloser, err := ...` reuse it (valid: each has one new var on the LHS).
- Config-file flag is `--config <path>`; SSE-via-YAML is `output: { sse: { enabled: true } }` (resolves to `cfg.SSEAddr = "127.0.0.1:8080"`). MCP is CLI-only (`--mcp`).
- The existing SSE e2e is `TestE2ESSEDeliversEvents` in `e2e_test.go`. MCP e2e lives in `e2e_mcp_test.go`, `e2e_mcp_tools_test.go`, `e2e_mcp_tui_test.go`.

---

## Task 1: SSE build-tag pair + `main` wiring

**Files:** Create `feature_sse.go`, `feature_nosse.go`, `feature_nosse_test.go`; Modify `main.go`.

- [ ] **Step 1: Create `feature_sse.go`**

```go
//go:build !nosse

package main

import (
	"fmt"

	"github.com/homeend/log-listener/internal/config"
	"github.com/homeend/log-listener/internal/sink"
)

// buildSSE starts the SSE broadcast hub if --sse / output.sse was configured,
// returning it as a sink.Sink for the Fanout (which owns Close). Returns a nil
// Sink when SSE wasn't requested. Replaces the wiring previously inline in run().
func buildSSE(cfg *config.Config) (sink.Sink, error) {
	if cfg.SSEAddr == "" {
		return nil, nil
	}
	hub := sink.NewSSEHub(cfg.SSEAddr)
	if err := hub.Start(); err != nil {
		return nil, fmt.Errorf("sse: %w", err)
	}
	return hub, nil
}
```

- [ ] **Step 2: Create `feature_nosse.go`**

```go
//go:build nosse

package main

import (
	"errors"

	"github.com/homeend/log-listener/internal/config"
	"github.com/homeend/log-listener/internal/sink"
)

// buildSSE is the no-SSE stub. Requesting SSE on a binary built without SSE
// support is a hard error; otherwise it is a no-op.
func buildSSE(cfg *config.Config) (sink.Sink, error) {
	if cfg.SSEAddr != "" {
		return nil, errors.New("--sse: this binary was built without SSE support (use a full build)")
	}
	return nil, nil
}
```

- [ ] **Step 3: Rewire `run()` in `main.go` to use `buildSSE`**

Replace the inline SSE block:

```go
	var sseHub *sink.SSEHub
	if cfg.SSEAddr != "" {
		sseHub = sink.NewSSEHub(cfg.SSEAddr)
		if err := sseHub.Start(); err != nil {
			fmt.Fprintln(stderr, "log-listener: sse:", err)
			return 1
		}
	}
```

with:

```go
	sseSink, err := buildSSE(cfg)
	if err != nil {
		fmt.Fprintln(stderr, "log-listener:", err)
		return 1
	}
```

Then update the three fanout constructions to use `sseSink` instead of `sseHub`:
- `--once` branch: `sink.NewFanout(stdoutSink, sseSink, fileSink)`
- TUI branch: `sink.NewFanout(sseSink, fileSink)`
- non-TUI `runWatch` branch: `sink.NewFanout(stdoutSink, sseSink, fileSink)`

- [ ] **Step 4: Build both variants and run default tests**

Run: `go build . && go build -tags nosse . && go test ./... && go vet ./...`
Expected: both builds succeed; all tests pass; vet clean. (The default build behaves exactly as before; the `nosse` build compiles with the stub.)

- [ ] **Step 5: Create `feature_nosse_test.go` (runs only under `-tags nosse`)**

```go
//go:build nosse

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoSSEBuildRejectsSSEFlag(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "a.log")
	if err := os.WriteFile(logPath, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errBuf bytes.Buffer
	code := run([]string{"-f", logPath, "--sse", "127.0.0.1:0", "--once", "--no-tui", "--no-color"}, &out, &errBuf)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (stderr: %q)", code, errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "built without SSE support") {
		t.Fatalf("stderr = %q, want mention of 'built without SSE support'", errBuf.String())
	}
}

func TestNoSSEBuildRejectsYAMLSSE(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "a.log")
	if err := os.WriteFile(logPath, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "ll.yml")
	if err := os.WriteFile(cfgPath, []byte("output:\n  sse:\n    enabled: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errBuf bytes.Buffer
	code := run([]string{"--config", cfgPath, "-f", logPath, "--once", "--no-tui", "--no-color"}, &out, &errBuf)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (stderr: %q)", code, errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "built without SSE support") {
		t.Fatalf("stderr = %q, want mention of 'built without SSE support'", errBuf.String())
	}
}
```

- [ ] **Step 6: Run the nosse-tagged tests**

Run: `go test -tags nosse -run 'TestNoSSE' -v .`
Expected: PASS — both tests confirm the hard error and exit code 1.

- [ ] **Step 7: Commit**

```bash
git add feature_sse.go feature_nosse.go feature_nosse_test.go main.go
git commit -m "feat(build): gate SSE behind nosse build tag"
```

---

## Task 2: MCP build-tag pair + `main` wiring (drops the SDK)

**Files:** Create `feature_mcp.go`, `feature_nomcp.go`, `feature_nomcp_test.go`; Modify `main.go`.

- [ ] **Step 1: Create `feature_mcp.go`**

```go
//go:build !nomcp

package main

import (
	"fmt"
	"io"

	"github.com/homeend/log-listener/internal/config"
	"github.com/homeend/log-listener/internal/linebuf"
	"github.com/homeend/log-listener/internal/mcp"
)

// startMCP builds and starts the embedded MCP server if --mcp was given.
// Returns the server (to defer Close) or a nil io.Closer when MCP wasn't
// requested. This is the ONLY file importing internal/mcp, so a nomcp build
// excludes it and the go-sdk dependency entirely.
func startMCP(cfg *config.Config, buf *linebuf.Buffer, stderr io.Writer) (io.Closer, error) {
	if cfg.MCPAddr == "" {
		return nil, nil
	}
	srv := mcp.New(cfg.MCPAddr, buf)
	if err := srv.Start(); err != nil {
		return nil, fmt.Errorf("mcp: %w", err)
	}
	fmt.Fprintf(stderr, "log-listener: mcp on http://%s\n", srv.Addr())
	return srv, nil
}
```

- [ ] **Step 2: Create `feature_nomcp.go`**

```go
//go:build nomcp

package main

import (
	"errors"
	"io"

	"github.com/homeend/log-listener/internal/config"
	"github.com/homeend/log-listener/internal/linebuf"
)

// startMCP is the no-MCP stub. Requesting --mcp on a binary built without MCP
// support is a hard error; otherwise it is a no-op. Imports no mcp package.
func startMCP(cfg *config.Config, _ *linebuf.Buffer, _ io.Writer) (io.Closer, error) {
	if cfg.MCPAddr != "" {
		return nil, errors.New("--mcp: this binary was built without MCP support (use a full build)")
	}
	return nil, nil
}
```

- [ ] **Step 3: Rewire `run()` in `main.go` and remove the `internal/mcp` import**

Replace the inline MCP block:

```go
	var mcpServer *mcp.Server
	if cfg.MCPAddr != "" {
		mcpServer = mcp.New(cfg.MCPAddr, buf)
		if err := mcpServer.Start(); err != nil {
			fmt.Fprintln(stderr, "log-listener: mcp:", err)
			return 1
		}
		defer mcpServer.Close()
		fmt.Fprintf(stderr, "log-listener: mcp on http://%s\n", mcpServer.Addr())
	}
```

with:

```go
	mcpCloser, err := startMCP(cfg, buf, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "log-listener:", err)
		return 1
	}
	if mcpCloser != nil {
		defer mcpCloser.Close()
	}
```

Then delete the import line `"github.com/homeend/log-listener/internal/mcp"` from `main.go`'s import block.

- [ ] **Step 4: Build both variants, verify the SDK is dropped, run default tests**

Run:
```bash
go build . && go vet ./... && go test ./...
go build -tags nomcp -o /tmp/ll-nomcp .
go version -m /tmp/ll-nomcp | grep modelcontextprotocol || echo "SDK ABSENT (good)"
go build -o /tmp/ll-full .
go version -m /tmp/ll-full | grep modelcontextprotocol && echo "SDK PRESENT in full (good)"
```
Expected: default build/test/vet green; the `nomcp` binary prints "SDK ABSENT (good)" (grep finds nothing); the full binary shows the `modelcontextprotocol/go-sdk` module line.

- [ ] **Step 5: Create `feature_nomcp_test.go` (runs only under `-tags nomcp`)**

```go
//go:build nomcp

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoMCPBuildRejectsMCPFlag(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "a.log")
	if err := os.WriteFile(logPath, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errBuf bytes.Buffer
	// --mcp is not active in --once mode, so use --no-tui (live watch) and a
	// short-circuit: startMCP runs before the watch loop blocks. To avoid a
	// blocking run, drive it through --once is NOT possible (mcp skipped there),
	// so we assert via a non-once invocation that returns on the startMCP error.
	code := run([]string{"-f", logPath, "--mcp", "127.0.0.1:0", "--no-tui", "--no-color"}, &out, &errBuf)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (stderr: %q)", code, errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "built without MCP support") {
		t.Fatalf("stderr = %q, want mention of 'built without MCP support'", errBuf.String())
	}
}
```

Note: `startMCP` is called in `run()` after the `--once` early-return but before the watch loop starts, so the stub's hard error returns code 1 immediately without entering a blocking tail loop.

- [ ] **Step 6: Run the nomcp-tagged test**

Run: `go test -tags nomcp -run 'TestNoMCP' -v .`
Expected: PASS — the build has no mcp import and `--mcp` hard-errors with exit 1.

- [ ] **Step 7: Verify the fully-minimal build compiles and vets**

Run: `go build -tags "nomcp nosse" . && go vet -tags "nomcp nosse" ./...`
Expected: both green — the minimal build compiles with both stubs.

Note: a full `go test -tags "nomcp nosse" ./...` is also green at this point (the e2e harness builds its subprocess with a bare `go build` — no tags — so the MCP/SSE e2e tests exercise a full binary and pass even under these tags). Task 3 then tags those e2e tests so a minimal test run stops compiling the SDK into the test binary and stops running feature e2e against a full binary. The full minimal test run is verified in Task 3 Step 3.

- [ ] **Step 8: Commit**

```bash
git add feature_mcp.go feature_nomcp.go feature_nomcp_test.go main.go
git commit -m "feat(build): gate MCP behind nomcp build tag (drops go-sdk)"
```

---

## Task 3: Tag the feature-running e2e tests

The e2e tests don't *fail* under the stripping tag — the e2e harness builds its subprocess with a bare `go build` (no tags), so they exercise a full binary and pass regardless. Tagging them anyway is hygiene with two concrete benefits: (1) `e2e_mcp_tools_test.go` and `e2e_mcp_tui_test.go` import the SDK (`mcpsdk`) and `internal/mcp`, so a `!nomcp` constraint keeps the SDK out of the *test* binary under `-tags nomcp`; (2) a `go test -tags "nomcp nosse"` run then actually means "exercise the minimal feature set" instead of silently running MCP/SSE e2e against a full binary.

**Files:** Modify `e2e_mcp_test.go`, `e2e_mcp_tools_test.go`, `e2e_mcp_tui_test.go`, `e2e_test.go`; Create `e2e_sse_test.go`.

- [ ] **Step 1: Add `//go:build !nomcp` to each MCP e2e file**

At the very top of `e2e_mcp_test.go`, `e2e_mcp_tools_test.go`, and `e2e_mcp_tui_test.go`, add as the first line, followed by a blank line, before `package main`:

```go
//go:build !nomcp

package main
```

(Preserve the rest of each file unchanged.)

- [ ] **Step 2: Move `TestE2ESSEDeliversEvents` into a new tagged file**

Cut the entire `func TestE2ESSEDeliversEvents(t *testing.T) { ... }` from `e2e_test.go` and paste it into a new `e2e_sse_test.go`:

```go
//go:build !nosse

package main

import (
	// include exactly the imports TestE2ESSEDeliversEvents uses
	// (e.g. "net/http", "strings", "testing", "time", etc. — copy from e2e_test.go)
)

// TestE2ESSEDeliversEvents — moved here so it is excluded from nosse builds,
// where the binary has no SSE server.
func TestE2ESSEDeliversEvents(t *testing.T) {
	// ... unchanged body ...
}
```

Determine the needed imports by reading the function body; only include imports it actually uses (some, like `startListener`, are package-local helpers needing no import). Remove any now-unused imports from `e2e_test.go`.

- [ ] **Step 3: Verify default and minimal test runs**

Run:
```bash
go test ./...
go vet ./...
go test -tags "nomcp nosse" ./...
go vet -tags "nomcp nosse" ./...
```
Expected: all green. The default run includes the MCP + SSE e2e tests; the `nomcp nosse` run excludes them and instead runs the disabled-stub tests.

- [ ] **Step 4: Commit**

```bash
git add e2e_mcp_test.go e2e_mcp_tools_test.go e2e_mcp_tui_test.go e2e_test.go e2e_sse_test.go
git commit -m "test(e2e): tag MCP/SSE e2e tests out of nomcp/nosse builds"
```

---

## Task 4: `build.sh` targets + docs

**Files:** Modify `build.sh`, `README.md`, `CHANGELOG.md`.

- [ ] **Step 1: Add build/test targets to `build.sh`**

In the `case "$target" in` block, after the `build-static)` case, add:

```sh
  build-nomcp)
    go build -tags nomcp -o "$BINARY" "$CMD"
    echo "built ./$BINARY (no MCP)"
    ;;
  build-nosse)
    go build -tags nosse -o "$BINARY" "$CMD"
    echo "built ./$BINARY (no SSE)"
    ;;
  build-minimal)
    go build -tags "nomcp nosse" -o "$BINARY" "$CMD"
    echo "built ./$BINARY (no MCP, no SSE)"
    ;;
  test-nomcp)
    go test -tags nomcp "$PKG"
    ;;
  test-nosse)
    go test -tags nosse "$PKG"
    ;;
  test-minimal)
    go test -tags "nomcp nosse" "$PKG"
    ;;
```

Then update the usage comment block (the `# Usage:` lines near the top, currently lines ~5–14) to list the new targets:

```sh
#   build-nomcp    binary without the MCP server (drops the go-sdk dependency)
#   build-nosse    binary without the SSE server
#   build-minimal  binary without MCP and SSE
#   test-nomcp     go test -tags nomcp ./...
#   test-nosse     go test -tags nosse ./...
#   test-minimal   go test -tags "nomcp nosse" ./...
```

The `help` target already prints lines 2–14 of the script, so updating the comment updates `help` automatically. If the new comment lines push past line 14, update the `help` target's `sed -n '2,14p'` range to cover the added lines.

- [ ] **Step 2: Verify build.sh targets work**

Run: `./build.sh build-minimal && ./build.sh test-minimal && ./build.sh build && ./build.sh help`
Expected: minimal binary builds; minimal tests pass; full binary builds; help lists the new targets.

- [ ] **Step 3: Document build variants in README.md**

In `README.md`, add a short "Build variants" subsection near the existing build/install docs:

```markdown
### Build variants

The default build (`go build`, `go install …@latest`, `./build.sh build`) includes
everything. To produce leaner binaries, use build tags:

| Command | Result |
|---------|--------|
| `go build -tags nomcp` (`./build.sh build-nomcp`) | No MCP server; **drops the `modelcontextprotocol/go-sdk` dependency**. |
| `go build -tags nosse` (`./build.sh build-nosse`) | No SSE server. |
| `go build -tags "nomcp nosse"` (`./build.sh build-minimal`) | Neither MCP nor SSE. |

A stripped binary still recognizes the corresponding flag, but asking for the
removed feature (`--mcp`, `--sse`, or an `output.sse` YAML block) is a hard error.
```

- [ ] **Step 4: Add a CHANGELOG entry**

In `CHANGELOG.md`, under `## [Unreleased]`, add:

```markdown
### Build variants: `nomcp` / `nosse` tags
- **`go build -tags nomcp`** compiles a binary without the embedded MCP server,
  dropping the `modelcontextprotocol/go-sdk` dependency entirely. **`-tags nosse`**
  drops the SSE broadcast server. Tags compose (`-tags "nomcp nosse"`). The default
  build is unchanged (full-featured), so `go install …@latest` is unaffected.
- Asking a stripped binary for the removed feature (`--mcp`, `--sse`, or a YAML
  `output.sse` block) is a hard error with a clear message and non-zero exit.
- `./build.sh` gains `build-nomcp`, `build-nosse`, `build-minimal`, `test-minimal`.
```

- [ ] **Step 5: Final full verification**

Run:
```bash
go test ./... && go vet ./... && go test -race ./...
go test -tags "nomcp nosse" ./... && go vet -tags "nomcp nosse" ./...
go build -tags nomcp -o /tmp/ll-nomcp . && (go version -m /tmp/ll-nomcp | grep -q modelcontextprotocol && echo "FAIL: SDK still present" || echo "OK: SDK dropped")
```
Expected: all green; final line prints "OK: SDK dropped".

- [ ] **Step 6: Commit**

```bash
git add build.sh README.md CHANGELOG.md
git commit -m "docs(build): build.sh variant targets + README/CHANGELOG for nomcp/nosse"
```

---

## Self-Review

**1. Spec coverage:**
- Two opt-out tags `nomcp`/`nosse`, default full → Tasks 1, 2. ✓
- `config` untouched; gating via `main` file pairs → Tasks 1, 2 (no config change). ✓
- `feature_mcp.go` sole importer of `internal/mcp`; `main.go` import removed → Task 2 Steps 1, 3. ✓
- SDK dropped under `nomcp`, verified via `go version -m` → Task 2 Step 4, Task 4 Step 5. ✓
- Hard error for CLI flag and YAML `output.sse` → Tasks 1 (nosse: both flag + YAML tests), 2 (nomcp: flag test; MCP is CLI-only). ✓
- `buildSSE`/`startMCP` signatures match spec (`buildSSE(cfg)`, `startMCP(cfg, buf, stderr)`) → consistent across Tasks 1, 2 and `main` wiring. ✓
- Tag feature-running e2e tests → Task 3. ✓
- `build.sh` targets + README + CHANGELOG → Task 4. ✓
- Minimal build compiles + tests pass → Task 2 Step 7, Task 3 Step 3, Task 4 Step 5. ✓
- Non-goals respected (no `--version`, no config gating, no new deps). ✓

**2. Placeholder scan:** No TBD/TODO. The one "copy the imports it uses" instruction in Task 3 Step 2 is a concrete, bounded action (the function body is fixed), not a placeholder — exact imports are determined by reading the moved function.

**3. Type consistency:** `buildSSE(cfg *config.Config) (sink.Sink, error)` and `startMCP(cfg *config.Config, buf *linebuf.Buffer, stderr io.Writer) (io.Closer, error)` are used identically in both tag variants and in `main`'s call sites. `sseSink`/`mcpCloser` names consistent. Error message substrings (`"built without SSE support"`, `"built without MCP support"`) match between stub and test.
