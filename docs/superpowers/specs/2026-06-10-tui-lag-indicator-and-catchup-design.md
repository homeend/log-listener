# Lag indicator, debug-dump lag diagnostics, and manual catch-up (F2)

**Date:** 2026-06-10
**Status:** Approved (design)
**Scope:** This change only. Non-blocking TUI push (F3, the backpressure root-cause fix) is a **separate** follow-up design/plan.

## Problem

The TUI can show the same block of lines rolling over and over (e.g. `ABCD ABCD ABCD`).
Investigation of `debug-log-*.txt` dumps established the cause: **backlog replay**, not a
re-read bug. log-listener reads each byte forward exactly once (every backward seek is
diag-logged; dumps showed one stale ROTATE and zero TRUNCATE), but its read offset had
fallen far **behind** the file's true EOF and was grinding through a dense burst of
duplicate lines that a stuck upstream agent had written at machine speed.

Why it fell behind: the pump goroutine (`main.go` `runWatchTUI`) is fully serial per line —
`Render → buf.Append → app.Push → fanout.Emit`. `app.Push` calls `prog.Send` (`app.go`),
which **blocks** when the terminal can't repaint fast enough (WSL/Windows Terminal). A block
there stalls the whole pump, fills the watcher's cap-1024 `events` channel, and stops the
tailer from reading — so the terminal's paint speed becomes the tailer's read speed.

This change does **not** fix that backpressure (that is F3). It gives the user:
1. **Visibility** — a live indicator of how far behind log-listener is, plus concrete
   per-file lag in the debug dump.
2. **A manual escape hatch** — a keybinding to fast-forward all tailers to live (F2),
   dropping the stale backlog, with a marker recording exactly what was skipped.

## Goals / non-goals

- **Goal:** live "behind N bytes" indicator in the TUI; per-file lag + channel saturation
  in the debug dump; a manual catch-up key that skips tailers to EOF and records the skip.
- **Non-goal:** automatic skipping on a threshold (manual only — user decides from the
  indicator). Per-file selective skip (catch up skips *all* files). Fixing backpressure (F3).
- **Non-goal:** exact lines-behind. Lag is reported in **bytes** (cheap, honest). Counting
  unread newlines would require reading the unread region. An approximate line estimate is
  out of scope unless trivially derivable.

## Components & data flow

### 1. `internal/watch` — expose lag, perform catch-up

- **`Tailer.pos` becomes `atomic.Int64`.** It is mutated in `Tick`/`readAvailable`/`open`
  on the watcher's `loop()` goroutine and must be read from the TUI goroutine for the
  indicator. Atomic makes that read race-free. All existing `t.pos` reads/writes migrate to
  `Load`/`Store`/`Add`.

- **`Watcher.Lag() LagStat`.** Snapshots the tailer list under `w.mu`, releases the lock,
  then `os.Stat`s each file *unlocked* (same snapshot-then-work pattern as `tickAll`, so a
  large file set doesn't hold `w.mu` across I/O).
  ```go
  type FileLag struct { Path string; Pos, Size, Lag int64 } // Lag = max(0, Size-Pos)
  type LagStat struct {
      TotalBytes int64
      Files      []FileLag
      Pending    int // len(w.events)
      PendingCap int // cap(w.events)
  }
  ```

- **`Watcher.SkipToEOF() SkipStat`.** Catch-up. The seek mutates the shared `*os.File`
  offset and `t.pos`/`t.buf`, which races `Tick`'s `Read` — so it is **routed through the
  watcher's own `loop()` goroutine** via a command channel (a `chan skipReq` selected in
  `loop()` alongside fsnotify events / tick / done). On that goroutine, for each tailer:
  `Seek(size, SeekStart)`, `pos.Store(size)`, `buf.Reset()`. Replies with:
  ```go
  type SkipStat struct { Files int; Bytes int64 }
  ```
  `Bytes` is the summed pre-skip lag. Lines-skipped is not tracked (would require reading the
  unread region); the marker reports **bytes and file count only**.

### 2. TUI — live lag indicator

- New nil-safe `tui.Options` field `Lag func() watch.LagStat`, stored on the model.
  (`internal/tui` importing `internal/watch` introduces no cycle — `watch` does not import
  `tui`. `main.go` already imports both and wires the callback.)
- The model polls `Lag()` on a **~1 s `tea.Tick`** loop (a self-rescheduling `tea.Cmd`),
  storing `m.lagBytes`. Not polled per-frame: stat-ing the full file set on every repaint
  would add the very latency we are surfacing.
- Rendered in the footer's right-hand status region (`footerhints.go`) **only when behind**:
  e.g. `⤓ behind 7.9 MB`, using a human-readable byte formatter. Hidden at lag 0.

### 3. TUI — manual catch-up (F2)

- New keymap action `ActionCatchUp` (`internal/keymap/actions.go` + `defaults.go`). Default
  key **`c`** ("catch up" — verified free against `defaults.go`; `ctrl+e` is already
  `ActionToggleRenderers`). Regenerate `KEYBINDINGS.md` via `--keybindings-doc` so
  `TestDocsUpToDate` stays green. Footer hint label `catch up`.
- New nil-safe `tui.Options` field `CatchUp func() watch.SkipStat`, backed by
  `watcher.SkipToEOF`. `main.go` wires it.
- Key handler in `update.go` returns a `tea.Cmd` (runs off the model goroutine) that calls
  `CatchUp()` and returns a `catchUpResultMsg{stat watch.SkipStat}`.
- `Update` handles `catchUpResultMsg` by:
  - **appending one synthetic marker event** through the normal `appendEvent` path so it
    lands in scrollback and is copyable/searchable/consistent:
    `⤓ skipped 7.9 MB across 1 file(s) to catch up to live`;
  - flashing the footer (reuse the `saveResultMsg` flash path).
- **Lossy by design:** skipped bytes never reach `linebuf`/MCP/SSE. The marker is the record.
- Skips **all** tailers to live. A flood is usually one file, but "catch up" means catch up
  everywhere; selective per-file skip is YAGNI.

### 4. Debug dump — lag diagnostics

- New `== tailer lag ==` section in `debugdump.go`, driven by the same `Lag()` callback:
  - `events channel: Pending/PendingCap` (was the pump saturated?);
  - per-file `path pos=… size=… lag=…`, sorted by lag descending, top 20;
  - `total lag = … bytes`.
- This is the concrete evidence requested: it would have shown the eu29 file with a large
  `lag` and a saturated `Pending`, pinning backlog-replay directly from one dump.

## Concurrency summary (primary risk)

| Operation | Goroutine | Safety |
|---|---|---|
| `pos` read (lag) | TUI | `atomic.Int64` load; `os.Stat` unlocked after snapshot |
| `pos`/`Seek`/`buf` write (skip) | watcher `loop()` | serialized via command channel — no `Seek`/`Read` race |
| marker injection | model goroutine | via returned `Msg`, no re-entrant `Push` |

## Testing

- **`internal/watch`:**
  - `Lag()` returns correct `Size-Pos` after partial appends/reads; never negative.
  - `SkipToEOF()` advances `pos` to EOF; a subsequent `Tick` yields only post-skip content;
    `SkipStat.Bytes` equals the pre-skip lag.
  - `-race` test: concurrent `Tick`s + `Lag()` reads + a `SkipToEOF()`.
- **`internal/tui`:**
  - catch-up handler injects exactly one marker event (assert via the buffer/`m.lines`).
  - footer shows `behind N` when `Lag()` reports >0, hides at 0.
  - debug dump contains the `== tailer lag ==` section with expected fields.
- **Docs:** `TestDocsUpToDate` green after regenerating `KEYBINDINGS.md`.
- Full suite: `go test ./...`, `go vet ./...`, `go test -race ./...` green.

## Commit convention

Follows the repo norm: implementation commit then a review-fixes commit, each leaving
test/vet/race green.
