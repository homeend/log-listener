# Renderer Unification: a pluggable `renderFunc` interface (#4) — Design

**Date:** 2026-06-09
**Status:** Draft — pending user review of this written spec.
**Scope:** `internal/render` (the template DSL + render-calls) and the
`$`-prefixed syntax migration across the in-repo configs/catalog/docs/tests.
**Breaking:** YES — the render-call syntax changes from `json($1)` to
`$json($1)` (and `xml($1)` → `$xml($1)`). Pre-1.0 (v0.1.0); now — while the
parser is being rewritten — is the cheapest time to make it.

## Context

Roadmap item #4 (see `[[plugin-architecture-roadmap]]`): "unify the
json/xml/exception renderers under one interface." Investigation reframed the
scope:

- **`exception` is excluded.** It is a *block annotator* —
  `Process(b *Block, lines []Line)` mutating `Block.Exception`, in
  `internal/blocks` — already behind its own `Processor` interface with a
  sibling (the language-guesser). It is a different abstraction from the
  json/xml *render-functions* (`string → Part`). Forcing them together would
  relocate complexity, not reduce it. Exception stays exactly as-is.
- **`json` + `xml` are the genuine pair.** Both are `func(string) (Part, bool)`
  dispatched through the template DSL, with their type-specific logic currently
  scattered across three sites: `ParseTemplate` (hardcoded `json(`/`xml(`),
  `Execute` (`partRenderJSON`/`partRenderXML`), and `DecomposeLines` (`case
  "json"`/`case "xml"`). This is what gets unified.

This is the deletion-/cohesion-focused half of the current effort. Honest
expectation: **net LOC is roughly flat** (the parse/display logic per type is
irreducible; a registry/interface costs lines). The win is *cohesion and
extensibility*: each render type becomes a single self-contained file, and the
three dispatch sites carry **zero** type-specific knowledge — adding a new
render type (yaml, base64, …) is one new file and one `init()` registration,
touching none of the dispatch sites.

## Goals

- One `renderFunc` interface owning everything about a render type: its DSL
  keyword, how it parses a capture into a typed `Part`, and how that `Part`'s
  value becomes finished display lines.
- The three dispatch sites (`ParseTemplate`, `Execute`, `DecomposeLines`)
  dispatch generically through a name-keyed registry; none mentions `json`,
  `xml`, marshalling, type assertions, or newline-splitting.
- `$`-prefixed render-call syntax so render-calls are sigil-marked (like
  captures), removing all literal-vs-keyword parsing ambiguity by construction,
  and enabling typo detection (`$jsno(…)` → error, not silent literal text).

## Non-goals

- Touching `internal/blocks` / the exception or language-guess processors.
- Changing what json/xml *produce* (the rendered output for a migrated template
  is identical to today's output for the old template).
- Adding new render types in this slice (the throwaway in tests proves
  pluggability; shipping yaml/base64 is future work).
- A compatibility shim for the old bare-word syntax (explicitly rejected — it
  would re-introduce the bare-word detection path and defeat the point).

## The `renderFunc` interface + registry

New file `internal/render/renderfunc.go`:

```go
// renderFunc is a named, pluggable render-call usable in the template DSL as
// $name($N). Name() is BOTH the DSL keyword and the Part.Type produced on a
// successful parse, so ParseTemplate, Execute, and DecomposeLines can all
// dispatch by name through the registry. Implementations live one-per-file and
// self-register via init().
type renderFunc interface {
	// Name is the DSL keyword (after the '$') and the Part.Type emitted on a
	// successful, non-empty parse. Must be ASCII [A-Za-z][A-Za-z0-9]*.
	Name() string

	// Parse turns a capture string into a Part. ok=false means "this capture is
	// not my type" — the renderer does not match and falls through (first-match-
	// wins). On empty/whitespace input Parse returns an empty text Part with
	// ok=true (matching today's behavior). On success it returns
	// Part{Type: Name(), Value: <typed value>}, true.
	Parse(raw string) (Part, bool)

	// Lines turns a successful Part's Value into finished display rows — already
	// split, with every type-specific guard inside. A nil/empty result
	// contributes no rows. (Replaces the per-type marshal/assert/split logic
	// that used to live in DecomposeLines; emptiness is expressed by slice
	// length, so there is no "is an empty string a real result?" ambiguity.)
	Lines(v interface{}) []string
}

var renderFuncs = map[string]renderFunc{}

// registerRenderFunc adds r to the registry; duplicate names panic at init so a
// collision is caught at startup, not silently shadowed.
func registerRenderFunc(r renderFunc) {
	if _, dup := renderFuncs[r.Name()]; dup {
		panic("render: duplicate renderFunc " + r.Name())
	}
	renderFuncs[r.Name()] = r
}
```

### json.go and xml.go become self-registering implementations

`json.go` (the existing `renderJSON` body moves into `Parse`; the
`MarshalIndent`+split that lived in `DecomposeLines` moves into `Lines`):

```go
type jsonRender struct{}

func init() { registerRenderFunc(jsonRender{}) }

func (jsonRender) Name() string { return "json" }

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

func (jsonRender) Lines(v interface{}) []string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil // marshal guard lives here
	}
	return strings.Split(string(b), "\n") // the split lives here
}
```

`xml.go` (the `prettyXML` helper stays as-is; `Parse` wraps it; `Lines` owns the
type assertion + split):

```go
type xmlRender struct{}

func init() { registerRenderFunc(xmlRender{}) }

func (xmlRender) Name() string { return "xml" }

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

func (xmlRender) Lines(v interface{}) []string {
	s, ok := v.(string)
	if !ok {
		return nil // type-assert guard lives here
	}
	return strings.Split(s, "\n")
}
```

## The `$`-prefixed DSL (breaking)

### Grammar change

- **Old:** `literal | $N | json($N) | xml($N)` — render-calls were bare keywords.
- **New:** `literal | $$ | $N | $name($N)` — render-calls are `$`-prefixed.
  `name` is `[A-Za-z][A-Za-z0-9]*` and must be a registered renderFunc.

### Parser (`ParseTemplate`)

The two hardcoded `startsWith(src,i,"json(")` / `"xml("` cases are **removed**.
Render-call detection moves into the existing `$` branch:

```
at '$', peek src[i+1]:
   '$'      -> literal '$'                         (escape, unchanged)
   [0-9]    -> capture group $N                    (unchanged)
   [A-Za-z] -> RENDER-CALL (new):
                 name = scan [A-Za-z][A-Za-z0-9]*
                 require '('  ->  parseRenderCall(after '(')   (REUSED verbatim)
                 renderFuncs[name]?  hit  -> templatePart{kind: partRender, group, rf}
                                     miss -> error: "unknown render function $<name>"
   else     -> error: "invalid escape $<c>"        (unchanged shape)
```

Because a literal word never begins with `$`, text like `format(`, `jsonish(`,
or a bare `json` is *trivially* literal — the registry is consulted only after a
`$`. The literal-vs-keyword ambiguity cannot occur. `parseRenderCall` (which
parses the `$N)` innards) is reused unchanged.

`templatePart` gains an `rf renderFunc` field; the `partKind` enum drops
`partRenderJSON`/`partRenderXML` and gains a single `partRender`.

### Executor (`Execute`)

The two `partRenderJSON`/`partRenderXML` cases collapse to one:

```go
case partRender:
	flushText()
	part, ok := p.rf.Parse(capture(p.group))
	if !ok {
		return nil, false // renderer falls through (first-match-wins) — unchanged
	}
	parts = append(parts, part)
```

### Decomposer (`DecomposeLines`)

The `case "json"`/`case "xml"` arms collapse to a generic lookup; the `"text"`
arm stays explicit (text is not a renderFunc — it is the head + inline
continuation rows):

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
// ... head/continuation assembly + expandTabs unchanged ...
```

`expandTabs` stays in the decomposer: it is a uniform, type-agnostic
display-width step applied to *every* physical row (text and block alike), not
per-type behavior.

## Error handling & invariants

- **`Parse` `ok=false` → renderer falls through.** The locked first-match-wins
  rule. Unchanged; `Execute` returns `nil,false`.
- **`Lines` returns `[]string`; nil/empty → no rows.** Emptiness is slice
  length, never a sentinel string. The two old guards (`MarshalIndent err==nil`,
  `v.(string)` ok) now live inside `jsonRender.Lines` / `xmlRender.Lines`.
- **`Part.Type == Name()` on success.** The dispatch key. Documented invariant;
  the empty-input case deliberately returns `Type:"text"` (handled by the text
  path, never reaching `Lines`).
- **Unknown `$name(` is an error**, not silent literal text — typo detection,
  enabled by the sigil.
- **Duplicate registration panics at `init`.**

## Breaking change & migration

Render-call syntax `json($N)` → `$json($N)`, `xml($N)` → `$xml($N)` (prepend one
`$`). In-repo migration (mechanical):

- **Shipped defaults:** `internal/catalog/catalog.yml`.
- **Example configs:** `log-listener.example.yml`, `goland-logs.yml`.
- **Tests/fixtures:** `internal/render/render_test.go`,
  `internal/render/decompose_test.go`, `e2e_test.go`, `main_test.go`,
  `internal/catalog/resolve_test.go`, `internal/catalog/schema_test.go`, and any
  other `*_test.go` the grep `json($|xml($` surfaces at implementation time.
- **User docs:** `README.md` (the DSL reference table + examples), `CHANGELOG.md`
  (a breaking-change entry under Unreleased).

Historical planning docs (`PLAN.md`, `docs/superpowers/plans/*`,
`docs/superpowers/specs/*` other than this one) are left as-is — they record
what was true then.

The catalog passes `Template` strings through verbatim (`resolve.go` copies
`r.Template`); there is **no** code that generates render-call strings, so the
migration is config/test/doc only plus the parser/interface rewrite.

## Behavior-preservation contract & tests

The rendered *output* for a migrated template is identical to today's output for
the old template. Existing `render_test.go` / `decompose_test.go` keep their
assertions; only the template literals migrate to the `$`-prefixed form.

New tests (`renderfunc_test.go` + additions):

- **Pluggability:** register a throwaway renderFunc (e.g. `up` that uppercases
  its capture into a text Part) in the test; assert `$up($1)` parses and renders
  through it — with **zero** edits to `ParseTemplate`/`Execute`/`DecomposeLines`.
- **Typo detection:** `ParseTemplate("$jsno($1)")` returns an error naming the
  unknown function.
- **Literal safety (now trivial, still pinned):** `format($1)`, `jsonish($1)`,
  and a bare `json` (no `$`) parse as literal text with no render part.
- **`Lines` guard paths:** `xmlRender.Lines(123)` (non-string) → nil; a json
  value that fails to marshal → nil (no spurious blank row).
- **Duplicate registration panics** (recover-based test).
- **Empty-capture path:** `$json($N)` with an empty capture → an empty text part
  (no json block), matching today.

Plus `go test ./...`, `go vet ./...`, `go test -race ./...`, and the
`-tags nomcp` / `-tags nosse` builds green.

## Honest LOC note

Removes: 2 `partKind` constants, the two `startsWith` parser cases, the two
`Execute` cases, the two `DecomposeLines` arms (~30–40 lines of duplicated
dispatch). Adds: the interface + registry (~25 lines), `Name`/`Lines` methods on
json+xml (~20 lines), and the generic `$name(` parser branch (~15 lines). **Net
roughly flat to slightly positive.** The deliverable is cohesion (each type
sealed in one file), extensibility (new type = one file), and a more robust
parser (ambiguity gone by construction) — not a line-count reduction.

## Success criteria

- A `renderFunc` interface + name-keyed registry exists; `json` and `xml` are
  self-registering implementations that own their parse + display (`Lines`) +
  all type-specific guards.
- `ParseTemplate`, `Execute`, and `DecomposeLines` contain no `json`/`xml`
  literals, no marshalling, no type assertions, no newline-splitting — they
  dispatch only through the registry.
- Render-call syntax is `$name($N)`; unknown names error; literal words are
  never mistaken for calls.
- All in-repo configs/catalog/docs/tests migrated; full suite + vet + race +
  tagged builds green; rendered output unchanged for migrated templates.
- Adding a render type is demonstrably one file + one `init()` (proven by the
  throwaway test func).
