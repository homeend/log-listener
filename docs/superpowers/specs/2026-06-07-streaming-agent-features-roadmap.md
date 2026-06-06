# Roadmap: streaming → agent features

**Date:** 2026-06-07
**Status:** shape agreed; specs written per-feature at the start of each cycle.

This is the one-screen shape of three related features. They are built in
sequence, each with its own spec → plan → implementation cycle. This document
records the cross-cutting decisions so later cycles don't re-litigate them; the
detailed contract for each feature lives in its own design doc.

## The three features

1. **Save** (`s` / `S`) — write what's on screen (or the whole buffer) to a
   text file. Smallest, self-contained. **Built first.**
2. **Block annotation + render plugins** — group consecutive lines into
   *blocks*, run pluggable *processors* that attach metadata (e.g. "this might
   be an exception, possibly Java"), and let toggleable *render plugins*
   decorate them. Plus jump-to-next/prev-block and block highlighting.
3. **Embedded MCP server** — an HTTP MCP endpoint running *inside the live
   process*, sharing the same in-memory buffer the user is watching, so an
   external agent can query exactly what the user sees.

**Build order: #2 → #3 → #1.** The one hard dependency: #1's `list_exceptions`
tool reuses #3's exception processor, so #3 must land before #1.

## Cross-cutting decisions (locked)

### Transport for the MCP server: embedded HTTP, not stdio
stdio spawns a *fresh subprocess* with empty memory — it can never see the
buffer the user is watching. The requirement is the opposite: the user watches
a live stream and asks an agent to inspect *that* stream. So the MCP server is
**embedded in the running process** and exposed over **Streamable HTTP** (the
SDK's `StreamableHTTPHandler`), enabled by a flag and running **alongside** the
TUI / stdout / SSE — exactly like the existing `--sse` hub. Plain SSE is
rejected: it is emit-only and cannot serve request/response tool calls.

### MCP library: official Go SDK (`github.com/modelcontextprotocol/go-sdk`)
Chosen over hand-rolling JSON-RPC. Verified at v1.6.1: **CGO-free**
(`CGO_ENABLED=0` build succeeds → `build-static` survives) and provides typed
tool registration with auto-generated JSON Schema, all transports, and the
`initialize` handshake. This **overrides the locked "only 5 deps" rule** — a
deliberate choice. New transitive deps: `google/jsonschema-go`,
`segmentio/encoding` (+`asm`), `yosida95/uritemplate`, `golang.org/x/oauth2`,
`golang.org/x/sys`. Record this in CLAUDE.md when #1 is implemented.

### Identity: an opaque ID per line and per block
- **Line ID** — auto-generated, opaque, assigned when a line enters the shared
  buffer. The agent resolves `get_line(id)` / `get_range(idA, idB)`; ranges are
  resolved by buffer lookup (IDs are not ordered).
- **Block ID** — its own namespace, assigned by the segmenter. The user can
  hand *any* block to an agent; "exception" is just one metadata tag a
  processor may attach, not a precondition for referenceability.
- Both IDs are **copyable via a keybinding** so the user can paste them to an
  agent. The clipboard path is **OSC 52** (terminal-clipboard escape; dep-free,
  works over SSH, terminal-dependent) rather than a CGO/X11 clipboard library.

### Block model (shared package, consumed by both TUI and MCP)
- A **block** = a maximal run of `[head + continuation lines]` over the line
  sequence (reusing the existing `isContinuation` predicate). Works the same
  whether the lines came from one multiline entry or several consecutive
  single-line entries (a tailed stack trace).
- **Processors annotate only — they never re-segment.** Segmentation stays a
  single concern (indentation/continuation runs). v1 ships exactly one
  processor (exception detection) and one render plugin (`renderException`); the
  *seam* is pluggable but there is no config-driven registry yet.
- **Named v1 limitation:** non-indented frames (PHP `#0`, Go `panic:` headers,
  Java `Caused by:`) are not joined into the block by indentation alone. The
  per-language research still informs detection *within* a block.

## Open items deferred to their cycles
- **Designating "which" line/block to copy:** the TUI today is viewport/tail
  based with no per-line cursor. #1/#3 will add a cursor + the copy keys.
- **Block-ID stability under streaming:** a block is "open" while continuations
  keep arriving; ID assignment/stability is resolved in the #3 spec.
- **Buffer cap vs TUI scrollback:** the MCP ring must be ≥ TUI scrollback so a
  line the user sees is always resolvable. Sized in the #1 spec.
