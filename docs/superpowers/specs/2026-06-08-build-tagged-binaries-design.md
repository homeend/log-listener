# Build-Tag-Gated MCP/SSE Binaries — Design

**Date:** 2026-06-08
**Status:** Approved (design)
**Scope:** `main.go` (new build-tagged file pairs), e2e test tagging, `build.sh`,
docs. No change to `internal/config`, `internal/mcp`, or `internal/sink` logic.

## Context

Sub-project #3 of the plugin architecture (see
[[plugin-architecture-roadmap]] in project memory). The user wants to compile
`log-listener` **without** the MCP server and/or **without** SSE, producing
leaner binaries. The MCP server is the only consumer of the external
`github.com/modelcontextprotocol/go-sdk` dependency; gating it out must drop
that dependency from the binary entirely. SSE uses only stdlib (`net/http`), so
gating it is about feature/flag minimalism, not binary size.

This builds directly on sub-project #1: the `sink.Sink` interface + `sink.Fanout`
registry, whose `Add`/`NewFanout` skip nil — the seam a gated SSE constructor
plugs into.

## Decisions (locked)

- **Two independent opt-out build tags:** `nomcp` and `nosse`.
- **Default build includes both features** (so `go build` and
  `go install github.com/homeend/log-listener@latest` stay full-featured — those
  paths cannot pass `-tags`).
- **`go build -tags nomcp`** drops the MCP server AND the `go-sdk` dependency.
  **`-tags nosse`** drops the SSE server. Tags compose: `-tags "nomcp nosse"`.
- **Asking a stripped binary for the missing feature is a hard error** (non-zero
  exit), for both the CLI flag (`--mcp`/`--sse`) and a YAML `output.sse` block.

## Mechanism

A Go package is in the binary iff a *compiled* file imports it. `internal/mcp`
(sole importer of the SDK) is imported today only by `main`. A `//go:build`
constraint selects which files compile for a given `-tags` set. By relocating
the `internal/mcp` import into a file that is excluded under `nomcp`, a `nomcp`
build never imports `internal/mcp`, so the package and the SDK are absent from
the binary.

`internal/config` is **not** gated: it only parses `--mcp`/`--sse`/`output.sse`
into string fields (`cfg.MCPAddr`, `cfg.SSEAddr`) and imports no feature package,
so it compiles identically in every build. The flag still parses; the *decision
to act on it* is what's gated, and lives entirely in `main`. Consequently a
stripped binary treats `--mcp` as a *recognized-but-unsupported* flag (hard
error), not an "unknown flag".

## Components

Four new files in `package main`, as two mutually-exclusive pairs. Each pair
defines one function with an identical signature, so `main.run()` calls it
tag-agnostically.

### MCP pair

**`feature_mcp.go`** (`//go:build !nomcp`) — the only file importing `internal/mcp`:

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
// requested. Mirrors the wiring previously inline in run().
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

**`feature_nomcp.go`** (`//go:build nomcp`) — no mcp import:

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
// support is a hard error; otherwise it is a no-op.
func startMCP(cfg *config.Config, _ *linebuf.Buffer, _ io.Writer) (io.Closer, error) {
	if cfg.MCPAddr != "" {
		return nil, errors.New("--mcp: this binary was built without MCP support (use a full build)")
	}
	return nil, nil
}
```

### SSE pair

**`feature_sse.go`** (`//go:build !nosse`):

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
// Sink when SSE wasn't requested.
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

**`feature_nosse.go`** (`//go:build nosse`):

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

Note: `sink.SSEHub` returns `(sink.Sink, error)`; the disabled stub returns a
`nil` `sink.Sink`. `internal/sink/sse.go` remains compiled under `nosse`
(stdlib-only, no dependency) — only its construction is gated.

## `main.go` changes

`run()` replaces its inline SSE and MCP construction with calls to the gated
functions. The current inline SSE block (`sink.NewSSEHub` + `Start`) and MCP
block (`mcp.New` + `Start` + the `mcp on http://` log line + `defer Close`) are
removed; `main.go` no longer imports `internal/mcp`.

```go
// SSE: gated. Result feeds the per-mode Fanout (nil skipped; Fanout owns Close).
sseSink, err := buildSSE(cfg)
if err != nil {
	fmt.Fprintln(stderr, "log-listener:", err)
	return 1
}
// ... fileSink construction unchanged ...

// --once / TUI / runWatch branches build NewFanout(stdoutSink, sseSink, fileSink)
// (TUI: NewFanout(sseSink, fileSink)), exactly as today but with sseSink in
// place of the old sseHub variable.

// MCP: gated. Not started in --once (the --once branch returns before this).
mcpCloser, err := startMCP(cfg, buf, stderr)
if err != nil {
	fmt.Fprintln(stderr, "log-listener:", err)
	return 1
}
if mcpCloser != nil {
	defer mcpCloser.Close()
}
```

The `--once` ordering is preserved: MCP is still constructed only on the
non-`--once` path (the `--once` branch returns first), matching today's "MCP not
started in `--once`" rule.

## Tests

- **Full build** (`go test ./...`) covers everything as today.
- **Tag the feature-running e2e tests** so they don't run under the stripping
  tag (where the binary hard-errors on the feature):
  - MCP e2e files (`e2e_mcp_test.go`, `e2e_mcp_tools_test.go`, `e2e_mcp_tui_test.go`)
    get `//go:build !nomcp`.
  - The single SSE e2e test `TestE2ESSEDeliversEvents` is moved out of
    `e2e_test.go` into a new `e2e_sse_test.go` with `//go:build !nosse`.
- **Disabled-stub tests** (run only under their tag):
  - `feature_nomcp_test.go` (`//go:build nomcp`): building/invoking with `--mcp`
    yields the hard error and non-zero exit; without `--mcp`, normal run.
  - `feature_nosse_test.go` (`//go:build nosse`): same for `--sse` and for a YAML
    `output.sse` block.
  These exercise `startMCP`/`buildSSE` stubs via `run(args, stdout, stderr)`
  directly (no subprocess needed), asserting the returned exit code and stderr
  message.
- **Minimal build compiles and passes:** `go build -tags "nomcp nosse" .` and
  `go test -tags "nomcp nosse" ./...` are green.
- `internal/mcp` and `internal/sink` unit tests are unaffected.

## build.sh + docs

- New `build.sh` targets:
  - `build-nomcp` → `go build -tags nomcp`
  - `build-nosse` → `go build -tags nosse`
  - `build-minimal` → `go build -tags "nomcp nosse"`
  - `test-minimal` → `go test -tags "nomcp nosse" ./...`
  Update the in-script usage comment (lines 2–14) and the `help` target to list
  them.
- README: a short "Build variants" subsection documenting the tags, that the
  default build is full, and that `nomcp` drops the MCP SDK dependency. Note the
  hard-error behavior.
- CHANGELOG `[Unreleased]`: a "Build variants" entry.

## Verification of dependency drop

The plan includes an explicit check that `nomcp` excised the SDK:

```bash
go build -tags nomcp -o /tmp/ll-nomcp .
go version -m /tmp/ll-nomcp | grep modelcontextprotocol   # expect: no output
go build -o /tmp/ll-full .
go version -m /tmp/ll-full | grep modelcontextprotocol     # expect: present
```

## Non-goals (YAGNI)

- No `--version`/`--features` introspection flag (the hard error already tells
  the user what's missing).
- No build-tag gating of SSE *code* for size; `internal/sink/sse.go` stays
  compiled under `nosse`.
- No general plugin/capability registry — that is the later cycle #2 over
  `keymap.Action`.
- No new dependencies; no change to `internal/config` parsing, `internal/mcp`,
  or `internal/sink` behavior.

## Success criteria

- `go build` (default) is byte-for-byte behavior-identical to today: MCP + SSE
  both work.
- `go build -tags nomcp` produces a binary with no `internal/mcp` / no go-sdk
  (`go version -m` confirms), where `--mcp` hard-errors with a clear message and
  non-zero exit.
- `go build -tags nosse` produces a binary where `--sse` and a YAML `output.sse`
  block hard-error with a clear message and non-zero exit.
- `main.go` no longer imports `internal/mcp`.
- `go test ./...`, `go vet ./...`, `go test -race ./...`, and
  `go test -tags "nomcp nosse" ./...` are all green.
