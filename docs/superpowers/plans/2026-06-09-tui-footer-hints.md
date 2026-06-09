# Context-Sensitive Footer Hints Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the TUI bottom bar show the keys most useful at the current moment (visual/block/search/browse/tail) on the left, with a compact live-status tail on the right.

**Architecture:** A new `internal/tui/footerhints.go` owns three pure-ish methods — `contextHints()` (precedence selector → label + ordered hint strings), `compactStatus()` (abbreviated right-hand status), and `composeFooterBar()` (width-fit + right-align). `renderFooter` in `view.go` is rewired to call them, replacing both the existing `visualMode` early-return and the normal status block. The full-width takeover bars (search-input, wrap-prompt, flash) are unchanged and still checked first. All keys render through the existing `m.keyDisplay`, so per-OS forms and YAML overrides are honored.

**Tech Stack:** Go 1.26, bubbletea, lipgloss, go-runewidth (via existing `dispWidth`). Tests via `go test`.

---

## Spec

Authoritative design: `docs/superpowers/specs/2026-06-09-tui-footer-hints-design.md`.

Precedence (first match wins): **VISUAL > BLOCK > SEARCH/FILTER > BROWSE > tail(default)**.

## Reference: existing code this plan touches

- `internal/tui/view.go`:
  - `func (m *model) hint(a keymap.Action, label string) string` — returns `keyDisplay(a) + " " + label`. **Reuse it.**
  - `func (m *model) keyDisplay(a keymap.Action) string` — per-OS glyph, nil-safe.
  - `func (m *model) renderFooter() string` — the function rewired in Task 4. Today it checks (in order): `visualMode` → full hint bar; `searchInput` → `/typed_`; `wrapPrompt` → y/n; `flash` → message; else status counters.
  - package vars `headerBg` (bg fill, used by mode bars) and `dimStyle` (faint, used by the normal status line).
- `internal/tui/width.go`: `func dispWidth(s string) int` — ANSI-aware display width.
- `internal/tui/viewanchor.go`: `func (m *model) streamTopRow() int`.
- Model state fields (`internal/tui/app.go`): `visualMode bool`, `blockFocused bool`, `matcher *searchmatch.Matcher`, `filterMode bool`, `tailMode bool`, `searchQuery string`, `searchInput bool`, `wrapPrompt rune`, `flash string`, `width int`, `lines []displayLine`, `km *keymap.Keymap`.
- Keymap actions (all already exist in `internal/keymap/actions.go`): `ActionSearch, ActionNextMatch, ActionPrevMatch, ActionFilter, ActionNextBlock, ActionPrevBlock, ActionNextMarkedBlock, ActionPrevMarkedBlock, ActionToggleExceptionMarks, ActionCopyReference, ActionCopyText, ActionSaveViewport, ActionVisualSelect, ActionCollapseAll, ActionCloseOverlay, ActionTop, ActionBottom, ActionScrollUp, ActionScrollDown, ActionPageUp, ActionPageDown, ActionHelp`.
- Test helpers: `newModel(scrollback int) *model`; set window via `m.Update(tea.WindowSizeMsg{Width: W, Height: H})` (returns `tea.Model`, assert `.(*model)`); set search via `m.matcher, _ = searchmatch.Compile("term", false)` (`import "github.com/homeend/log-listener/internal/searchmatch"`); deterministic glyphs via `m.km = keymap.Default("linux")`.

**No changes** to `internal/keymap`, `internal/config`, or any file outside `internal/tui`. No new keybindings.

---

### Task 1: Compact status tail

**Files:**
- Create: `internal/tui/footerhints.go`
- Test: `internal/tui/footerhints_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/tui/footerhints_test.go`:

```go
package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/keymap"
	"github.com/homeend/log-listener/internal/searchmatch"
)

func newFooterModel(t *testing.T) *model {
	t.Helper()
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 12})
	return m2.(*model)
}

func TestCompactStatusTailMode(t *testing.T) {
	m := newFooterModel(t)
	m.lines = make([]displayLine, 7)
	m.tailMode = true
	got := m.compactStatus()
	if !strings.Contains(got, "ev 7") || !strings.Contains(got, "tail") {
		t.Fatalf("compactStatus tail = %q, want ev 7 + tail", got)
	}
}

func TestCompactStatusBrowseWithSearch(t *testing.T) {
	m := newFooterModel(t)
	m.lines = make([]displayLine, 9)
	m.tailMode = false
	m.setStreamTopRow(3)
	m.matcher, _ = compileTestMatcher(t, "err")
	m.searchQuery = "err"
	got := m.compactStatus()
	if !strings.Contains(got, "ev 9") || !strings.Contains(got, "@3/9") || !strings.Contains(got, "/err") {
		t.Fatalf("compactStatus browse = %q, want ev 9 + @3/9 + /err", got)
	}
}
```

Add these helpers at the bottom of the same test file (the imports above already cover them):

```go
func compileTestMatcher(t *testing.T, q string) (*searchmatch.Matcher, error) {
	t.Helper()
	return searchmatch.Compile(q, false)
}

func keymapDefaultLinux() *keymap.Keymap { return keymap.Default("linux") }

func keymapResolveFilterF() (*keymap.Keymap, error) {
	return keymap.Resolve("linux", map[string][]string{"filter": {"F"}}, nil)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run TestCompactStatus -v`
Expected: FAIL — `m.compactStatus undefined`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/tui/footerhints.go`:

```go
package tui

import "fmt"

// compactStatus is the abbreviated right-hand tail of the bottom bar: event
// count, scroll position (tail or @top/total), and the committed search term.
func (m *model) compactStatus() string {
	pos := "tail"
	if !m.tailMode {
		pos = fmt.Sprintf("@%d/%d", m.streamTopRow(), len(m.lines))
	}
	s := fmt.Sprintf("ev %d · %s", len(m.lines), pos)
	if m.matcher != nil {
		s += " · /" + m.searchQuery
	}
	return s
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/ -run TestCompactStatus -v`
Expected: PASS (both subtests).

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/tui/footerhints.go internal/tui/footerhints_test.go
git add internal/tui/footerhints.go internal/tui/footerhints_test.go
git commit -m "feat(tui): compact footer status tail (ev/pos/term)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 2: Context hint sets + precedence

**Files:**
- Modify: `internal/tui/footerhints.go`
- Test: `internal/tui/footerhints_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/tui/footerhints_test.go`:

```go
func TestContextHintsDefaultTail(t *testing.T) {
	m := newFooterModel(t)
	m.km = keymapDefaultLinux()
	m.tailMode = true
	label, hints := m.contextHints()
	if label != "" {
		t.Fatalf("default label = %q, want empty", label)
	}
	joined := strings.Join(hints, " | ")
	for _, want := range []string{"search", "select", "blocks", "collapse", "help"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("default hints %q missing %q", joined, want)
		}
	}
}

func TestContextHintsBrowse(t *testing.T) {
	m := newFooterModel(t)
	m.km = keymapDefaultLinux()
	m.tailMode = false
	label, hints := m.contextHints()
	if label != "BROWSE" {
		t.Fatalf("label = %q, want BROWSE", label)
	}
	joined := strings.Join(hints, " | ")
	for _, want := range []string{"tail", "top", "scroll", "page", "select"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("browse hints %q missing %q", joined, want)
		}
	}
}

func TestContextHintsSearch(t *testing.T) {
	m := newFooterModel(t)
	m.km = keymapDefaultLinux()
	m.tailMode = false
	m.matcher, _ = compileTestMatcher(t, "x")
	label, hints := m.contextHints()
	if label != "SEARCH" {
		t.Fatalf("label = %q, want SEARCH", label)
	}
	joined := strings.Join(hints, " | ")
	for _, want := range []string{"next·prev", "filter", "blocks", "clear"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("search hints %q missing %q", joined, want)
		}
	}
	if strings.Contains(joined, "unfilter") {
		t.Fatalf("search (not filter) should say filter, not unfilter: %q", joined)
	}
}

func TestContextHintsFilterVariant(t *testing.T) {
	m := newFooterModel(t)
	m.km = keymapDefaultLinux()
	m.matcher, _ = compileTestMatcher(t, "x")
	m.filterMode = true
	label, hints := m.contextHints()
	if label != "FILTER" {
		t.Fatalf("label = %q, want FILTER", label)
	}
	if !strings.Contains(strings.Join(hints, " | "), "unfilter") {
		t.Fatalf("filter variant should say unfilter: %v", hints)
	}
}

func TestContextHintsBlock(t *testing.T) {
	m := newFooterModel(t)
	m.km = keymapDefaultLinux()
	m.blockFocused = true
	label, hints := m.contextHints()
	if label != "BLOCK" {
		t.Fatalf("label = %q, want BLOCK", label)
	}
	for _, want := range []string{"next·prev", "marked", "marks", "copy"} {
		if !strings.Contains(strings.Join(hints, " | "), want) {
			t.Fatalf("block hints %v missing %q", hints, want)
		}
	}
}

func TestContextHintsVisual(t *testing.T) {
	m := newFooterModel(t)
	m.km = keymapDefaultLinux()
	m.visualMode = true
	label, hints := m.contextHints()
	if label != "VISUAL" {
		t.Fatalf("label = %q, want VISUAL", label)
	}
	for _, want := range []string{"space anchor", "ref", "text", "save", "cancel"} {
		if !strings.Contains(strings.Join(hints, " | "), want) {
			t.Fatalf("visual hints %v missing %q", hints, want)
		}
	}
}

func TestContextHintsPrecedence(t *testing.T) {
	// All overlapping states on at once: visual must win.
	m := newFooterModel(t)
	m.km = keymapDefaultLinux()
	m.visualMode = true
	m.blockFocused = true
	m.matcher, _ = compileTestMatcher(t, "x")
	m.tailMode = false
	if label, _ := m.contextHints(); label != "VISUAL" {
		t.Fatalf("visual should win, got %q", label)
	}
	// Block beats search+browse.
	m.visualMode = false
	if label, _ := m.contextHints(); label != "BLOCK" {
		t.Fatalf("block should win, got %q", label)
	}
	// Search beats browse.
	m.blockFocused = false
	if label, _ := m.contextHints(); label != "SEARCH" {
		t.Fatalf("search should win, got %q", label)
	}
	// Browse beats tail.
	m.matcher = nil
	if label, _ := m.contextHints(); label != "BROWSE" {
		t.Fatalf("browse should win, got %q", label)
	}
}

func TestContextHintsOverrideReflected(t *testing.T) {
	// Remap Filter to "F"; the SEARCH hint must show the new glyph.
	km, err := keymapResolveFilterF()
	if err != nil {
		t.Fatal(err)
	}
	m := newFooterModel(t)
	m.km = km
	m.matcher, _ = compileTestMatcher(t, "x")
	_, hints := m.contextHints()
	joined := strings.Join(hints, " | ")
	if !strings.Contains(joined, "F filter") {
		t.Fatalf("override not reflected, hints = %q", joined)
	}
}
```

(`keymapDefaultLinux`, `keymapResolveFilterF`, and `compileTestMatcher` were already added to the test file in Task 1.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run TestContextHints -v`
Expected: FAIL — `m.contextHints undefined`.

- [ ] **Step 3: Write minimal implementation**

Add to `internal/tui/footerhints.go` (add `"github.com/homeend/log-listener/internal/keymap"` to its imports):

```go
// hintPair renders "<key1>·<key2> <label>" for a forward/back action pair.
func (m *model) hintPair(a1, a2 keymap.Action, label string) string {
	return m.keyDisplay(a1) + "·" + m.keyDisplay(a2) + " " + label
}

// contextHints selects the active context by precedence (visual > block >
// search/filter > browse > tail) and returns its short uppercase label
// (empty for the default tail context) plus the ordered hint strings. Every
// key is resolved through keyDisplay, so per-OS forms and overrides apply.
func (m *model) contextHints() (label string, hints []string) {
	switch {
	case m.visualMode:
		return "VISUAL", []string{
			"space anchor",
			m.hint(keymap.ActionCopyReference, "ref"),
			m.hint(keymap.ActionCopyText, "text"),
			m.hint(keymap.ActionSaveViewport, "save"),
			m.hint(keymap.ActionCloseOverlay, "cancel"),
		}
	case m.blockFocused:
		return "BLOCK", []string{
			m.hintPair(keymap.ActionNextBlock, keymap.ActionPrevBlock, "next·prev"),
			m.hintPair(keymap.ActionNextMarkedBlock, keymap.ActionPrevMarkedBlock, "marked"),
			m.hint(keymap.ActionToggleExceptionMarks, "marks"),
			m.hint(keymap.ActionCopyReference, "copy"),
			m.hint(keymap.ActionCloseOverlay, "esc"),
		}
	case m.matcher != nil:
		word, lbl := "filter", "SEARCH"
		if m.filterMode {
			word, lbl = "unfilter", "FILTER"
		}
		return lbl, []string{
			m.hintPair(keymap.ActionNextMatch, keymap.ActionPrevMatch, "next·prev"),
			m.hint(keymap.ActionFilter, word),
			m.hintPair(keymap.ActionNextBlock, keymap.ActionPrevBlock, "blocks"),
			m.hint(keymap.ActionCloseOverlay, "clear"),
		}
	case !m.tailMode:
		return "BROWSE", []string{
			m.hint(keymap.ActionBottom, "tail"),
			m.hint(keymap.ActionTop, "top"),
			m.hintPair(keymap.ActionScrollUp, keymap.ActionScrollDown, "scroll"),
			m.hintPair(keymap.ActionPageUp, keymap.ActionPageDown, "page"),
			m.hint(keymap.ActionVisualSelect, "select"),
		}
	default:
		return "", []string{
			m.hint(keymap.ActionSearch, "search"),
			m.hint(keymap.ActionVisualSelect, "select"),
			m.hintPair(keymap.ActionNextBlock, keymap.ActionPrevBlock, "blocks"),
			m.hint(keymap.ActionCollapseAll, "collapse"),
			m.hint(keymap.ActionHelp, "help"),
		}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/ -run TestContextHints -v`
Expected: PASS (all subtests).

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/tui/footerhints.go internal/tui/footerhints_test.go
git add internal/tui/footerhints.go internal/tui/footerhints_test.go
git commit -m "feat(tui): context hint sets + precedence selector

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 3: Compose + width-fit the bottom bar

**Files:**
- Modify: `internal/tui/footerhints.go`
- Test: `internal/tui/footerhints_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/tui/footerhints_test.go`:

```go
func TestComposeFooterBarFitsWidth(t *testing.T) {
	m := newFooterModel(t)
	m.km = keymapDefaultLinux()
	m.width = 120
	m.lines = make([]displayLine, 5)
	bar := m.composeFooterBar(m.contextHints())
	if dispWidth(bar) != 120 {
		t.Fatalf("bar width = %d, want 120\n%q", dispWidth(bar), bar)
	}
	if !strings.Contains(stripANSI(bar), "ev 5") {
		t.Fatalf("bar missing status tail: %q", stripANSI(bar))
	}
}

func TestComposeFooterBarNarrowTruncates(t *testing.T) {
	m := newFooterModel(t)
	m.km = keymapDefaultLinux()
	m.width = 34 // too narrow for the full default hint list + status
	m.lines = make([]displayLine, 5)
	bar := m.composeFooterBar(m.contextHints())
	plain := stripANSI(bar)
	if dispWidth(bar) != 34 {
		t.Fatalf("narrow bar width = %d, want 34\n%q", dispWidth(bar), plain)
	}
	if !strings.Contains(plain, "…") {
		t.Fatalf("narrow bar should drop low-priority hints with an ellipsis: %q", plain)
	}
	if !strings.Contains(plain, "ev 5") {
		t.Fatalf("narrow bar must keep the status tail: %q", plain)
	}
	// Highest-priority hint (search) survives; lowest (help) is dropped.
	if !strings.Contains(plain, "search") {
		t.Fatalf("narrow bar should keep the top-priority hint: %q", plain)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run TestComposeFooterBar -v`
Expected: FAIL — `m.composeFooterBar undefined`.

- [ ] **Step 3: Write minimal implementation**

Add to `internal/tui/footerhints.go` (add `"strings"` to its imports):

```go
// fitHints renders the left side (optional label + hints joined by " · ")
// within budget display columns. When the full list overflows it drops hint
// entries from the right end (lowest priority) and appends "…" until it fits;
// the label is always kept.
func fitHints(label string, hints []string, budget int) string {
	prefix := ""
	if label != "" {
		prefix = label + "  "
	}
	full := prefix + strings.Join(hints, " · ")
	if dispWidth(full) <= budget {
		return full
	}
	for n := len(hints) - 1; n >= 1; n-- {
		s := prefix + strings.Join(hints[:n], " · ") + " · …"
		if dispWidth(s) <= budget {
			return s
		}
	}
	return prefix + "…"
}

// composeFooterBar lays out the bottom bar: fitted context hints on the left,
// the compact status tail right-aligned to m.width. Mode contexts (non-empty
// label) use the headerBg fill, matching the existing visual-mode bar; the
// default tail context uses dimStyle, matching the old status line.
func (m *model) composeFooterBar(label string, hints []string) string {
	right := m.compactStatus()
	style := dimStyle
	if label != "" {
		style = headerBg
	}
	if m.width <= 0 {
		return style.MaxHeight(1).Render(" " + strings.Join(hints, " · ") + " ")
	}
	// Reserve: 1 leading space + 1 trailing space + at least 1 gap + status.
	budget := m.width - dispWidth(right) - 3
	if budget < 0 {
		budget = 0
	}
	left := fitHints(label, hints, budget)
	gap := m.width - dispWidth(left) - dispWidth(right) - 2 // 1 lead + 1 trail
	if gap < 1 {
		gap = 1
	}
	text := " " + left + strings.Repeat(" ", gap) + right + " "
	return style.Width(m.width).MaxHeight(1).Render(text)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/ -run TestComposeFooterBar -v`
Expected: PASS (both subtests).

If the width-120 assertion is off by lipgloss padding behavior, confirm `style.Width(m.width)` pads/clamps to exactly `m.width`; the `text` is already built to `m.width` columns so `.Width` is a no-op pad. Do not add a second pad.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/tui/footerhints.go internal/tui/footerhints_test.go
git add internal/tui/footerhints.go internal/tui/footerhints_test.go
git commit -m "feat(tui): compose + width-fit the context footer bar

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 4: Rewire renderFooter

**Files:**
- Modify: `internal/tui/view.go` (`renderFooter`, currently lines ~97-154)
- Test: `internal/tui/footerhints_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/tui/footerhints_test.go`:

```go
func TestRenderFooterUsesContextHints(t *testing.T) {
	m := newFooterModel(t)
	m.km = keymapDefaultLinux()
	m.lines = make([]displayLine, 3)

	// Default tail: shows default hints + status, no mode label.
	plain := stripANSI(m.renderFooter())
	if !strings.Contains(plain, "search") || !strings.Contains(plain, "ev 3") {
		t.Fatalf("tail footer = %q, want default hints + status", plain)
	}

	// Visual mode: VISUAL label + its hints.
	m.visualMode = true
	plain = stripANSI(m.renderFooter())
	if !strings.Contains(plain, "VISUAL") || !strings.Contains(plain, "save") {
		t.Fatalf("visual footer = %q, want VISUAL hints", plain)
	}
	m.visualMode = false

	// Takeover bars still win: typing a search query.
	m.searchInput = true
	m.searchQuery = "abc"
	plain = stripANSI(m.renderFooter())
	if !strings.Contains(plain, "/abc") || strings.Contains(plain, "ev 3") {
		t.Fatalf("search-input footer = %q, want /abc takeover (no status)", plain)
	}
	m.searchInput = false

	// Flash still takes over.
	m.flash = "copied 2 lines"
	plain = stripANSI(m.renderFooter())
	if !strings.Contains(plain, "copied 2 lines") {
		t.Fatalf("flash footer = %q, want flash message", plain)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run TestRenderFooterUsesContextHints -v`
Expected: FAIL — current `renderFooter` shows `events: 3 …` (not `ev 3`) and visual returns the old hard-coded string without going through the new path; the `ev 3` / VISUAL-label assertions fail. (If it happens to pass on some assertions, the test still fails overall because the tail branch emits `events:` not the new `ev` status — verify it is RED before continuing.)

- [ ] **Step 3: Replace the implementation**

In `internal/tui/view.go`, replace the entire body of `renderFooter` (the `visualMode` early-return through the final `return dimStyle...` status block) with this. Keep the doc comment above it accurate by replacing it too:

```go
// renderFooter assembles the bottom status bar. Full-width takeover bars are
// checked first (search input, wrap prompt, transient flash); otherwise the
// bar is context-driven hints on the left + a compact status tail on the
// right (see footerhints.go).
func (m *model) renderFooter() string {
	if m.searchInput {
		prefix := " /"
		if m.searchRegex {
			prefix = " /(regex) "
		}
		return headerBg.Width(m.width).MaxHeight(1).Render(prefix + m.searchQuery + "_")
	}
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
	return m.composeFooterBar(m.contextHints())
}
```

This removes the now-unused status-counter code (`pos`, `cols`, `groupStat`, `rendStat`, `search`, `colStat` locals) from `renderFooter`. Leave `disabledGroupCount` and `disabledRendererCount` in place — they are still used by the side panels.

- [ ] **Step 4: Run the test + the whole suite + gates**

Run: `go test ./internal/tui/ -run TestRenderFooterUsesContextHints -v`
Expected: PASS.

Run: `go build ./... && go vet ./...`
Expected: clean. If `go vet` flags `disabledGroupCount`/`disabledRendererCount` or any removed local as unused, confirm whether those funcs are still referenced (`grep -rn "disabledGroupCount\|disabledRendererCount" internal/tui`). They are used by `renderGroupsPanel`/`renderRenderersPanel`, so they stay; do not delete them.

Run: `go test ./...`
Expected: PASS. Some existing footer tests may assert the old `events: N` text — search for them: `grep -rn "events: \|col: \|· rend: " internal/tui/*_test.go`. If any assert on the old footer string, update those assertions to the new compact form (`ev N`, `@top/total`) — the change is intentional and approved in the spec. Update them to match; do not revert the feature.

Run: `go test -race ./internal/tui/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/tui/view.go internal/tui/footerhints_test.go
git add internal/tui/view.go internal/tui/footerhints_test.go
git commit -m "feat(tui): rewire renderFooter to context-sensitive hint bar

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 5: Docs

**Files:**
- Modify: `README.md`, `CHANGELOG.md`

- [ ] **Step 1: Update CHANGELOG**

Add an entry under the current unreleased/top section of `CHANGELOG.md` (match the existing bullet style):

```markdown
- TUI bottom bar is now context-sensitive: it shows the keys most useful at
  the moment (visual/block/search/browse/tail) on the left, with a compact
  `ev N · @pos · /term` status tail on the right. Keys respect per-OS forms
  and keybinding overrides.
```

- [ ] **Step 2: Update README**

In `README.md`, find the TUI keys/footer description (search: `grep -n "footer\|status line\|events:" README.md`). Add or adjust a sentence near the TUI usage section:

```markdown
The bottom bar adapts to what you're doing — it surfaces the most relevant
keys for the current mode (visual selection, block focus, active search,
browsing, or live tail) and shows a compact event/position/search-term status
on the right.
```

If no existing footer description exists, add the sentence at the end of the TUI section's intro paragraph.

- [ ] **Step 3: Verify docs build / gates still green**

Run: `go test ./...`
Expected: PASS (docs-only change, but run to be safe — `TestDocsUpToDate` guards KEYBINDINGS.md, which is unaffected since no keybindings changed).

- [ ] **Step 4: Commit**

```bash
git add README.md CHANGELOG.md
git commit -m "docs: context-sensitive TUI footer hints (README + CHANGELOG)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Self-Review notes

- **Spec coverage:** layout (Task 3/4), 5 contexts + precedence (Task 2), compact status (Task 1), width-fit truncation (Task 3), takeover bars preserved (Task 4), override reflection via keyDisplay (Task 2 test), tests enumerated in spec all mapped (Tasks 1-4), docs at delivery (Task 5). ✓
- **Type consistency:** `contextHints() (string, []string)`, `compactStatus() string`, `composeFooterBar(label string, hints []string) string`, `fitHints(label string, hints []string, budget int) string`, `hintPair(a1, a2 keymap.Action, label string) string` — names consistent across all tasks. `composeFooterBar(m.contextHints())` relies on Go's multi-value pass-through (legal: one func returns exactly the two args the other takes). ✓
- **No new deps, no keymap/config edits, no new keybindings.** ✓
