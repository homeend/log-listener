# Split `internal/tui/app.go` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split the 1678-line `internal/tui/app.go` into focused, single-responsibility files within `package tui`, with zero behavior change.

**Architecture:** Pure relocation. Each task moves a named set of top-level declarations (functions/types/vars/consts) out of `app.go` into a new (or existing) file in the same package, fixes imports, and verifies the build + the unchanged test suite stay green. Because everything stays in `package tui`, no call sites change and no behavior can change — a mistake can only surface as a compile error.

**Tech Stack:** Go 1.26, `internal/tui` package. No new dependencies, no new tests.

**Spec:** `docs/superpowers/specs/2026-06-08-tui-appgo-split-design.md` (read it — the function→file assignment table is authoritative).

---

## How to move code (read first — applies to every task)

Each task is mechanical and identical in shape:

1. **Create the new file** (or open the existing target) with `package tui` as the first line.
2. **Move the named declarations** — cut each listed declaration *together with its doc comment* from `app.go` and paste it into the target file. Move whole declarations only; never split one.
3. **Fix imports — TOUCHED FILES ONLY.** `goimports` is installed at `/home/homeend/go/bin/goimports` (not on `$PATH` — use the full path, or `"$(go env GOPATH)/bin/goimports"`). Run it on ONLY the two files this task touches (the new file + `app.go`), e.g.:
   `"$(go env GOPATH)/bin/goimports" -w internal/tui/<newfile>.go internal/tui/app.go`
   This adds imports the new file needs and removes ones `app.go` no longer uses. Its import grouping matches the repo (verified — no reordering churn). Then `gofmt -w internal/tui/<newfile>.go internal/tui/app.go`.
   **Do NOT run goimports/gofmt on the whole `internal/tui/` directory** — two unrelated files (`multiline_test.go`, `visual_test.go`) carry *pre-existing* gofmt deviations on `main`; leave them alone (out of scope).
4. **Verify green:**
   - `gofmt -l internal/tui/<newfile>.go internal/tui/app.go` → no output (the files you touched are clean)
   - `go build ./...` → exit 0
   - `go test ./internal/tui/` → PASS
5. **Commit** that file's move.

**Conservation check (run after each move):** the set of declarations is only relocated, never changed. After a move, `go vet ./internal/tui/` must be clean and the test suite unchanged. A duplicate-declaration or missing-declaration error from `go build` means a decl was copied instead of moved, or missed — fix before committing.

**Commands** (from `/mnt/t/others/log-listener`):
`go build ./...`, `go test ./internal/tui/`, `go vet ./...`, `go test -race ./internal/tui/`, `go build -tags nomcp ./... && go build -tags nosse ./...`.

**Do NOT** change any function body, signature, name, or comment. **Do NOT** add tests. **Do NOT** touch other packages or other `internal/tui/*.go` files except `viewport.go` (Task 2 adds to it) and `app.go` (everything is cut from it).

---

## Task 1: `width.go` — shared ANSI/width helpers

**Files:**
- Create: `internal/tui/width.go`
- Modify: `internal/tui/app.go` (remove the moved decls)

- [ ] **Step 1: Create `internal/tui/width.go` with `package tui` and move these declarations from `app.go`** (current locations):
  - `var ansiRE` (line 28)
  - `func stripANSI` (line 30)
  - `func runeLen` (line 32)
  - `func dispWidth` (line 38)
  - `func runeWidth` (line 42)

  Keep their doc comments. These are used across the package (also by `search.go`, `visual.go`) — moving them does not change those call sites (same package).

- [ ] **Step 2: Fix imports + format**

Run on the TWO touched files only (new file + `app.go`): `"$(go env GOPATH)/bin/goimports" -w <touched files> && gofmt -l <touched files>` (whole-dir would churn the two pre-existing-dirty test files — see preamble).
`width.go` needs `regexp`, `unicode/utf8`, and `github.com/mattn/go-runewidth`. `app.go` should drop any of those it no longer uses. Expected: `gofmt -l` empty.

- [ ] **Step 3: Build + test green**

Run: `go build ./... && go test ./internal/tui/ && go vet ./internal/tui/`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/tui/width.go internal/tui/app.go
git commit -m "refactor(tui): extract ANSI/width helpers to width.go

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 2: `viewport.go` — receive scroll helpers + movement consts

**Files:**
- Modify: `internal/tui/viewport.go` (receive decls)
- Modify: `internal/tui/app.go` (remove the moved decls)

- [ ] **Step 1: Move these declarations from `app.go` into `internal/tui/viewport.go`** (append after the existing `panBy`, each with its doc comment, blank line between functions so each keeps its own godoc):
  - the `const ( horizStep … hitMargin )` block (line 401)
  - `func (m *model) unstickFromTail` (line 445)
  - `func (m *model) maybeReStick` (line 466)
  - `func (m *model) contentHeight` (line 1150)

  These are viewport-position concerns that belong with `scrollBy`/`scrollFiles`/`panBy`. `hitMargin` is also referenced by `search.go` — unchanged (same package).

- [ ] **Step 2: Fix imports + format**

Run on the TWO touched files only (new file + `app.go`): `"$(go env GOPATH)/bin/goimports" -w <touched files> && gofmt -l <touched files>` (whole-dir would churn the two pre-existing-dirty test files — see preamble).
`viewport.go` likely needs no new imports (these use only model fields / arithmetic). `app.go` drops nothing import-wise unless a now-unused import remains. Expected: `gofmt -l` empty.

- [ ] **Step 3: Build + test green**

Run: `go build ./... && go test ./internal/tui/ && go vet ./internal/tui/`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/tui/viewport.go internal/tui/app.go
git commit -m "refactor(tui): move viewport helpers + movement consts into viewport.go

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 3: `reconcile.go` — buffer→view reconciliation

**Files:**
- Create: `internal/tui/reconcile.go`
- Modify: `internal/tui/app.go`

- [ ] **Step 1: Create `internal/tui/reconcile.go` (`package tui`) and move these from `app.go`** (with doc comments):
  - `func tuiDecompose` (line 431)
  - `func (m *model) appendEvent` (line 790)
  - `func (m *model) appendStored` (line 800)
  - `func displayLinesFromEntry` (line 811)
  - `func (m *model) reconcile` (line 834)
  - `func (m *model) dragViewStateDown` (line 905)
  - `func (m *model) visibleEntries` (line 938)
  - `func (m *model) reRenderAll` (line 952)

- [ ] **Step 2: Fix imports + format**

Run on the TWO touched files only (new file + `app.go`): `"$(go env GOPATH)/bin/goimports" -w <touched files> && gofmt -l <touched files>` (whole-dir would churn the two pre-existing-dirty test files — see preamble).
`reconcile.go` likely needs `github.com/homeend/log-listener/internal/render` and `internal/linebuf` (and whatever `reconcile`/`reRenderAll` reference). Let `goimports` resolve it. Expected: `gofmt -l` empty.

- [ ] **Step 3: Build + test green**

Run: `go build ./... && go test ./internal/tui/ && go vet ./internal/tui/`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/tui/reconcile.go internal/tui/app.go
git commit -m "refactor(tui): extract reconcile/append path to reconcile.go

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 4: `render.go` — display-line rendering + visibility predicates

**Files:**
- Create: `internal/tui/render.go`
- Modify: `internal/tui/app.go`

- [ ] **Step 1: Create `internal/tui/render.go` (`package tui`) and move these from `app.go`** (with doc comments):
  - `func decomposeEvent` (line 982)
  - `func (m *model) renderDisplayLine` (line 1009)
  - `func (m *model) renderDisplayLineAt` (line 1018)
  - `func (m *model) renderDisplayLineCore` (line 1032)
  - `func (m *model) groupEnabledLine` (line 1078)
  - `func (m *model) lineEnabled` (line 1091)
  - `func (m *model) filteredIndices` (line 1103)
  - `func isContinuation` (line 1137)

  Note: `renderDisplayLineCore` references `matchStyle`/`currentMatchStyle`, which move to `view.go` in Task 6 — that is a same-package cross-file reference and needs no import. The package won't fully build until Task 6 only if a style var is temporarily undefined; but since those vars still live in `app.go` until Task 6, they remain defined throughout. (They leave `app.go` only in Task 6, into `view.go`, both in-package — always defined.)

- [ ] **Step 2: Fix imports + format**

Run on the TWO touched files only (new file + `app.go`): `"$(go env GOPATH)/bin/goimports" -w <touched files> && gofmt -l <touched files>` (whole-dir would churn the two pre-existing-dirty test files — see preamble).
Expected: `gofmt -l` empty.

- [ ] **Step 3: Build + test green**

Run: `go build ./... && go test ./internal/tui/ && go vet ./internal/tui/`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/tui/render.go internal/tui/app.go
git commit -m "refactor(tui): extract display-line rendering to render.go

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 5: `update.go` — key-dispatch + reload

**Files:**
- Create: `internal/tui/update.go`
- Modify: `internal/tui/app.go`

- [ ] **Step 1: Create `internal/tui/update.go` (`package tui`) and move these from `app.go`** (with doc comments):
  - `func (m *model) Update` (line 495 — the large key-dispatch switch)
  - `func (m *model) applyReload` (line 757)

- [ ] **Step 2: Fix imports + format**

Run on the TWO touched files only (new file + `app.go`): `"$(go env GOPATH)/bin/goimports" -w <touched files> && gofmt -l <touched files>` (whole-dir would churn the two pre-existing-dirty test files — see preamble).
`update.go` likely needs `github.com/charmbracelet/bubbletea` (`tea`), `internal/keymap`, and possibly `internal/render`. Let `goimports` resolve it; `app.go` will shed now-unused imports (e.g. it may lose `tea`/`keymap` if no longer referenced there). Expected: `gofmt -l` empty.

- [ ] **Step 3: Build + test green**

Run: `go build ./... && go test ./internal/tui/ && go vet ./internal/tui/`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/tui/update.go internal/tui/app.go
git commit -m "refactor(tui): extract Update key-dispatch to update.go

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 6: `view.go` — View, footer, panels, stream/viewport rendering

**Files:**
- Create: `internal/tui/view.go`
- Modify: `internal/tui/app.go`

- [ ] **Step 1: Create `internal/tui/view.go` (`package tui`) and move these from `app.go`** (with doc comments):
  - the style `var ( groupStyle … currentMatchStyle )` block (line 483)
  - `func (m *model) hint` (line 1160)
  - `func (m *model) resolvedKM` (line 1167)
  - `func (m *model) keyDisplay` (line 1177)
  - `func (m *model) View` (line 1181)
  - `func (m *model) renderFooter` (line 1226)
  - `func (m *model) disabledGroupCount` (line 1281)
  - `func (m *model) disabledRendererCount` (line 1291)
  - `func (m *model) toggleRenderer` (line 1306)
  - `func (m *model) renderGroupsPanel` (line 1317)
  - `func rendererShiftChar` (line 1366)
  - `func (m *model) renderRenderersPanel` (line 1374)
  - `func (m *model) padRow` (line 1417)
  - `func pluralS` (line 1428)
  - `func (m *model) collectVisible` (line 1439)
  - `func (m *model) publishViewport` (line 1487)
  - `func (m *model) renderStream` (line 1500)
  - `func (m *model) blankRow` (line 1540)
  - `func (m *model) blankRows` (line 1548)
  - `func (m *model) clipLine` (line 1581)
  - `func clipANSIWindow` (line 1600)
  - `func (m *model) renderFiles` (line 1647)

- [ ] **Step 2: Fix imports + format**

Run on the TWO touched files only (new file + `app.go`): `"$(go env GOPATH)/bin/goimports" -w <touched files> && gofmt -l <touched files>` (whole-dir would churn the two pre-existing-dirty test files — see preamble).
`view.go` likely needs `fmt`, `strings`, `github.com/charmbracelet/lipgloss`, `github.com/mattn/go-runewidth`, `internal/keymap`. Let `goimports` resolve it; `app.go` sheds the now-unused ones. Expected: `gofmt -l` empty.

- [ ] **Step 3: Build + test green (this is the big move — watch for any leftover decl in app.go)**

Run: `go build ./... && go test ./internal/tui/ && go vet ./internal/tui/`
Expected: PASS.

- [ ] **Step 4: Confirm `app.go` is now just the facade + model**

Run: `grep -n "^func \|^type \|^var \|^const " internal/tui/app.go`
Expected: only `defaultScrollback`, the types (`FileEntry`/`displayLine`/messages/`App`/`GroupInfo`/`RendererInfo`/`Options`/`scrollbackEvent`/`model`/`RenderFunc`), and the funcs `New`/`Run`/`Push`/`SetFiles`/`Reload`/`Quit`/`newModel`/`Init`. Nothing from the moved clusters. `wc -l internal/tui/app.go` should be ~280.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/view.go internal/tui/app.go
git commit -m "refactor(tui): extract View/footer/panels/stream rendering to view.go

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 7: Final verification

**Files:** none (verification only).

- [ ] **Step 1: Every former app.go decl lives in exactly one file**

Run: `grep -rn "^func (m \*model) reconcile\b\|^func (m \*model) Update\b\|^func (m \*model) View\b\|^func dispWidth\b\|^func (m \*model) contentHeight\b" internal/tui/*.go`
Expected: exactly one definition of each (in reconcile.go, update.go, view.go, width.go, viewport.go respectively). No duplicates.

- [ ] **Step 2: Formatting + full gates**

Run: `gofmt -l internal/tui/app.go internal/tui/update.go internal/tui/reconcile.go internal/tui/render.go internal/tui/view.go internal/tui/width.go internal/tui/viewport.go`
Expected: empty (all split files clean). Note: a whole-dir `gofmt -l internal/tui/` will still list the two pre-existing-dirty files `multiline_test.go` and `visual_test.go` — that is expected and out of scope; do NOT "fix" them.

Then: `go build ./... && go test ./... && go vet ./... && go test -race ./internal/tui/`
Expected: everything PASS.

- [ ] **Step 3: Tagged builds (CGO-free invariant)**

Run: `go build -tags nomcp ./... && go build -tags nosse ./... && go build ./...`
Expected: PASS.

- [ ] **Step 4: Confirm the split shape**

Run: `wc -l internal/tui/app.go internal/tui/update.go internal/tui/reconcile.go internal/tui/render.go internal/tui/view.go internal/tui/width.go internal/tui/viewport.go`
Expected: `app.go` ~280; `update.go` ~310; `reconcile.go` ~210; `render.go` ~200; `view.go` ~470; `width.go` ~25; `viewport.go` ~110. (Exact counts will vary; the point is `app.go` is no longer the catch-all.)

---

## Self-review notes (reconciled against the spec)

- **Spec coverage:** every file in the spec's assignment table maps to a task — width.go (T1), viewport.go receivers (T2), reconcile.go (T3), render.go (T4), update.go (T5), view.go (T6), final verification (T7). Every top-level decl currently in app.go is assigned in exactly one task (the union of T1–T6 decl lists equals the `grep -n "^func\|^type\|^var\|^const"` output of app.go, minus the facade/model decls that stay).
- **No behavior change:** no task edits a body/signature/name; all are moves. The only verification is build + unchanged tests green.
- **Ordering rationale:** width.go first (most shared, smallest); viewport.go receivers next; then reconcile/render/update; view.go last (largest, and it carries the style vars that render.go references — both stay in-package and defined throughout, so build is green at every step).
- **Decl-name consistency:** the function names in the task lists are copied verbatim from the `grep` of app.go (e.g. `renderDisplayLineCore`, `dragViewStateDown`, `clipANSIWindow`) — they must match exactly when grepping to confirm the move.
- **Import handling:** every task uses `goimports -w` to converge imports, then `gofmt -l` must be empty — the plan does not hand-enumerate import lists as gospel (goimports is authoritative), but names the likely additions per file as a sanity check.
