# Save Viewport / Scrollback Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add two TUI keybindings — `s` (save the visible viewport) and `S` (save the full scrollback) — that write the stream to a timestamped `screen-log-listener-*.txt` file, confirmed by a transient footer message.

**Architecture:** Two new keymap actions flow through `internal/keymap` (constants + per-OS defaults + regenerated `KEYBINDINGS.md`). In `internal/tui`, pure `model` methods (`snapshotViewport`, `snapshotScrollback`) turn display lines into plain text; a `tea.Cmd` performs the file write off the model goroutine and returns a `saveResultMsg` that sets a transient `flash` string shown in the footer. The two snapshot methods are kept as pure `[]string`-returning methods so feature #1 (MCP) can reuse them as `get_viewport` / `get_scrollback` tools.

**Tech Stack:** Go 1.26, bubbletea (`tea.Cmd`/`tea.Msg`), the existing `internal/keymap` action table, `os.WriteFile`.

**Spec:** `docs/superpowers/specs/2026-06-07-save-viewport-design.md`

---

## File Structure

- `internal/keymap/actions.go` — add `ActionSaveViewport`, `ActionSaveScrollback` constants + `AllActions` entries.
- `internal/keymap/defaults.go` — bind `s` / `S` (OS-independent).
- `internal/keymap/defaults_test.go` — test the new defaults.
- `KEYBINDINGS.md` — regenerated from the action table.
- `internal/tui/save.go` (new) — `plainExportLine`, `snapshotViewport`, `snapshotScrollback`, `saveResultMsg`, `saveCmd`, `writeExport`.
- `internal/tui/save_test.go` (new) — unit tests for the above.
- `internal/tui/app.go` — `flash` + `saveDir` model fields; clear `flash` on key events; dispatch the two actions; handle `saveResultMsg`; footer `flash` branch.
- `internal/tui/app_test.go` — dispatch/flash integration test (or place in `save_test.go`).
- `README.md`, `CHANGELOG.md` — document the feature.

---

## Task 1: Keymap actions + defaults + regenerated doc

**Files:**
- Modify: `internal/keymap/actions.go`
- Modify: `internal/keymap/defaults.go`
- Test: `internal/keymap/defaults_test.go`
- Regenerate: `KEYBINDINGS.md`

- [ ] **Step 1: Write the failing test**

Append to `internal/keymap/defaults_test.go`:

```go
func TestSaveActionsHaveDefaults(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		dm := defaultFor(goos)
		if !equalSlice(dm[ActionSaveViewport], []string{"s"}) {
			t.Errorf("%s: save_viewport default = %v, want [s]", goos, dm[ActionSaveViewport])
		}
		if !equalSlice(dm[ActionSaveScrollback], []string{"S"}) {
			t.Errorf("%s: save_scrollback default = %v, want [S]", goos, dm[ActionSaveScrollback])
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestSaveActionsHaveDefaults ./internal/keymap/`
Expected: FAIL — `ActionSaveViewport` / `ActionSaveScrollback` are undefined (compile error).

- [ ] **Step 3: Add the action constants**

In `internal/keymap/actions.go`, add to the `const (...)` block after `ActionResetHoriz`:

```go
	ActionResetHoriz      Action = "reset_horiz"
	ActionSaveViewport    Action = "save_viewport"
	ActionSaveScrollback  Action = "save_scrollback"
```

And append to `AllActions` after the `ActionResetHoriz` entry:

```go
	{ActionResetHoriz, "Reset horizontal scroll", "Return to column 0.", "main"},
	{ActionSaveViewport, "Save viewport", "Write the visible rows to a text file.", "main"},
	{ActionSaveScrollback, "Save scrollback", "Write the full scrollback buffer to a text file.", "main"},
```

- [ ] **Step 4: Add the default keys**

In `internal/keymap/defaults.go`, add to the OS-independent `m := map[Action][]string{...}` literal (e.g. right after `ActionResetHoriz: {"0"},`):

```go
		ActionResetHoriz:      {"0"},
		ActionSaveViewport:    {"s"},
		ActionSaveScrollback:  {"S"},
```

- [ ] **Step 5: Run keymap tests — the new test passes, the doc test fails**

Run: `go test ./internal/keymap/`
Expected: `TestSaveActionsHaveDefaults` PASS, `TestDefaultForCoversEveryAction` PASS, but `TestDocsUpToDate` FAIL ("KEYBINDINGS.md is stale") — the action table changed but the committed doc hasn't.

- [ ] **Step 6: Regenerate the keybindings doc**

Run: `./build.sh keybindings-docs`
Expected: prints `wrote ./KEYBINDINGS.md`. The file now contains "Save viewport" and "Save scrollback" rows.

- [ ] **Step 7: Run keymap tests — all green**

Run: `go test ./internal/keymap/`
Expected: PASS (including `TestDocsUpToDate`).

- [ ] **Step 8: Commit**

```bash
git add internal/keymap/actions.go internal/keymap/defaults.go internal/keymap/defaults_test.go KEYBINDINGS.md
git commit -m "feat(keymap): save_viewport (s) / save_scrollback (S) actions"
```

---

## Task 2: Plain-text export + snapshot methods

**Files:**
- Create: `internal/tui/save.go`
- Test: `internal/tui/save_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/tui/save_test.go`:

```go
package tui

import (
	"strings"
	"testing"

	"github.com/homeend/log-listener/internal/render"
)

func TestPlainExportLine(t *testing.T) {
	head := displayLine{group: "acp", file: "a.log", body: "hello world", bodyWidth: 11}
	if got := plainExportLine(head); got != "[acp] a.log: hello world" {
		t.Errorf("head export = %q", got)
	}
	// Block line: ANSI stripped, no prefix.
	block := displayLine{group: "acp", file: "a.log", body: dimStyle.Render("  at Foo.bar"), isBlock: true}
	if got := plainExportLine(block); got != "  at Foo.bar" {
		t.Errorf("block export = %q (want stripped, unprefixed)", got)
	}
}

func TestSnapshotScrollbackReturnsEveryLine(t *testing.T) {
	m := newModel(100)
	m.appendEvent(render.Event{Group: "g", File: "/x/a.log",
		Rendered: []render.Part{{Type: "text", Value: "LINE-ONE"}}})
	m.appendEvent(render.Event{Group: "g", File: "/x/a.log",
		Rendered: []render.Part{{Type: "text", Value: "LINE-TWO"}}})
	out := m.snapshotScrollback()
	if len(out) != 2 {
		t.Fatalf("want 2 lines, got %d: %v", len(out), out)
	}
	if !strings.Contains(out[0], "LINE-ONE") || !strings.Contains(out[1], "LINE-TWO") {
		t.Errorf("snapshot = %v", out)
	}
	if !strings.HasPrefix(out[0], "[g] a.log: ") {
		t.Errorf("head line missing prefix: %q", out[0])
	}
}

func TestSnapshotViewportMatchesVisible(t *testing.T) {
	m := newModel(100)
	m.height = 4 // contentHeight = 2 rows visible
	m.width = 80
	for i := 0; i < 10; i++ {
		m.appendEvent(render.Event{Group: "g", File: "/x/a.log",
			Rendered: []render.Part{{Type: "text", Value: "ROW"}}})
	}
	out := m.snapshotViewport()
	if len(out) != m.contentHeight() {
		t.Fatalf("viewport snapshot = %d lines, want contentHeight %d", len(out), m.contentHeight())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run 'TestPlainExportLine|TestSnapshot' ./internal/tui/`
Expected: FAIL — `plainExportLine`, `snapshotScrollback`, `snapshotViewport` undefined (compile error).

- [ ] **Step 3: Write the implementation**

Create `internal/tui/save.go`:

```go
package tui

// plainExportLine renders one displayLine to plain (unstyled) export text.
// Head lines always carry the "[group] file: " prefix — even when the on-screen
// group/file columns are toggled off — because the export is a complete record,
// not a WYSIWYG screenshot. Continuation / JSON / XML block rows carry no prefix
// and keep their own indentation, with styling stripped.
func plainExportLine(dl displayLine) string {
	if dl.isBlock {
		return stripANSI(dl.body)
	}
	return "[" + dl.group + "] " + dl.file + ": " + stripANSI(dl.body)
}

// snapshotViewport returns the rows currently visible on screen as plain text —
// honoring browse position, group disable, collapse, and filter mode (via
// collectVisible), minus styling, plus full prefixes.
func (m *model) snapshotViewport() []string {
	idxs := m.collectVisible(m.contentHeight())
	out := make([]string, 0, len(idxs))
	for _, i := range idxs {
		out = append(out, plainExportLine(m.lines[i]))
	}
	return out
}

// snapshotScrollback returns the entire buffer as plain text, in order,
// ignoring transient view toggles (collapse/filter) and group enable/disable.
// "Save scrollback" means the whole buffer, period.
func (m *model) snapshotScrollback() []string {
	out := make([]string, 0, len(m.lines))
	for i := range m.lines {
		out = append(out, plainExportLine(m.lines[i]))
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -run 'TestPlainExportLine|TestSnapshot' ./internal/tui/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/save.go internal/tui/save_test.go
git commit -m "feat(tui): plain-text export + viewport/scrollback snapshots"
```

---

## Task 3: File write with timestamped, collision-safe naming

**Files:**
- Modify: `internal/tui/save.go`
- Test: `internal/tui/save_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/tui/save_test.go`:

```go
func TestWriteExportNamingAndContent(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 7, 1, 33, 55, 0, time.UTC)

	p1, err := writeExport(dir, []string{"a", "b"}, now)
	if err != nil {
		t.Fatalf("writeExport: %v", err)
	}
	if base := filepath.Base(p1); base != "screen-log-listener-20260607-013355.txt" {
		t.Errorf("base name = %q", base)
	}
	got, _ := os.ReadFile(p1)
	if string(got) != "a\nb\n" {
		t.Errorf("content = %q, want trailing newline", string(got))
	}

	// Same second → numeric suffix, no overwrite.
	p2, err := writeExport(dir, []string{"c"}, now)
	if err != nil {
		t.Fatalf("writeExport 2: %v", err)
	}
	if base := filepath.Base(p2); base != "screen-log-listener-20260607-013355-1.txt" {
		t.Errorf("collision base name = %q", base)
	}
	if first, _ := os.ReadFile(p1); string(first) != "a\nb\n" {
		t.Errorf("first file was clobbered: %q", string(first))
	}
}
```

Add the imports this test needs to the existing import block of `internal/tui/save_test.go`:

```go
import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/homeend/log-listener/internal/render"
)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestWriteExportNamingAndContent ./internal/tui/`
Expected: FAIL — `writeExport` undefined (compile error).

- [ ] **Step 3: Write the implementation**

Append to `internal/tui/save.go`. Add to its import block:

```go
import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)
```

Then the functions:

```go
// writeExport writes lines to screen-log-listener-<timestamp>.txt in dir (the
// current working directory when dir == ""), never overwriting an existing
// file: on a name clash it appends -1, -2, … before the extension. Returns the
// final path. now is injected so the name is deterministic in tests.
func writeExport(dir string, lines []string, now time.Time) (string, error) {
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		dir = wd
	}
	base := "screen-log-listener-" + now.Format("20060102-150405")
	path := filepath.Join(dir, base+".txt")
	for i := 1; ; i++ {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			break
		}
		path = filepath.Join(dir, fmt.Sprintf("%s-%d.txt", base, i))
	}
	content := strings.Join(lines, "\n")
	if len(lines) > 0 {
		content += "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return path, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -run TestWriteExportNamingAndContent ./internal/tui/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/save.go internal/tui/save_test.go
git commit -m "feat(tui): timestamped, collision-safe export file writer"
```

---

## Task 4: Wire into the model — fields, dispatch, flash, footer

**Files:**
- Modify: `internal/tui/save.go` (add `saveResultMsg` + `saveCmd`)
- Modify: `internal/tui/app.go`
- Test: `internal/tui/save_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/tui/save_test.go`:

```go
func TestSaveKeyWritesFileAndFlashes(t *testing.T) {
	m := newModel(100)
	m.saveDir = t.TempDir()
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = m2.(*model)
	m.appendEvent(render.Event{Group: "g", File: "/x/a.log",
		Rendered: []render.Part{{Type: "text", Value: "SAVED-ROW"}}})

	// Press S (save scrollback) → a non-nil command.
	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
	m = m2.(*model)
	if cmd == nil {
		t.Fatal("save key should return a tea.Cmd")
	}

	// Run the command → a saveResultMsg; feed it back.
	msg := cmd()
	res, ok := msg.(saveResultMsg)
	if !ok {
		t.Fatalf("cmd produced %T, want saveResultMsg", msg)
	}
	if res.err != nil {
		t.Fatalf("save failed: %v", res.err)
	}
	m2, _ = m.Update(res)
	m = m2.(*model)
	if !strings.Contains(m.flash, "saved") {
		t.Errorf("flash = %q, want a 'saved …' message", m.flash)
	}
	if !strings.Contains(m.renderFooter(), "saved") {
		t.Errorf("footer should show the flash: %q", m.renderFooter())
	}

	// The written file exists and holds the row.
	got, _ := os.ReadFile(res.path)
	if !strings.Contains(string(got), "SAVED-ROW") {
		t.Errorf("file content = %q", string(got))
	}

	// Any next key clears the flash.
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m = m2.(*model)
	if m.flash != "" {
		t.Errorf("flash should clear on next key, got %q", m.flash)
	}
}
```

Add `tea "github.com/charmbracelet/bubbletea"` to the `internal/tui/save_test.go` import block:

```go
import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/render"
)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestSaveKeyWritesFileAndFlashes ./internal/tui/`
Expected: FAIL — `saveResultMsg` undefined and `model` has no `flash`/`saveDir` fields (compile error).

- [ ] **Step 3: Add the message + command to `save.go`**

Append to `internal/tui/save.go` (add `tea "github.com/charmbracelet/bubbletea"` to its import block):

```go
// saveResultMsg reports the outcome of a background export write.
type saveResultMsg struct {
	path string
	n    int
	err  error
}

// saveCmd captures the already-computed export lines and writes them off the
// model goroutine, yielding a saveResultMsg. The snapshot is taken by the
// caller (in Update) because m.lines is owned by the model goroutine.
func (m *model) saveCmd(lines []string) tea.Cmd {
	dir := m.saveDir
	return func() tea.Msg {
		path, err := writeExport(dir, lines, time.Now())
		return saveResultMsg{path: path, n: len(lines), err: err}
	}
}
```

- [ ] **Step 4: Add the model fields**

In `internal/tui/app.go`, add two fields to the `model` struct (e.g. just after `collapseMultiline bool`):

```go
	// flash is a transient status line (e.g. a save confirmation) shown in the
	// footer until the next key event. saveDir overrides the export directory
	// (default "" = cwd); it is a test seam, never set in production.
	flash   string
	saveDir string
```

- [ ] **Step 5: Clear flash on key events + dispatch the save actions**

In `internal/tui/app.go`, inside `Update`, at the very top of `case tea.KeyMsg:` (before the `m.searchInput` / `m.wrapPrompt` modal checks), clear the flash:

```go
	case tea.KeyMsg:
		// Any keypress dismisses a transient flash message.
		m.flash = ""
		// Modal key paths take priority — search input swallows almost
		// everything, and a pending wrap prompt swallows y/n/Esc before
		// the normal dispatcher sees them.
		if m.searchInput {
			return m.handleSearchInputKey(msg), nil
		}
```

Then add two cases to the `switch action {` block (e.g. after `case keymap.ActionResetHoriz:`):

```go
		case keymap.ActionSaveViewport:
			return m, m.saveCmd(m.snapshotViewport())
		case keymap.ActionSaveScrollback:
			return m, m.saveCmd(m.snapshotScrollback())
```

- [ ] **Step 6: Handle saveResultMsg in Update**

In `internal/tui/app.go`, add a case to the outer `switch msg := msg.(type)` in `Update` (e.g. after `case QuitMsg:`):

```go
	case saveResultMsg:
		if msg.err != nil {
			m.flash = "save failed: " + msg.err.Error()
		} else {
			m.flash = fmt.Sprintf("saved %d lines to %s", msg.n, msg.path)
		}
```

(`fmt` is already imported in `app.go`.)

- [ ] **Step 7: Show flash in the footer**

In `internal/tui/app.go`, in `renderFooter`, add a branch after the `m.wrapPrompt` block and before the normal-status assembly:

```go
	if m.wrapPrompt != 0 {
		text := " No more hits — wrap to top? (y/n) "
		if m.wrapPrompt == 'p' {
			text = " No more hits — wrap to bottom? (y/n) "
		}
		return headerBg.Width(m.width).MaxHeight(1).Render(text)
	}
	if m.flash != "" {
		return headerBg.Width(m.width).MaxHeight(1).Render(" " + m.flash + " ")
	}
```

- [ ] **Step 8: Run test to verify it passes**

Run: `go test -run TestSaveKeyWritesFileAndFlashes ./internal/tui/`
Expected: PASS.

- [ ] **Step 9: Run the full TUI + keymap suites**

Run: `go test ./internal/tui/ ./internal/keymap/`
Expected: PASS (no regressions).

- [ ] **Step 10: Commit**

```bash
git add internal/tui/save.go internal/tui/app.go internal/tui/save_test.go
git commit -m "feat(tui): wire s/S save keys, file write, footer flash"
```

---

## Task 5: Documentation + full quality gate

**Files:**
- Modify: `README.md`
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Add the two keys to the README keybindings table**

In `README.md`, in the `### Keybindings` table, add two rows immediately after the `**`m`**` collapse-multiline row:

```markdown
| **`s`**             | **Save the visible viewport to a `screen-log-listener-*.txt` file (cwd).** |
| **`S`**             | **Save the full scrollback buffer to a `screen-log-listener-*.txt` file (cwd).** |
```

- [ ] **Step 2: Add a CHANGELOG entry**

In `CHANGELOG.md`, under `## [Unreleased]`, add a new section (above "OS-aware keybindings"):

```markdown
### Save view to a text file
- **`s`** writes the currently visible rows, and **`S`** writes the entire
  scrollback buffer, to a timestamped `screen-log-listener-<ts>.txt` file in the
  working directory (numeric suffix on same-second collisions). Output is plain
  text: ANSI stripped, full `[group] file:` prefixes kept regardless of column
  toggles. A footer message confirms the path (or reports a write error) until
  the next keypress. Both keys are remappable via the `keybindings:` block.
```

- [ ] **Step 3: Verify the keybindings doc is current**

Run: `./build.sh keybindings-docs && git diff --exit-code KEYBINDINGS.md`
Expected: exit 0 (no diff — the doc was already regenerated in Task 1).

- [ ] **Step 4: Run the full quality gate**

Run: `go vet ./... && go test ./... && go test -race ./...`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add README.md CHANGELOG.md
git commit -m "docs: document s/S save-to-file keys"
```

---

## Self-Review (completed during planning)

- **Spec coverage:** two actions through keymap (Task 1) ✓; plain-text format with full prefixes + both snapshots (Task 2) ✓; `tea.Cmd` write, `screen-` prefix, timestamp, collision suffix, trailing newline, `saveDir` seam (Task 3) ✓; `flash` field, clear-on-key, dispatch, `saveResultMsg`, footer branch (Task 4) ✓; README + CHANGELOG + KEYBINDINGS.md + quality gate (Tasks 1 & 5) ✓. Forward-reuse note (snapshots → MCP) is satisfied by keeping them as pure `[]string` model methods (Task 2).
- **Placeholder scan:** none — every code step shows complete code.
- **Type consistency:** `plainExportLine(displayLine) string`, `snapshotViewport()/snapshotScrollback() []string`, `writeExport(dir string, lines []string, now time.Time) (string, error)`, `saveResultMsg{path string; n int; err error}`, `saveCmd(lines []string) tea.Cmd`, model fields `flash string` / `saveDir string` — names and signatures are consistent across Tasks 2–4 and the tests.

## Out of Scope (YAGNI)

Interactive filename/dir prompts, embedding line IDs (arrive with feature #1), styled/HTML/JSON export, append mode, an always-on footer hint for the keys (discoverable via `KEYBINDINGS.md`).
