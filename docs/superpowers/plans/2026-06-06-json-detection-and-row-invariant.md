# Validity-Based JSON/XML Detection + Row Invariant — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop log-listener from mangling non-JSON `{…}` lines: a renderer matches only if its `json()/xml()` actually parses (else it falls through to the raw line), and the TUI never stores a render result as a text blob with embedded newlines.

**Architecture:** Fix A (render layer): `renderJSON`/`renderXML` and `Template.Execute` report a parse-failure boolean; `Pipeline.Render` skips a renderer whose render-call can't parse. Fix B (TUI layer): `decomposeEvent` splits multi-line text into a list of `displayLine`s (head + block rows) so one displayLine = one terminal row. The two fixes are independent.

**Tech Stack:** Go 1.26, standard `testing`.

**Spec:** `docs/superpowers/specs/2026-06-06-json-detection-and-row-invariant-design.md`

---

## Task 1: Fix B — `decomposeEvent` stores a list of lines

**Files:**
- Modify: `internal/tui/app.go` (`decomposeEvent`)
- Test: `internal/tui/multiline_test.go` (`TestDecomposeNeverLeavesEmbeddedNewline` already present; add a `View()` test)

- [ ] **Step 1: Confirm the existing failing test + add a symptom test.**

`internal/tui/multiline_test.go` already contains `TestDecomposeNeverLeavesEmbeddedNewline` (asserts no `displayLine.body` contains `\n`). Append this `View()`-level test:

```go
func TestEmbeddedNewlineKeepsHeaderRow(t *testing.T) {
	m := newModel(1000)
	m.groupOrder = []string{"goland"}
	m.groupEnabled["goland"] = true
	m.appendEvent(render.Event{Group: "goland", File: "/idea.log", Rendered: []render.Part{
		{Type: "text", Value: "INFO Saved path macros: \n"},
		{Type: "text", Value: "{DB_ARTIFACTS_BUNDLE=C:\\x\\artifacts}"},
	}})
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	m = m2.(*model)
	rows := strings.Split(m.View(), "\n")
	if len(rows) != 10 {
		t.Fatalf("View must be exactly height(10) rows, got %d", len(rows))
	}
	if !strings.Contains(rows[0], "log-listener") {
		t.Fatalf("header row missing/overflowed: %q", rows[0])
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test -run 'TestDecomposeNeverLeavesEmbeddedNewline|TestEmbeddedNewlineKeepsHeaderRow' ./internal/tui/`
Expected: FAIL — `decomposeEvent` leaves the `\n` in one body, and `View()` renders 11 rows (the embedded `\n` adds a physical line).

- [ ] **Step 3: Split the head text on `\n` in `decomposeEvent`.**

In `internal/tui/app.go`, the current `decomposeEvent` builds the head like this:

```go
	base := filepath.Base(ev.File)
	text := strings.TrimRight(textBuf.String(), "\n")
	out := []displayLine{{
		group: ev.Group, file: base,
		body:      text,
		bodyWidth: runeLen(text),
	}}
	for _, b := range blocks {
		for _, ln := range strings.Split(b, "\n") {
			out = append(out, displayLine{
				group:     ev.Group,
				file:      base,
				body:      dimStyle.Render(ln),
				bodyWidth: runeLen(ln),
				isBlock:   true,
			})
		}
	}
	return out
```

Replace it with (split the head text into a head row + block continuation rows):

```go
	base := filepath.Base(ev.File)
	text := strings.TrimRight(textBuf.String(), "\n")
	// A text part may carry embedded newlines (a template "\n" literal). Each
	// physical line must be its own displayLine so the "one displayLine = one
	// terminal row" invariant holds — otherwise the row wraps and breaks the
	// layout. The first line is the head (keeps the [group] file: prefix);
	// the rest render as block continuation rows, exactly like JSON/XML lines.
	textLines := strings.Split(text, "\n")
	out := []displayLine{{
		group: ev.Group, file: base,
		body:      textLines[0],
		bodyWidth: runeLen(textLines[0]),
	}}
	for _, ln := range textLines[1:] {
		out = append(out, displayLine{
			group:     ev.Group,
			file:      base,
			body:      dimStyle.Render(ln),
			bodyWidth: runeLen(ln),
			isBlock:   true,
		})
	}
	for _, b := range blocks {
		for _, ln := range strings.Split(b, "\n") {
			out = append(out, displayLine{
				group:     ev.Group,
				file:      base,
				body:      dimStyle.Render(ln),
				bodyWidth: runeLen(ln),
				isBlock:   true,
			})
		}
	}
	return out
```

`strings.Split` returns a one-element slice when there is no `\n`, so this is a no-op for every normal line.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/tui/` → all PASS. `go vet ./internal/tui/` → clean.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/app.go internal/tui/multiline_test.go
git commit -m "phase 1: decompose stores multi-line text as a list of rows

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 2: Fix A — renderer falls through when `json()`/`xml()` can't parse

**Files:**
- Modify: `internal/render/json.go` (`renderJSON`)
- Modify: `internal/render/xml.go` (`renderXML`)
- Modify: `internal/render/template.go` (`Execute`)
- Modify: `internal/render/pipeline.go` (`Render`)
- Test: `internal/render/render_test.go`

- [ ] **Step 1: Write/adjust the failing tests.**

(a) Add a new pipeline test (this is the core behavior):

```go
func TestPipelineRendererFallsThroughOnUnparseableJSON(t *testing.T) {
	specs := []config.RendererSpec{
		// Matches any line ending in {…}, tries to render the braces as JSON.
		{Name: "trailing-json", LineRegex: `^(.*?\s)(\{.+\})\s*$`, Template: `$1\njson($2)`},
	}
	p, err := NewPipeline(specs, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	// {KEY=value} is not JSON: the renderer must NOT win; the line passes
	// through raw (one text part = the original line, Renderer empty).
	ev, ok := p.Render(time.Now(), "g", "/idea.log", "msg: {DB=C:\\x}")
	if !ok {
		t.Fatal("non-drop mode must still emit the raw line")
	}
	if ev.Renderer != "" {
		t.Fatalf("renderer should fall through on unparseable JSON, got %q", ev.Renderer)
	}
	if len(ev.Rendered) != 1 || ev.Rendered[0].Type != "text" ||
		ev.Rendered[0].Value.(string) != "msg: {DB=C:\\x}" {
		t.Fatalf("expected raw passthrough, got %+v", ev.Rendered)
	}
	// Valid trailing JSON still wins and renders a block.
	ev, ok = p.Render(time.Now(), "g", "/idea.log", `msg: {"a":1}`)
	if !ok || ev.Renderer != "trailing-json" {
		t.Fatalf("valid JSON should render, got ok=%v renderer=%q", ok, ev.Renderer)
	}
}

func TestPipelineUnparseableJSONDroppedWhenDropUnmatched(t *testing.T) {
	specs := []config.RendererSpec{
		{Name: "trailing-json", LineRegex: `^(.*?\s)(\{.+\})\s*$`, Template: `$1\njson($2)`},
	}
	p, _ := NewPipeline(specs, nil, nil, true) // drop_unmatched
	if _, ok := p.Render(time.Now(), "g", "/idea.log", "msg: {DB=x}"); ok {
		t.Fatal("regex-matched but unparseable render-call must drop under drop_unmatched")
	}
}
```

(b) Update the existing `Execute` call sites to the new two-value form and flip the two "falls back to text" unit tests to the new semantics. Apply these exact edits in `internal/render/render_test.go`:

- In `TestParseTemplateBasic`: `parts := tpl.Execute(...)` → `parts, _ := tpl.Execute([]string{"FULL", "2026-05-28", "ERROR", ` + "`" + `{"u":"bob"}` + "`" + `})`
- In `TestParseTemplateEscapes`: `parts := tpl.Execute([]string{"_", "X"})` → `parts, _ := tpl.Execute([]string{"_", "X"})`
- In `TestParseTemplateXMLCall`: `parts := tpl.Execute(...)` → `parts, _ := tpl.Execute([]string{"_", ` + "`" + `<a><b>1</b></a>` + "`" + `})`
- In `TestCaptureOutOfRange`: `parts := tpl.Execute([]string{"only", "one"})` → `parts, _ := tpl.Execute([]string{"only", "one"})`
- Replace `TestJSONRendererInvalidFallsBackToText` entirely with:

```go
func TestJSONRendererInvalidReportsNotOK(t *testing.T) {
	tpl, _ := ParseTemplate(`json($1)`)
	if _, ok := tpl.Execute([]string{"_", "not-json"}); ok {
		t.Fatal("unparseable JSON must make Execute report ok=false")
	}
}
```

- Replace `TestXMLRendererInvalidFallsBackToText` entirely with:

```go
func TestXMLRendererInvalidReportsNotOK(t *testing.T) {
	tpl, _ := ParseTemplate(`xml($1)`)
	if _, ok := tpl.Execute([]string{"_", "<broken"}); ok {
		t.Fatal("unparseable XML must make Execute report ok=false")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/render/`
Expected: FAIL — `Execute` returns a single value (build errors at the new `, ok :=`/`, _ :=` call sites) and the new pipeline test fails.

- [ ] **Step 3: Implement the `(Part, bool)` returns.**

`internal/render/json.go` — replace `renderJSON`:

```go
// renderJSON parses the input as JSON. ok=false means the input is not valid
// JSON (the caller should treat the renderer as non-matching). The returned
// Part on failure holds the raw text only as a convenience and is discarded
// by the pipeline. Empty input is ok (an empty text part).
func renderJSON(s string) (Part, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Part{Type: "text", Value: ""}, true
	}
	var v interface{}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return Part{Type: "text", Value: s}, false
	}
	return Part{Type: "json", Value: v}, true
}
```

`internal/render/xml.go` — replace `renderXML` (keep `prettyXML` unchanged):

```go
// renderXML pretty-prints the input XML. ok=false means the input is not valid
// XML (the caller should treat the renderer as non-matching). Empty input is ok.
func renderXML(s string) (Part, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Part{Type: "text", Value: ""}, true
	}
	pretty, err := prettyXML(s)
	if err != nil {
		return Part{Type: "text", Value: s}, false
	}
	return Part{Type: "xml", Value: pretty}, true
}
```

`internal/render/template.go` — replace `Execute`:

```go
// Execute renders the template against the given regex captures. captures[0]
// is the full match; captures[1..N] are the parenthesized groups. Out-of-range
// $N references expand to empty string. ok=false means a json()/xml() call
// could not parse its capture — the caller should treat the renderer as not
// matching and fall through.
func (t *Template) Execute(captures []string) ([]Part, bool) {
	var parts []Part
	var text strings.Builder
	flushText := func() {
		if text.Len() > 0 {
			parts = append(parts, Part{Type: "text", Value: text.String()})
			text.Reset()
		}
	}
	capture := func(n int) string {
		if n < 0 || n >= len(captures) {
			return ""
		}
		return captures[n]
	}
	for _, p := range t.parts {
		switch p.kind {
		case partLiteral:
			text.WriteString(p.text)
		case partCapture:
			text.WriteString(capture(p.group))
		case partRenderJSON:
			flushText()
			part, ok := renderJSON(capture(p.group))
			if !ok {
				return nil, false
			}
			parts = append(parts, part)
		case partRenderXML:
			flushText()
			part, ok := renderXML(capture(p.group))
			if !ok {
				return nil, false
			}
			parts = append(parts, part)
		}
	}
	flushText()
	return parts, true
}
```

`internal/render/pipeline.go` — in `Render`, replace the match/render block:

```go
		caps := r.Match(path, raw)
		if caps == nil {
			continue
		}
		parts, ok := r.template.Execute(caps)
		if !ok {
			// A json()/xml() call couldn't parse — the renderer doesn't apply.
			continue
		}
		ev.Renderer = r.Name
		ev.Captures = caps
		ev.Rendered = parts
		return ev, true
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/render/` → all PASS. `go vet ./internal/render/` → clean.

- [ ] **Step 5: Commit**

```bash
git add internal/render/json.go internal/render/xml.go internal/render/template.go internal/render/pipeline.go internal/render/render_test.go
git commit -m "phase 2: renderer falls through when json()/xml() can't parse

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 3: e2e — the IntelliJ macro line renders as one raw line

**Files:**
- Test: `cmd/log-listener/e2e_test.go`

- [ ] **Step 1: Write the regression test.** First inspect the existing e2e harness: `grep -n "func Test\|startListener\|--once\|--no-tui\|--no-color\|runMain\|t.TempDir" cmd/log-listener/e2e_test.go` and reuse the same helper an existing one-shot test uses. Then add a test that, mirroring that harness:
  - writes a config with a `directories` or `files` group plus the two goland renderers:

    ```
    renderers:
      - name: json-line
        line_regex: '^\s*(\{.*\})\s*$'
        template: 'json($1)'
      - name: idea-trailing-json
        line_regex: '^(.*?\s)(\{.+\})\s*$'
        template: '$1\njson($2)'
    ```
  - writes a log file with two lines:
    - `2026 INFO Saved path macros: {DB_ARTIFACTS_BUNDLE=C:\x\artifacts}` (non-JSON braces)
    - `2026 INFO payload: {"a":1}` (valid trailing JSON)
  - runs the program one-shot/no-tui/no-color exactly as the existing e2e tests do.
  - asserts on stdout:
    - the macro line appears **intact on one line**: `strings.Contains(out, "Saved path macros: {DB_ARTIFACTS_BUNDLE=C:\\x\\artifacts}")` is true (it was NOT split — fell through to raw).
    - the valid-JSON line still pretty-prints: the output contains a line with `"a": 1` (indented JSON block).

  Name it `TestE2ENonJSONBracesRenderRaw`. If the existing harness genuinely can't assert this, report BLOCKED rather than inventing a fragile test.

- [ ] **Step 2: Run to verify** it passes with Tasks 1–2 applied (this is mostly a regression guard; with Fix A the macro line falls through to raw). Run: `go test -run TestE2ENonJSONBracesRenderRaw ./cmd/log-listener/`. If it fails, the failure indicates the harness assumptions need adjusting (fix the test harness usage, not the source).

- [ ] **Step 3: Commit**

```bash
git add cmd/log-listener/e2e_test.go
git commit -m "phase 3: e2e — non-JSON trailing braces render as the raw line

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 4: Docs + full verification

**Files:**
- Modify: `CLAUDE.md`, `README.md`, `CHANGELOG.md`

- [ ] **Step 1: Update `CLAUDE.md` locked rules.** In the "Locked design rules" list, refine the first-match-wins bullet to note render-call validity. Replace the bullet:

```
- **First-match-wins, everywhere**: file → group assignment, and
  line → renderer matching, both use declaration order.
```

with:

```
- **First-match-wins, everywhere**: file → group assignment, and
  line → renderer matching, both use declaration order. A renderer matches
  only if its `line_regex` matches AND its `json()`/`xml()` render-calls
  actually parse; a parse failure makes the renderer fall through to the next
  one (or to raw / drop), so non-JSON `{…}` is never mangled.
```

- [ ] **Step 2: Update `README.md`.** In the renderer-pipeline / "Lines that no renderer matches" area, add a sentence: a renderer whose `json()`/`xml()` call can't parse its capture is treated as a non-match and the line falls through to the next renderer (and ultimately renders as the original raw line, or is dropped under `output.drop_unmatched`).

- [ ] **Step 3: Update `CHANGELOG.md`.** Under `## [Unreleased]`, add:

```markdown
### Renderer validity & multi-line rendering fixes
- **JSON/XML detection is validity-based**: a renderer matches only when its
  `json()`/`xml()` call actually parses. Lines that match a renderer's regex
  but carry non-JSON braces (e.g. IntelliJ's `{KEY=value}` macro dumps, or
  exception messages ending in `{…}`) now fall through and render as the
  original single line instead of being split/mangled.
- **TUI row invariant**: multi-line rendered text is stored as a list of rows,
  so an embedded newline can no longer wrap a row, push the header off-screen,
  or corrupt horizontal scrolling.
```

- [ ] **Step 4: Full verification**

Run:
```bash
go test ./...
go vet ./...
go test -race ./internal/render/ ./internal/tui/ ./cmd/log-listener/
```
Expected: all PASS / clean.

- [ ] **Step 5: Commit**

```bash
git add CLAUDE.md README.md CHANGELOG.md
git commit -m "phase 4: document validity-based detection and row invariant

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Self-Review Notes

- **Spec coverage:** Fix A — `renderJSON`/`renderXML` `(Part,bool)` + `Execute` `([]Part,bool)` + `Render` fall-through (Task 2); drop_unmatched edge (Task 2 test); locked-rule doc (Task 4). Fix B — decompose splits to a list of rows (Task 1). e2e (Task 3). Docs (Task 4). Parked centering tweak intentionally excluded.
- **Type consistency:** `Execute` returns `([]Part, bool)` and every call site (pipeline + 4 test sites) is updated; `renderJSON`/`renderXML` return `(Part, bool)` and are only called by `Execute`.
- **No placeholders.** Task 3 reuses the existing e2e harness (the one detail an implementer must read from the file), with an explicit BLOCKED fallback.
