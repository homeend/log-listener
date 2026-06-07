# Block Annotation + Render Plugins Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Detect exception/stack-trace blocks across common languages, mark them with a left bar in the TUI, and let the user jump between blocks — built on a neutral `internal/blocks` package the future MCP server can reuse.

**Architecture:** A dependency-free `internal/blocks` package segments a neutral line slice into blocks (indentation + a small signature set) and runs annotate-only processors (one: exception detection). The TUI caches blocks (dirty-flagged, one recompute path), draws a width-accounted column-0 bar on exception blocks via a toggleable render plugin, and adds two block-navigation key sets through the existing keymap.

**Tech Stack:** Go 1.26, `strings`/`regexp`, bubbletea TUI, the existing `internal/keymap` action table.

**Spec:** `docs/superpowers/specs/2026-06-07-block-annotation-design.md`

---

## Research Findings (verified — drives the concrete code below)

Continuation signatures (non-indented lines that still continue a block; tab/4-space-indented frames are already caught by the whitespace rule and are deliberately excluded):

| Signature (line start) | Language | Note |
|------------------------|----------|------|
| `Caused by:`           | Java/Kotlin | chained causes |
| `goroutine `           | Go | header after `panic:` |
| `#<digits> `           | PHP | stack frames `#0 /path(line): fn()` |

Exception detection markers (set `Exception.Language`):

| Language | Distinctive marker |
|----------|--------------------|
| python | line starts `Traceback (most recent call last):` |
| go | line starts `panic:` or `goroutine `, or contains `runtime error:` |
| rust | line starts `thread '` and contains `panicked at` (format changed in 1.73; substring stable) |
| c/c++ | line contains `AddressSanitizer:` or `terminate called after throwing an instance of` |
| php | line contains `PHP ` + (`Fatal error`/`Uncaught`), or starts `Stack trace:` |
| java | a frame `at …(File.java:NN)` (one colon) |
| kotlin | a frame `at …(File.kt:NN)` |
| javascript | a frame `at … :line:col` (two colons), non-`.ts` |
| typescript | a JS-shape frame whose path contains `.ts:` |

JS/TS share a runtime stack; we distinguish only by a `.ts:` path. Honest v1 limitations carry over from the spec (Go panics still fragment; heuristic, not a parser).

---

## File Structure

- `internal/blocks/blocks.go` (new) — `Line`, `Block`, `ExceptionInfo`, `IsWhitespaceCont`, `IsContinuation`, `Processor`, `Annotate`, `Segment`.
- `internal/blocks/exception.go` (new) — `exceptionProcessor` + language detection.
- `internal/blocks/blocks_test.go`, `internal/blocks/exception_test.go` (new).
- `internal/tui/blocks.go` (new) — `blockLines`, `ensureBlocks`, `inExceptionBlock`, `exceptionBar`, the four nav helpers.
- `internal/tui/app.go` — new model fields, dirty in the four mutators, bar in `renderStream`, action dispatch.
- `internal/tui/blocks_test.go` (new) — cache lifecycle, bar width, nav.
- `internal/keymap/{actions,defaults}.go` + tests; `KEYBINDINGS.md`.
- `README.md`, `CHANGELOG.md`.

---

## Task 1: blocks package — types, predicates, segmentation

**Files:** Create `internal/blocks/blocks.go`; Test `internal/blocks/blocks_test.go`.

- [ ] **Step 1: Write the failing test**

Create `internal/blocks/blocks_test.go`:

```go
package blocks

import (
	"reflect"
	"testing"
)

func lines(ss ...string) []Line {
	out := make([]Line, len(ss))
	for i, s := range ss {
		out[i] = Line{Text: s}
	}
	return out
}

func ranges(bs []Block) [][2]int {
	out := make([][2]int, len(bs))
	for i, b := range bs {
		out[i] = [2]int{b.Start, b.End}
	}
	return out
}

func TestSegmentWhitespaceContinuation(t *testing.T) {
	// Head + two indented frames = one block; then a new head.
	got := ranges(Segment(lines(
		"NullPointerException: boom",
		"\tat Foo.bar(Foo.java:1)",
		"    at Foo.baz(Foo.java:2)",
		"next normal line",
	)))
	want := [][2]int{{0, 2}, {3, 3}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ranges = %v, want %v", got, want)
	}
}

func TestSegmentSignatureContinuation(t *testing.T) {
	// Non-indented "Caused by:", "goroutine ", and PHP "#0 " continue a block.
	got := ranges(Segment(lines(
		"panic: boom",
		"goroutine 1 [running]:",
		"Caused by: other",
		"#0 /a.php(9): f()",
		"plain head",
	)))
	want := [][2]int{{0, 3}, {4, 4}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ranges = %v, want %v", got, want)
	}
}

func TestSegmentBareAtIsNotASignature(t *testing.T) {
	// A non-indented "at ..." line is NOT a continuation (own block).
	got := ranges(Segment(lines("head", "at 10:00 server started")))
	want := [][2]int{{0, 0}, {1, 1}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ranges = %v, want %v", got, want)
	}
}

func TestSegmentRenderBlockRowContinues(t *testing.T) {
	ls := []Line{{Text: "msg:"}, {Text: "{", IsRenderBlock: true}, {Text: "}", IsRenderBlock: true}}
	got := ranges(Segment(ls))
	want := [][2]int{{0, 2}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ranges = %v, want %v", got, want)
	}
}

func TestSegmentLeadingWhitespaceIsDegenerateHead(t *testing.T) {
	got := ranges(Segment(lines("  indented first", "plain")))
	want := [][2]int{{0, 0}, {1, 1}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ranges = %v, want %v", got, want)
	}
}
```

- [ ] **Step 2: Run, verify FAIL** (package/symbols undefined):
`go test ./internal/blocks/`

- [ ] **Step 3: Implement.** Create `internal/blocks/blocks.go`:

```go
// Package blocks groups a line stream into multi-line blocks (stack traces,
// pretty-printed JSON/XML, indented continuations) and runs annotate-only
// processors over them. Neutral and dependency-free so both the TUI and the
// MCP server can consume it.
package blocks

import "strings"

// Line is the neutral input: the plain (ANSI-stripped) text of one row, plus
// whether it is a render-block row (pretty-printed JSON/XML), which is always a
// continuation regardless of leading whitespace.
type Line struct {
	Text          string
	IsRenderBlock bool
}

// ExceptionInfo is the exception processor's annotation.
type ExceptionInfo struct {
	Language string // "java", "python", … or "" if unsure
}

// Block is a contiguous run of lines [Start, End] (inclusive indices into the
// Line slice it was segmented from), plus processor annotations.
type Block struct {
	Start, End int
	Exception  *ExceptionInfo
}

// Processed reports whether any processor matched this block. v1: exception only.
func (b Block) Processed() bool { return b.Exception != nil }

// IsWhitespaceCont is the whitespace-only continuation test shared with the
// TUI's isContinuation: a render-block row, or a non-empty line whose first
// byte is a space or tab. The segmenter layers signatures on top via
// IsContinuation; collapse uses only this primitive.
func IsWhitespaceCont(ln Line) bool {
	if ln.IsRenderBlock {
		return true
	}
	if ln.Text == "" {
		return false
	}
	c := ln.Text[0]
	return c == ' ' || c == '\t'
}

// hasContSignature matches the small set of non-indented prefixes that
// nonetheless continue a block. Tab/space-indented frames are already caught by
// IsWhitespaceCont and are intentionally absent here.
func hasContSignature(text string) bool {
	if strings.HasPrefix(text, "Caused by:") || strings.HasPrefix(text, "goroutine ") {
		return true
	}
	// PHP frames: '#' + digits + ' ' at line start (e.g. "#0 /path(9): f()").
	if len(text) >= 3 && text[0] == '#' && text[1] >= '0' && text[1] <= '9' {
		i := 1
		for i < len(text) && text[i] >= '0' && text[i] <= '9' {
			i++
		}
		if i < len(text) && text[i] == ' ' {
			return true
		}
	}
	return false
}

// IsContinuation is the segmenter's predicate: whitespace OR a signature.
func IsContinuation(ln Line) bool {
	if IsWhitespaceCont(ln) {
		return true
	}
	return hasContSignature(ln.Text)
}

// Processor annotates a block in place. Processors MUST NOT change Start/End.
type Processor interface {
	Process(b *Block, lines []Line)
}

// processors is the fixed processor set, populated by each processor file's
// init (v1: exception.go). No config-driven registry.
var processors []Processor

// Annotate runs every processor over a single (possibly still-growing) block.
func Annotate(b *Block, lines []Line) {
	for _, p := range processors {
		p.Process(b, lines)
	}
}

// Segment groups lines into blocks (head + following continuations) and
// annotates each with the processors. Pure / full recompute.
func Segment(lines []Line) []Block {
	var blocks []Block
	i := 0
	for i < len(lines) {
		start := i
		i++
		for i < len(lines) && IsContinuation(lines[i]) {
			i++
		}
		b := Block{Start: start, End: i - 1}
		Annotate(&b, lines)
		blocks = append(blocks, b)
	}
	return blocks
}
```

- [ ] **Step 4: Run, verify PASS:** `go test ./internal/blocks/`
- [ ] **Step 5: Commit:**
```bash
git add internal/blocks/blocks.go internal/blocks/blocks_test.go
git commit -m "feat(blocks): segmentation (indentation + signatures) + processor seam"
```

---

## Task 2: blocks package — exception processor

**Files:** Create `internal/blocks/exception.go`; Test `internal/blocks/exception_test.go`.

- [ ] **Step 1: Write the failing test**

Create `internal/blocks/exception_test.go`:

```go
package blocks

import "testing"

func exFromText(ss ...string) *ExceptionInfo {
	bs := Segment(lines(ss...))
	if len(bs) == 0 {
		return nil
	}
	return bs[0].Exception
}

func TestExceptionDetectionPerLanguage(t *testing.T) {
	cases := []struct {
		name string
		lang string
		text []string
	}{
		{"python", "python", []string{"Traceback (most recent call last):", "  File \"a.py\", line 1, in <module>", "ValueError: x"}},
		{"go", "go", []string{"panic: boom", "goroutine 1 [running]:", "\tmain.go:9 +0x1d"}},
		{"rust", "rust", []string{"thread 'main' panicked at src/main.rs:3:5:", "  boom"}},
		{"csanitizer", "c/c++", []string{"==123==ERROR: AddressSanitizer: heap-use-after-free", "    #0 0x1 in f a.c:1"}},
		{"php", "php", []string{"PHP Fatal error:  Uncaught Exception: x in /a.php:1", "Stack trace:", "#0 /a.php(9): f()"}},
		{"java", "java", []string{"java.lang.NullPointerException: x", "\tat com.foo.Bar.baz(Bar.java:42)"}},
		{"kotlin", "kotlin", []string{"java.lang.IllegalStateException", "\tat com.foo.Main.run(Main.kt:7)"}},
		{"node", "javascript", []string{"TypeError: x is not a function", "    at Object.<anonymous> (/app/a.js:10:5)"}},
		{"ts", "typescript", []string{"TypeError: x", "    at Foo (/app/a.ts:10:5)"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ex := exFromText(c.text...)
			if ex == nil {
				t.Fatalf("%s: expected an exception annotation, got nil", c.name)
			}
			if ex.Language != c.lang {
				t.Errorf("%s: language = %q, want %q", c.name, ex.Language, c.lang)
			}
		})
	}
}

func TestNonExceptionBlockUnflagged(t *testing.T) {
	if ex := exFromText("just a normal log line"); ex != nil {
		t.Errorf("plain line flagged as exception: %+v", ex)
	}
}

func TestProcessorDoesNotChangeBoundaries(t *testing.T) {
	bs := Segment(lines("panic: x", "goroutine 1 [running]:"))
	if bs[0].Start != 0 || bs[0].End != 1 {
		t.Errorf("processor altered block range: %+v", bs[0])
	}
}
```

- [ ] **Step 2: Run, verify FAIL** (exception detection not implemented → languages nil):
`go test -run TestExceptionDetection ./internal/blocks/`

- [ ] **Step 3: Implement.** Create `internal/blocks/exception.go`:

```go
package blocks

import (
	"regexp"
	"strings"
)

type exceptionProcessor struct{}

func init() { processors = append(processors, exceptionProcessor{}) }

// jvmFrameRE matches a JVM stack frame ending in (File.java:NN) or (File.kt:NN).
var jvmFrameRE = regexp.MustCompile(`\.(java|kt):\d+\)`)

// jsFrameRE matches a V8/Node frame tail ":line:col" (two colons), optionally
// closed by ")".
var jsFrameRE = regexp.MustCompile(`:\d+:\d+\)?$`)

// Process flags the block as a likely exception and guesses the language by
// scanning its lines for per-language markers. Heuristic, signature-based —
// precision over recall. Single-line headers win first; otherwise frame shape
// distinguishes JVM (Java/Kotlin) from JS/TS.
func (exceptionProcessor) Process(b *Block, lines []Line) {
	for i := b.Start; i <= b.End && i < len(lines); i++ {
		t := lines[i].Text
		switch {
		case strings.HasPrefix(t, "Traceback (most recent call last):"):
			b.Exception = &ExceptionInfo{Language: "python"}
			return
		case strings.HasPrefix(t, "panic:") || strings.HasPrefix(t, "goroutine ") || strings.Contains(t, "runtime error:"):
			b.Exception = &ExceptionInfo{Language: "go"}
			return
		case strings.HasPrefix(t, "thread '") && strings.Contains(t, "panicked at"):
			b.Exception = &ExceptionInfo{Language: "rust"}
			return
		case strings.Contains(t, "AddressSanitizer:") || strings.Contains(t, "terminate called after throwing an instance of"):
			b.Exception = &ExceptionInfo{Language: "c/c++"}
			return
		case (strings.Contains(t, "PHP ") && (strings.Contains(t, "Fatal error") || strings.Contains(t, "Uncaught"))) || strings.HasPrefix(t, "Stack trace:"):
			b.Exception = &ExceptionInfo{Language: "php"}
			return
		}
	}
	if lang, ok := detectFrameLanguage(b, lines); ok {
		b.Exception = &ExceptionInfo{Language: lang}
	}
}

// detectFrameLanguage classifies a block by the shape of its `at …` frames.
func detectFrameLanguage(b Block, lines []Line) (string, bool) {
	var java, kotlin, ts, js bool
	for i := b.Start; i <= b.End && i < len(lines); i++ {
		t := strings.TrimLeft(lines[i].Text, " \t")
		if !strings.HasPrefix(t, "at ") {
			continue
		}
		switch {
		case jvmFrameRE.MatchString(t):
			if strings.Contains(t, ".kt:") {
				kotlin = true
			} else {
				java = true
			}
		case strings.Contains(t, ".ts:") && jsFrameRE.MatchString(t):
			ts = true
		case jsFrameRE.MatchString(t):
			js = true
		}
	}
	switch {
	case kotlin:
		return "kotlin", true
	case java:
		return "java", true
	case ts:
		return "typescript", true
	case js:
		return "javascript", true
	}
	return "", false
}
```

- [ ] **Step 4: Run, verify PASS:** `go test ./internal/blocks/`
- [ ] **Step 5: Commit:**
```bash
git add internal/blocks/exception.go internal/blocks/exception_test.go
git commit -m "feat(blocks): exception processor with per-language detection"
```

---

## Task 3: TUI block cache (adapter, dirty flag, recompute)

**Files:** Create `internal/tui/blocks.go`; Modify `internal/tui/app.go`; Test `internal/tui/blocks_test.go`.

Context: `model.lines []displayLine` (`displayLine{group,file,body string; bodyWidth int; isBlock bool}`); `stripANSI(s string) string` exists. Mutators of `m.lines`: `appendEvent`, `trimToCap` (both in app.go), `reRenderAll`, `applyReload`.

- [ ] **Step 1: Write the failing test**

Create `internal/tui/blocks_test.go`:

```go
package tui

import (
	"testing"

	"github.com/homeend/log-listener/internal/render"
)

func TestEnsureBlocksRecomputesAfterAppend(t *testing.T) {
	m := newModel(100)
	m.appendEvent(render.Event{Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "panic: boom"}}})
	m.appendEvent(render.Event{Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "goroutine 1 [running]:"}}})
	m.ensureBlocks()
	if len(m.blocks) != 1 {
		t.Fatalf("want 1 block, got %d", len(m.blocks))
	}
	if m.blocks[0].Exception == nil || m.blocks[0].Exception.Language != "go" {
		t.Errorf("block not flagged go: %+v", m.blocks[0])
	}
}

func TestAppendSetsBlocksDirty(t *testing.T) {
	m := newModel(100)
	m.ensureBlocks() // clean
	m.appendEvent(render.Event{Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "x"}}})
	if !m.blocksDirty {
		t.Error("appendEvent must set blocksDirty")
	}
}

func TestInExceptionBlock(t *testing.T) {
	m := newModel(100)
	m.appendEvent(render.Event{Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "Traceback (most recent call last):"}}})
	m.appendEvent(render.Event{Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "  File \"a.py\", line 1, in <module>"}}})
	m.ensureBlocks()
	if !m.inExceptionBlock(0) || !m.inExceptionBlock(1) {
		t.Errorf("both rows of a python traceback should be in an exception block")
	}
}
```

- [ ] **Step 2: Run, verify FAIL** (undefined `blocks`, `blocksDirty`, `ensureBlocks`, `inExceptionBlock`):
`go test -run 'TestEnsureBlocks|TestAppendSetsBlocksDirty|TestInExceptionBlock' ./internal/tui/`

- [ ] **Step 3: Add model fields.** In `internal/tui/app.go`, add to the `model` struct after the `flash`/`saveDir` fields:

```go
	// blocks is the cached segmentation of m.lines (see internal/blocks).
	// blocksDirty is set by every m.lines mutator; ensureBlocks recomputes
	// when dirty. showExceptionMarks toggles the renderException left bar.
	blocks             []blocks.Block
	blocksDirty        bool
	showExceptionMarks bool
```

Add the import to app.go's import block: `"github.com/homeend/log-listener/internal/blocks"`. In `newModel`, set the default: add `showExceptionMarks: true,` to the returned `&model{...}` literal.

- [ ] **Step 4: Set the dirty flag in all four mutators.** In `internal/tui/app.go`, add `m.blocksDirty = true` as the last statement of each: `appendEvent`, `trimToCap`, `reRenderAll`, `applyReload`. (For `trimToCap`, place it at the very end of the function, after the early `return` guards — only the paths that actually mutate reach it; an early `return` when nothing is trimmed correctly skips it.)

- [ ] **Step 5: Implement the cache helpers.** Create `internal/tui/blocks.go`:

```go
package tui

import "github.com/homeend/log-listener/internal/blocks"

// blockLines adapts m.lines into the neutral blocks.Line slice: ANSI stripped,
// with the render-block flag carried through.
func (m *model) blockLines() []blocks.Line {
	out := make([]blocks.Line, len(m.lines))
	for i, dl := range m.lines {
		out[i] = blocks.Line{Text: stripANSI(dl.body), IsRenderBlock: dl.isBlock}
	}
	return out
}

// ensureBlocks recomputes the block cache when dirty. Single recompute path —
// every m.lines mutator sets blocksDirty, so the cache is current wherever it
// is read (renderStream, navigation).
func (m *model) ensureBlocks() {
	if !m.blocksDirty {
		return
	}
	m.blocks = blocks.Segment(m.blockLines())
	m.blocksDirty = false
}

// inExceptionBlock reports whether the line at absolute index idx belongs to a
// block the exception processor flagged. Callers must have called ensureBlocks.
func (m *model) inExceptionBlock(idx int) bool {
	for _, b := range m.blocks {
		if idx < b.Start {
			return false // blocks are ordered; no later block can contain idx
		}
		if idx <= b.End {
			return b.Exception != nil
		}
	}
	return false
}
```

- [ ] **Step 6: Run, verify PASS:** `go test -run 'TestEnsureBlocks|TestAppendSetsBlocksDirty|TestInExceptionBlock' ./internal/tui/` then `go test ./internal/tui/`.

- [ ] **Step 7: Commit:**
```bash
git add internal/tui/blocks.go internal/tui/app.go internal/tui/blocks_test.go
git commit -m "feat(tui): block cache (adapter, dirty flag, exception lookup)"
```

---

## Task 4: keymap — five block actions + doc

**Files:** Modify `internal/keymap/actions.go`, `internal/keymap/defaults.go`, `internal/keymap/actions_test.go`, `internal/keymap/defaults_test.go`; Regenerate `KEYBINDINGS.md`.

- [ ] **Step 1: Write the failing test.** Append to `internal/keymap/defaults_test.go`:

```go
func TestBlockActionsHaveDefaults(t *testing.T) {
	want := map[Action]string{
		ActionNextBlock:            "]",
		ActionPrevBlock:            "[",
		ActionNextMarkedBlock:      "}",
		ActionPrevMarkedBlock:      "{",
		ActionToggleExceptionMarks: "e",
	}
	for _, goos := range []string{"linux", "darwin", "windows"} {
		dm := defaultFor(goos)
		for a, key := range want {
			if !equalSlice(dm[a], []string{key}) {
				t.Errorf("%s: %s default = %v, want [%s]", goos, a, dm[a], key)
			}
		}
	}
}
```

- [ ] **Step 2: Run, verify FAIL** (actions undefined): `go test -run TestBlockActionsHaveDefaults ./internal/keymap/`

- [ ] **Step 3: Add constants + AllActions.** In `internal/keymap/actions.go`, add after `ActionSaveScrollback` in the `const (...)` block:

```go
	ActionNextBlock            Action = "next_block"
	ActionPrevBlock            Action = "prev_block"
	ActionNextMarkedBlock      Action = "next_marked_block"
	ActionPrevMarkedBlock      Action = "prev_marked_block"
	ActionToggleExceptionMarks Action = "toggle_exception_marks"
```

And append to `AllActions` after the `ActionSaveScrollback` entry:

```go
	{ActionNextBlock, "Next block", "Jump to the next multi-line block.", "main"},
	{ActionPrevBlock, "Previous block", "Jump to the previous multi-line block.", "main"},
	{ActionNextMarkedBlock, "Next marked block", "Jump to the next processor-matched block (e.g. exception).", "main"},
	{ActionPrevMarkedBlock, "Previous marked block", "Jump to the previous processor-matched block.", "main"},
	{ActionToggleExceptionMarks, "Toggle exception marks", "Show/hide the exception left-bar.", "main"},
```

- [ ] **Step 4: Update the count assertion.** In `internal/keymap/actions_test.go`, change the exact count in `TestAllActionsUniqueAndNonEmpty` from `28` to `33` (both the `if len(AllActions) != 28` and the error string).

- [ ] **Step 5: Add defaults.** In `internal/keymap/defaults.go`, add to the OS-independent `m` map after `ActionSaveScrollback: {"S"},`:

```go
		ActionNextBlock:            {"]"},
		ActionPrevBlock:            {"["},
		ActionNextMarkedBlock:      {"}"},
		ActionPrevMarkedBlock:      {"{"},
		ActionToggleExceptionMarks: {"e"},
```

- [ ] **Step 6: Run keymap tests** — new test + `TestDefaultForCoversEveryAction` pass, `TestDocsUpToDate` fails (stale doc): `go test ./internal/keymap/`

- [ ] **Step 7: Regenerate the doc:** `./build.sh keybindings-docs`

- [ ] **Step 8: Run keymap tests — all green:** `go test ./internal/keymap/`

- [ ] **Step 9: Commit:**
```bash
git add internal/keymap/actions.go internal/keymap/actions_test.go internal/keymap/defaults.go internal/keymap/defaults_test.go KEYBINDINGS.md
git commit -m "feat(keymap): block nav (]/[ }/{) + exception-marks toggle (e)"
```

---

## Task 5: renderException left bar (width-accounted) + toggle

**Files:** Modify `internal/tui/blocks.go`, `internal/tui/app.go`; Test `internal/tui/blocks_test.go`.

Context: `renderStream(rows)` loops `for _, idx := range visible { styled, visW := m.renderDisplayLineAt(idx); rendered = append(rendered, m.clipLine(styled, visW)) }`. `dimStyle`/`lipgloss` styles exist; `dispWidth(s)` returns display columns. The action switch in `Update` dispatches `keymap.Action*` cases.

- [ ] **Step 1: Write the failing test.** Append to `internal/tui/blocks_test.go`:

```go
func TestExceptionBarPrependedAndWidthSafe(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	m.appendEvent(render.Event{Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "panic: boom"}}})
	m.appendEvent(render.Event{Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "goroutine 1 [running]:"}}})

	view := m.renderStream(m.contentHeight())
	// The bar glyph appears (marks are on by default).
	if !strings.Contains(view, "▌") {
		t.Fatalf("expected exception bar glyph in view:\n%s", view)
	}
	// Width invariant: no rendered row exceeds the terminal width.
	for _, ln := range strings.Split(view, "\n") {
		if w := dispWidth(ln); w > m.width {
			t.Errorf("row exceeds width %d (got %d): %q", m.width, w, ln)
		}
	}

	// Toggle off → no bar.
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	m = m2.(*model)
	if strings.Contains(m.renderStream(m.contentHeight()), "▌") {
		t.Errorf("bar should disappear when marks are toggled off")
	}
}
```

Add `"strings"` and `tea "github.com/charmbracelet/bubbletea"` to `internal/tui/blocks_test.go`'s imports:

```go
import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/render"
)
```

- [ ] **Step 2: Run, verify FAIL** (no bar yet; `e` unhandled): `go test -run TestExceptionBarPrependedAndWidthSafe ./internal/tui/`

- [ ] **Step 3: Add the bar helper.** Append to `internal/tui/blocks.go`:

```go
import "github.com/charmbracelet/lipgloss"

// exceptionBarStyle renders the left-bar glyph in an alert color.
var exceptionBarStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("9")) // red

// exceptionBarWidth is the display-column width of the bar prefix "▌ ",
// MEASURED with the same dispWidth the renderer uses (▌ U+258C is East-Asian
// ambiguous — its width varies by locale, so a hardcoded 2 could be wrong and
// re-introduce the row-overflow/wrap bug). Computed once at init.
var exceptionBarWidth = dispWidth("▌ ")

// exceptionBar returns the styled bar prefix and true when the line at idx
// should be barred (marks on AND the line is in an exception block). The
// returned width (exceptionBarWidth) MUST be added to the row's visW so
// clipLine pads/clips against the true width.
func (m *model) exceptionBar(idx int) (string, bool) {
	if !m.showExceptionMarks {
		return "", false
	}
	if !m.inExceptionBlock(idx) {
		return "", false
	}
	return exceptionBarStyle.Render("▌") + " ", true
}
```

Merge the two imports in `blocks.go` into one block:

```go
import (
	"github.com/charmbracelet/lipgloss"

	"github.com/homeend/log-listener/internal/blocks"
)
```

- [ ] **Step 4: Integrate into renderStream.** In `internal/tui/app.go`, at the top of `renderStream` (after the `len(m.lines)==0` guard), add `m.ensureBlocks()`. Then change the per-line loop body from:

```go
	for _, idx := range visible {
		styled, visW := m.renderDisplayLineAt(idx)
		rendered = append(rendered, m.clipLine(styled, visW))
	}
```

to:

```go
	for _, idx := range visible {
		styled, visW := m.renderDisplayLineAt(idx)
		if bar, ok := m.exceptionBar(idx); ok {
			styled = bar + styled
			visW += exceptionBarWidth
		}
		rendered = append(rendered, m.clipLine(styled, visW))
	}
```

- [ ] **Step 5: Dispatch the toggle.** In `internal/tui/app.go`, add a case to the `switch action {` block (after `ActionCollapseAll`):

```go
		case keymap.ActionToggleExceptionMarks:
			m.showExceptionMarks = !m.showExceptionMarks
```

- [ ] **Step 6: Run, verify PASS:** `go test -run TestExceptionBarPrependedAndWidthSafe ./internal/tui/` then `go test ./internal/tui/`.

- [ ] **Step 7: Commit:**
```bash
git add internal/tui/blocks.go internal/tui/app.go internal/tui/blocks_test.go
git commit -m "feat(tui): renderException left bar (width-accounted) + e toggle"
```

---

## Task 6: block navigation (]/[ }/{)

**Files:** Modify `internal/tui/blocks.go`, `internal/tui/app.go`; Test `internal/tui/blocks_test.go`.

Context: `unstickFromTail()` exits tail keeping the window; `m.streamTop` is the absolute top index when `!tailMode`; `m.tailMode` true means pinned to bottom. `lineEnabled(dl)` reports group-enabled-and-not-collapsed. Navigation should move to a block head that is enabled.

- [ ] **Step 1: Write the failing test.** Append to `internal/tui/blocks_test.go`:

```go
func TestBlockNavigation(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	// Three single-line heads (each its own block) + one exception block.
	for _, v := range []string{"head A", "head B", "panic: boom"} {
		m.appendEvent(render.Event{Group: "g", File: "/a.log",
			Rendered: []render.Part{{Type: "text", Value: v}}})
	}
	m.appendEvent(render.Event{Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "goroutine 1 [running]:"}}})
	// blocks: [0,0] head A, [1,1] head B, [2,3] panic (go).

	// From the top, next block goes to index 1.
	m.tailMode = false
	m.streamTop = 0
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	m = m2.(*model)
	if m.streamTop != 1 {
		t.Errorf("] from 0 → streamTop %d, want 1", m.streamTop)
	}

	// next MARKED block from the top jumps straight to the exception (index 2).
	m.tailMode = false
	m.streamTop = 0
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'}'}})
	m = m2.(*model)
	if m.streamTop != 2 {
		t.Errorf("} from 0 → streamTop %d, want 2 (exception head)", m.streamTop)
	}

	// prev block from index 2 goes to 1.
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})
	m = m2.(*model)
	if m.streamTop != 1 {
		t.Errorf("[ from 2 → streamTop %d, want 1", m.streamTop)
	}
}
```

- [ ] **Step 2: Run, verify FAIL** (nav keys unhandled): `go test -run TestBlockNavigation ./internal/tui/`

- [ ] **Step 3: Add the nav helpers.** Append to `internal/tui/blocks.go`:

```go
// navAnchor is the index block navigation measures from: the current top when
// browsing, or len(m.lines) when pinned to the tail (so "previous" walks back
// from the end and "next" finds nothing).
func (m *model) navAnchor() int {
	if m.tailMode {
		return len(m.lines)
	}
	return m.streamTop
}

// blockHeadEnabled reports whether a block's head row is currently visible
// (group enabled, not collapsed away). Navigation skips hidden heads.
func (m *model) blockHeadEnabled(b blocks.Block) bool {
	if b.Start < 0 || b.Start >= len(m.lines) {
		return false
	}
	return m.lineEnabled(m.lines[b.Start])
}

// jumpToBlockHead moves the viewport so the block head at line idx is the top
// row, leaving tail mode. Mirrors search-hit navigation's "anchor at top".
func (m *model) jumpToBlockHead(idx int) {
	m.unstickFromTail()
	m.tailMode = false
	m.streamTop = idx
	if m.streamTop < 0 {
		m.streamTop = 0
	}
}

// gotoNextBlock moves to the next block head after the anchor. markedOnly limits
// the search to processor-matched (Processed) blocks. No-op if none.
func (m *model) gotoNextBlock(markedOnly bool) {
	m.ensureBlocks()
	anchor := m.navAnchor()
	for _, b := range m.blocks {
		if b.Start <= anchor {
			continue
		}
		if markedOnly && !b.Processed() {
			continue
		}
		if !m.blockHeadEnabled(b) {
			continue
		}
		m.jumpToBlockHead(b.Start)
		return
	}
}

// gotoPrevBlock moves to the last block head before the anchor.
func (m *model) gotoPrevBlock(markedOnly bool) {
	m.ensureBlocks()
	anchor := m.navAnchor()
	for i := len(m.blocks) - 1; i >= 0; i-- {
		b := m.blocks[i]
		if b.Start >= anchor {
			continue
		}
		if markedOnly && !b.Processed() {
			continue
		}
		if !m.blockHeadEnabled(b) {
			continue
		}
		m.jumpToBlockHead(b.Start)
		return
	}
}
```

- [ ] **Step 4: Dispatch the four actions.** In `internal/tui/app.go`, add to the `switch action {` block (after the `ActionToggleExceptionMarks` case):

```go
		case keymap.ActionNextBlock:
			m.gotoNextBlock(false)
		case keymap.ActionPrevBlock:
			m.gotoPrevBlock(false)
		case keymap.ActionNextMarkedBlock:
			m.gotoNextBlock(true)
		case keymap.ActionPrevMarkedBlock:
			m.gotoPrevBlock(true)
```

- [ ] **Step 5: Run, verify PASS:** `go test -run TestBlockNavigation ./internal/tui/` then `go test ./internal/tui/`.

- [ ] **Step 6: Commit:**
```bash
git add internal/tui/blocks.go internal/tui/app.go internal/tui/blocks_test.go
git commit -m "feat(tui): block navigation (]/[ all, }/{ processed)"
```

---

## Task 7: documentation + full quality gate

**Files:** Modify `README.md`, `CHANGELOG.md`.

- [ ] **Step 1: README keybindings table.** In `README.md`, in the `### Keybindings` table, add after the `**`S`**` save-scrollback row:

```markdown
| **`]`** / **`[`**   | **Jump to the next / previous multi-line block.**     |
| **`}`** / **`{`**   | **Jump to the next / previous processor-matched block (e.g. exception).** |
| **`e`**             | **Toggle the exception left-bar marker.**             |
```

- [ ] **Step 2: CHANGELOG entry.** In `CHANGELOG.md`, under `## [Unreleased]`, add as the first subsection:

```markdown
### Block annotation + exception marks
- Multi-line log units (stack traces, pretty-printed JSON/XML, indented
  continuations) are grouped into **blocks** by a neutral `internal/blocks`
  package: indentation plus a small signature set (`Caused by:`, `goroutine `,
  PHP `#<n>`) so multi-part traces group together.
- An **exception processor** flags blocks that look like stack traces and
  guesses the language (Python/Java/Kotlin/Go/JS/TS/Rust/C-C++/PHP). Detection
  is heuristic; Go panics may still split into multiple blocks.
- **`e`** toggles a red left-bar (`▌`) drawn on exception blocks. **`]`/`[`**
  jump between all blocks; **`}`/`{`** jump between processor-matched blocks.
  All keys are remappable via the `keybindings:` block. IDs/clipboard for
  agent hand-off arrive with the MCP server.
```

- [ ] **Step 3: Verify the keybindings doc is current:**
`./build.sh keybindings-docs && git diff --exit-code KEYBINDINGS.md`
Expected: exit 0 (already regenerated in Task 4).

- [ ] **Step 4: Full quality gate:** `go vet ./... && go test ./... && go test -race ./...` — all PASS.

- [ ] **Step 5: Commit:**
```bash
git add README.md CHANGELOG.md
git commit -m "docs: document block navigation + exception marks"
```

---

## Self-Review (completed during planning)

- **Spec coverage:** neutral package + segmentation (Task 1) ✓; processor seam + exception detection + per-language signatures, finalized from research (Task 2) ✓; block cache with dirty flag set by all four mutators + adapter (Task 3) ✓; five keymap actions + doc (Task 4) ✓; column-0 width-accounted bar + toggle (Task 5) ✓; two nav key sets, visibility-aware, reusing tail/anchor patterns (Task 6) ✓; docs + gate (Task 7) ✓. Shared whitespace primitive `IsWhitespaceCont` exists for collapse reuse (Task 1) ✓. "Two continuations" seam documented; collapse untouched ✓.
- **Placeholder scan:** none — every code step is complete; the signature table is concrete (research done above).
- **Type consistency:** `blocks.Line{Text,IsRenderBlock}`, `blocks.Block{Start,End,Exception}`, `Block.Processed()`, `blocks.Segment/Annotate/IsContinuation/IsWhitespaceCont`, `Processor.Process(*Block,[]Line)`; TUI `m.blocks`/`m.blocksDirty`/`m.showExceptionMarks`, `blockLines`/`ensureBlocks`/`inExceptionBlock`/`exceptionBar`/`exceptionBarWidth`/`gotoNextBlock`/`gotoPrevBlock`/`jumpToBlockHead`/`navAnchor`/`blockHeadEnabled`; actions `ActionNextBlock`/`ActionPrevBlock`/`ActionNextMarkedBlock`/`ActionPrevMarkedBlock`/`ActionToggleExceptionMarks` — consistent across tasks and tests.

## Out of Scope (YAGNI)

Per-line/block IDs, cursor, OSC 52 clipboard (feature #1); config-driven plugin registry; a second processor or render plugin; language tag rendering; merging collapse with block awareness; perfect single-block grouping for every language.
