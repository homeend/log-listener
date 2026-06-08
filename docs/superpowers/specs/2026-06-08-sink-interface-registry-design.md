# Sink Interface + Fanout Registry — Design

**Date:** 2026-06-08
**Status:** Approved (design)
**Scope:** `internal/sink`, `main.go`. Refactor — behavior-preserving except one
documented micro-change (see "Intentional behavior change").

## Context

This is sub-project #1 of a larger architectural direction: moving
`log-listener` toward a pipe-and-filters core with a compile-time plugin
system. The end goal is **separate build-tagged binaries** — one built with
MCP/SSE, one without.

Today the output fan-out is hardcoded. `main.go`'s `emit()` calls
`stdoutSink.Emit`, then `sseHub.Emit` (nil-guarded), then `fileSink.Emit`
(nil-guarded); the TUI pump goroutine (`runWatchTUI`) duplicates the same
nil-guarded sequence. There is **no `Sink` interface** — the three concrete
sinks are passed individually through `emit`, `runOnce`, `runWatch`, and
`runWatchTUI`. There is no single object a future build-tag file can plug into.

This sub-project introduces that seam — a `Sink` interface and a `Fanout`
registry — **without** changing any runtime behavior. Build-tag gating and the
separate binaries are the next cycle and are explicitly out of scope here.

## Goal

Replace the hardcoded fan-out with one registry object (`sink.Fanout`) so that
output sinks are uniform and a future build-tagged constructor can add or omit
a sink by returning `nil`. Output must remain byte-identical.

## Design

### Component: `sink.Sink` interface + `sink.Fanout`

New surface in `internal/sink`:

```go
// Sink is a passive output that receives every emitted event.
type Sink interface {
	Emit(render.Event)
	Close() error
}

// Fanout is the ordered registry of passive sinks. It is itself a Sink,
// so it composes.
type Fanout struct{ sinks []Sink }

func NewFanout(sinks ...Sink) *Fanout   // skips nil entries
func (f *Fanout) Add(s Sink)            // skips nil; build-tag plug-in point (next cycle)
func (f *Fanout) Emit(ev render.Event)  // calls each sink's Emit in registration order
func (f *Fanout) Close() error          // closes each sink; joins errors via errors.Join
```

- `Stdout` gains a no-op `Close() error { return nil }`. `SSEHub` and
  `FileSink` already have `Close`, so both already satisfy `Sink`.
- `NewFanout` and `Add` skip `nil` entries. This is the seam: next cycle, a
  build-tagged constructor (e.g. `buildSSE(cfg)`) returns the hub when SSE is
  compiled in and `nil` when it is compiled out, and `main` calls
  `fanout.Add(buildSSE(cfg))` unconditionally.
- **Ordering is preserved.** Registration order equals the current call order
  (stdout → sse → file), so byte output is identical.

**Rejected alternatives:**
- A plain `[]Sink` held in `main`: provides no single registry object, which is
  the entire point of the seam.
- `init()`-based self-registration: too implicit, and unnecessary — separate
  binaries will use build tags on explicit constructors next cycle.

### Data flow

Two roles stay privileged and are **not** sinks:

- `linebuf.Append` runs first and assigns `ev.ID`. It mutates the event and is
  the ID authority; it remains a direct call before fan-out.
- The TUI `app` is the interactive primary via `app.Push`. bubbletea owns its
  lifecycle, so it is not a passive sink and is not a `Fanout` member.

The `Fanout` carries only passive output sinks:

| Mode                              | Primary             | `Fanout` holds        |
|-----------------------------------|---------------------|-----------------------|
| `runOnce` / `runWatch` (non-TUI)  | `stdout` (in fanout)| stdout, sse?, file?   |
| `runWatchTUI`                     | `app.Push` (direct) | sse?, file?           |

Concrete changes in `main.go`:

- `emit()` drops its `(stdoutSink, sseHub, fileSink)` parameters and gains one
  `*sink.Fanout`. Body becomes: `ev.ID = buf.Append(ev); fanout.Emit(ev)`.
- The TUI pump becomes `rev.ID = buf.Append(rev); app.Push(rev);
  fanout.Emit(rev)`. The preload loop (`main.go:494`) becomes `fanout.Emit(ev)`.
- `runOnce`, `runWatch`, `runWatchTUI` collapse their three sink params into one
  `*sink.Fanout`.
- `main.run()` builds the appropriate `Fanout` once per mode and uses
  `defer fanout.Close()` in place of the individual `defer ...Close()` calls.

### Error handling

- `Fanout.Close()` closes every sink even if one fails, and returns
  `errors.Join` of all errors. `main` logs/handles it exactly as it handles the
  current `FileSink`/`SSEHub` close errors (no new fatal paths).
- `Emit` has no error return (matching today's sinks); per-sink emit failures
  are handled internally by each sink as they are today.

## Intentional behavior change

There is one deliberate, reviewed exception to "behavior-preserving":

- **TUI-mode preload now also reaches SSE.** Today, preload events in TUI mode
  go only to the TUI app and the output file, skipping the SSE hub — even though
  *live* events in TUI mode do reach SSE. Routing TUI preload through the unified
  `{sse, file}` fanout removes this latent inconsistency, so TUI preload now
  mirrors non-TUI preload. Impact is negligible (no SSE client is connected at
  startup before preload flushes) and no existing test exercises the
  TUI+SSE+preload combination.

All other output is byte-identical.

## Non-goals

- No build tags, no separate binaries (next cycle).
- MCP is untouched — it is a service, not a sink, and stays wired separately.
- No change to what any sink emits, to `linebuf`, or to TUI internals.

## Testing

- `Fanout` unit tests (`internal/sink`): emits to all sinks in registration
  order; `nil` entries are skipped by both `NewFanout` and `Add`; `Close`
  closes all sinks and joins their errors; a fake sink records `Emit`/`Close`
  calls and order.
- All existing sink and integration tests must pass **unchanged** — stdout
  output, `-o` output-file passthrough, and SSE broadcast. These are the proof
  of behavior preservation (the one TUI-preload→SSE exception above has no test
  and is intentional).
- `go test ./...`, `go vet ./...`, and `go test -race ./...` stay green.

## Success criteria

- `sink.Sink` and `sink.Fanout` exist; `Stdout`, `SSEHub`, `FileSink` all
  satisfy `Sink`.
- `emit()` and the TUI pump fan out through a single `*sink.Fanout`.
- The three run-mode functions take one `*sink.Fanout` instead of three sinks.
- Output is byte-identical to before in all modes; full test/vet/race green.
