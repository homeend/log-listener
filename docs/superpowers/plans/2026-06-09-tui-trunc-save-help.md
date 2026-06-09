# TUI usability batch (truncation / save-selection / help panel) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add three independent TUI conveniences — middle-ellipsis filename truncation (toggleable + configurable), save-the-visual-selection via `s`, and a searchable help panel (`?`).

**Architecture:** Each feature follows an established in-repo pattern: truncation is a display-only render-time transform (like the column toggles, no cache rebuild); save-selection reinterprets the existing `save_viewport` action inside visual mode (like `y`/`Y` copy); the help panel is a modal overlay (like the files overlay) whose keys come from `(*keymap.Keymap).Display` — the same source as `KEYBINDINGS.md`, so it can't drift.

**Tech Stack:** Go 1.26, bubbletea, lipgloss, go-runewidth. Spec: `docs/superpowers/specs/2026-06-09-tui-trunc-save-help-design.md`.

**Conventions for every task:**
- TDD: write the failing test first, see it fail, implement, see it pass, commit.
- After each task the full suite must be green: `go test ./...`, `go vet ./...`, `go test -race ./...`.
- Run a single package's tests with `go test ./internal/<pkg>/`.
- Commit messages: `feat(tui): …` / `feat(keymap): …` / `feat(config): …` / `docs: …`.

---

## Task 1: `truncateMiddle` display-width helper

Pure function, no model state. Lives with the other width helpers.

**Files:**
- Modify: `internal/tui/width.go` (add `truncateMiddle`, `takeCols`, `takeColsRight`)
- Test: `internal/tui/width_test.go` (create if absent)

- [ ] **Step 1: Write the failing tests**

Add to `internal/tui/width_test.go`:

```go
package tui

import "testing"

func TestTruncateMiddleFitsUnchanged(t *testing.T) {
	if got := truncateMiddle("short.log", 16); got != "short.log" {
		t.Fatalf("want unchanged, got %q", got)
	}
	// Exactly at the limit is unchanged.
	if got := truncateMiddle("sixteen-chars.lg", 16); got != "sixteen-chars.lg" {
		t.Fatalf("want unchanged at limit, got %q", got)
	}
}

func TestTruncateMiddleLongASCII(t *testing.T) {
	// "application-server.log" is 22 cols; maxCols 16 -> avail 13, left 7, right 6.
	if got := truncateMiddle("application-server.log", 16); got != "applica...er.log" {
		t.Fatalf("got %q", got)
	}
}

func TestTruncateMiddleNeverExceedsWidth(t *testing.T) {
	// Wide (CJK) runes count as 2 cols; result must never overflow maxCols.
	s := "アプリケーションサーバ.log" // mix of wide runes + ASCII
	got := truncateMiddle(s, 16)
	if dispWidth(got) > 16 {
		t.Fatalf("overflow: %q has width %d", got, dispWidth(got))
	}
}

func TestTruncateMiddleDegenerate(t *testing.T) {
	if got := truncateMiddle("anything", 0); got != "" {
		t.Fatalf("maxCols 0 want empty, got %q", got)
	}
	// maxCols <= 3: no room for "..." plus content -> hard prefix, no ellipsis.
	if got := truncateMiddle("anything", 3); got != "any" {
		t.Fatalf("maxCols 3 want %q, got %q", "any", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tui/ -run TestTruncateMiddle -v`
Expected: FAIL — `undefined: truncateMiddle`.

- [ ] **Step 3: Implement the helpers**

Append to `internal/tui/width.go`:

```go
// takeCols returns the longest prefix of s whose display width is <= n,
// never splitting a wide rune (so the result may be < n columns).
func takeCols(s string, n int) string {
	w := 0
	for i, r := range s {
		rw := runeWidth(r)
		if w+rw > n {
			return s[:i]
		}
		w += rw
	}
	return s
}

// takeColsRight returns the longest suffix of s whose display width is <= n,
// never splitting a wide rune.
func takeColsRight(s string, n int) string {
	rs := []rune(s)
	w := 0
	for i := len(rs) - 1; i >= 0; i-- {
		rw := runeWidth(rs[i])
		if w+rw > n {
			return string(rs[i+1:])
		}
		w += rw
	}
	return s
}

// truncateMiddle shortens s to at most maxCols display columns by replacing the
// middle with "...", measured with go-runewidth so wide/CJK names never
// overflow. s is returned unchanged if it already fits. Degenerate cases:
// maxCols <= 0 -> ""; maxCols <= 3 (no room for "..." plus content) -> the
// first maxCols columns of s with no ellipsis.
func truncateMiddle(s string, maxCols int) string {
	if maxCols <= 0 {
		return ""
	}
	if dispWidth(s) <= maxCols {
		return s
	}
	if maxCols <= 3 {
		return takeCols(s, maxCols)
	}
	avail := maxCols - 3
	left := (avail + 1) / 2
	right := avail - left
	return takeCols(s, left) + "..." + takeColsRight(s, right)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tui/ -run TestTruncateMiddle -v`
Expected: PASS (all four).

- [ ] **Step 5: Commit**

```bash
git add internal/tui/width.go internal/tui/width_test.go
git commit -m "feat(tui): truncateMiddle display-width helper"
```

---

## Task 2: Truncation render wiring (model fields + render-path)

The model gains the two fields and the render path truncates the file column. No
toggle key, no config yet — fields are set directly in tests. The key trap: the
truncated name must feed BOTH `fileStyle.Render` AND the `visW` accumulator.

**Files:**
- Modify: `internal/tui/app.go` (add fields `truncateFiles`, `filenameWidth`; const `defaultFilenameWidth`; method `effFilenameWidth`)
- Modify: `internal/tui/render.go:99-103` (truncate in `renderDisplayLineCore`)
- Test: `internal/tui/render_truncate_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `internal/tui/render_truncate_test.go`:

```go
package tui

import (
	"strings"
	"testing"
)

func TestRenderTruncatesFileColumnAndWidth(t *testing.T) {
	m := newModel(100)
	m.showGroup = false // isolate the file column
	m.showFile = true
	m.truncateFiles = true
	m.filenameWidth = 16

	dl := displayLine{
		file:      "application-server.log", // 22 cols -> "applica...er.log" (16)
		body:      "x",
		bodyWidth: 1,
	}
	out, w := m.renderDisplayLineCore(dl, false)

	if !strings.Contains(stripANSI(out), "applica...er.log") {
		t.Fatalf("file not truncated in output: %q", stripANSI(out))
	}
	if strings.Contains(stripANSI(out), "application-server.log") {
		t.Fatalf("full name leaked into output: %q", stripANSI(out))
	}
	// visW = bodyWidth(1) + dispWidth("applica...er.log")(16) + 2 (": ") = 19.
	if w != 19 {
		t.Fatalf("reported width want 19, got %d", w)
	}
}

func TestRenderNoTruncateWhenOff(t *testing.T) {
	m := newModel(100)
	m.showGroup = false
	m.showFile = true
	m.truncateFiles = false // off

	dl := displayLine{file: "application-server.log", body: "x", bodyWidth: 1}
	out, _ := m.renderDisplayLineCore(dl, false)
	if !strings.Contains(stripANSI(out), "application-server.log") {
		t.Fatalf("name should be full when truncation off: %q", stripANSI(out))
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/tui/ -run TestRenderTruncate -v` and `... -run TestRenderNoTruncate -v`
Expected: FAIL — `dl.truncateFiles undefined` / fields not on `model`.

- [ ] **Step 3: Add model fields + constant + method**

In `internal/tui/app.go`, near the other display-toggle fields (`showFile`, `showGroup`, around line 283), add:

```go
	truncateFiles bool // toggle: middle-ellipsis long filenames in the file column
	filenameWidth int  // max display cols before truncation; <=0 => defaultFilenameWidth
```

Near the other TUI constants (e.g. where `defaultScrollback` is defined), add:

```go
const defaultFilenameWidth = 16
```

Add the method (put it in `app.go` near `newModel`, or in `render.go`):

```go
// effFilenameWidth is the truncation limit in display columns, applying the
// default when unset — mirroring how Scrollback treats 0 as "use the default".
func (m *model) effFilenameWidth() int {
	if m.filenameWidth > 0 {
		return m.filenameWidth
	}
	return defaultFilenameWidth
}
```

- [ ] **Step 4: Truncate in the render path**

In `internal/tui/render.go`, replace the `if m.showFile { … }` block in
`renderDisplayLineCore` (currently lines 99-103):

```go
	if m.showFile {
		name := dl.file
		if m.truncateFiles {
			name = truncateMiddle(dl.file, m.effFilenameWidth())
		}
		sb.WriteString(fileStyle.Render(name))
		sb.WriteString(": ")
		visW += dispWidth(name) + 2 // ": "
	}
```

(`name` is computed once and used for both `Render` and `visW` — do not duplicate the `truncateMiddle` call or measure `dl.file`.)

- [ ] **Step 5: Run to verify it passes**

Run: `go test ./internal/tui/ -run 'TestRenderTruncate|TestRenderNoTruncate' -v`
Expected: PASS.

- [ ] **Step 6: Full gates + commit**

Run: `go test ./... && go vet ./...`
Expected: PASS.

```bash
git add internal/tui/app.go internal/tui/render.go internal/tui/render_truncate_test.go
git commit -m "feat(tui): truncate file column when truncateFiles is set"
```

---

## Task 3: Truncation toggle action + `f` key

Wire the runtime toggle through the keymap and regenerate the doc.

**Files:**
- Modify: `internal/keymap/actions.go` (new action + `AllActions` entry)
- Modify: `internal/keymap/defaults.go` (default key `f`)
- Modify: `internal/tui/update.go` (toggle case)
- Modify: `KEYBINDINGS.md` (regenerated, not hand-edited)
- Test: `internal/tui/update_truncate_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `internal/tui/update_truncate_test.go`:

```go
package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestToggleFilenameTruncKey(t *testing.T) {
	m := newModel(100)
	if m.truncateFiles {
		t.Fatal("should start off")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if !m.truncateFiles {
		t.Fatal("f did not toggle truncation on")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if m.truncateFiles {
		t.Fatal("f did not toggle truncation back off")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/tui/ -run TestToggleFilenameTruncKey -v`
Expected: FAIL — `f` is unbound, so `truncateFiles` never flips.

- [ ] **Step 3: Add the action**

In `internal/keymap/actions.go`, add the constant in the `const (…)` block (after `ActionVisualSelect`):

```go
	ActionToggleFilenameTrunc Action = "toggle_filename_trunc"
```

Append to `AllActions` (after the `ActionVisualSelect` entry):

```go
	{ActionToggleFilenameTrunc, "Toggle filename truncation", "Shorten long filenames in the file column with a middle ellipsis.", "main"},
```

- [ ] **Step 4: Add the default key**

In `internal/keymap/defaults.go`, add to the `map[Action][]string` literal:

```go
		ActionToggleFilenameTrunc: {"f"},
```

- [ ] **Step 5: Handle the action**

In `internal/tui/update.go`, add a case in the action `switch` (e.g. after `ActionCollapseAll`):

```go
		case keymap.ActionToggleFilenameTrunc:
			m.truncateFiles = !m.truncateFiles
```

- [ ] **Step 6: Run to verify the toggle test passes**

Run: `go test ./internal/tui/ -run TestToggleFilenameTruncKey -v`
Expected: PASS.

- [ ] **Step 7: Regenerate KEYBINDINGS.md**

Run: `./build.sh keybindings-docs`
Then verify the doc test: `go test ./internal/keymap/ -run TestDocsUpToDate -v`
Expected: PASS (the new row is now present).

- [ ] **Step 8: Full gates + commit**

Run: `go test ./... && go vet ./...`
Expected: PASS.

```bash
git add internal/keymap/actions.go internal/keymap/defaults.go internal/tui/update.go internal/tui/update_truncate_test.go KEYBINDINGS.md
git commit -m "feat(keymap): f toggles filename truncation"
```

---

## Task 4: Truncation config plumbing

Plumb `tui.truncate_filenames` / `tui.filename_width` from YAML through resolved
config and `tui.Options` into the model; document in the example YAML.

**Files:**
- Modify: `internal/config/yaml.go` (raw `TUI` fields + flatten block ~line 370)
- Modify: `internal/config/cli.go` (resolved `Config` fields)
- Modify: `internal/tui/app.go` (`Options` fields + `tui.New` wiring)
- Modify: `main.go:470` (pass the two fields)
- Modify: `log-listener.example.yml` (document under `tui:`)
- Test: `internal/config/yaml_test.go` (new test)

- [ ] **Step 1: Write the failing test**

Add to `internal/config/yaml_test.go`:

```go
func TestLoadTUITruncation(t *testing.T) {
	dir := t.TempDir()
	yml := writeYAML(t, dir, "log.yml", `
files:
  - id: default
    paths: ['/tmp/output-*.log']
tui:
  truncate_filenames: true
  filename_width: 20
`)
	homeStub := func() (string, error) { return dir, nil }
	cfg, err := loadWithFS([]string{"--config", yml}, refNow, homeStub)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.TUITruncateFilenames {
		t.Fatal("truncate_filenames not loaded")
	}
	if cfg.TUIFilenameWidth != 20 {
		t.Fatalf("filename_width: %d", cfg.TUIFilenameWidth)
	}
}

func TestLoadTUITruncationDefaults(t *testing.T) {
	dir := t.TempDir()
	yml := writeYAML(t, dir, "log.yml", `
files:
  - id: default
    paths: ['/tmp/output-*.log']
tui:
  scrollback: 5000
`)
	homeStub := func() (string, error) { return dir, nil }
	cfg, err := loadWithFS([]string{"--config", yml}, refNow, homeStub)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TUITruncateFilenames {
		t.Fatal("truncate_filenames should default false")
	}
	if cfg.TUIFilenameWidth != 0 {
		t.Fatalf("filename_width should default 0 (=> 16 at consumption), got %d", cfg.TUIFilenameWidth)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/config/ -run TestLoadTUITruncation -v`
Expected: FAIL — `cfg.TUITruncateFilenames` / `cfg.TUIFilenameWidth` undefined.

- [ ] **Step 3: Add raw YAML fields**

In `internal/config/yaml.go`, extend the `TUI` struct (currently `Enabled`, `Scrollback`):

```go
type TUI struct {
	Enabled           *bool `yaml:"enabled,omitempty"`
	Scrollback        *int  `yaml:"scrollback,omitempty"`
	TruncateFilenames *bool `yaml:"truncate_filenames,omitempty"`
	FilenameWidth     *int  `yaml:"filename_width,omitempty"`
}
```

- [ ] **Step 4: Add resolved fields**

In `internal/config/cli.go`, near `TUIScrollback int` (line 39), add:

```go
	TUITruncateFilenames bool // tui.truncate_filenames; default false
	TUIFilenameWidth     int  // tui.filename_width; 0 => default 16 at consumption
```

- [ ] **Step 5: Flatten in mergeYAMLInto**

In `internal/config/yaml.go`, inside the `if yc.TUI != nil {` block (after the `Scrollback` handling, ~line 379):

```go
		if t.TruncateFilenames != nil {
			cfg.TUITruncateFilenames = *t.TruncateFilenames
		}
		if t.FilenameWidth != nil {
			cfg.TUIFilenameWidth = *t.FilenameWidth
		}
```

- [ ] **Step 6: Run to verify the config tests pass**

Run: `go test ./internal/config/ -run TestLoadTUITruncation -v`
Expected: PASS (both).

- [ ] **Step 7: Plumb into the TUI Options + model + main**

In `internal/tui/app.go`, add to the `Options` struct:

```go
	TruncateFiles bool // tui.truncate_filenames default
	FilenameWidth int  // tui.filename_width (0 => default)
```

In `tui.New`, after `m.setViewport = opts.SetViewport` (around line 133):

```go
	m.truncateFiles = opts.TruncateFiles
	m.filenameWidth = opts.FilenameWidth
```

In `main.go`, in the `tui.New(tui.Options{…})` literal (line 470), add:

```go
		TruncateFiles: cfg.TUITruncateFilenames,
		FilenameWidth: cfg.TUIFilenameWidth,
```

- [ ] **Step 8: Document in the example YAML**

In `log-listener.example.yml`, under the `tui:` block (after the `scrollback:` line), add:

```yaml
  truncate_filenames: false    # middle-ellipsis long filenames in the file column
  filename_width: 16           # max display columns before truncation (toggle live with 'f')
```

- [ ] **Step 9: Full gates + commit**

Run: `go test ./... && go vet ./...`
Expected: PASS.

```bash
git add internal/config/yaml.go internal/config/cli.go internal/config/yaml_test.go internal/tui/app.go main.go log-listener.example.yml
git commit -m "feat(config): tui.truncate_filenames + tui.filename_width"
```

---

## Task 5: `snapshotSelection` for save-selection

Pure model method that turns the current visual span into export lines.

**Files:**
- Modify: `internal/tui/save.go` (add `snapshotSelection`)
- Test: `internal/tui/save_test.go` (new test)

- [ ] **Step 1: Write the failing test**

Add to `internal/tui/save_test.go`:

```go
func TestSnapshotSelection(t *testing.T) {
	m := newModel(100)
	m.lines = []displayLine{
		{group: "g", file: "a.log", body: "line one"},
		{group: "g", file: "a.log", body: "line two"},
		{group: "g", file: "a.log", body: "line three"},
	}
	// Select rows 0..1 (anchor 0, cursor 1).
	m.visualMode = true
	m.setVisualAnchorRow(0)
	m.setVisualCursorRow(1)

	got := m.snapshotSelection()
	want := []string{
		"[g] a.log: line one",
		"[g] a.log: line two",
	}
	if len(got) != len(want) {
		t.Fatalf("want %d lines, got %d: %#v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d: want %q, got %q", i, want[i], got[i])
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/tui/ -run TestSnapshotSelection -v`
Expected: FAIL — `undefined: snapshotSelection`.

- [ ] **Step 3: Implement**

Add to `internal/tui/save.go`:

```go
// snapshotSelection returns the visual selection's rows as plain export text
// (full prefixes, styling stripped) via plainExportLine, in display order.
// Visual mode guarantees len(m.lines) > 0 and a valid [lo, hi] from
// selectionBounds.
func (m *model) snapshotSelection() []string {
	lo, hi := m.selectionBounds()
	out := make([]string, 0, hi-lo+1)
	for i := lo; i <= hi; i++ {
		out = append(out, plainExportLine(m.lines[i]))
	}
	return out
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/tui/ -run TestSnapshotSelection -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/save.go internal/tui/save_test.go
git commit -m "feat(tui): snapshotSelection exports the visual span"
```

---

## Task 6: Save selection with `s` in visual mode

Reinterpret `save_viewport` (key `s`) inside visual mode as save-selection. This
requires changing `handleVisualKey`'s return type to thread a `tea.Cmd`.

**Files:**
- Modify: `internal/tui/visual.go` (signature + save case)
- Modify: `internal/tui/update.go:24` (call site)
- Modify: `internal/keymap/actions.go` (extend `ActionVisualSelect` description)
- Modify: `KEYBINDINGS.md` (regenerated — the description column changed)
- Test: `internal/tui/visual_test.go` (new test)

- [ ] **Step 1: Write the failing test**

Add to `internal/tui/visual_test.go`:

```go
func TestVisualSaveKeyReturnsCmdAndExits(t *testing.T) {
	m := newModel(100)
	m.lines = []displayLine{
		{group: "g", file: "a.log", body: "one"},
		{group: "g", file: "a.log", body: "two"},
	}
	m.visualMode = true
	m.setVisualAnchorRow(0)
	m.setVisualCursorRow(1)

	_, cmd := m.handleVisualKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if cmd == nil {
		t.Fatal("s in visual mode must return a save Cmd")
	}
	if m.visualMode {
		t.Fatal("s should exit visual mode after saving")
	}
}
```

(If `visual_test.go` lacks the bubbletea import, add `tea "github.com/charmbracelet/bubbletea"`.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/tui/ -run TestVisualSaveKey -v`
Expected: FAIL — `handleVisualKey` returns a single value (won't compile against the two-value call), or `s` is ignored.

- [ ] **Step 3: Change the signature + add the save case**

In `internal/tui/visual.go`, change `handleVisualKey` to return `(tea.Model, tea.Cmd)`. Every existing `return m` becomes `return m, nil`. Add the save case in the keymap-resolved switch, alongside `ActionCopyReference`/`ActionCopyText`:

```go
func (m *model) handleVisualKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if act, ok := m.resolvedKM().Lookup(msg.String()); ok {
		switch act {
		case keymap.ActionCopyReference:
			m.copyVisualSelection()
			m.exitVisual()
			return m, nil
		case keymap.ActionCopyText:
			m.copyVisualText()
			m.exitVisual()
			return m, nil
		case keymap.ActionSaveViewport:
			lines := m.snapshotSelection()
			m.exitVisual()
			return m, m.saveCmd(lines)
		}
	}
	switch msg.String() {
	case "up", "k":
		m.moveVisualCursor(-1)
	case "down", "j":
		m.moveVisualCursor(1)
	case " ":
		m.setVisualAnchorRow(m.visualCursorRow())
	case "esc":
		m.exitVisual()
	}
	return m, nil
}
```

- [ ] **Step 4: Update the call site**

In `internal/tui/update.go`, line 24, change:

```go
		if m.visualMode {
			return m.handleVisualKey(msg)
		}
```

(was `return m.handleVisualKey(msg), nil`).

- [ ] **Step 5: Run to verify it passes**

Run: `go test ./internal/tui/ -run TestVisualSaveKey -v`
Expected: PASS. Also run the whole tui package to catch any other caller of the changed signature: `go test ./internal/tui/`.

- [ ] **Step 6: Document the `s` binding in the action description**

In `internal/keymap/actions.go`, update the `ActionVisualSelect` `AllActions` entry's description to:

```go
	{ActionVisualSelect, "Visual select", "Enter visual line-selection mode (space sets the start; y copies the range, Y the text, s saves it to a file, all exit; esc cancels).", "main"},
```

- [ ] **Step 7: Regenerate KEYBINDINGS.md**

Run: `./build.sh keybindings-docs && go test ./internal/keymap/ -run TestDocsUpToDate -v`
Expected: PASS.

- [ ] **Step 8: Full gates + commit**

Run: `go test ./... && go vet ./...`
Expected: PASS.

```bash
git add internal/tui/visual.go internal/tui/update.go internal/tui/visual_test.go internal/keymap/actions.go KEYBINDINGS.md
git commit -m "feat(tui): s saves the visual selection to a file"
```

---

## Task 7: Help overlay — action, state, and modal input

Register `ActionHelp` (`?`), add the overlay state fields, open it, and make it
fully modal (printable keys filter, j/k scroll, esc/? close). Rendering comes in
Task 8 — this task lands behavior verified by state, not pixels.

**Files:**
- Modify: `internal/keymap/actions.go` (`ActionHelp` + entry)
- Modify: `internal/keymap/defaults.go` (default key `?`)
- Modify: `internal/tui/app.go` (fields `showHelp`, `helpQuery`, `helpScroll`)
- Create: `internal/tui/help.go` (`handleHelpKey`)
- Modify: `internal/tui/update.go` (modal guard + open case)
- Modify: `KEYBINDINGS.md` (regenerated)
- Test: `internal/tui/help_test.go` (create)

- [ ] **Step 1: Write the failing tests**

Create `internal/tui/help_test.go`:

```go
package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestHelpOpensAndClosesOverlays(t *testing.T) {
	m := newModel(100)
	m.showFiles = true
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	if !m.showHelp {
		t.Fatal("? did not open help")
	}
	if m.showFiles {
		t.Fatal("opening help should close the files overlay")
	}
	// esc closes.
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.showHelp {
		t.Fatal("esc did not close help")
	}
}

func TestHelpModalFilterAndScroll(t *testing.T) {
	m := newModel(100)
	m.showHelp = true
	// Printable keys build the filter.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
	if m.helpQuery != "qu" {
		t.Fatalf("helpQuery = %q, want %q", m.helpQuery, "qu")
	}
	// backspace trims.
	m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if m.helpQuery != "q" {
		t.Fatalf("helpQuery after backspace = %q", m.helpQuery)
	}
	// '?' closes even mid-filter.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	if m.showHelp {
		t.Fatal("? did not close help")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/tui/ -run TestHelp -v`
Expected: FAIL — `m.showHelp` undefined / `?` unbound.

- [ ] **Step 3: Add the action + key**

In `internal/keymap/actions.go`, add to the `const` block:

```go
	ActionHelp Action = "help"
```

Append to `AllActions`:

```go
	{ActionHelp, "Help", "Show the searchable keybindings panel.", "main"},
```

In `internal/keymap/defaults.go`, add:

```go
		ActionHelp: {"?"},
```

- [ ] **Step 4: Add model fields**

In `internal/tui/app.go`, near the overlay fields (`showFiles`, `showGroupsPanel`):

```go
	showHelp   bool   // help overlay open (modal)
	helpQuery  string // live filter for the help list (independent of searchQuery)
	helpScroll int    // first visible help row
```

- [ ] **Step 5: Add the modal guard + open case**

In `internal/tui/update.go`, in the `tea.KeyMsg` branch, add the guard alongside the others (after the `wrapPrompt` guard, ~line 31):

```go
		if m.showHelp {
			return m.handleHelpKey(msg), nil
		}
```

Add the open case in the action `switch`:

```go
		case keymap.ActionHelp:
			m.showHelp = true
			m.helpQuery = ""
			m.helpScroll = 0
			m.showFiles = false
			m.showGroupsPanel = false
			m.showRenderersPanel = false
```

- [ ] **Step 6: Implement handleHelpKey**

Create `internal/tui/help.go`:

```go
package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// handleHelpKey processes keys while the help overlay is open. It is fully
// modal: esc/? close, j/k/arrows scroll, backspace trims the filter, and any
// other printable rune extends the filter. Everything else is ignored.
func (m *model) handleHelpKey(msg tea.KeyMsg) *model {
	switch msg.String() {
	case "esc", "?":
		m.showHelp = false
		m.helpQuery = ""
		return m
	case "up", "k":
		m.helpScroll--
		if m.helpScroll < 0 {
			m.helpScroll = 0
		}
		return m
	case "down", "j":
		m.helpScroll++
		return m
	case "backspace":
		if r := []rune(m.helpQuery); len(r) > 0 {
			m.helpQuery = string(r[:len(r)-1])
			m.helpScroll = 0
		}
		return m
	}
	// Printable single-rune keys extend the filter.
	if msg.Type == tea.KeyRunes && len(msg.Runes) == 1 {
		m.helpQuery += string(msg.Runes)
		m.helpScroll = 0
	}
	return m
}
```

(The `helpScroll++` upper bound is clamped against the filtered row count in
Task 8's render, where the row total is known; an over-large `helpScroll` simply
shows a full last page.)

- [ ] **Step 7: Run to verify the help tests pass**

Run: `go test ./internal/tui/ -run TestHelp -v`
Expected: PASS.

- [ ] **Step 8: Regenerate KEYBINDINGS.md**

Run: `./build.sh keybindings-docs && go test ./internal/keymap/ -run TestDocsUpToDate -v`
Expected: PASS.

- [ ] **Step 9: Full gates + commit**

Run: `go test ./... && go vet ./...`
Expected: PASS.

```bash
git add internal/keymap/actions.go internal/keymap/defaults.go internal/tui/app.go internal/tui/help.go internal/tui/update.go internal/tui/help_test.go KEYBINDINGS.md
git commit -m "feat(tui): ? opens a modal help overlay (state + input)"
```

---

## Task 8: Help overlay — rows, filtering, and rendering

Build the displayed rows from `keymap.AllActions` + the resolved keymap, filter
by `helpQuery`, and render the panel mirroring the files overlay.

**Files:**
- Modify: `internal/tui/help.go` (`helpRow`, `helpRows`, `renderHelp`)
- Modify: `internal/tui/view.go:71-80` (dispatch `renderHelp` when `showHelp`)
- Test: `internal/tui/help_test.go` (add tests)

- [ ] **Step 1: Write the failing tests**

Add to `internal/tui/help_test.go`:

```go
import (
	"strings"

	"github.com/homeend/log-listener/internal/keymap"
)

func TestHelpRowsReflectResolvedKeys(t *testing.T) {
	m := newModel(100)
	rows := m.helpRows()
	if len(rows) != len(keymap.AllActions) {
		t.Fatalf("want %d rows, got %d", len(keymap.AllActions), len(rows))
	}
	// The Quit row's keys must match the resolved keymap's Display.
	var quit helpRow
	for _, r := range rows {
		if r.title == "Quit" {
			quit = r
		}
	}
	if quit.keys != m.resolvedKM().Display(keymap.ActionQuit) {
		t.Fatalf("quit keys %q != resolved Display %q", quit.keys, m.resolvedKM().Display(keymap.ActionQuit))
	}
}

func TestHelpRowsFilter(t *testing.T) {
	m := newModel(100)
	m.helpQuery = "quit"
	rows := m.helpRows()
	if len(rows) == 0 {
		t.Fatal("filter 'quit' matched nothing")
	}
	for _, r := range rows {
		hay := strings.ToLower(r.keys + " " + r.title + " " + r.desc)
		if !strings.Contains(hay, "quit") {
			t.Fatalf("row %q does not match filter", r.title)
		}
	}
}

func TestRenderHelpShowsFilteredTitles(t *testing.T) {
	m := newModel(100)
	m.width = 80
	m.height = 24
	m.showHelp = true
	m.helpQuery = "quit"
	out := stripANSI(m.renderHelp(10))
	if !strings.Contains(out, "Quit") {
		t.Fatalf("render missing Quit row: %q", out)
	}
	if strings.Contains(out, "Page up") {
		t.Fatalf("render should be filtered to 'quit', leaked: %q", out)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/tui/ -run 'TestHelpRows|TestRenderHelp' -v`
Expected: FAIL — `helpRow` / `helpRows` / `renderHelp` undefined.

- [ ] **Step 3: Implement rows + filter + render**

Add to `internal/tui/help.go` (add `"strings"` and the `keymap` import):

```go
// helpRow is one line of the help overlay: the resolved key display for an
// action plus its title and description.
type helpRow struct {
	keys, title, desc string
}

// helpRows builds the help list from keymap.AllActions in order, resolving keys
// for the current OS via the same Display the KEYBINDINGS.md doc uses. When
// helpQuery is set, only rows whose keys+title+desc contain it (case-insensitive)
// are kept.
func (m *model) helpRows() []helpRow {
	q := strings.ToLower(m.helpQuery)
	out := make([]helpRow, 0, len(keymap.AllActions))
	for _, d := range keymap.AllActions {
		r := helpRow{
			keys:  m.resolvedKM().Display(d.Action),
			title: d.Title,
			desc:  d.Desc,
		}
		if q != "" {
			hay := strings.ToLower(r.keys + " " + r.title + " " + r.desc)
			if !strings.Contains(hay, q) {
				continue
			}
		}
		out = append(out, r)
	}
	return out
}

// renderHelp draws the modal help overlay into rows display lines, mirroring the
// files overlay: a header, the filtered rows windowed by helpScroll, then blank
// fill. The footer of the panel echoes the active filter.
func (m *model) renderHelp(rows int) string {
	all := m.helpRows()

	// Clamp helpScroll so the last page stays full.
	avail := rows - 1
	if avail < 1 {
		avail = 1
	}
	if m.helpScroll > len(all)-avail {
		m.helpScroll = len(all) - avail
	}
	if m.helpScroll < 0 {
		m.helpScroll = 0
	}

	out := make([]string, 0, rows)
	title := " Help — type to filter · j/k scroll · " +
		m.keyDisplay(keymap.ActionHelp) + "/" + m.keyDisplay(keymap.ActionCloseOverlay) + " close "
	if m.helpQuery != "" {
		title = " Help — /" + m.helpQuery + " "
	}
	out = append(out, headerBg.Width(m.width).MaxHeight(1).Render(title))

	if len(all) == 0 {
		out = append(out, m.padRow(dimStyle.Render("  (no matching keys)")))
		for i := 2; i < rows; i++ {
			out = append(out, m.blankRow())
		}
		return strings.Join(out, "\n")
	}

	start := m.helpScroll
	end := start + avail
	if end > len(all) {
		end = len(all)
	}
	for i := start; i < end; i++ {
		r := all[i]
		out = append(out, m.padRow(fmt.Sprintf("  %-22s  %-28s  %s",
			r.keys, r.title, dimStyle.Render(r.desc))))
	}
	for i := end - start; i < avail; i++ {
		out = append(out, m.blankRow())
	}
	return strings.Join(out, "\n")
}
```

Add `"fmt"` to the imports in `help.go`.

- [ ] **Step 4: Dispatch from View**

In `internal/tui/view.go`, add a case to the `switch` in `View()` (before `showGroupsPanel`, so help takes precedence):

```go
	switch {
	case m.showHelp:
		body = m.renderHelp(contentH)
	case m.showGroupsPanel:
		body = m.renderGroupsPanel(contentH)
```

- [ ] **Step 5: Run to verify it passes**

Run: `go test ./internal/tui/ -run 'TestHelpRows|TestRenderHelp' -v`
Expected: PASS.

- [ ] **Step 6: Full gates + commit**

Run: `go test ./... && go vet ./...`
Expected: PASS.

```bash
git add internal/tui/help.go internal/tui/view.go internal/tui/help_test.go
git commit -m "feat(tui): render the searchable help overlay"
```

---

## Task 9: Documentation (README + CHANGELOG)

Per the repo convention, deliver user-facing docs with the features.

**Files:**
- Modify: `README.md`
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Update README**

Add the three features to the relevant places in `README.md`:
- In the TUI keybindings/usage section, add rows for `f` (toggle filename truncation), `?` (help panel), and note `s` saves the selection in visual mode (alongside the existing `y`/`Y`).
- In the config reference, document `tui.truncate_filenames` (default false) and `tui.filename_width` (default 16).

(Match the surrounding README format — find the existing key list / config table and extend it in the same style.)

- [ ] **Step 2: Update CHANGELOG**

Add an entry under the current/unreleased section of `CHANGELOG.md`:

```markdown
### Added
- TUI: middle-ellipsis filename truncation in the file column — toggle live with `f`, or set defaults via `tui.truncate_filenames` / `tui.filename_width`.
- TUI: `s` saves the current visual selection to a file (parallel to `y`/`Y` copy).
- TUI: `?` opens a searchable help overlay listing every keybinding for the current OS.
```

- [ ] **Step 3: Verify docs are consistent**

Run: `go test ./... && go vet ./...`
Expected: PASS (sanity — no code changed, but confirms the tree is clean).

- [ ] **Step 4: Commit**

```bash
git add README.md CHANGELOG.md
git commit -m "docs: README + CHANGELOG for trunc/save-selection/help"
```

---

## Final review

After all tasks: dispatch a final whole-branch code review, then run the full
gates once more including the race detector:

```bash
go test ./... && go vet ./... && go test -race ./...
```

Then proceed to **superpowers:finishing-a-development-branch**.

---

## Self-review notes (plan author)

- **Spec coverage:** truncation helper (T1), render+width trap (T2), toggle key (T3), config plumbing (T4); save snapshot (T5) + visual `s` + signature change (T6); help action/state/modal input (T7) + rows/filter/render (T8); docs (T9). All spec sections mapped.
- **Correction vs spec:** the spec said "regenerate `log-listener.example.yml` + golden test." There is no generator and no golden test for it — it is hand-maintained — so T4 hand-edits its `tui:` block. No `emit.go` change needed (the example file is authored, not emitted).
- **Type consistency:** `truncateFiles`/`filenameWidth`/`effFilenameWidth`/`defaultFilenameWidth`, `TUITruncateFilenames`/`TUIFilenameWidth`, `Options.TruncateFiles`/`FilenameWidth`, `snapshotSelection`, `handleVisualKey (tea.Model, tea.Cmd)`, `showHelp`/`helpQuery`/`helpScroll`, `helpRow`/`helpRows`/`renderHelp`/`handleHelpKey` — used identically across tasks.
- **Signature ripple:** T6 changes `handleVisualKey`'s return type; the only caller is `update.go:24` (verified). Step 5 re-runs the whole tui package to catch any test caller.
