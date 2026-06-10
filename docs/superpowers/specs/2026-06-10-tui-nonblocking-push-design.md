# Non-blocking TUI push (F3) — backpressure root-cause fix

**Date:** 2026-06-10
**Status:** Approved (design) — Approach B
**Scope:** This change only. Follows the F2 lag-indicator/catch-up change
(`2026-06-10-tui-lag-indicator-and-catchup-design.md`), which surfaced/mitigated
the *symptom*; this fixes the *cause*.

## Problem

The pump goroutine (`main.go` `runWatchTUI`) is serial per line:
`Render → buf.Append → app.Push → fanout.Emit`. `app.Push` calls
`prog.Send(EventMsg{ev})` (app.go), which **blocks** when bubbletea's Update/paint
loop can't keep up (slow terminal repaint on WSL/Windows Terminal). A block there
stalls the whole pump, fills the watcher's cap-1024 `events` channel, and stops the
tailer reading — so the terminal's paint speed becomes the tailer's read speed, and
log-listener falls behind a fast source and replays the backlog.

## Key insight

The pump appends to `linebuf` **before** `app.Push`, and the TUI's `EventMsg`
handler ignores the payload — `case EventMsg: m.reconcile()` just re-reads the
shared buffer. So the TUI does not need every event *delivered*; it needs a
*signal* to reconcile, and signals can coalesce. F3 is a coalescing-signal change,
not a data-plumbing change. `linebuf` (MCP) and SSE are upstream of the push and
stay fully lossless.

## Design (Approach B: signal channel + forwarder)

- **App gains** a coalescing signal channel and a forwarder-stop channel:
  ```go
  type App struct {
      prog *tea.Program
      mu   sync.Mutex
      done bool
      sig  chan struct{} // cap 1: "reconcile pending"
      stop chan struct{} // closed on exit to stop the forwarder
  }
  ```
  `New` initializes `sig: make(chan struct{}, 1)`, `stop: make(chan struct{})`.

- **`Push` becomes non-blocking** (keeps its `render.Event` param for call-site
  stability; the payload was already unused downstream):
  ```go
  func (a *App) Push(render.Event) {
      a.mu.Lock(); d := a.done; a.mu.Unlock()
      if d { return }
      select {
      case a.sig <- struct{}{}: // mark dirty
      default:                  // already pending — safe to drop (see below)
      }
  }
  ```

- **Forwarder goroutine** turns signals into reconcile messages; started in `Run`
  before `prog.Run()`, stopped after:
  ```go
  func (a *App) forward() {
      for {
          select {
          case <-a.sig:
              a.prog.Send(reconcileMsg{}) // no-op if the program has stopped
          case <-a.stop:
              return
          }
      }
  }
  ```
  `Run`: `go a.forward()` → `a.prog.Run()` → set `done` → `close(a.stop)`.

- **New message + handler:** `type reconcileMsg struct{}`; in `update.go`
  add `case reconcileMsg: m.reconcile()`. The `EventMsg` case stays (harmless;
  no sender after this change, but keeps the type/handler for any future direct
  use) — actually it is removed-as-sender only; the pump now triggers reconciles
  via the forwarder.

## Why it is lossless (coalescing correctness)

The pump appends event E to `linebuf` **before** calling `Push`. Two cases at the
non-blocking send:
- **`sig` empty →** send succeeds; the forwarder will emit a `reconcileMsg` whose
  `reconcile()` runs *after* E was appended, so it sees E.
- **`sig` full (drop) →** a signal is already queued and not yet converted (the
  forwarder converts then the channel is empty again). That pending signal yields a
  future `reconcileMsg` whose `reconcile()` runs after E's append, so it sees E.

In both cases a reconcile that observes E is guaranteed. The only unsafe pattern —
"append with no future reconcile" — cannot occur, because a full channel *is* a
guaranteed pending reconcile. Many bursts collapse into one reconcile+paint.

## Lifecycle / safety notes

- The forwarder runs on its own goroutine, so `prog.Send` blocking (back-pressure
  from a busy loop) never reaches the pump — only the forwarder waits.
- `prog.Send` after the program stops is a documented no-op, so the forwarder never
  hangs on shutdown; `close(a.stop)` then ends it.
- Pre-`Run` window: the pump may `Push` before `Run` starts the forwarder. Those
  fill `sig` (non-blocking); the forwarder drains them once started. `prog.Send`
  happens on the forwarder goroutine (never the main goroutine), so the
  "Send-before-Run deadlocks main" hazard does not apply. Initial preload state is
  already rendered by `New`'s seeding `reconcile()`.

## Testing

- **`App.Push` is non-blocking + coalescing:** construct an `App{sig: cap1}`, call
  `Push` many times with no draining; it must return promptly and leave exactly one
  queued signal (`len(a.sig) == 1`). `done` short-circuits.
- **`reconcileMsg` reconciles:** seed the shared buffer, feed `reconcileMsg` via
  `Update`, assert `m.lines` reflects the buffer (mirrors the old `EventMsg` test).
- **`-race`** across the suite (forwarder + Push + reconcile).
- Full suite / vet / race green; F2 lag tests unaffected.

## Out of scope

No change to `linebuf`, SSE, stdout, or the watcher. No automatic catch-up. The F2
indicator/`c` remain as a manual safety net (now rarely needed).
