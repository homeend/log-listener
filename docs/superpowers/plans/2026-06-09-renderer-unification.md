# Renderer Unification (#4) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Unify the `json` and `xml` render-functions under one pluggable `renderFunc` interface (`Name`/`Parse`/`Lines`) with a name-keyed registry, make the three dispatch sites (`ParseTemplate`/`Execute`/`DecomposeLines`) generic, and switch the render-call DSL syntax to `$name($N)`.

**Architecture:** Route json/xml through a registry *first* (keeping the old `json(` syntax, every commit green), then flip the parser to the `$`-prefixed syntax atomically with the in-repo template migration. The syntax (`json(` vs `$json(`) only affects `ParseTemplate`; `Execute`/`DecomposeLines` are syntax-agnostic, so they genericize independently of the breaking change.

**Tech Stack:** Go 1.26, `internal/render` package, no new dependencies. Tests via `go test ./...`.

**Spec:** `docs/superpowers/specs/2026-06-09-renderer-unification-design.md` (read it first — especially the `renderFunc` contract, the `$`-branch grammar, and the breaking-change migration list).

---

## Background the implementer needs

**Current shape (read these first):**
- `internal/render/template.go` — `Part{Type, Value}`, `templatePart{kind, text, group}`, the `partKind` enum (`partLiteral`/`partCapture`/`partRenderJSON`/`partRenderXML`), `ParseTemplate` (a char-scan loop), `parseRenderCall` (parses the `$N)` innards of a render-call), `startsWith` (used ONLY by the two render-call cases), and `Execute` (switches on `partKind`).
- `internal/render/json.go` — `renderJSON(s string) (Part, bool)`.
- `internal/render/xml.go` — `renderXML(s string) (Part, bool)` + `prettyXML`.
- `internal/render/decompose.go` — `DecomposeLines(ev Event) []DisplayLine` with a `switch p.Type` over `"text"`/`"json"`/`"xml"`, plus `expandTabs`.

**The `renderFunc` contract (target):**
```go
type renderFunc interface {
	Name() string                  // DSL keyword (after '$') AND Part.Type on success; ASCII [A-Za-z][A-Za-z0-9]*
	Parse(raw string) (Part, bool) // capture → Part; ok=false → renderer falls through; empty → text Part, ok=true
	Lines(v interface{}) []string  // a success Part's Value → finished rows; nil → no rows (all guards inside)
}
```

**Invariants to preserve exactly:**
- `Parse` `ok=false` → `Execute` returns `nil,false` → renderer falls through (first-match-wins).
- Empty/whitespace capture → `Parse` returns `Part{Type:"text", Value:""}, true` (an empty text Part), NOT a json/xml Part.
- On success `Part.Type == Name()`.
- `Lines` returns `[]string` (no bool); emptiness is slice length. The old guards (`MarshalIndent err==nil`; `v.(string)` ok) move *inside* `Lines`.
- `expandTabs` stays in `DecomposeLines` (uniform, type-agnostic display step).

**Build/test commands** (repo root):
- Package: `go test ./internal/render/`
- Full suite: `go test ./...`
- Vet/race/tags: `go vet ./...`, `go test -race ./internal/render/`, `go build -tags nomcp ./...`, `go build -tags nosse ./...`

**Commit discipline:** every task ends green (`go build ./... && go test ./...`).

---

## File structure

| File | Responsibility | Task |
|------|----------------|------|
| `internal/render/renderfunc.go` | NEW. `renderFunc` interface + `renderFuncs` registry + `registerRenderFunc`. | 1 |
| `internal/render/renderfunc_test.go` | NEW. Registry + (later) pluggability/typo tests. | 1, 4 |
| `internal/render/json.go` | `jsonRender` type (Name/Parse/Lines) + `init()`; old `renderJSON` removed. | 2 |
| `internal/render/xml.go` | `xmlRender` type (Name/Parse/Lines) + `init()`; `prettyXML` kept; old `renderXML` removed. | 2 |
| `internal/render/template.go` | `partKind`→`partRender`; `templatePart` gains `rf`; `Execute` generic; `ParseTemplate` routed through registry (T2) then flipped to `$name(` (T4); `startsWith` removed (T4). | 2, 4 |
| `internal/render/decompose.go` | Generic `registry[p.Type].Lines(...)` block path. | 3 |
| 8 `*_test.go` + 3 configs | Template-string migration `json($`→`$json($`, `xml($`→`$xml($`. | 4 |
| `README.md`, `CHANGELOG.md` | DSL docs + breaking-change entry. | 5 |

---

## Task 1: `renderFunc` interface + registry

**Files:**
- Create: `internal/render/renderfunc.go`
- Create: `internal/render/renderfunc_test.go`

- [ ] **Step 1: Write the failing registry test**

Create `internal/render/renderfunc_test.go`:

```go
package render

import "testing"

// fakeRF is a throwaway renderFunc for registry tests.
type fakeRF struct{ name string }

func (f fakeRF) Name() string                 { return f.name }
func (fakeRF) Parse(raw string) (Part, bool)  { return Part{Type: "text", Value: raw}, true }
func (fakeRF) Lines(v interface{}) []string   { return nil }

func TestRegisterAndLookup(t *testing.T) {
	registerRenderFunc(fakeRF{name: "test_reg_lookup"})
	if renderFuncs["test_reg_lookup"] == nil {
		t.Fatal("registered renderFunc not found in registry")
	}
}

func TestDuplicateRegistrationPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate registration should panic")
		}
	}()
	registerRenderFunc(fakeRF{name: "test_dup"})
	registerRenderFunc(fakeRF{name: "test_dup"}) // must panic
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test -run 'TestRegisterAndLookup|TestDuplicateRegistrationPanics' ./internal/render/`
Expected: FAIL to compile — `undefined: renderFunc`, `renderFuncs`, `registerRenderFunc`.

- [ ] **Step 3: Implement `renderfunc.go`**

Create `internal/render/renderfunc.go`:

```go
package render

// renderFunc is a named, pluggable render-call usable in the template DSL as
// $name($N). Name() is BOTH the DSL keyword and the Part.Type produced on a
// successful parse, so ParseTemplate, Execute, and DecomposeLines can all
// dispatch by name through the registry. Implementations live one-per-file and
// self-register via init().
type renderFunc interface {
	// Name is the DSL keyword (after '$') and the Part.Type emitted on a
	// successful non-empty parse. Must be ASCII [A-Za-z][A-Za-z0-9]*.
	Name() string

	// Parse turns a capture into a Part. ok=false means "not my type" — the
	// renderer falls through (first-match-wins). Empty/whitespace input returns
	// an empty text Part with ok=true. On success: Part{Type: Name(), Value}, true.
	Parse(raw string) (Part, bool)

	// Lines turns a successful Part's Value into finished display rows (already
	// split; every type-specific guard inside). nil/empty → no rows.
	Lines(v interface{}) []string
}

// renderFuncs is the name-keyed registry, populated by each render file's init().
var renderFuncs = map[string]renderFunc{}

// registerRenderFunc adds r; a duplicate name panics at init so a collision is
// caught at startup instead of being silently shadowed.
func registerRenderFunc(r renderFunc) {
	if _, dup := renderFuncs[r.Name()]; dup {
		panic("render: duplicate renderFunc " + r.Name())
	}
	renderFuncs[r.Name()] = r
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test -run 'TestRegisterAndLookup|TestDuplicateRegistrationPanics' ./internal/render/`
Expected: PASS (2 tests).

- [ ] **Step 5: Vet + package green**

Run: `go vet ./internal/render/ && go test ./internal/render/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/render/renderfunc.go internal/render/renderfunc_test.go
git commit -m "feat(render): add renderFunc interface + name-keyed registry (#4)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 2: Convert json/xml to the interface; route `Execute` through the registry (old syntax preserved)

Replace the free `renderJSON`/`renderXML` with self-registering `jsonRender`/`xmlRender` types, and make `Execute` dispatch through a single `partRender` case via the registry. `ParseTemplate` still detects the OLD `json(`/`xml(` syntax (the breaking flip is Task 4), but now stores the resolved `rf`. **Behavior is identical; all existing tests stay green unchanged.**

**Files:**
- Modify: `internal/render/json.go`, `internal/render/xml.go`, `internal/render/template.go`

- [ ] **Step 1: Rewrite `json.go` to the interface**

Replace the entire contents of `internal/render/json.go`:

```go
package render

import (
	"encoding/json"
	"strings"
)

type jsonRender struct{}

func init() { registerRenderFunc(jsonRender{}) }

func (jsonRender) Name() string { return "json" }

// Parse decodes the capture as JSON. ok=false means it is not valid JSON (the
// renderer falls through). Empty input is an empty text Part.
func (jsonRender) Parse(raw string) (Part, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return Part{Type: "text", Value: ""}, true
	}
	var v interface{}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return Part{Type: "text", Value: s}, false
	}
	return Part{Type: "json", Value: v}, true
}

// Lines pretty-prints the decoded value into block rows. A marshal failure
// (essentially unreachable for a value that came from Unmarshal) yields no rows.
func (jsonRender) Lines(v interface{}) []string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil
	}
	return strings.Split(string(b), "\n")
}
```

- [ ] **Step 2: Rewrite `xml.go` to the interface**

Replace the contents of `internal/render/xml.go` (keep `prettyXML` exactly as-is):

```go
package render

import (
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

type xmlRender struct{}

func init() { registerRenderFunc(xmlRender{}) }

func (xmlRender) Name() string { return "xml" }

// Parse pretty-prints the capture as XML. ok=false means it is not valid XML
// (the renderer falls through). Empty input is an empty text Part.
func (xmlRender) Parse(raw string) (Part, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return Part{Type: "text", Value: ""}, true
	}
	pretty, err := prettyXML(s)
	if err != nil {
		return Part{Type: "text", Value: s}, false
	}
	return Part{Type: "xml", Value: pretty}, true
}

// Lines splits the pretty-printed XML string into block rows. A non-string
// value (shouldn't happen for an xml Part) yields no rows.
func (xmlRender) Lines(v interface{}) []string {
	s, ok := v.(string)
	if !ok {
		return nil
	}
	return strings.Split(s, "\n")
}

func prettyXML(in string) (string, error) {
	dec := xml.NewDecoder(strings.NewReader(in))
	var out strings.Builder
	enc := xml.NewEncoder(&out)
	enc.Indent("", "  ")
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		if err := enc.EncodeToken(tok); err != nil {
			return "", err
		}
	}
	if err := enc.Flush(); err != nil {
		return "", err
	}
	return out.String(), nil
}
```

- [ ] **Step 3: Update `template.go` — partKind, templatePart, ParseTemplate cases, Execute**

In `internal/render/template.go`:

(a) Replace the two render constants in the `partKind` block:
```go
const (
	partLiteral partKind = iota
	partCapture
	partRenderJSON
	partRenderXML
)
```
with:
```go
const (
	partLiteral partKind = iota
	partCapture
	partRender
)
```

(b) Add an `rf` field to `templatePart`:
```go
type templatePart struct {
	kind  partKind
	text  string
	group int
	rf    renderFunc // set when kind == partRender
}
```

(c) In `ParseTemplate`, replace the two render-call cases (still detecting the OLD syntax, now storing `rf`):
```go
		case startsWith(src, i, "json("):
			flush()
			n, end, err := parseRenderCall(src, i+len("json("))
			if err != nil {
				return nil, err
			}
			t.parts = append(t.parts, templatePart{kind: partRender, group: n, rf: renderFuncs["json"]})
			i = end
		case startsWith(src, i, "xml("):
			flush()
			n, end, err := parseRenderCall(src, i+len("xml("))
			if err != nil {
				return nil, err
			}
			t.parts = append(t.parts, templatePart{kind: partRender, group: n, rf: renderFuncs["xml"]})
			i = end
```

(d) In `Execute`, replace the two render cases with one:
```go
		case partRender:
			flushText()
			part, ok := p.rf.Parse(capture(p.group))
			if !ok {
				return nil, false
			}
			parts = append(parts, part)
```

- [ ] **Step 4: Build + full existing suite green (behavior unchanged, old syntax)**

Run: `go build ./... && go test ./internal/render/ && go vet ./internal/render/`
Expected: PASS — every existing render test passes UNCHANGED (templates still use `json($1)`; routing now goes through the registry).

- [ ] **Step 5: Whole-repo green**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/render/json.go internal/render/xml.go internal/render/template.go
git commit -m "refactor(render): route json/xml through the renderFunc registry (old syntax intact) (#4)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 3: Genericize `DecomposeLines` via `Lines`

Replace the per-type `case "json"`/`case "xml"` block handling with a generic registry lookup. The `jsonRender.Lines`/`xmlRender.Lines` from Task 2 reproduce the exact marshal/assert/split, so existing decompose tests stay green; add tests for the guard paths (which were previously untested).

**Files:**
- Modify: `internal/render/decompose.go`
- Modify: `internal/render/decompose_test.go`

- [ ] **Step 1: Add guard-path tests**

Append to `internal/render/decompose_test.go`:

```go
func TestDecomposeXMLNonStringValueProducesNoBlock(t *testing.T) {
	// An xml Part whose Value isn't a string must contribute zero rows (not a
	// blank row). Only the text head should remain.
	got := DecomposeLines(Event{Rendered: []Part{
		{Type: "text", Value: "head"},
		{Type: "xml", Value: 123}, // malformed: not a string
	}})
	if len(got) != 1 || got[0].Text != "head" || got[0].IsCont {
		t.Fatalf("bad xml value must yield only the head row, got %+v", got)
	}
}

func TestDecomposeUnknownPartTypeIsIgnored(t *testing.T) {
	got := DecomposeLines(Event{Rendered: []Part{
		{Type: "text", Value: "head"},
		{Type: "nope", Value: "x"}, // unregistered type
	}})
	if len(got) != 1 || got[0].Text != "head" {
		t.Fatalf("unknown part type must be ignored, got %+v", got)
	}
}
```

- [ ] **Step 2: Run to verify they fail (old decompose mishandles these)**

Run: `go test -run 'TestDecomposeXMLNonStringValueProducesNoBlock|TestDecomposeUnknownPartTypeIsIgnored' ./internal/render/`
Expected: the unknown-type test passes already (old switch has no default), but the xml-non-string test must pass too under the OLD code (old `case "xml"` already guards `v.(string)`), so BOTH may pass. That's fine — they pin behavior. If both already pass, proceed (they guard the refactor in Step 3).

- [ ] **Step 3: Genericize the block loop in `decompose.go`**

In `DecomposeLines`, replace the part loop:
```go
	for _, p := range ev.Rendered {
		switch p.Type {
		case "text":
			if s, ok := p.Value.(string); ok {
				textBuf.WriteString(s)
			}
		case "json":
			if b, err := json.MarshalIndent(p.Value, "", "  "); err == nil {
				blocks = append(blocks, string(b))
			}
		case "xml":
			if s, ok := p.Value.(string); ok {
				blocks = append(blocks, s)
			}
		}
	}
```
with:
```go
	for _, p := range ev.Rendered {
		if p.Type == "text" {
			if s, ok := p.Value.(string); ok {
				textBuf.WriteString(s)
			}
			continue
		}
		if rf, ok := renderFuncs[p.Type]; ok {
			blockLines = append(blockLines, rf.Lines(p.Value)...)
		}
	}
```

Rename the accumulator and its consumer: change the declaration `var blocks []string` to `var blockLines []string`, and replace the block-appending loop:
```go
	for _, b := range blocks {
		for _, ln := range strings.Split(b, "\n") {
			out = append(out, DisplayLine{Text: expandTabs(ln), IsCont: true})
		}
	}
```
with (the lines are already split by `Lines`):
```go
	for _, ln := range blockLines {
		out = append(out, DisplayLine{Text: expandTabs(ln), IsCont: true})
	}
```

Then remove the now-unused `"encoding/json"` import from `decompose.go` (the file no longer marshals).

- [ ] **Step 4: Build + decompose tests green**

Run: `go build ./... && go test ./internal/render/`
Expected: PASS — existing decompose tests unchanged + the two new guard tests pass; `DecomposeLines` now contains no `json`/`xml`/`MarshalIndent`.

- [ ] **Step 5: Whole-repo green**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/render/decompose.go internal/render/decompose_test.go
git commit -m "refactor(render): DecomposeLines dispatches blocks via renderFunc.Lines (#4)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 4: Flip the parser to `$name($N)` + migrate all in-repo templates (atomic breaking change)

This is the breaking commit. Replace the OLD `json(`/`xml(` detection with the generic `$name(` branch, then migrate every in-repo template literal in the SAME commit (the old syntax stops working, so tests/configs must flip together). Add the new parser tests.

**Files:**
- Modify: `internal/render/template.go`
- Modify (template migration, `json($`→`$json($` and `xml($`→`$xml($`): `internal/render/render_test.go`, `main_test.go`, `e2e_test.go`, `internal/catalog/schema_test.go`, `internal/catalog/resolve_test.go`, `internal/config/emit_test.go`, `internal/config/yaml_test.go`, `internal/tui/multiline_test.go`, `log-listener.example.yml`, `goland-logs.yml`, `internal/catalog/catalog.yml`
- Modify: `internal/render/renderfunc_test.go` (new parser tests)

- [ ] **Step 1: Add an `isIdentChar` helper and flip the parser**

In `internal/render/template.go`:

(a) Delete the two `case startsWith(src, i, "json(")` / `"xml("` blocks from `ParseTemplate`.

(b) Extend the `$` case. Replace:
```go
		case c == '$' && i+1 < len(src):
			nx := src[i+1]
			switch {
			case nx == '$':
				lit.WriteByte('$')
				i += 2
			case nx >= '0' && nx <= '9':
				flush()
				j := i + 1
				for j < len(src) && src[j] >= '0' && src[j] <= '9' {
					j++
				}
				n, _ := strconv.Atoi(src[i+1 : j])
				t.parts = append(t.parts, templatePart{kind: partCapture, group: n})
				i = j
			default:
				return nil, fmt.Errorf("template: invalid escape $%c at %d", nx, i)
			}
```
with:
```go
		case c == '$' && i+1 < len(src):
			nx := src[i+1]
			switch {
			case nx == '$':
				lit.WriteByte('$')
				i += 2
			case nx >= '0' && nx <= '9':
				flush()
				j := i + 1
				for j < len(src) && src[j] >= '0' && src[j] <= '9' {
					j++
				}
				n, _ := strconv.Atoi(src[i+1 : j])
				t.parts = append(t.parts, templatePart{kind: partCapture, group: n})
				i = j
			case isLetter(nx):
				// $name($N) render-call. name must be a registered renderFunc.
				flush()
				j := i + 1
				for j < len(src) && isIdentChar(src[j]) {
					j++
				}
				name := src[i+1 : j]
				rf := renderFuncs[name]
				if rf == nil {
					return nil, fmt.Errorf("template: unknown render function $%s at %d", name, i)
				}
				if j >= len(src) || src[j] != '(' {
					return nil, fmt.Errorf("template: expected ( after $%s at %d", name, j)
				}
				n, end, err := parseRenderCall(src, j+1)
				if err != nil {
					return nil, err
				}
				t.parts = append(t.parts, templatePart{kind: partRender, group: n, rf: rf})
				i = end
			default:
				return nil, fmt.Errorf("template: invalid escape $%c at %d", nx, i)
			}
```

(c) Delete the now-unused `startsWith` function. Add the two helpers (place near `parseRenderCall`):
```go
func isLetter(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

func isIdentChar(b byte) bool {
	return isLetter(b) || b >= '0' && b <= '9'
}
```

- [ ] **Step 2: Migrate the render-package tests + add new parser tests**

In `internal/render/render_test.go`, apply `json($`→`$json($` and `xml($`→`$xml($` to every template literal, INCLUDING the negative-error cases at lines ~62–64 (`json($)`→`$json($)`, `json($1`→`$json($1`, `xml($`→`$xml($`) so they keep testing render-call parse errors under the new syntax. (Concretely the templates at ~12, 45, 62, 63, 64, 171, 178, 318, 445, 470.)

Append the new parser tests to `internal/render/renderfunc_test.go`:

```go
// upRender is a throwaway renderFunc proving pluggability: $up($N) uppercases
// the capture into a text Part, with ZERO edits to ParseTemplate/Execute.
type upRender struct{}

func (upRender) Name() string { return "up" }
func (upRender) Parse(raw string) (Part, bool) {
	return Part{Type: "text", Value: strings.ToUpper(raw)}, true
}
func (upRender) Lines(v interface{}) []string { return nil }

func TestPluggableRenderFuncParsesAndExecutes(t *testing.T) {
	registerRenderFunc(upRender{})
	tpl, err := ParseTemplate(`$up($1)`)
	if err != nil {
		t.Fatalf("ParseTemplate($up): %v", err)
	}
	parts, ok := tpl.Execute([]string{"full", "hello"})
	if !ok || len(parts) != 1 || parts[0].Value != "HELLO" {
		t.Fatalf("pluggable func: got %+v ok=%v", parts, ok)
	}
}

func TestUnknownRenderFuncErrors(t *testing.T) {
	if _, err := ParseTemplate(`$jsno($1)`); err == nil {
		t.Fatal("unknown render function must error (typo detection)")
	}
}

func TestLiteralWordsAreNotRenderCalls(t *testing.T) {
	// Words that aren't $-prefixed are plain literal text, never render-calls.
	for _, src := range []string{`format($1)`, `jsonish($1)`, `level: json`} {
		tpl, err := ParseTemplate(src)
		if err != nil {
			t.Fatalf("%q should parse as literal, got error %v", src, err)
		}
		parts, ok := tpl.Execute([]string{"full", "X"})
		if !ok {
			t.Fatalf("%q execute failed", src)
		}
		for _, p := range parts {
			if p.Type == "json" || p.Type == "xml" {
				t.Fatalf("%q must not produce a render part, got %+v", src, parts)
			}
		}
	}
}
```

Add `"strings"` to `renderfunc_test.go`'s imports.

- [ ] **Step 3: Migrate the remaining test files and configs**

Apply the same `json($`→`$json($` / `xml($`→`$xml($` swap to every template literal in:
`main_test.go`, `e2e_test.go`, `internal/catalog/schema_test.go`, `internal/catalog/resolve_test.go`, `internal/config/emit_test.go`, `internal/config/yaml_test.go`, `internal/tui/multiline_test.go`, `log-listener.example.yml`, `goland-logs.yml`, `internal/catalog/catalog.yml`.

Find every occurrence first:
```bash
grep -rnF -e 'json($' -e 'xml($' --include='*_test.go' --include='*.yml' --include='*.yaml' . | grep -v docs/
```
Swap each (the `($` suffix guarantees you only touch render-calls, never `json.Marshal(` / `encoding/json`). In `schema_test.go`/`resolve_test.go` migrate BOTH the input template string AND any asserted expected value (e.g. `c.Renderers["json-line"].Template != "json($1)"` → `!= "$json($1)"`).

- [ ] **Step 4: Build + run the render package**

Run: `go build ./... && go test ./internal/render/`
Expected: PASS — existing tests (now `$json($1)`) green, plus the 3 new parser tests.

- [ ] **Step 5: Whole-repo green (the migration is complete)**

Run: `go test ./...`
Expected: PASS. If a test fails with rendered output showing literal `json(` text, a template literal was missed — find it with the grep in Step 3 and migrate it.

- [ ] **Step 6: Vet + race + tagged builds**

Run: `go vet ./... && go test -race ./internal/render/ && go build -tags nomcp ./... && go build -tags nosse ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "feat(render)!: \$-prefixed render-call syntax; unknown funcs error (#4)

BREAKING: render-call templates change from json(\$1)/xml(\$1) to
\$json(\$1)/\$xml(\$1). Render-call detection moves into the \$ sigil branch, so
literal words can never be mistaken for calls and an unknown \$name() is a parse
error (typo detection). Migrated catalog, example configs, and all tests.

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 5: Migrate user docs + CHANGELOG + final verification

**Files:**
- Modify: `README.md`, `CHANGELOG.md`

- [ ] **Step 1: Migrate `README.md`**

Update every render-call example and the DSL reference table to the `$`-prefixed form:
- `json($N)` → `$json($N)`, `xml($N)` → `$xml($N)` in the prose (around the "output" bullet ~28–29), the example configs (~360, 366, 369, 476), and the DSL table rows (~448–449: `` `$json($N)` `` / `` `$xml($N)` ``).
Find them: `grep -nF -e 'json($' -e 'xml($' README.md`.

- [ ] **Step 2: Add a breaking-change CHANGELOG entry**

Under `## [Unreleased]` in `CHANGELOG.md`, add at the top:
```markdown
### Changed (BREAKING): render-call DSL syntax is now `$`-prefixed
- Template render-calls change from `json($N)`/`xml($N)` to `$json($N)`/`$xml($N)`.
  Render-calls now live behind the `$` sigil (like `$N` captures), so literal
  text can never be mistaken for a call, and an unknown `$name(...)` is a parse
  error instead of silent literal output. Update any custom config templates by
  prefixing `json(`/`xml(` with `$`.

### Internal: pluggable `renderFunc` interface
- `json` and `xml` are unified under one `renderFunc` interface (`Name`/`Parse`/
  `Lines`) with a name-keyed registry; `ParseTemplate`, `Execute`, and
  `DecomposeLines` dispatch generically. A new render type is one self-
  registering file — no edits to the dispatch sites. Exception detection is
  unchanged (a different abstraction, already behind its own `Processor` interface).
```

- [ ] **Step 3: Final full verification**

Run: `go test ./... && go vet ./... && go test -race ./... && go build -tags nomcp ./... && go build -tags nosse ./... && go build ./...`
Expected: all PASS.

- [ ] **Step 4: Confirm the dispatch sites are clean**

Run: `grep -nF -e '"json"' -e '"xml"' -e 'MarshalIndent' internal/render/template.go internal/render/decompose.go`
Expected: empty (no `json`/`xml` literals or marshalling left in the dispatch sites; they live only in `json.go`/`xml.go`).

- [ ] **Step 5: Commit**

```bash
git add README.md CHANGELOG.md
git commit -m "docs(render): document \$-prefixed render-call syntax + breaking change (#4)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Self-review notes (reconciled against the spec)

- **Spec coverage:** interface+registry (T1), json/xml self-registering impls owning parse+Lines (T2), Execute generic (T2), DecomposeLines generic (T3), `$`-prefix grammar + unknown-name error + literal safety (T4), pluggability proof (T4), migration of catalog/configs/tests (T4) + docs/CHANGELOG (T5), behavior-preservation (existing tests green through T2/T3, migrated-but-equivalent in T4). Every spec section maps to a task.
- **Type consistency:** `renderFunc{Name() string; Parse(string)(Part,bool); Lines(interface{})[]string}`; `renderFuncs map[string]renderFunc`; `registerRenderFunc`; `templatePart.rf`; `partRender` — used identically across tasks.
- **Green floor:** T1–T3 keep the OLD syntax and existing tests pass unchanged; only T4 changes syntax, and it migrates every in-repo template in the same commit (the only way a breaking syntax change stays green).
- **Migration precision:** the `json($`/`xml($` fixed-string pattern (note the `($`) only matches render-calls, never `json.Marshal(`/`encoding/json`.
- **Watch-point:** `render_test.go:62–64` are negative parse-error tests — migrate them too so they exercise the new render-call error paths, not an unrelated `$)` escape error.
