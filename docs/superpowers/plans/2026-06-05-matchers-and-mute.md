# Matchers & Mute Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a reusable `matchers` library plus a `mute` section that drops matching log lines before any sink, and let renderers reference a named matcher instead of an inline `line_regex`.

**Architecture:** A new `internal/match` package owns a `Matcher` (literal-or-regex over line/name/path, AND semantics). The `render` package unifies renderers on `*match.Matcher`, adds a `MuteRule` type, and enforces mute as the first step of `Pipeline.Render`. `config` carries the new YAML sections through to `Config`; `cmd` threads them into both `NewPipeline` call sites.

**Tech Stack:** Go 1.26, `regexp`, `gopkg.in/yaml.v3`, standard `testing`.

**Spec:** `docs/superpowers/specs/2026-06-05-matchers-and-mute-design.md`

---

## File Structure

- **Create** `internal/match/match.go` — `Spec`, `Matcher`, `Compile`, `Match`, `HasLineRegex`.
- **Create** `internal/match/match_test.go` — unit tests for the above.
- **Modify** `internal/config/yaml.go` — add `MatcherSpec`, `MuteSpec`; extend `File` (`Matchers`, `Mute`) and `Renderer` (`Matcher`); carry through in `mergeYAMLInto`.
- **Modify** `internal/config/cli.go` — extend `Config` (`Matchers`, `MuteSpecs`) and `RendererSpec` (`Matcher`).
- **Modify** `internal/render/pipeline.go` — unify `Renderer` on `*match.Matcher`; change `Compile`/`Match` signatures; add `MuteRule`, `Pipeline.mutes`, mute check in `Render`, extend `NewPipeline`; add `toMatchSpec` helper.
- **Modify** `internal/render/render_test.go` — new tests for matcher-backed renderers and mute.
- **Modify** `cmd/log-listener/main.go` — update both `render.NewPipeline` call sites.
- **Modify** `README.md`, `CHANGELOG.md` — document the feature.

Note on validation placement: renderer/mute/matcher *reference* validation happens at `render.NewPipeline` (pipeline build at startup), consistent with where `line_regex` is validated today. Duplicate matcher names are rejected by YAML map decoding. The repo uses `make test`, `make vet`, `make race`.

---

## Task 1: `internal/match` package

**Files:**
- Create: `internal/match/match.go`
- Test: `internal/match/match_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package match

import "testing"

func TestCompileRequiresAtLeastOneCriterion(t *testing.T) {
	if _, err := Compile(Spec{}); err == nil {
		t.Fatal("expected error for empty matcher")
	}
}

func TestCompileRejectsLiteralAndRegexSameDimension(t *testing.T) {
	cases := []Spec{
		{Line: "a", LineRegex: "a"},
		{Name: "a", NameRegex: "a"},
		{Path: "a", PathRegex: "a"},
	}
	for _, s := range cases {
		if _, err := Compile(s); err == nil {
			t.Fatalf("expected error for both literal+regex set: %+v", s)
		}
	}
}

func TestCompileRejectsInvalidRegex(t *testing.T) {
	if _, err := Compile(Spec{LineRegex: "a[b"}); err == nil {
		t.Fatal("expected error for invalid regex")
	}
}

func TestMatchNameLiteralIsExactBasename(t *testing.T) {
	m, err := Compile(Spec{Name: "idea.log"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Match("/var/log/idea.log", "anything"); !ok {
		t.Fatal("exact basename should match")
	}
	if _, ok := m.Match("/var/log/idea.log.1", "anything"); ok {
		t.Fatal("rotated name must NOT match an exact literal")
	}
}

func TestMatchPathLiteralIsExactFullPath(t *testing.T) {
	m, _ := Compile(Spec{Path: "/var/log/app.log"})
	if _, ok := m.Match("/var/log/app.log", "x"); !ok {
		t.Fatal("exact path should match")
	}
	if _, ok := m.Match("/var/log/app.log.1", "x"); ok {
		t.Fatal("non-equal path must not match")
	}
}

func TestMatchLineLiteralIsExactWholeLine(t *testing.T) {
	m, _ := Compile(Spec{Line: "DEBUG"})
	if _, ok := m.Match("/f", "DEBUG"); !ok {
		t.Fatal("exact line should match")
	}
	if _, ok := m.Match("/f", "DEBUG: details"); ok {
		t.Fatal("substring must NOT match an exact line literal")
	}
}

func TestMatchRegexAndCaptures(t *testing.T) {
	m, _ := Compile(Spec{LineRegex: `^(\d+) (.*)$`})
	caps, ok := m.Match("/f", "42 hello")
	if !ok || len(caps) != 3 || caps[1] != "42" || caps[2] != "hello" {
		t.Fatalf("captures = %v ok=%v", caps, ok)
	}
}

func TestMatchAndAcrossDimensions(t *testing.T) {
	m, _ := Compile(Spec{Name: "idea.log", LineRegex: "ERROR"})
	if _, ok := m.Match("/x/idea.log", "ERROR here"); !ok {
		t.Fatal("both criteria satisfied should match")
	}
	if _, ok := m.Match("/x/other.log", "ERROR here"); ok {
		t.Fatal("name mismatch must fail AND")
	}
	if _, ok := m.Match("/x/idea.log", "info"); ok {
		t.Fatal("line mismatch must fail AND")
	}
}

func TestHasLineRegex(t *testing.T) {
	with, _ := Compile(Spec{LineRegex: "x"})
	if !with.HasLineRegex() {
		t.Fatal("expected HasLineRegex true")
	}
	without, _ := Compile(Spec{Name: "idea.log"})
	if without.HasLineRegex() {
		t.Fatal("expected HasLineRegex false")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/match/`
Expected: FAIL — package/types not defined (build error).

- [ ] **Step 3: Implement the package**

```go
// Package match provides a reusable predicate over a log line's content, the
// source file's basename, and its full path. Each dimension is matched either
// by an exact literal or by a regular expression; at least one dimension must
// be set, and all set dimensions must match (AND).
package match

import (
	"fmt"
	"path/filepath"
	"regexp"
)

// Spec is the YAML-agnostic matcher definition. For each dimension set either
// the literal field OR the *Regex field, never both. At least one must be set.
type Spec struct {
	Line, LineRegex string
	Name, NameRegex string
	Path, PathRegex string
}

// Matcher is a compiled Spec.
type Matcher struct {
	line       string
	lineRE     *regexp.Regexp
	name       string
	nameRE     *regexp.Regexp
	path       string
	pathRE     *regexp.Regexp
	hasLineLit bool
	hasNameLit bool
	hasPathLit bool
}

// Compile validates and compiles a Spec.
func Compile(s Spec) (*Matcher, error) {
	m := &Matcher{}
	set := 0

	if s.Line != "" && s.LineRegex != "" {
		return nil, fmt.Errorf("matcher: set only one of line or line_regex")
	}
	if s.Line != "" {
		m.line, m.hasLineLit = s.Line, true
		set++
	}
	if s.LineRegex != "" {
		re, err := regexp.Compile(s.LineRegex)
		if err != nil {
			return nil, fmt.Errorf("matcher: line_regex: %w", err)
		}
		m.lineRE = re
		set++
	}

	if s.Name != "" && s.NameRegex != "" {
		return nil, fmt.Errorf("matcher: set only one of name or name_regex")
	}
	if s.Name != "" {
		m.name, m.hasNameLit = s.Name, true
		set++
	}
	if s.NameRegex != "" {
		re, err := regexp.Compile(s.NameRegex)
		if err != nil {
			return nil, fmt.Errorf("matcher: name_regex: %w", err)
		}
		m.nameRE = re
		set++
	}

	if s.Path != "" && s.PathRegex != "" {
		return nil, fmt.Errorf("matcher: set only one of path or path_regex")
	}
	if s.Path != "" {
		m.path, m.hasPathLit = s.Path, true
		set++
	}
	if s.PathRegex != "" {
		re, err := regexp.Compile(s.PathRegex)
		if err != nil {
			return nil, fmt.Errorf("matcher: path_regex: %w", err)
		}
		m.pathRE = re
		set++
	}

	if set == 0 {
		return nil, fmt.Errorf("matcher: at least one of line/name/path (or their *_regex form) must be set")
	}
	return m, nil
}

// HasLineRegex reports whether a line_regex criterion is set (used to validate
// renderer references, which need capture groups).
func (m *Matcher) HasLineRegex() bool { return m.lineRE != nil }

// Match reports whether path+line satisfy every set criterion (AND). caps
// holds the line_regex submatches (caps[0] is the whole match) when a line
// regex is set and matched; otherwise caps is nil.
func (m *Matcher) Match(path, line string) (caps []string, ok bool) {
	base := filepath.Base(path)

	if m.hasNameLit && base != m.name {
		return nil, false
	}
	if m.nameRE != nil && !m.nameRE.MatchString(base) {
		return nil, false
	}
	if m.hasPathLit && path != m.path {
		return nil, false
	}
	if m.pathRE != nil && !m.pathRE.MatchString(path) {
		return nil, false
	}
	if m.hasLineLit && line != m.line {
		return nil, false
	}
	if m.lineRE != nil {
		caps = m.lineRE.FindStringSubmatch(line)
		if caps == nil {
			return nil, false
		}
	}
	return caps, true
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/match/`
Expected: PASS (all tests).

- [ ] **Step 5: Commit**

```bash
git add internal/match/match.go internal/match/match_test.go
git commit -m "phase 1: add internal/match matcher package

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 2: config schema — matchers, mute, renderer.matcher

**Files:**
- Modify: `internal/config/yaml.go`
- Modify: `internal/config/cli.go:19-42` (Config struct) and the `RendererSpec` type (`yaml.go:20-30`)
- Test: `internal/config/yaml_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/config/yaml_test.go`:

```go
func TestLoadCarriesMatchersAndMute(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log-listener.yml")
	yaml := `
directories:
  - id: app
    paths: ["/var/log"]
matchers:
  health:
    line_regex: 'GET /health'
  idea-file:
    name: idea.log
mute:
  - id: drop-health
    matcher: health
  - id: silence-debug
    line: DEBUG
    applies_to: { groups: [app] }
renderers:
  - name: idea-json
    matcher: idea-file
    template: 'json($1)'
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadWithFS([]string{"--config", path}, time.Now(), defaultHomeDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Matchers) != 2 || cfg.Matchers["health"].LineRegex != "GET /health" {
		t.Fatalf("matchers not carried: %+v", cfg.Matchers)
	}
	if cfg.Matchers["idea-file"].Name != "idea.log" {
		t.Fatalf("idea-file matcher: %+v", cfg.Matchers["idea-file"])
	}
	if len(cfg.MuteSpecs) != 2 {
		t.Fatalf("mute specs = %d, want 2", len(cfg.MuteSpecs))
	}
	if cfg.MuteSpecs[0].ID != "drop-health" || cfg.MuteSpecs[0].Matcher != "health" {
		t.Fatalf("mute[0] = %+v", cfg.MuteSpecs[0])
	}
	if cfg.MuteSpecs[1].Line != "DEBUG" || cfg.MuteSpecs[1].AppliesTo == nil ||
		len(cfg.MuteSpecs[1].AppliesTo.Groups) != 1 {
		t.Fatalf("mute[1] = %+v", cfg.MuteSpecs[1])
	}
	if len(cfg.RendererSpecs) != 1 || cfg.RendererSpecs[0].Matcher != "idea-file" {
		t.Fatalf("renderer matcher not carried: %+v", cfg.RendererSpecs)
	}
}
```

(Ensure `yaml_test.go` imports `os`, `path/filepath`, `time` — most are already present; add any missing.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestLoadCarriesMatchersAndMute ./internal/config/`
Expected: FAIL — `cfg.Matchers`, `cfg.MuteSpecs`, `RendererSpec.Matcher`, and the YAML fields don't exist (build error).

- [ ] **Step 3: Add schema types and carry-through**

In `internal/config/yaml.go`, add `Matcher` to `RendererSpec` (struct currently at lines 20-30):

```go
type RendererSpec struct {
	Name      string
	LineRegex string
	Template  string
	Matcher   string // optional: name of a matcher in the matchers library
	AppliesTo *AppliesTo
	StartOff  bool
}
```

Add new schema types (e.g. just after the `Renderer` struct):

```go
// MatcherSpec is a reusable matcher definition from the `matchers:` map. For
// each dimension set either the literal key or the *_regex key.
type MatcherSpec struct {
	Line      string `yaml:"line,omitempty"`
	LineRegex string `yaml:"line_regex,omitempty"`
	Name      string `yaml:"name,omitempty"`
	NameRegex string `yaml:"name_regex,omitempty"`
	Path      string `yaml:"path,omitempty"`
	PathRegex string `yaml:"path_regex,omitempty"`
}

// MuteSpec is one entry in the `mute:` list. It sets exactly one of `matcher`
// (a named reference) or inline matcher fields (embedded MatcherSpec). `id` is
// an optional identity used in diagnostic messages; it is named `id` (not
// `name`) to avoid colliding with the matcher's inline `name` field.
type MuteSpec struct {
	ID          string         `yaml:"id,omitempty"`
	Matcher     string         `yaml:"matcher,omitempty"`
	MatcherSpec `yaml:",inline"`
	AppliesTo   *AppliesToSpec `yaml:"applies_to,omitempty"`
}
```

Add `Matcher` to the YAML `Renderer` struct (currently lines 78-85):

```go
type Renderer struct {
	Name      string         `yaml:"name"`
	LineRegex string         `yaml:"line_regex,omitempty"`
	Template  string         `yaml:"template"`
	Matcher   string         `yaml:"matcher,omitempty"`
	AppliesTo *AppliesToSpec `yaml:"applies_to,omitempty"`
	Disabled  bool           `yaml:"disabled,omitempty"`
	Off       bool           `yaml:"off,omitempty"`
}
```

Note: `line_regex` gains `,omitempty` (was required-looking); both forms still parse — validation that exactly one of line_regex/matcher is set happens at pipeline build.

Extend the `File` struct (currently lines 43-50):

```go
type File struct {
	Directories      []DirGroup             `yaml:"directories,omitempty"`
	Files            []FileGroup            `yaml:"files,omitempty"`
	GlobalFileFilter *Filter                `yaml:"global_file_filter,omitempty"`
	Matchers         map[string]MatcherSpec `yaml:"matchers,omitempty"`
	Mute             []MuteSpec             `yaml:"mute,omitempty"`
	Renderers        []Renderer             `yaml:"renderers,omitempty"`
	Output           *Output                `yaml:"output,omitempty"`
	TUI              *TUI                   `yaml:"tui,omitempty"`
}
```

In `mergeYAMLInto`, set `Matcher` when building each renderer spec (inside the existing renderers loop, where `spec := RendererSpec{...}` is built around lines 282-287):

```go
		spec := RendererSpec{
			Name:      yr.Name,
			LineRegex: yr.LineRegex,
			Template:  yr.Template,
			Matcher:   yr.Matcher,
			StartOff:  yr.Off,
		}
```

Then, after the renderers loop (before the `output` block), carry matchers + mute through:

```go
	// matchers / mute — YAML-only, carried through verbatim (validated at
	// pipeline build). Map decoding already rejects duplicate matcher names.
	if yc.Matchers != nil {
		cfg.Matchers = make(map[string]MatcherSpec, len(yc.Matchers))
		for k, v := range yc.Matchers {
			cfg.Matchers[k] = v
		}
	}
	cfg.MuteSpecs = append(cfg.MuteSpecs, yc.Mute...)
```

In `internal/config/cli.go`, extend `Config` (after `RendererSpecs` at line 37):

```go
	RendererSpecs []RendererSpec
	Matchers      map[string]MatcherSpec
	MuteSpecs     []MuteSpec
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -run TestLoadCarriesMatchersAndMute ./internal/config/`
Expected: PASS. Then `go test ./internal/config/` — all PASS (existing renderer tests still parse since `line_regex` stays optional).

- [ ] **Step 5: Commit**

```bash
git add internal/config/yaml.go internal/config/cli.go internal/config/yaml_test.go
git commit -m "phase 2: carry matchers/mute/renderer-matcher through config

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 3: render — unify Renderer on match.Matcher

**Files:**
- Modify: `internal/render/pipeline.go` (`Renderer`, `Compile`, `Match`, `NewPipeline`, `Render`)
- Test: `internal/render/render_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/render/render_test.go`:

```go
func TestRendererViaMatcherCaptures(t *testing.T) {
	matchers := map[string]config.MatcherSpec{
		"json-on-idea": {Name: "idea.log", LineRegex: `^\s*(\{.*\})\s*$`},
	}
	specs := []config.RendererSpec{
		{Name: "idea-json", Matcher: "json-on-idea", Template: "json($1)"},
	}
	p, err := NewPipeline(specs, matchers, nil, false)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	// Matches: basename idea.log AND the json line.
	ev, ok := p.Render(time.Time{}, "g", "/var/log/idea.log", `{"a":1}`)
	if !ok || ev.Renderer != "idea-json" {
		t.Fatalf("expected idea-json render, got ok=%v renderer=%q", ok, ev.Renderer)
	}
	// Same line in a different file: name criterion gates it out -> falls
	// through to raw text (drop=false).
	ev, ok = p.Render(time.Time{}, "g", "/var/log/other.log", `{"a":1}`)
	if !ok || ev.Renderer != "" {
		t.Fatalf("expected raw passthrough for other.log, got renderer=%q", ev.Renderer)
	}
}

func TestRendererMatcherWithoutLineRegexIsError(t *testing.T) {
	matchers := map[string]config.MatcherSpec{"nameonly": {Name: "idea.log"}}
	specs := []config.RendererSpec{{Name: "r", Matcher: "nameonly", Template: "x"}}
	if _, err := NewPipeline(specs, matchers, nil, false); err == nil {
		t.Fatal("expected error: matcher used by renderer has no line_regex")
	}
}

func TestRendererRequiresExactlyOneOfLineRegexOrMatcher(t *testing.T) {
	both := []config.RendererSpec{{Name: "r", LineRegex: "x", Matcher: "m", Template: "t"}}
	if _, err := NewPipeline(both, map[string]config.MatcherSpec{"m": {LineRegex: "y"}}, nil, false); err == nil {
		t.Fatal("expected error when both line_regex and matcher set")
	}
	neither := []config.RendererSpec{{Name: "r", Template: "t"}}
	if _, err := NewPipeline(neither, nil, nil, false); err == nil {
		t.Fatal("expected error when neither line_regex nor matcher set")
	}
}

func TestRendererUnknownMatcherRef(t *testing.T) {
	specs := []config.RendererSpec{{Name: "r", Matcher: "ghost", Template: "t"}}
	if _, err := NewPipeline(specs, nil, nil, false); err == nil {
		t.Fatal("expected error for unknown matcher reference")
	}
}
```

Existing tests call `NewPipeline(specs, dropUnmatched)` — they will be updated in this task's Step 3 to the new signature.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/render/`
Expected: FAIL — `NewPipeline` arity changed / undefined; build error.

- [ ] **Step 3: Refactor `Renderer` and `Compile`/`Match`; update `NewPipeline` signature**

In `internal/render/pipeline.go`, add the `match` import and a `toMatchSpec` helper:

```go
import (
	"fmt"
	"path/filepath"
	"sync/atomic"
	"time"

	"log-listener/internal/config"
	"log-listener/internal/match"
)

// toMatchSpec converts a config matcher spec into a match.Spec.
func toMatchSpec(s config.MatcherSpec) match.Spec {
	return match.Spec{
		Line: s.Line, LineRegex: s.LineRegex,
		Name: s.Name, NameRegex: s.NameRegex,
		Path: s.Path, PathRegex: s.PathRegex,
	}
}
```

(Remove the now-unused `regexp` import from this file; `match` owns regex compilation.)

Replace the `Renderer` struct and `Compile`/`Match` (lines 13-75) with a matcher-backed version:

```go
// Renderer is a compiled rendering rule. The matcher provides the content
// match + template captures; applies_to (groups/pathGlobs) scopes it.
type Renderer struct {
	Name      string
	matcher   *match.Matcher
	template  *Template
	groups    map[string]bool
	pathGlobs []string
}

// Compile turns a config.RendererSpec into a runtime Renderer. Exactly one of
// LineRegex or Matcher must be set. A matcher used here must carry a
// line_regex (captures feed the template).
func Compile(spec config.RendererSpec, matchers map[string]config.MatcherSpec) (*Renderer, error) {
	hasLine := spec.LineRegex != ""
	hasMatcher := spec.Matcher != ""
	if hasLine == hasMatcher {
		return nil, fmt.Errorf("renderer %q: set exactly one of line_regex or matcher", spec.Name)
	}

	var ms match.Spec
	if hasMatcher {
		cm, ok := matchers[spec.Matcher]
		if !ok {
			return nil, fmt.Errorf("renderer %q: unknown matcher %q", spec.Name, spec.Matcher)
		}
		ms = toMatchSpec(cm)
	} else {
		ms = match.Spec{LineRegex: spec.LineRegex}
	}

	m, err := match.Compile(ms)
	if err != nil {
		return nil, fmt.Errorf("renderer %q: %w", spec.Name, err)
	}
	if !m.HasLineRegex() {
		return nil, fmt.Errorf("renderer %q: matcher %q has no line_regex (nothing to capture)", spec.Name, spec.Matcher)
	}

	tpl, err := ParseTemplate(spec.Template)
	if err != nil {
		return nil, fmt.Errorf("renderer %q: template: %w", spec.Name, err)
	}

	r := &Renderer{Name: spec.Name, matcher: m, template: tpl}
	if spec.AppliesTo != nil {
		if len(spec.AppliesTo.Groups) > 0 {
			r.groups = make(map[string]bool, len(spec.AppliesTo.Groups))
			for _, g := range spec.AppliesTo.Groups {
				r.groups[g] = true
			}
		}
		r.pathGlobs = append([]string(nil), spec.AppliesTo.Paths...)
	}
	return r, nil
}

// Applies reports whether the renderer's applies_to scope admits group+path.
func (r *Renderer) Applies(group, path string) bool {
	if r.groups != nil && !r.groups[group] {
		return false
	}
	if len(r.pathGlobs) == 0 {
		return true
	}
	for _, g := range r.pathGlobs {
		if ok, _ := filepath.Match(g, path); ok {
			return true
		}
		if ok, _ := filepath.Match(g, filepath.Base(path)); ok {
			return true
		}
	}
	return false
}

// Match runs the matcher against path+line. Returns the capture slice
// (index 0 = full match) or nil if it does not match.
func (r *Renderer) Match(path, line string) []string {
	caps, ok := r.matcher.Match(path, line)
	if !ok {
		return nil
	}
	return caps
}
```

Update `NewPipeline` (lines 103-116) to the new signature and per-renderer compile call:

```go
// NewPipeline compiles renderer specs (resolving matcher references against
// the matchers library) and mute specs into a Pipeline. Renderer order is
// preserved. Each renderer starts enabled unless its spec sets StartOff.
func NewPipeline(specs []config.RendererSpec, matchers map[string]config.MatcherSpec, mutes []config.MuteSpec, dropUnmatched bool) (*Pipeline, error) {
	p := &Pipeline{drop: dropUnmatched}
	for _, s := range specs {
		r, err := Compile(s, matchers)
		if err != nil {
			return nil, err
		}
		flag := &atomic.Bool{}
		flag.Store(!s.StartOff)
		p.renderers = append(p.renderers, r)
		p.enabled = append(p.enabled, flag)
	}
	// mute rules compiled in Task 4.
	return p, nil
}
```

In `Render` (lines 123-146), change the match call to pass `path`:

```go
		caps := r.Match(path, raw)
		if caps == nil {
			continue
		}
```

Update existing `render_test.go` calls of `NewPipeline(specs, drop)` to `NewPipeline(specs, nil, nil, drop)`. Search: `grep -n "NewPipeline(" internal/render/render_test.go`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/render/`
Expected: PASS — new matcher tests pass; existing renderer tests pass with the updated signature.

- [ ] **Step 5: Commit**

```bash
git add internal/render/pipeline.go internal/render/render_test.go
git commit -m "phase 3: unify renderers on match.Matcher

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 4: render — MuteRule and pre-render mute check

**Files:**
- Modify: `internal/render/pipeline.go` (add `MuteRule`, `compileMute`, `inlineEmpty`, `Pipeline.mutes`, mute loop in `Render`, build mutes in `NewPipeline`)
- Test: `internal/render/render_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/render/render_test.go`:

```go
func TestMuteDropsLine(t *testing.T) {
	matchers := map[string]config.MatcherSpec{"health": {LineRegex: "GET /health"}}
	mutes := []config.MuteSpec{{ID: "h", Matcher: "health"}}
	p, err := NewPipeline(nil, matchers, mutes, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.Render(time.Time{}, "g", "/f", "GET /health 200"); ok {
		t.Fatal("muted line should be dropped (ok=false)")
	}
	if _, ok := p.Render(time.Time{}, "g", "/f", "GET /api 200"); !ok {
		t.Fatal("non-muted line should pass through")
	}
}

func TestMuteInlineFields(t *testing.T) {
	mutes := []config.MuteSpec{{ID: "dbg", MatcherSpec: config.MatcherSpec{Line: "DEBUG"}}}
	p, _ := NewPipeline(nil, nil, mutes, false)
	if _, ok := p.Render(time.Time{}, "g", "/f", "DEBUG"); ok {
		t.Fatal("inline mute should drop exact line")
	}
	if _, ok := p.Render(time.Time{}, "g", "/f", "DEBUG: x"); !ok {
		t.Fatal("exact-literal mute must not drop substring")
	}
}

func TestMuteAppliesToScopesByGroup(t *testing.T) {
	mutes := []config.MuteSpec{{
		ID:          "dbg",
		MatcherSpec: config.MatcherSpec{LineRegex: "DEBUG"},
		AppliesTo:   &config.AppliesToSpec{Groups: []string{"app"}},
	}}
	p, _ := NewPipeline(nil, nil, mutes, false)
	if _, ok := p.Render(time.Time{}, "app", "/f", "DEBUG x"); ok {
		t.Fatal("DEBUG in group app should be muted")
	}
	if _, ok := p.Render(time.Time{}, "other", "/f", "DEBUG x"); !ok {
		t.Fatal("DEBUG outside group app should NOT be muted")
	}
}

func TestMutePrecedesDropUnmatched(t *testing.T) {
	// drop=true and no renderers: unmatched lines drop anyway, but a muted
	// line must drop regardless. Verify mute path returns ok=false even with
	// drop already true (no panic, no render).
	mutes := []config.MuteSpec{{ID: "h", MatcherSpec: config.MatcherSpec{LineRegex: "X"}}}
	p, _ := NewPipeline(nil, nil, mutes, true)
	if _, ok := p.Render(time.Time{}, "g", "/f", "X"); ok {
		t.Fatal("muted line dropped")
	}
}

func TestMuteRequiresExactlyOneOfRefOrInline(t *testing.T) {
	both := []config.MuteSpec{{ID: "x", Matcher: "m", MatcherSpec: config.MatcherSpec{Line: "y"}}}
	if _, err := NewPipeline(nil, map[string]config.MatcherSpec{"m": {Line: "z"}}, both, false); err == nil {
		t.Fatal("expected error: both ref and inline set")
	}
	neither := []config.MuteSpec{{ID: "x"}}
	if _, err := NewPipeline(nil, nil, neither, false); err == nil {
		t.Fatal("expected error: neither ref nor inline set")
	}
}

func TestMuteUnknownMatcherRef(t *testing.T) {
	mutes := []config.MuteSpec{{ID: "x", Matcher: "ghost"}}
	if _, err := NewPipeline(nil, nil, mutes, false); err == nil {
		t.Fatal("expected error for unknown matcher reference")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/render/`
Expected: FAIL — `MuteSpec`/mute behavior not wired; muted lines still render (ok=true), and error cases don't error.

- [ ] **Step 3: Implement MuteRule + wiring**

In `internal/render/pipeline.go`, add the `Pipeline.mutes` field (in the `Pipeline` struct):

```go
type Pipeline struct {
	renderers []*Renderer
	enabled   []*atomic.Bool
	mutes     []*MuteRule
	drop      bool
}
```

Add the mute loop at the very top of `Render` (before the `ev := Event{...}` line):

```go
func (p *Pipeline) Render(now time.Time, group, path, raw string) (Event, bool) {
	for _, mr := range p.mutes {
		if mr.Mutes(group, path, raw) {
			return Event{}, false
		}
	}
	ev := Event{Ts: now, File: path, Group: group, Raw: raw}
	// ... unchanged ...
}
```

Build mutes in `NewPipeline` (replace the `// mute rules compiled in Task 4.` placeholder):

```go
	for _, ms := range mutes {
		mr, err := compileMute(ms, matchers)
		if err != nil {
			return nil, err
		}
		p.mutes = append(p.mutes, mr)
	}
	return p, nil
```

Add the `MuteRule` type and helpers (e.g. at the end of the file):

```go
// MuteRule drops a line before rendering when its matcher matches and its
// applies_to scope (group ids + path globs, AND) admits the line.
type MuteRule struct {
	id        string
	matcher   *match.Matcher
	groups    map[string]bool
	pathGlobs []string
}

// Mutes reports whether the rule drops the given line.
func (mr *MuteRule) Mutes(group, path, line string) bool {
	if mr.groups != nil && !mr.groups[group] {
		return false
	}
	if len(mr.pathGlobs) > 0 {
		matched := false
		for _, g := range mr.pathGlobs {
			if ok, _ := filepath.Match(g, path); ok {
				matched = true
				break
			}
			if ok, _ := filepath.Match(g, filepath.Base(path)); ok {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	_, ok := mr.matcher.Match(path, line)
	return ok
}

func inlineEmpty(s config.MatcherSpec) bool {
	return s.Line == "" && s.LineRegex == "" &&
		s.Name == "" && s.NameRegex == "" &&
		s.Path == "" && s.PathRegex == ""
}

func compileMute(ms config.MuteSpec, matchers map[string]config.MatcherSpec) (*MuteRule, error) {
	id := ms.ID
	if id == "" {
		id = "(unnamed)"
	}
	hasRef := ms.Matcher != ""
	hasInline := !inlineEmpty(ms.MatcherSpec)
	if hasRef == hasInline {
		return nil, fmt.Errorf("mute %q: set exactly one of matcher (reference) or inline matcher fields", id)
	}

	var spec match.Spec
	if hasRef {
		cm, ok := matchers[ms.Matcher]
		if !ok {
			return nil, fmt.Errorf("mute %q: unknown matcher %q", id, ms.Matcher)
		}
		spec = toMatchSpec(cm)
	} else {
		spec = toMatchSpec(ms.MatcherSpec)
	}

	m, err := match.Compile(spec)
	if err != nil {
		return nil, fmt.Errorf("mute %q: %w", id, err)
	}

	mr := &MuteRule{id: ms.ID, matcher: m}
	if ms.AppliesTo != nil {
		if len(ms.AppliesTo.Groups) > 0 {
			mr.groups = make(map[string]bool, len(ms.AppliesTo.Groups))
			for _, g := range ms.AppliesTo.Groups {
				mr.groups[g] = true
			}
		}
		mr.pathGlobs = append([]string(nil), ms.AppliesTo.Paths...)
	}
	return mr, nil
}
```

Note: `MuteRule.id` is stored for future diagnostics; if `go vet`/staticcheck flags it as unused, keep it (it documents the rule and is used in error context construction via `ms.ID`). If the linter is strict about the struct field specifically, reference it in a `String()` method:

```go
func (mr *MuteRule) String() string { return "mute(" + mr.id + ")" }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/render/`
Expected: PASS (all mute tests + existing).

- [ ] **Step 5: Commit**

```bash
git add internal/render/pipeline.go internal/render/render_test.go
git commit -m "phase 4: add mute rules as pre-render drop

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 5: cmd wiring

**Files:**
- Modify: `cmd/log-listener/main.go:54` and `cmd/log-listener/main.go:145`
- Test: existing `cmd/log-listener/e2e_test.go` (add one mute e2e)

- [ ] **Step 1: Write the failing test**

Add to `cmd/log-listener/e2e_test.go` a test that writes a config with a mute rule and asserts the muted line is absent from stdout. Mirror the existing e2e helpers in that file (reuse whatever `runMain`/temp-dir harness the other e2e tests use). Concretely:

```go
func TestE2EMuteDropsLine(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "app.log")
	if err := os.WriteFile(logPath, []byte("KEEP one\nGET /health 200\nKEEP two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "log-listener.yml")
	cfg := "files:\n  - id: app\n    paths: [\"" + logPath + "\"]\n" +
		"mute:\n  - id: h\n    line_regex: 'GET /health'\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	out := runMainOnce(t, "--config", cfgPath, "--once", "--no-tui") // adapt to existing harness
	if strings.Contains(out, "/health") {
		t.Fatalf("muted line leaked into output:\n%s", out)
	}
	if !strings.Contains(out, "KEEP one") || !strings.Contains(out, "KEEP two") {
		t.Fatalf("kept lines missing:\n%s", out)
	}
}
```

Adapt `runMainOnce` and flags to the actual e2e harness in that file (check `grep -n "func Test" cmd/log-listener/e2e_test.go` and reuse its pattern; the project supports `--once`).

- [ ] **Step 2: Run test to verify it fails**

Run: `go build ./... `
Expected: FAIL — `render.NewPipeline` is called with the old 2-arg signature in `main.go`, so the build breaks.

- [ ] **Step 3: Update both call sites**

`cmd/log-listener/main.go:54`:

```go
	pipeline, err := render.NewPipeline(cfg.RendererSpecs, cfg.Matchers, cfg.MuteSpecs, cfg.DropUnmatched)
```

`cmd/log-listener/main.go:145` (inside `buildRuntime`):

```go
	pipeline, err := render.NewPipeline(cfg.RendererSpecs, cfg.Matchers, cfg.MuteSpecs, dropUnmatched)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go build ./... && go test ./cmd/log-listener/`
Expected: PASS — build succeeds; e2e mute test passes; existing e2e tests still pass.

- [ ] **Step 5: Commit**

```bash
git add cmd/log-listener/main.go cmd/log-listener/e2e_test.go
git commit -m "phase 5: wire matchers/mute into pipeline construction

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 6: docs + full verification

**Files:**
- Modify: `README.md`, `CHANGELOG.md`

- [ ] **Step 1: Document the feature**

Add a README section describing `matchers`, `mute`, and renderer `matcher:`, with the schema example from the spec. Add a `CHANGELOG.md` entry summarizing: reusable matchers (literal/regex over line/name/path, AND), `mute` pre-render line dropping with `applies_to` scoping, and renderer `matcher` references.

- [ ] **Step 2: Full verification**

Run:
```bash
go test ./...
go vet ./...
go test -race ./...
```
Expected: all PASS / clean.

- [ ] **Step 3: Commit**

```bash
git add README.md CHANGELOG.md
git commit -m "phase 6: document matchers and mute

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Self-Review Notes

- **Spec coverage:** matchers library (Task 1+2), literal/regex per dimension + AND + exact semantics (Task 1), `mute` pre-render drop + precedence + `applies_to` (Task 4), renderer `matcher` with captures + validation (Task 3), config carry-through (Task 2), cmd wiring (Task 5), docs (Task 6). Out-of-scope items (TUI toggle, catalog emission, age/exclude on matchers) intentionally omitted.
- **Type consistency:** `match.Spec`/`match.Matcher`/`Compile`/`Match(path,line)`/`HasLineRegex` are used identically across tasks; `config.MatcherSpec`/`MuteSpec` field names (`ID`, `Matcher`, embedded `MatcherSpec`, `AppliesTo`) match between config and render; `render.NewPipeline(specs, matchers, mutes, drop)` arity is consistent across Tasks 3-5; `Renderer.Match(path, line)` signature change is applied in `Render` and tests.
- **Validation locations:** all reference/shape validation lives in `render.NewPipeline`→`Compile`/`compileMute`; duplicate matcher names handled by YAML map decoding.
