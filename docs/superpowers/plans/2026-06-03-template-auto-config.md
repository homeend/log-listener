# Template-based Auto-Configuration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `log-listener init <apps...>` that generates a ready-to-run `log-listener.yml` from an embedded (and optionally online-updated) catalog of per-application log templates, resolved for the current OS.

**Architecture:** A new `internal/catalog` package owns the catalog schema (`go:embed`ed bottled `catalog.yml`), composes fragments into per-app sources, expands OS/`{product}` tokens, and probe-and-picks existing directories — producing a `config.File`. The `internal/config` YAML schema is promoted to exported types and gains a `Marshal` + lossless `MergeFiles` so the generator emits YAML the existing loader already consumes. A new `init` subcommand in `cmd/log-listener` wires arg-parsing, prompts, and file write/merge. Phase 2 adds an online `Fetcher` that version-compares a remote catalog and falls back to bottled on any failure.

**Tech Stack:** Go 1.26, `gopkg.in/yaml.v3`, stdlib `embed`/`net/http`. No new third-party deps (preserves the single-static-binary rule).

**Reference spec:** `docs/superpowers/specs/2026-06-03-template-auto-config-design.md`

---

## File Structure

**New files:**
- `internal/catalog/schema.go` — catalog types + `Parse([]byte) (*Catalog, error)` (strict).
- `internal/catalog/embed.go` — `//go:embed catalog.yml` + `Bundled() (*Catalog, error)`.
- `internal/catalog/catalog.yml` — the bottled catalog data (JetBrains family + Junie).
- `internal/catalog/expand.go` — `{product}` substitution + OS path-token expansion helpers.
- `internal/catalog/resolve.go` — `Env` + `(*Catalog).Resolve(names, env) (*config.File, error)`.
- `internal/catalog/remote.go` *(Phase 2)* — `Fetcher` interface, HTTP fetcher, cache, `Select`.
- `internal/config/emit.go` — `(*File).Marshal()` + `MergeFiles` + `ParseFile`.
- `cmd/log-listener/init.go` — `runInit(args, stdout, stderr) int`.
- Test files alongside each (`*_test.go`).

**Modified files:**
- `internal/config/yaml.go` — promote unexported YAML structs to exported types (used by both loader and emitter); add `,omitempty`.
- `internal/config/yaml_test.go` — rename type references.
- `cmd/log-listener/main.go` — dispatch `init` subcommand.

**Responsibility boundaries:** schema/parse (`schema.go`) is separate from resolution (`resolve.go`) and token mechanics (`expand.go`); network lives only in `remote.go`; emission/merge lives in `config/emit.go`; CLI/prompt lives only in `init.go`. Resolution is pure given an injected `Env` (no direct filesystem/OS reads).

---

## PHASE 1 — Offline `init` (independently shippable)

### Task 1: Catalog schema + strict parse

**Files:**
- Create: `internal/catalog/schema.go`
- Test: `internal/catalog/schema_test.go`

- [ ] **Step 1: Write the failing test**

```go
package catalog

import "testing"

func TestParseMinimalCatalog(t *testing.T) {
	src := []byte(`
version: 3
defaults:
  output: { color: true, drop_unmatched: false }
  tui: { enabled: true, scrollback: 20000 }
fragments:
  jetbrains-base:
    sources:
      - id: main
        filter: 'idea\.log$'
        locations:
          - dir: { linux: '~/.cache/JetBrains/{product}*/log' }
apps:
  goland:
    renderers: [json-line]
    use:
      - { frag: jetbrains-base, product: GoLand }
    sources:
      - id: acp
        filter: '\.log$'
        locations:
          - dir: { linux: '~/.cache/JetBrains/GoLand*/log/acp' }
renderers:
  json-line: { line_regex: '^\s*(\{.*\})\s*$', template: 'json($1)' }
bundles:
  jetbrains: [goland]
`)
	c, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Version != 3 {
		t.Errorf("Version = %d, want 3", c.Version)
	}
	if got := c.Fragments["jetbrains-base"].Sources[0].Locations[0].Dir["linux"]; got != "~/.cache/JetBrains/{product}*/log" {
		t.Errorf("fragment dir = %q", got)
	}
	app := c.Apps["goland"]
	if len(app.Use) != 1 || app.Use[0].Frag != "jetbrains-base" || app.Use[0].Product != "GoLand" {
		t.Errorf("app.Use = %+v", app.Use)
	}
	if app.Sources[0].ID != "acp" {
		t.Errorf("app inline source = %+v", app.Sources)
	}
	if c.Renderers["json-line"].Template != "json($1)" {
		t.Errorf("renderer = %+v", c.Renderers["json-line"])
	}
	if got := c.Bundles["jetbrains"]; len(got) != 1 || got[0] != "goland" {
		t.Errorf("bundle = %v", got)
	}
	if !c.Defaults.TUI.Enabled || c.Defaults.TUI.Scrollback != 20000 {
		t.Errorf("defaults.tui = %+v", c.Defaults.TUI)
	}
}

func TestParseRejectsUnknownKey(t *testing.T) {
	_, err := Parse([]byte("version: 1\nbogus_key: true\n"))
	if err == nil {
		t.Fatal("expected error for unknown key, got nil")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/catalog/ -run TestParse -v`
Expected: build failure / FAIL — `undefined: Parse`.

- [ ] **Step 3: Write the implementation**

```go
// Package catalog turns an embedded (or online-updated) catalog of per-app
// log templates into a config.File for the current OS. See
// docs/superpowers/specs/2026-06-03-template-auto-config-design.md.
package catalog

import (
	"bytes"

	"gopkg.in/yaml.v3"
)

// Catalog is the top-level catalog document.
type Catalog struct {
	Version   int                 `yaml:"version"`
	Defaults  Defaults            `yaml:"defaults"`
	Fragments map[string]Fragment `yaml:"fragments"`
	Apps      map[string]App      `yaml:"apps"`
	Renderers map[string]Renderer `yaml:"renderers"`
	Bundles   map[string][]string `yaml:"bundles"`
}

// Defaults supplies the global output/tui blocks when no selected app sets them.
type Defaults struct {
	Output OutputDefaults `yaml:"output"`
	TUI    TUIDefaults    `yaml:"tui"`
}

type OutputDefaults struct {
	Color         bool `yaml:"color"`
	DropUnmatched bool `yaml:"drop_unmatched"`
}

type TUIDefaults struct {
	Enabled    bool `yaml:"enabled"`
	Scrollback int  `yaml:"scrollback"`
}

// Fragment is a reusable bundle of sources, optionally parameterized by {product}.
type Fragment struct {
	Sources []Source `yaml:"sources"`
}

// Source is one discovery target: a filter plus ordered drift candidates.
type Source struct {
	ID        string     `yaml:"id"`
	Filter    string     `yaml:"filter"`
	Locations []Location `yaml:"locations"`
}

// Location is one drift candidate; Dir maps an OS key (linux/darwin/windows)
// to a path that may contain ~, %VAR%, $VAR, and {product}.
type Location struct {
	Dir map[string]string `yaml:"dir"`
}

// App is a named template composed from fragments plus inline sources.
type App struct {
	Renderers []string `yaml:"renderers"`
	Use       []Use    `yaml:"use"`
	Sources   []Source `yaml:"sources"`
}

// Use references a fragment and binds its {product} parameter.
type Use struct {
	Frag    string `yaml:"frag"`
	Product string `yaml:"product"`
}

// Renderer is a named entry in the catalog's reusable renderer library.
type Renderer struct {
	LineRegex string `yaml:"line_regex"`
	Template  string `yaml:"template"`
}

// Parse decodes a catalog document strictly (unknown keys are an error).
func Parse(data []byte) (*Catalog, error) {
	var c Catalog
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, err
	}
	return &c, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/catalog/ -run TestParse -v`
Expected: PASS (both tests).

- [ ] **Step 5: Commit**

```bash
git add internal/catalog/schema.go internal/catalog/schema_test.go
git commit -m "catalog: schema types and strict Parse"
```

---

### Task 2: Embedded bottled catalog loader

**Files:**
- Create: `internal/catalog/embed.go`, `internal/catalog/catalog.yml` (stub, expanded in Task 7)
- Test: `internal/catalog/embed_test.go`

- [ ] **Step 1: Create a minimal valid `catalog.yml` stub**

```yaml
version: 1
defaults:
  output: { color: true, drop_unmatched: false }
  tui: { enabled: true, scrollback: 20000 }
fragments: {}
apps: {}
renderers: {}
bundles: {}
```

- [ ] **Step 2: Write the failing test**

```go
package catalog

import "testing"

func TestBundledParses(t *testing.T) {
	c, err := Bundled()
	if err != nil {
		t.Fatalf("Bundled: %v", err)
	}
	if c.Version < 1 {
		t.Errorf("Version = %d, want >= 1", c.Version)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/catalog/ -run TestBundled -v`
Expected: FAIL — `undefined: Bundled`.

- [ ] **Step 4: Write the implementation**

```go
package catalog

import _ "embed"

//go:embed catalog.yml
var bundledYAML []byte

// Bundled returns the catalog compiled into the binary.
func Bundled() (*Catalog, error) {
	return Parse(bundledYAML)
}
```

- [ ] **Step 5: Run + commit**

Run: `go test ./internal/catalog/ -run TestBundled -v` → Expected: PASS

```bash
git add internal/catalog/embed.go internal/catalog/embed_test.go internal/catalog/catalog.yml
git commit -m "catalog: embed bottled catalog.yml"
```

---

### Task 3: Promote config YAML schema to exported types + `Marshal`

The generator must emit YAML the loader accepts, and `MergeFiles` (Task 4) must be **lossless** for user files. Both require the loader's full schema as exported types. This task renames the unexported YAML structs in `internal/config/yaml.go` to exported names, adds `,omitempty` for clean emission, and adds a `Marshal` method.

**Files:**
- Modify: `internal/config/yaml.go` (struct block ~lines 41-104, plus all internal references)
- Modify: `internal/config/yaml_test.go` (type references)
- Create: `internal/config/emit.go`
- Test: `internal/config/emit_test.go`

- [ ] **Step 1: Replace the struct block in `yaml.go`**

Replace the type declarations `yamlConfig`, `yamlDirGroup`, `yamlFileGroup`, `yamlFilter`, `yamlRenderer`, `yamlAppliesTo`, `yamlOutput`, `yamlSSE`, `yamlTUI` (the block currently spanning ~lines 41-104) with these exported versions (yaml tags unchanged; `,omitempty` added; field names unchanged):

```go
// File is the YAML config schema, shared by the loader (readYAMLFile /
// mergeYAMLInto) and the emitter (emit.go). One struct set = no read/write drift.
type File struct {
	Directories      []DirGroup  `yaml:"directories,omitempty"`
	Files            []FileGroup `yaml:"files,omitempty"`
	GlobalFileFilter *Filter     `yaml:"global_file_filter,omitempty"`
	Renderers        []Renderer  `yaml:"renderers,omitempty"`
	Output           *Output     `yaml:"output,omitempty"`
	TUI              *TUI        `yaml:"tui,omitempty"`
}

type DirGroup struct {
	ID         string   `yaml:"id"`
	Paths      []string `yaml:"paths,omitempty"`
	Recursive  *bool    `yaml:"recursive,omitempty"`
	FileFilter *Filter  `yaml:"file_filter,omitempty"`
	Disabled   bool     `yaml:"disabled,omitempty"`
	Off        bool     `yaml:"off,omitempty"`
}

type FileGroup struct {
	ID       string   `yaml:"id"`
	Paths    []string `yaml:"paths,omitempty"`
	Disabled bool     `yaml:"disabled,omitempty"`
	Off      bool     `yaml:"off,omitempty"`
}

type Filter struct {
	NameRegex    string `yaml:"name_regex,omitempty"`
	ExcludeRegex string `yaml:"exclude_regex,omitempty"`
	Older        string `yaml:"older,omitempty"`
	Younger      string `yaml:"younger,omitempty"`
}

type Renderer struct {
	Name      string         `yaml:"name"`
	LineRegex string         `yaml:"line_regex"`
	Template  string         `yaml:"template"`
	AppliesTo *AppliesToSpec `yaml:"applies_to,omitempty"`
	Disabled  bool           `yaml:"disabled,omitempty"`
	Off       bool           `yaml:"off,omitempty"`
}

// AppliesToSpec is the YAML form of a renderer scope (distinct from the
// compiled AppliesTo type carried into the render pipeline).
type AppliesToSpec struct {
	Groups []string `yaml:"groups,omitempty"`
	Paths  []string `yaml:"paths,omitempty"`
}

type Output struct {
	Color         *bool `yaml:"color,omitempty"`
	DropUnmatched *bool `yaml:"drop_unmatched,omitempty"`
	SSE           *SSE  `yaml:"sse,omitempty"`
}

type SSE struct {
	Enabled *bool  `yaml:"enabled,omitempty"`
	Addr    string `yaml:"addr,omitempty"`
}

type TUI struct {
	Enabled    *bool `yaml:"enabled,omitempty"`
	Scrollback *int  `yaml:"scrollback,omitempty"`
}
```

- [ ] **Step 2: Update internal references in `yaml.go`**

Apply these exact identifier renames throughout the rest of `yaml.go` (function bodies of `readYAMLFile`, `mergeYAMLInto`, and signatures):

- `*yamlConfig` → `*File`, `yamlConfig` → `File`
- `yc.Directories` ranges over `[]DirGroup` (was `[]yamlDirGroup`) — no field renames needed
- `yamlFilterToDiscover(yf *yamlFilter, ...)` → `yamlFilterToDiscover(yf *Filter, ...)`
- references to `yc.GlobalFileFilter`, `ydg.FileFilter`, `yr.AppliesTo` types now resolve to `*Filter` / `*AppliesToSpec` — update the local type names only where written explicitly (e.g. the `yr.AppliesTo != nil` block constructs `&AppliesTo{...}`, which is the **compiled** type and stays unchanged).

Run `go build ./internal/config/` after editing and fix any remaining `yaml*` identifiers the compiler flags.

- [ ] **Step 3: Update `yaml_test.go` references**

Rename any `yamlConfig`/`yamlDirGroup`/etc. literals in `internal/config/yaml_test.go` to the new exported names (`File`/`DirGroup`/etc.). Run `go test ./internal/config/` to confirm the existing suite still compiles and passes.

Run: `go test ./internal/config/ -v`
Expected: PASS (all pre-existing tests green — behavior unchanged, only type names).

- [ ] **Step 4: Add the emitter in `emit.go`**

```go
package config

import (
	"bytes"

	"gopkg.in/yaml.v3"
)

// Marshal renders the File as indented YAML suitable for writing to a
// log-listener.yml. omitempty on the schema keeps the output minimal.
func (f *File) Marshal() ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(f); err != nil {
		return nil, err
	}
	_ = enc.Close()
	return buf.Bytes(), nil
}

// ParseFile decodes a log-listener.yml into the File schema (lenient: unknown
// keys are ignored so a future schema can still be merged). Used by MergeFiles.
func ParseFile(data []byte) (*File, error) {
	var f File
	if len(data) == 0 {
		return &f, nil
	}
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	return &f, nil
}
```

- [ ] **Step 5: Write a round-trip compatibility test in `emit_test.go`**

This is the guard that emitted YAML stays loadable — the "no drift" guarantee.

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileMarshalLoadsBackThroughLoad(t *testing.T) {
	rec := false
	f := &File{
		Directories: []DirGroup{{
			ID:         "goland",
			Paths:      []string{"/tmp/does-not-matter/log"},
			Recursive:  &rec,
			FileFilter: &Filter{NameRegex: `idea\.log$`},
		}},
		Renderers: []Renderer{{
			Name: "json-line", LineRegex: `^\s*(\{.*\})\s*$`, Template: "json($1)",
		}},
	}
	data, err := f.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "log-listener.yml")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load([]string{"--config", path}, time.Now())
	if err != nil {
		t.Fatalf("Load(emitted): %v\n---\n%s", err, data)
	}
	if len(cfg.Groups) != 1 || cfg.Groups[0].ID != "goland" {
		t.Errorf("groups = %+v", cfg.Groups)
	}
	if len(cfg.RendererSpecs) != 1 || cfg.RendererSpecs[0].Name != "json-line" {
		t.Errorf("renderers = %+v", cfg.RendererSpecs)
	}
}
```

- [ ] **Step 6: Run + commit**

Run: `go test ./internal/config/ -v` → Expected: PASS

```bash
git add internal/config/yaml.go internal/config/yaml_test.go internal/config/emit.go internal/config/emit_test.go
git commit -m "config: export YAML schema, add File.Marshal + round-trip guard"
```

---

### Task 4: Lossless `MergeFiles`

**Files:**
- Modify: `internal/config/emit.go`
- Test: `internal/config/emit_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestMergeFilesAppendsOnlyNew(t *testing.T) {
	rec := false
	existing := &File{
		Directories: []DirGroup{{ID: "goland", Paths: []string{"/a"}, Recursive: &rec}},
		Renderers:   []Renderer{{Name: "json-line", LineRegex: "x", Template: "y"}},
		Output:      &Output{}, // already present -> must NOT be overwritten by defaults
	}
	gen := &File{
		Directories: []DirGroup{
			{ID: "goland", Paths: []string{"/SHOULD-BE-IGNORED"}}, // dup id -> skipped
			{ID: "idea", Paths: []string{"/b"}},                   // new -> appended
		},
		Renderers: []Renderer{
			{Name: "json-line", LineRegex: "NEW", Template: "NEW"}, // dup name -> skipped
			{Name: "idea-trailing-json", LineRegex: "p", Template: "q"},
		},
		Output: &Output{}, // existing wins -> ignored
		TUI:    &TUI{},    // existing has none -> set
	}
	out := MergeFiles(existing, gen)

	if len(out.Directories) != 2 || out.Directories[0].ID != "goland" || out.Directories[1].ID != "idea" {
		t.Fatalf("dirs = %+v", out.Directories)
	}
	if out.Directories[0].Paths[0] != "/a" {
		t.Errorf("existing goland clobbered: %+v", out.Directories[0])
	}
	if len(out.Renderers) != 2 || out.Renderers[1].Name != "idea-trailing-json" {
		t.Errorf("renderers = %+v", out.Renderers)
	}
	if out.Renderers[0].LineRegex != "x" {
		t.Errorf("existing renderer clobbered: %+v", out.Renderers[0])
	}
	if out.Output != existing.Output {
		t.Errorf("Output should be the existing pointer (unchanged)")
	}
	if out.TUI != gen.TUI {
		t.Errorf("TUI should be filled from gen when existing had none")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/config/ -run TestMergeFiles -v`
Expected: FAIL — `undefined: MergeFiles`.

- [ ] **Step 3: Implement `MergeFiles`**

```go
// MergeFiles returns existing with any groups/renderers from gen that are not
// already present (by id / name) appended. Existing entries are never modified
// or removed; Output/TUI from gen are applied only when existing has none.
// Lossless for user files: every loader-recognized field lives on File.
func MergeFiles(existing, gen *File) *File {
	out := *existing // shallow copy of header; slices are appended below

	dirIDs := map[string]bool{}
	for _, d := range out.Directories {
		dirIDs[d.ID] = true
	}
	for _, d := range gen.Directories {
		if !dirIDs[d.ID] {
			out.Directories = append(out.Directories, d)
			dirIDs[d.ID] = true
		}
	}

	fileIDs := map[string]bool{}
	for _, f := range out.Files {
		fileIDs[f.ID] = true
	}
	for _, f := range gen.Files {
		if !fileIDs[f.ID] {
			out.Files = append(out.Files, f)
			fileIDs[f.ID] = true
		}
	}

	rendNames := map[string]bool{}
	for _, r := range out.Renderers {
		rendNames[r.Name] = true
	}
	for _, r := range gen.Renderers {
		if !rendNames[r.Name] {
			out.Renderers = append(out.Renderers, r)
			rendNames[r.Name] = true
		}
	}

	if out.Output == nil {
		out.Output = gen.Output
	}
	if out.TUI == nil {
		out.TUI = gen.TUI
	}
	return &out
}
```

- [ ] **Step 4: Run + commit**

Run: `go test ./internal/config/ -run TestMergeFiles -v` → Expected: PASS

```bash
git add internal/config/emit.go internal/config/emit_test.go
git commit -m "config: lossless MergeFiles (append new groups/renderers)"
```

---

### Task 5: Token + `{product}` expansion

**Files:**
- Create: `internal/catalog/expand.go`
- Test: `internal/catalog/expand_test.go`

- [ ] **Step 1: Write the failing test**

```go
package catalog

import "testing"

func TestExpandPath(t *testing.T) {
	env := func(k string) string {
		return map[string]string{"LOCALAPPDATA": `C:/Users/me/AppData/Local`, "XDG_CACHE": "/x"}[k]
	}
	cases := []struct{ in, want string }{
		{"~/.cache/JetBrains/{product}*/log", "/home/me/.cache/JetBrains/GoLand*/log"},
		{"%LOCALAPPDATA%/JetBrains/{product}*/log", "C:/Users/me/AppData/Local/JetBrains/GoLand*/log"},
		{"$XDG_CACHE/{product}", "/x/GoLand"},
		{"%MISSING%/x", "%MISSING%/x"}, // unknown var left intact
	}
	for _, c := range cases {
		got := expandPath(substituteProduct(c.in, "GoLand"), "/home/me", env)
		if got != c.want {
			t.Errorf("expand(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSubstituteProductNoToken(t *testing.T) {
	if got := substituteProduct("/var/log/app", "GoLand"); got != "/var/log/app" {
		t.Errorf("got %q", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/catalog/ -run TestExpand -v`
Expected: FAIL — `undefined: expandPath`.

- [ ] **Step 3: Implement `expand.go`**

```go
package catalog

import (
	"os"
	"regexp"
	"strings"
)

var winVarRE = regexp.MustCompile(`%([A-Za-z_][A-Za-z0-9_]*)%`)

// substituteProduct replaces every {product} placeholder with product.
func substituteProduct(raw, product string) string {
	return strings.ReplaceAll(raw, "{product}", product)
}

// expandPath resolves ~, %WINVAR%, and $UNIXVAR against home/getenv. Unknown
// variables are left verbatim so a missing var produces a visibly-wrong path
// rather than a silently-empty one.
func expandPath(raw, home string, getenv func(string) string) string {
	if raw == "~" {
		return home
	}
	if strings.HasPrefix(raw, "~/") {
		raw = home + raw[1:]
	}
	raw = winVarRE.ReplaceAllStringFunc(raw, func(m string) string {
		name := m[1 : len(m)-1]
		if v := getenv(name); v != "" {
			return v
		}
		return m
	})
	raw = os.Expand(raw, func(name string) string {
		if v := getenv(name); v != "" {
			return v
		}
		return "${" + name + "}" // os.Expand strips $; restore unknowns approximately
	})
	return raw
}
```

Note: `os.Expand` consumes `$NAME`; for unknown unix vars we restore `${NAME}`. The catalog only uses `$XDG_*` style vars that are expected to exist on the target OS, so this edge is cosmetic.

- [ ] **Step 4: Run + commit**

Run: `go test ./internal/catalog/ -run "TestExpand|TestSubstitute" -v` → Expected: PASS

```bash
git add internal/catalog/expand.go internal/catalog/expand_test.go
git commit -m "catalog: {product} + OS path-token expansion"
```

---

### Task 6: Resolution — compose, probe-and-pick, emit `config.File`

**Files:**
- Create: `internal/catalog/resolve.go`
- Test: `internal/catalog/resolve_test.go`

- [ ] **Step 1: Write the failing test**

```go
package catalog

import (
	"testing"
)

func testCatalog(t *testing.T) *Catalog {
	t.Helper()
	c, err := Parse([]byte(`
version: 1
defaults:
  output: { color: true, drop_unmatched: false }
  tui: { enabled: true, scrollback: 20000 }
fragments:
  jetbrains-base:
    sources:
      - id: main
        filter: 'idea\.log(\.\d+)?$'
        locations:
          - dir: { linux: '~/.cache/JetBrains/{product}*/log' }
          - dir: { linux: '~/.{product}*/system/log' }
  junie-bridge:
    sources:
      - id: junie
        filter: '\.log$'
        locations:
          - dir: { linux: '~/.cache/JetBrains/{product}*/log/junie' }
apps:
  goland:
    renderers: [json-line]
    use:
      - { frag: jetbrains-base, product: GoLand }
    sources:
      - id: acp
        filter: '\.log$'
        locations:
          - dir: { linux: '~/.cache/JetBrains/GoLand*/log/acp' }
  idea:
    renderers: [json-line]
    use:
      - { frag: jetbrains-base, product: IntelliJIdea }
      - { frag: junie-bridge,   product: IntelliJIdea }
renderers:
  json-line: { line_regex: '^\s*(\{.*\})\s*$', template: 'json($1)' }
bundles:
  jetbrains: [goland, idea]
`))
	if err != nil {
		t.Fatalf("parse test catalog: %v", err)
	}
	return c
}

func TestResolveGoland_NewSchemeExists(t *testing.T) {
	c := testCatalog(t)
	exists := func(p string) bool {
		// new scheme present; acp present; legacy absent
		return p == "/home/me/.cache/JetBrains/GoLand*/log" ||
			p == "/home/me/.cache/JetBrains/GoLand*/log/acp"
	}
	env := Env{OS: "linux", Home: "/home/me", Getenv: func(string) string { return "" }, Exists: exists}

	f, err := c.Resolve([]string{"goland"}, env)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(f.Directories) != 2 {
		t.Fatalf("dirs = %+v", f.Directories)
	}
	if f.Directories[0].ID != "goland" || f.Directories[0].Paths[0] != "/home/me/.cache/JetBrains/GoLand*/log" {
		t.Errorf("base group = %+v", f.Directories[0])
	}
	if f.Directories[0].FileFilter == nil || f.Directories[0].FileFilter.NameRegex != `idea\.log(\.\d+)?$` {
		t.Errorf("base filter = %+v", f.Directories[0].FileFilter)
	}
	if f.Directories[1].ID != "goland-acp" {
		t.Errorf("acp group id = %q", f.Directories[1].ID)
	}
	if len(f.Renderers) != 1 || f.Renderers[0].Name != "json-line" {
		t.Errorf("renderers = %+v", f.Renderers)
	}
	if f.Output == nil || f.Output.Color == nil || !*f.Output.Color {
		t.Errorf("output defaults missing: %+v", f.Output)
	}
	if f.TUI == nil || f.TUI.Scrollback == nil || *f.TUI.Scrollback != 20000 {
		t.Errorf("tui defaults missing: %+v", f.TUI)
	}
}

func TestResolveFallbackToNewestWhenNoneExist(t *testing.T) {
	c := testCatalog(t)
	env := Env{OS: "linux", Home: "/home/me", Getenv: func(string) string { return "" },
		Exists: func(string) bool { return false }}
	f, err := c.Resolve([]string{"goland"}, env)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// none exist -> newest (first) candidate emitted best-effort
	if f.Directories[0].Paths[0] != "/home/me/.cache/JetBrains/GoLand*/log" {
		t.Errorf("fallback path = %v", f.Directories[0].Paths)
	}
}

func TestResolveBothCandidatesExistEmitsBoth(t *testing.T) {
	c := testCatalog(t)
	env := Env{OS: "linux", Home: "/home/me", Getenv: func(string) string { return "" },
		Exists: func(string) bool { return true }}
	f, _ := c.Resolve([]string{"goland"}, env)
	if len(f.Directories[0].Paths) != 2 {
		t.Errorf("want both candidate paths, got %v", f.Directories[0].Paths)
	}
}

func TestResolveBundleAndDedup(t *testing.T) {
	c := testCatalog(t)
	env := Env{OS: "linux", Home: "/home/me", Getenv: func(string) string { return "" },
		Exists: func(string) bool { return true }}
	f, err := c.Resolve([]string{"jetbrains"}, env) // expands to goland, idea
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// json-line referenced by both apps -> emitted once
	if len(f.Renderers) != 1 {
		t.Errorf("dedup failed: %+v", f.Renderers)
	}
	ids := map[string]bool{}
	for _, d := range f.Directories {
		if ids[d.ID] {
			t.Errorf("duplicate group id %q", d.ID)
		}
		ids[d.ID] = true
	}
	if !ids["idea-intellijidea-junie"] {
		t.Errorf("missing idea junie-bridge group; ids=%v", ids)
	}
}

func TestResolveUnknownName(t *testing.T) {
	c := testCatalog(t)
	env := Env{OS: "linux", Home: "/home/me", Getenv: func(string) string { return "" },
		Exists: func(string) bool { return true }}
	if _, err := c.Resolve([]string{"nope"}, env); err == nil {
		t.Fatal("expected error for unknown name")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/catalog/ -run TestResolve -v`
Expected: FAIL — `undefined: Env` / `Resolve`.

- [ ] **Step 3: Implement `resolve.go`**

```go
package catalog

import (
	"fmt"
	"runtime"
	"strings"

	"log-listener/internal/config"
)

// Env carries the host facts resolution depends on. Injected for testability;
// DefaultEnv builds the live one.
type Env struct {
	OS     string                  // "linux" | "darwin" | "windows"
	Home   string                  // user home directory
	Getenv func(string) string     // environment lookup
	Exists func(dirGlob string) bool // true if the dir-glob matches an existing directory
}

func osKey(os string) string {
	if os == "" {
		return runtime.GOOS
	}
	return os
}

// Resolve composes the named apps/bundles into a config.File for env's OS.
func (c *Catalog) Resolve(names []string, env Env) (*config.File, error) {
	apps, err := c.expandNames(names)
	if err != nil {
		return nil, err
	}
	key := osKey(env.OS)

	f := &config.File{}
	seenID := map[string]bool{}
	seenRend := map[string]bool{}

	for _, appName := range apps {
		app := c.Apps[appName] // existence guaranteed by expandNames

		// fragment-sourced groups (carry their product binding)
		for _, u := range app.Use {
			frag, ok := c.Fragments[u.Frag]
			if !ok {
				return nil, fmt.Errorf("app %q: unknown fragment %q", appName, u.Frag)
			}
			for _, src := range frag.Sources {
				c.emitSource(f, appName, u.Product, src, key, env, seenID)
			}
		}
		// inline app sources (no product binding)
		for _, src := range app.Sources {
			c.emitSource(f, appName, "", src, key, env, seenID)
		}
		// renderers (deduped by name across all apps)
		for _, rn := range app.Renderers {
			if seenRend[rn] {
				continue
			}
			r, ok := c.Renderers[rn]
			if !ok {
				return nil, fmt.Errorf("app %q: unknown renderer %q", appName, rn)
			}
			f.Renderers = append(f.Renderers, config.Renderer{
				Name: rn, LineRegex: r.LineRegex, Template: r.Template,
			})
			seenRend[rn] = true
		}
	}

	// global defaults
	color := c.Defaults.Output.Color
	drop := c.Defaults.Output.DropUnmatched
	f.Output = &config.Output{Color: &color, DropUnmatched: &drop}
	enabled := c.Defaults.TUI.Enabled
	scroll := c.Defaults.TUI.Scrollback
	f.TUI = &config.TUI{Enabled: &enabled, Scrollback: &scroll}

	return f, nil
}

// expandNames flattens bundles into a deduped, order-preserving app list.
func (c *Catalog) expandNames(names []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	add := func(app string) {
		if !seen[app] {
			seen[app] = true
			out = append(out, app)
		}
	}
	for _, n := range names {
		switch {
		case c.Bundles[n] != nil:
			for _, app := range c.Bundles[n] {
				if _, ok := c.Apps[app]; !ok {
					return nil, fmt.Errorf("bundle %q references unknown app %q", n, app)
				}
				add(app)
			}
		case func() bool { _, ok := c.Apps[n]; return ok }():
			add(n)
		default:
			return nil, fmt.Errorf("unknown app or bundle %q (see `log-listener init --list`)", n)
		}
	}
	return out, nil
}

// emitSource probe-and-picks a source's drift candidates and appends a
// directory group to f.
func (c *Catalog) emitSource(f *config.File, app, product string, src Source, key string, env Env, seenID map[string]bool) {
	var picked []string
	var newest string
	for _, loc := range src.Locations {
		raw, ok := loc.Dir[key]
		if !ok {
			continue // no path for this OS
		}
		p := expandPath(substituteProduct(raw, product), env.Home, env.Getenv)
		if newest == "" {
			newest = p
		}
		if env.Exists(p) {
			picked = append(picked, p)
		}
	}
	if len(picked) == 0 {
		if newest == "" {
			return // nothing defined for this OS at all
		}
		picked = []string{newest} // best-effort
	}
	rec := false
	g := config.DirGroup{
		ID:        groupID(app, product, src.ID, seenID),
		Paths:     picked,
		Recursive: &rec,
	}
	if src.Filter != "" {
		g.FileFilter = &config.Filter{NameRegex: src.Filter}
	}
	f.Directories = append(f.Directories, g)
}

// groupID builds a unique, readable directory-group id.
func groupID(app, product, sourceID string, seen map[string]bool) string {
	id := app
	if product != "" && !strings.EqualFold(product, app) {
		id += "-" + strings.ToLower(product)
	}
	if sourceID != "" && sourceID != "main" {
		id += "-" + sourceID
	}
	base := id
	for n := 2; seen[id]; n++ {
		id = fmt.Sprintf("%s-%d", base, n)
	}
	seen[id] = true
	return id
}
```

- [ ] **Step 4: Run to verify all resolve tests pass**

Run: `go test ./internal/catalog/ -run TestResolve -v`
Expected: PASS (all five).

- [ ] **Step 5: Commit**

```bash
git add internal/catalog/resolve.go internal/catalog/resolve_test.go
git commit -m "catalog: Resolve composes apps into a config.File (probe-and-pick)"
```

---

### Task 7: Real bottled `catalog.yml` + validity test

**Files:**
- Modify: `internal/catalog/catalog.yml`
- Test: `internal/catalog/catalog_content_test.go`

- [ ] **Step 1: Write the failing validity test**

```go
package catalog

import "testing"

func TestBundledResolvesEveryAppOnEveryOS(t *testing.T) {
	c, err := Bundled()
	if err != nil {
		t.Fatalf("Bundled: %v", err)
	}
	if len(c.Apps) == 0 {
		t.Fatal("bundled catalog has no apps")
	}
	env := func(os string) Env {
		return Env{OS: os, Home: "/home/u", Getenv: func(string) string { return "C:/AppData" },
			Exists: func(string) bool { return false }} // force best-effort path on all
	}
	for name := range c.Apps {
		for _, os := range []string{"linux", "darwin", "windows"} {
			f, err := c.Resolve([]string{name}, env(os))
			if err != nil {
				t.Errorf("Resolve(%q, %s): %v", name, os, err)
				continue
			}
			for _, d := range f.Directories {
				if len(d.Paths) == 0 {
					t.Errorf("%q/%s: group %q has no paths", name, os, d.ID)
				}
			}
		}
	}
	if c.Bundles["jetbrains"] == nil {
		t.Error("expected a 'jetbrains' bundle")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/catalog/ -run TestBundledResolves -v`
Expected: FAIL — stub catalog has no apps.

- [ ] **Step 3: Replace `internal/catalog/catalog.yml` with real content**

```yaml
# log-listener template catalog. `log-listener init <app>...` resolves these
# for the current OS. version is compared against the online catalog.
version: 1

defaults:
  output: { color: true, drop_unmatched: false }
  tui: { enabled: true, scrollback: 20000 }

fragments:
  # The shared JetBrains discovery: ~/.cache/JetBrains/<Product><Version>/log,
  # parameterized by {product}. Newest scheme first, legacy second.
  jetbrains-base:
    sources:
      - id: main
        filter: '^(idea\.log(\.\d+)?|open-telemetry-meters\..+\.json)$'
        locations:
          - dir:
              linux:   '~/.cache/JetBrains/{product}*/log'
              darwin:  '~/Library/Logs/JetBrains/{product}*'
              windows: '%LOCALAPPDATA%/JetBrains/{product}*/log'
          - dir:
              linux:   '~/.{product}*/system/log'
              darwin:  '~/Library/Caches/{product}*/log'
              windows: '%USERPROFILE%/.{product}*/system/log'
          - dir:  # WSL: the Windows-side install seen from Linux
              linux: '/mnt/c/Users/*/AppData/Local/JetBrains/{product}*/log'
  # Junie<->IDE bridge logs, physically inside a JetBrains product's log dir.
  junie-bridge:
    sources:
      - id: junie
        filter: '\.log$'
        locations:
          - dir:
              linux:   '~/.cache/JetBrains/{product}*/log/junie'
              darwin:  '~/Library/Logs/JetBrains/{product}*/junie'
              windows: '%LOCALAPPDATA%/JetBrains/{product}*/log/junie'
  # Junie agent's own logs.
  junie-direct:
    sources:
      - id: agent
        filter: '\.log$'
        locations:
          - dir:
              linux:   '~/.config/junie/log'
              darwin:  '~/Library/Logs/junie'
              windows: '%LOCALAPPDATA%/junie/log'

apps:
  goland:
    renderers: [json-line, idea-trailing-json]
    use:
      - { frag: jetbrains-base, product: GoLand }
    sources:
      - id: acp
        filter: '\.log$'
        locations:
          - dir:
              linux:   '~/.cache/JetBrains/GoLand*/log/acp'
              darwin:  '~/Library/Logs/JetBrains/GoLand*/acp'
              windows: '%LOCALAPPDATA%/JetBrains/GoLand*/log/acp'
  idea:
    renderers: [json-line, idea-trailing-json]
    use:
      - { frag: jetbrains-base, product: IntelliJIdea }
      - { frag: junie-bridge,   product: IntelliJIdea }
  pycharm:
    renderers: [json-line, idea-trailing-json]
    use:
      - { frag: jetbrains-base, product: PyCharm }
  webstorm:
    renderers: [json-line, idea-trailing-json]
    use:
      - { frag: jetbrains-base, product: WebStorm }
  junie:
    use:
      - { frag: junie-direct }
      - { frag: junie-bridge, product: IntelliJIdea }
      - { frag: junie-bridge, product: GoLand }

renderers:
  json-line:
    line_regex: '^\s*(\{.*\})\s*$'
    template: 'json($1)'
  idea-trailing-json:
    line_regex: '^(.*?\s)(\{.+\})\s*$'
    template: '$1\njson($2)'

bundles:
  jetbrains: [goland, idea, pycharm, webstorm]
```

- [ ] **Step 4: Bump the stub-replacing version and run**

Run: `go test ./internal/catalog/ -v`
Expected: PASS (all catalog tests, including `TestBundledResolvesEveryAppOnEveryOS`).

- [ ] **Step 5: Commit**

```bash
git add internal/catalog/catalog.yml internal/catalog/catalog_content_test.go
git commit -m "catalog: real bottled catalog (JetBrains family + Junie)"
```

---

### Task 8: `init` subcommand (offline)

**Files:**
- Create: `cmd/log-listener/init.go`
- Test: `cmd/log-listener/init_test.go`

`runInit` signature: `func runInit(args []string, stdout, stderr io.Writer) int`. Offline-only here (Phase 2 injects the catalog source); this task always uses `catalog.Bundled()`.

- [ ] **Step 1: Write the failing test**

```go
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitWritesFile(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "log-listener.yml")
	var stdout, stderr bytes.Buffer

	code := runInit([]string{"goland", "-o", out, "--offline"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d, stderr=%s", code, stderr.String())
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "id: goland") {
		t.Errorf("missing goland group:\n%s", data)
	}
	if !strings.Contains(string(data), "json-line") {
		t.Errorf("missing renderer:\n%s", data)
	}
}

func TestInitStdout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runInit([]string{"goland", "-o", "-", "--offline"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "directories:") {
		t.Errorf("stdout not YAML:\n%s", stdout.String())
	}
}

func TestInitUnknownApp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runInit([]string{"nope", "-o", "-", "--offline"}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("expected non-zero exit for unknown app")
	}
	if !strings.Contains(stderr.String(), "nope") {
		t.Errorf("stderr should name the bad app: %s", stderr.String())
	}
}

func TestInitNoOverwriteWithoutForce(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "log-listener.yml")
	if err := os.WriteFile(out, []byte("directories: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	// non-TTY buffers => no prompt => refuse without --force
	code := runInit([]string{"goland", "-o", out, "--offline"}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("expected refusal to overwrite without --force")
	}
}

func TestInitForceMerge(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "log-listener.yml")
	if err := os.WriteFile(out, []byte("directories:\n  - id: mine\n    paths: [/x]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := runInit([]string{"goland", "-o", out, "--offline", "--force", "--merge"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	data, _ := os.ReadFile(out)
	s := string(data)
	if !strings.Contains(s, "id: mine") || !strings.Contains(s, "id: goland") {
		t.Errorf("merge dropped an entry:\n%s", s)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/log-listener/ -run TestInit -v`
Expected: FAIL — `undefined: runInit`.

- [ ] **Step 3: Implement `init.go`**

```go
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"log-listener/internal/catalog"
	"log-listener/internal/config"
)

// runInit implements `log-listener init <app|bundle>... [flags]`.
// Flags: -o <path|-> (default ./log-listener.yml), --offline/--online,
// --force (overwrite/merge non-interactively), --merge (merge vs overwrite),
// --list (print available apps/bundles).
func runInit(args []string, stdout, stderr io.Writer) int {
	var names []string
	outPath := "log-listener.yml"
	var offline, force, merge, list bool
	online := false

	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "-o", "--output":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "log-listener init: -o needs a value")
				return 2
			}
			outPath = args[i+1]
			i++
		case "--offline":
			offline = true
		case "--online":
			online = true
		case "--force":
			force = true
		case "--merge":
			merge = true
		case "--list":
			list = true
		default:
			if strings.HasPrefix(a, "-") {
				fmt.Fprintf(stderr, "log-listener init: unknown flag %q\n", a)
				return 2
			}
			names = append(names, a)
		}
	}
	_ = online // Phase 2 uses this; offline path ignores it

	cat, err := catalog.Bundled()
	if err != nil {
		fmt.Fprintln(stderr, "log-listener init:", err)
		return 1
	}

	if list {
		printList(stdout, cat)
		return 0
	}
	if len(names) == 0 {
		fmt.Fprintln(stderr, "log-listener init: name at least one app or bundle (try --list)")
		return 2
	}
	_ = offline // offline is the only mode in Phase 1

	env := catalog.DefaultEnv()
	gen, err := cat.Resolve(names, env)
	if err != nil {
		fmt.Fprintln(stderr, "log-listener init:", err)
		return 2
	}

	if outPath == "-" {
		data, err := gen.Marshal()
		if err != nil {
			fmt.Fprintln(stderr, "log-listener init:", err)
			return 1
		}
		_, _ = stdout.Write(data)
		return 0
	}

	final := gen
	if existingData, err := os.ReadFile(outPath); err == nil {
		// file exists
		if !force {
			fmt.Fprintf(stderr, "log-listener init: %s exists; pass --force (with optional --merge) to write\n", outPath)
			return 1
		}
		if merge {
			existing, err := config.ParseFile(existingData)
			if err != nil {
				fmt.Fprintln(stderr, "log-listener init: cannot parse existing file:", err)
				return 1
			}
			final = config.MergeFiles(existing, gen)
		}
	}

	data, err := final.Marshal()
	if err != nil {
		fmt.Fprintln(stderr, "log-listener init:", err)
		return 1
	}
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		fmt.Fprintln(stderr, "log-listener init:", err)
		return 1
	}
	fmt.Fprintf(stdout, "wrote %s (%d groups, %d renderers)\n",
		outPath, len(final.Directories)+len(final.Files), len(final.Renderers))
	return 0
}

func printList(w io.Writer, cat *catalog.Catalog) {
	fmt.Fprintln(w, "apps:")
	for name := range cat.Apps {
		fmt.Fprintf(w, "  %s\n", name)
	}
	fmt.Fprintln(w, "bundles:")
	for name, apps := range cat.Bundles {
		fmt.Fprintf(w, "  %s: %s\n", name, strings.Join(apps, ", "))
	}
}

var _ = filepath.Join // retained for future path normalization
var _ = runtime.GOOS
```

- [ ] **Step 4: Add `catalog.DefaultEnv`**

Append to `internal/catalog/resolve.go`:

```go
import (
	"os"            // add to the existing import block
	"path/filepath" // add to the existing import block
)

// DefaultEnv builds the live Env: real OS, home dir, environment, and an
// Exists that reports whether a directory glob matches at least one directory.
func DefaultEnv() Env {
	home, _ := os.UserHomeDir()
	return Env{
		OS:     runtime.GOOS,
		Home:   home,
		Getenv: os.Getenv,
		Exists: func(dirGlob string) bool {
			matches, err := filepath.Glob(dirGlob)
			if err != nil {
				return false
			}
			for _, m := range matches {
				if fi, err := os.Stat(m); err == nil && fi.IsDir() {
					return true
				}
			}
			return false
		},
	}
}
```

(Adjust the `resolve.go` import block to include `os` and `path/filepath`; remove the placeholder `var _ =` lines from `init.go` if `filepath`/`runtime` end up unused — run `go build ./cmd/log-listener/` and let the compiler guide you.)

- [ ] **Step 5: Run + commit**

Run: `go test ./cmd/log-listener/ -run TestInit -v` → Expected: PASS (all five)

```bash
git add cmd/log-listener/init.go cmd/log-listener/init_test.go internal/catalog/resolve.go
git commit -m "init: offline subcommand writes/merges log-listener.yml"
```

---

### Task 9: Dispatch `init` from `main`

**Files:**
- Modify: `cmd/log-listener/main.go` (function `run`, top of body ~line 36)
- Test: `cmd/log-listener/init_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestRunDispatchesInit(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"init", "goland", "-o", "-", "--offline"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "directories:") {
		t.Errorf("init not dispatched:\n%s", stdout.String())
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/log-listener/ -run TestRunDispatchesInit -v`
Expected: FAIL — `run` treats `init` as an unknown flag (exit 2).

- [ ] **Step 3: Add dispatch at the top of `run`**

In `cmd/log-listener/main.go`, insert as the very first statements inside `func run(args []string, stdout, stderr io.Writer) int {` (before `config.Load`):

```go
	if len(args) > 0 && args[0] == "init" {
		return runInit(args[1:], stdout, stderr)
	}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./cmd/log-listener/ -run TestRunDispatchesInit -v`
Expected: PASS

- [ ] **Step 5: Full suite + vet + race, then commit**

Run:
```bash
go test ./... && go vet ./... && go test -race ./...
```
Expected: all PASS.

```bash
git add cmd/log-listener/main.go cmd/log-listener/init_test.go
git commit -m "main: dispatch the init subcommand"
```

**Phase 1 is complete and shippable here:** `log-listener init goland junie` produces a working `log-listener.yml` offline.

---

## PHASE 2 — Online catalog update

### Task 10: Remote fetch + cache behind a `Fetcher`

**Files:**
- Create: `internal/catalog/remote.go`
- Test: `internal/catalog/remote_test.go`

- [ ] **Step 1: Write the failing test**

```go
package catalog

import (
	"errors"
	"testing"
)

type fakeFetcher struct {
	data []byte
	err  error
}

func (f fakeFetcher) Fetch() ([]byte, error) { return f.data, f.err }

func newerCatalogYAML(v int) []byte {
	return []byte("version: " + itoa(v) + "\ndefaults:\n  output: {color: true, drop_unmatched: false}\n  tui: {enabled: true, scrollback: 1}\nfragments: {}\napps: {}\nrenderers: {}\nbundles: {}\n")
}

func TestSelectPrefersNewerRemote(t *testing.T) {
	bundled, _ := Parse(newerCatalogYAML(2))
	got := Select(bundled, fakeFetcher{data: newerCatalogYAML(5)})
	if got.Version != 5 {
		t.Errorf("version = %d, want 5 (remote newer)", got.Version)
	}
}

func TestSelectKeepsBundledWhenRemoteOlder(t *testing.T) {
	bundled, _ := Parse(newerCatalogYAML(9))
	got := Select(bundled, fakeFetcher{data: newerCatalogYAML(3)})
	if got.Version != 9 {
		t.Errorf("version = %d, want 9 (bundled newer)", got.Version)
	}
}

func TestSelectFallsBackOnFetchError(t *testing.T) {
	bundled, _ := Parse(newerCatalogYAML(4))
	got := Select(bundled, fakeFetcher{err: errors.New("offline")})
	if got.Version != 4 {
		t.Errorf("version = %d, want 4 (fallback)", got.Version)
	}
}

func TestSelectFallsBackOnMalformedRemote(t *testing.T) {
	bundled, _ := Parse(newerCatalogYAML(4))
	got := Select(bundled, fakeFetcher{data: []byte("{{ not yaml")})
	if got.Version != 4 {
		t.Errorf("version = %d, want 4 (malformed remote ignored)", got.Version)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/catalog/ -run TestSelect -v`
Expected: FAIL — `undefined: Select` / `itoa`.

- [ ] **Step 3: Implement `remote.go`**

```go
package catalog

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// CatalogURL is the raw URL of the published catalog. Fill in owner/repo before
// release; the default branch is assumed.
const CatalogURL = "https://raw.githubusercontent.com/OWNER/log-listener/main/internal/catalog/catalog.yml"

// Fetcher retrieves a raw catalog document. Network access lives only here.
type Fetcher interface {
	Fetch() ([]byte, error)
}

// HTTPFetcher fetches CatalogURL over HTTPS with a short timeout.
type HTTPFetcher struct {
	URL    string
	Client *http.Client
}

// NewHTTPFetcher returns a Fetcher for CatalogURL with a 5s timeout.
func NewHTTPFetcher() HTTPFetcher {
	return HTTPFetcher{URL: CatalogURL, Client: &http.Client{Timeout: 5 * time.Second}}
}

func (h HTTPFetcher) Fetch() ([]byte, error) {
	resp, err := h.Client.Get(h.URL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &httpError{resp.StatusCode}
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB cap
}

type httpError struct{ code int }

func (e *httpError) Error() string { return "catalog fetch: HTTP " + strconv.Itoa(e.code) }

// Select returns whichever of bundled or the fetched remote catalog has the
// higher version. ANY fetch/parse failure silently yields bundled.
func Select(bundled *Catalog, f Fetcher) *Catalog {
	data, err := f.Fetch()
	if err != nil {
		return bundled
	}
	remote, err := Parse(data)
	if err != nil {
		return bundled
	}
	if remote.Version > bundled.Version {
		cacheWrite(data) // best-effort
		return remote
	}
	return bundled
}

func itoa(n int) string { return strconv.Itoa(n) }

// cacheDir / cacheWrite persist the chosen remote for offline reuse.
func cacheDir() string {
	base, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "log-listener")
}

func cacheWrite(data []byte) {
	dir := cacheDir()
	if dir == "" {
		return
	}
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "catalog.yml"), data, 0o644)
}
```

- [ ] **Step 4: Run + commit**

Run: `go test ./internal/catalog/ -run TestSelect -v` → Expected: PASS

```bash
git add internal/catalog/remote.go internal/catalog/remote_test.go
git commit -m "catalog: online Fetcher + version-compare Select with bundled fallback"
```

---

### Task 11: Wire online prompt + `--online`/`--offline` into `init`

**Files:**
- Modify: `cmd/log-listener/init.go`
- Test: `cmd/log-listener/init_test.go`

Behavior: by default (per design) `init` **prompts every run** when stdin is a TTY. `--online` / `--offline` skip the prompt. Non-TTY stdin (tests, pipes) → offline, no prompt.

- [ ] **Step 1: Write the failing test**

```go
func TestInitOnlineUsesFetcher(t *testing.T) {
	var stdout, stderr bytes.Buffer
	// inject a fake fetcher returning a higher-version catalog with an extra app
	prev := initFetcher
	t.Cleanup(func() { initFetcher = prev })
	initFetcher = func() catalog.Fetcher {
		return stubFetcher([]byte(`
version: 9999
defaults: {output: {color: true, drop_unmatched: false}, tui: {enabled: true, scrollback: 1}}
fragments: {}
renderers: {}
bundles: {}
apps:
  zzz-remote-only:
    use: []
    sources:
      - id: main
        filter: '\.log$'
        locations: [ { dir: { linux: '/var/log/zzz' } } ]
`))
	}
	code := runInit([]string{"zzz-remote-only", "-o", "-", "--online"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "zzz-remote-only") {
		t.Errorf("remote app not resolved:\n%s", stdout.String())
	}
}

type stubFetcher []byte

func (s stubFetcher) Fetch() ([]byte, error) { return []byte(s), nil }
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/log-listener/ -run TestInitOnline -v`
Expected: FAIL — `undefined: initFetcher`.

- [ ] **Step 3: Refactor `init.go` to choose the catalog source**

Add near the top of `init.go`:

```go
// initFetcher is a seam so tests can inject a fake remote catalog.
var initFetcher = func() catalog.Fetcher { return catalog.NewHTTPFetcher() }
```

Replace the `cat, err := catalog.Bundled()` block and the `_ = offline/_ = online` lines with:

```go
	bundled, err := catalog.Bundled()
	if err != nil {
		fmt.Fprintln(stderr, "log-listener init:", err)
		return 1
	}

	cat := bundled
	useOnline := online
	if !online && !offline && isTTY(os.Stdin) {
		useOnline = promptYesNo(stdout, os.Stdin, "Check GitHub for newer templates?")
	}
	if useOnline {
		cat = catalog.Select(bundled, initFetcher())
	}
```

Add helpers to `init.go`:

```go
func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func promptYesNo(w io.Writer, r io.Reader, q string) bool {
	fmt.Fprintf(w, "%s [Y/n] ", q)
	buf := make([]byte, 1)
	if _, err := r.Read(buf); err != nil {
		return false
	}
	return buf[0] != 'n' && buf[0] != 'N'
}
```

Remove the now-unused `_ = online` / `_ = offline` placeholder lines.

- [ ] **Step 4: Run + commit**

Run: `go test ./cmd/log-listener/ -v` → Expected: PASS (offline tests unaffected — non-TTY stdin keeps them offline)

```bash
git add cmd/log-listener/init.go cmd/log-listener/init_test.go
git commit -m "init: online prompt + --online/--offline catalog selection"
```

---

### Task 12: Documentation + final gate

**Files:**
- Modify: `README.md` (add an `init` section), `CHANGELOG.md`

- [ ] **Step 1: Add a README section**

Under the capabilities/usage area of `README.md`, add:

````markdown
## Quick start with templates

Generate a config for known applications instead of hand-writing one:

```bash
log-listener init goland junie          # write ./log-listener.yml
log-listener init jetbrains -o dev.yml  # a whole product family
log-listener init goland -o -           # print to stdout
log-listener init --list                # show available apps and bundles
```

`init` resolves each app's log locations for your OS, picks the directories that
actually exist, attaches sensible renderers, and writes a `log-listener.yml` the
normal `log-listener` run consumes (and live-reloads). It will offer to fetch a
newer template catalog from GitHub; `--offline` skips the check, `--online`
forces it. An existing file is left untouched unless you pass `--force`
(optionally with `--merge` to append only new entries).
````

- [ ] **Step 2: Add a CHANGELOG entry**

Add a bullet under the current/unreleased section of `CHANGELOG.md`:

```markdown
- **Template auto-configuration.** `log-listener init <apps...>` generates a
  `log-listener.yml` from an embedded, OS-aware catalog of application log
  templates (JetBrains family + Junie), with optional online catalog updates.
```

- [ ] **Step 3: Final full gate**

Run:
```bash
go build ./... && go test ./... && go vet ./... && go test -race ./...
```
Expected: all PASS.

- [ ] **Step 4: Commit**

```bash
git add README.md CHANGELOG.md
git commit -m "docs: document the init template command"
```

---

## Self-Review notes (for the executor)

- **Spec coverage:** three axes → Task 1 schema (`locations` list + per-OS `dir` map + top-level `version`); fragments/composition → Tasks 1,6; probe-and-pick → Task 6; group-id uniqueness + renderer dedup → Task 6; renderers+defaults emitted → Task 6; `init` UX incl. `-o`, overwrite/merge, non-TTY → Tasks 8,11; online update + bottled fallback → Tasks 10,11; testing strategy → every task is TDD; the spec's "promote schema" refinement (kept as shared structs + a round-trip guard) → Task 3.
- **Known deviation from spec wording:** the spec said "promote the loader structs and reuse for emit." This plan does exactly that (Task 3 exports the loader structs and the emitter reuses them) and adds a round-trip test as the anti-drift guard. No separate emit schema.
- **Placeholder left intentionally:** `CatalogURL`'s `OWNER` (Task 10) — the real GitHub owner/repo, the one external unknown noted in spec §11. Fill before release; tests never hit the network (fakes only).
- **Type consistency:** `Env{OS,Home,Getenv,Exists}`, `Resolve`, `emitSource`, `groupID`, `config.File/DirGroup/Filter/Renderer/Output/TUI`, `MergeFiles`, `Select`, `Fetcher`, `initFetcher` are used consistently across tasks.
