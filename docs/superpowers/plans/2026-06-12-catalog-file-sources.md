# Catalog File-Based Sources Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let catalog sources declare `file:` locations (symmetric with `dir:`) that resolve to `config.FileGroup` entries, and add an authoring guide next to `catalog.yml`.

**Architecture:** `Location` gains a `File` map parallel to `Dir`. A strict `validate()` pass runs from `Parse` (bundled catalog only) enforcing exactly-one-of `dir`/`file`, uniform mode per source, and no `filter` on file sources. `emitSource` branches on mode: file-mode sources probe candidates with a new `Env.ExistsFile` and emit `config.FileGroup` into `f.Files`; dir mode is unchanged. `catalog.yml` itself is NOT modified.

**Tech Stack:** Go 1.26, `gopkg.in/yaml.v3` (strict decode via `KnownFields`), stdlib only.

**Spec:** `docs/superpowers/specs/2026-06-12-catalog-file-sources-design.md`

---

### Task 1: Schema — `file:` location type + strict validation

**Files:**
- Modify: `internal/catalog/schema.go`
- Test: `internal/catalog/schema_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/catalog/schema_test.go`:

```go
func TestParseFileLocation(t *testing.T) {
	c, err := Parse([]byte(`
version: 1
fragments:
  junie-logs:
    sources:
      - id: main
        locations:
          - file: { linux: '~/.junie/logs/agent.log' }
          - file: { linux: '~/.junie-local/logs/agent.log' }
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	locs := c.Fragments["junie-logs"].Sources[0].Locations
	if got := locs[0].File["linux"]; got != "~/.junie/logs/agent.log" {
		t.Errorf("file location = %q", got)
	}
	if locs[0].Dir != nil {
		t.Errorf("dir should be unset on a file location: %+v", locs[0].Dir)
	}
}

func TestParseRejectsLocationWithBothDirAndFile(t *testing.T) {
	_, err := Parse([]byte(`
version: 1
fragments:
  bad:
    sources:
      - id: main
        locations:
          - dir: { linux: '~/logs' }
            file: { linux: '~/logs/app.log' }
`))
	if err == nil {
		t.Fatal("expected error for location with both dir and file")
	}
}

func TestParseRejectsLocationWithNeither(t *testing.T) {
	_, err := Parse([]byte(`
version: 1
fragments:
  bad:
    sources:
      - id: main
        locations:
          - {}
`))
	if err == nil {
		t.Fatal("expected error for location with neither dir nor file")
	}
}

func TestParseRejectsMixedModeSource(t *testing.T) {
	_, err := Parse([]byte(`
version: 1
apps:
  bad:
    sources:
      - id: main
        locations:
          - dir: { linux: '~/logs' }
          - file: { linux: '~/logs/app.log' }
`))
	if err == nil {
		t.Fatal("expected error for source mixing dir and file locations")
	}
}

func TestParseRejectsFilterOnFileSource(t *testing.T) {
	_, err := Parse([]byte(`
version: 1
apps:
  bad:
    sources:
      - id: main
        filter: '\.log$'
        locations:
          - file: { linux: '~/logs/app.log' }
`))
	if err == nil {
		t.Fatal("expected error for filter on a file-based source")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run 'TestParse(FileLocation|Rejects)' ./internal/catalog/ -v`

Expected: `TestParseFileLocation` FAILS (unknown key `file` under strict decode) and `TestParseRejectsLocationWithNeither` FAILS with "expected error ... got nil". The other three `Rejects*` tests currently PASS for the wrong reason — their YAML contains a `file:` key, which strict decode already rejects as unknown. That is fine; they lock in the error behavior and keep passing once `file:` becomes a real key with validation.

- [ ] **Step 3: Implement `File` field and `validate()`**

In `internal/catalog/schema.go`, replace the `Location` type:

```go
// Location is one drift candidate. Exactly one of Dir/File is set; both map
// an OS key (linux/darwin/windows) to a path that may contain ~, %VAR%,
// $VAR, and {product}. Dir names a directory to watch (paired with the
// source's filter); File names one log file explicitly.
type Location struct {
	Dir  map[string]string `yaml:"dir,omitempty"`
	File map[string]string `yaml:"file,omitempty"`
}
```

Add `"fmt"` to the imports, then append validation at the bottom of the file:

```go
// validate enforces authoring rules the YAML schema alone cannot express:
// each location carries exactly one of dir/file, all locations of a source
// agree on one mode, and file-based sources have no filter (a filename regex
// is meaningless when the file is named explicitly). Run only from Parse —
// the strict bundled-catalog path — so the remote catalog stays lenient.
func (c *Catalog) validate() error {
	for name, frag := range c.Fragments {
		if err := validateSources("fragment "+name, frag.Sources); err != nil {
			return err
		}
	}
	for name, app := range c.Apps {
		if err := validateSources("app "+name, app.Sources); err != nil {
			return err
		}
	}
	return nil
}

func validateSources(owner string, srcs []Source) error {
	for _, src := range srcs {
		mode := ""
		for i, loc := range src.Locations {
			hasDir, hasFile := len(loc.Dir) > 0, len(loc.File) > 0
			var m string
			switch {
			case hasDir && hasFile:
				return fmt.Errorf("%s: source %q: location %d sets both dir and file", owner, src.ID, i)
			case hasDir:
				m = "dir"
			case hasFile:
				m = "file"
			default:
				return fmt.Errorf("%s: source %q: location %d sets neither dir nor file", owner, src.ID, i)
			}
			if mode == "" {
				mode = m
			} else if mode != m {
				return fmt.Errorf("%s: source %q: mixes dir and file locations", owner, src.ID)
			}
		}
		if mode == "file" && src.Filter != "" {
			return fmt.Errorf("%s: source %q: filter is not allowed on a file-based source", owner, src.ID)
		}
	}
	return nil
}
```

Wire it into `Parse` (and only `Parse` — `parseLenient` is untouched):

```go
func Parse(data []byte) (*Catalog, error) {
	var c Catalog
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, err
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}
```

- [ ] **Step 4: Run the package tests to verify everything passes**

Run: `go test ./internal/catalog/ -v`

Expected: ALL PASS — the five new tests, plus the existing suite (`TestParseMinimalCatalog`, the bundled-catalog content tests, resolve tests) which must not regress.

- [ ] **Step 5: Commit**

```bash
git add internal/catalog/schema.go internal/catalog/schema_test.go
git commit -m "feat(catalog): file: location type with strict validation"
```

---

### Task 2: Resolution — `Env.ExistsFile` + file-mode `emitSource`

**Files:**
- Modify: `internal/catalog/resolve.go`
- Test: `internal/catalog/resolve_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/catalog/resolve_test.go`. The fixture mirrors `testCatalog` style: one file-mode fragment with two drift candidates, plus an inline dir source so both modes coexist in one app.

```go
func testFileCatalog(t *testing.T) *Catalog {
	t.Helper()
	c, err := Parse([]byte(`
version: 1
defaults:
  output: { color: true, drop_unmatched: false }
  tui: { enabled: true, scrollback: 20000 }
fragments:
  agent-log:
    sources:
      - id: main
        locations:
          - file:
              linux:   '~/.{product}/logs/agent.log'
              windows: '%USERPROFILE%/.{product}/logs/agent.log'
          - file:
              linux:   '~/.{product}-local/logs/agent.log'
apps:
  junie:
    use:
      - { frag: agent-log, product: junie }
    sources:
      - id: extra
        filter: '\.log$'
        locations:
          - dir: { linux: '~/.junie/extra' }
`))
	if err != nil {
		t.Fatalf("parse test catalog: %v", err)
	}
	return c
}

func TestResolveFileSourceEmitsFileGroup(t *testing.T) {
	c := testFileCatalog(t)
	env := Env{OS: "linux", Home: "/home/me", Getenv: func(string) string { return "" },
		Exists:     func(string) bool { return true },
		ExistsFile: func(p string) bool { return p == "/home/me/.junie/logs/agent.log" }}

	f, err := c.Resolve([]string{"junie"}, env)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(f.Files) != 1 {
		t.Fatalf("files = %+v", f.Files)
	}
	// product "junie" equals the app name, source id is "main": both suffixes drop.
	if f.Files[0].ID != "junie" {
		t.Errorf("file group id = %q", f.Files[0].ID)
	}
	if len(f.Files[0].Paths) != 1 || f.Files[0].Paths[0] != "/home/me/.junie/logs/agent.log" {
		t.Errorf("file group paths = %v", f.Files[0].Paths)
	}
	// The dir-mode inline source still emits a directory group alongside.
	if len(f.Directories) != 1 || f.Directories[0].ID != "junie-extra" {
		t.Errorf("dirs = %+v", f.Directories)
	}
}

func TestResolveFileSourceFallbackWhenNoneExist(t *testing.T) {
	c := testFileCatalog(t)
	env := Env{OS: "linux", Home: "/home/me", Getenv: func(string) string { return "" },
		Exists:     func(string) bool { return false },
		ExistsFile: func(string) bool { return false }}

	f, err := c.Resolve([]string{"junie"}, env)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(f.Files) != 1 || len(f.Files[0].Paths) != 1 ||
		f.Files[0].Paths[0] != "/home/me/.junie/logs/agent.log" {
		t.Errorf("fallback = %+v", f.Files)
	}
}

func TestResolveFileSourceKeepsAllExisting(t *testing.T) {
	c := testFileCatalog(t)
	env := Env{OS: "linux", Home: "/home/me", Getenv: func(string) string { return "" },
		Exists:     func(string) bool { return true },
		ExistsFile: func(string) bool { return true }}

	f, err := c.Resolve([]string{"junie"}, env)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := []string{"/home/me/.junie/logs/agent.log", "/home/me/.junie-local/logs/agent.log"}
	if len(f.Files) != 1 || len(f.Files[0].Paths) != 2 ||
		f.Files[0].Paths[0] != want[0] || f.Files[0].Paths[1] != want[1] {
		t.Errorf("paths = %v, want %v", f.Files[0].Paths, want)
	}
}

func TestResolveFileSourceWindowsSeparators(t *testing.T) {
	c := testFileCatalog(t)
	env := Env{OS: "windows", Home: `C:\Users\me`,
		Getenv: func(k string) string {
			if k == "USERPROFILE" {
				return `C:\Users\me`
			}
			return ""
		},
		Exists:     func(string) bool { return false },
		ExistsFile: func(string) bool { return false }}

	f, err := c.Resolve([]string{"junie"}, env)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(f.Files) != 1 {
		t.Fatalf("files = %+v", f.Files)
	}
	got := f.Files[0].Paths[0]
	want := `C:\Users\me\.junie\logs\agent.log`
	if got != want {
		t.Errorf("windows path = %q, want %q", got, want)
	}
	if strings.Contains(got, "/") {
		t.Errorf("windows path still contains a forward slash: %q", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run TestResolveFileSource ./internal/catalog/ -v`

Expected: COMPILE ERROR — `unknown field ExistsFile in struct literal of type Env`. A compile failure is the failing state here.

- [ ] **Step 3: Implement `ExistsFile` and the file-mode branch**

In `internal/catalog/resolve.go`, extend `Env`:

```go
// Env carries the host facts resolution depends on. Injected for testability;
// DefaultEnv builds the live one.
type Env struct {
	OS         string                     // "linux" | "darwin" | "windows"
	Home       string                     // user home directory
	Getenv     func(string) string        // environment lookup
	Exists     func(dirGlob string) bool  // true if the dir-glob matches an existing directory
	ExistsFile func(pathGlob string) bool // true if the path-glob matches an existing regular file
}
```

Replace `emitSource`:

```go
// emitSource probe-and-picks a source's drift candidates and appends a
// directory group (dir-mode source) or a file group (file-mode source) to f.
func (c *Catalog) emitSource(f *config.File, app, product string, src Source, key string, env Env, seenID map[string]bool) {
	// A source is file-mode when any location declares file:. Parse-time
	// validation guarantees uniformity for the bundled catalog; for a lenient
	// remote catalog, off-mode locations simply contribute no candidate below.
	fileMode := false
	for _, loc := range src.Locations {
		if len(loc.File) > 0 {
			fileMode = true
			break
		}
	}
	exists := env.Exists
	if fileMode {
		exists = env.ExistsFile
	}

	var picked []string
	// firstCandidate is the first location that has a path for this OS, in
	// declaration order (newest scheme first). It is the best-effort fallback
	// emitted when no candidate currently exists on disk.
	var firstCandidate string
	for _, loc := range src.Locations {
		m := loc.Dir
		if fileMode {
			m = loc.File
		}
		raw, ok := m[key]
		if !ok {
			continue
		}
		p := normalizeSep(expandPath(substituteProduct(raw, product), env.Home, env.Getenv), key)
		if firstCandidate == "" {
			firstCandidate = p
		}
		if exists != nil && exists(p) {
			picked = append(picked, p)
		}
	}
	if len(picked) == 0 {
		if firstCandidate == "" {
			return
		}
		picked = []string{firstCandidate}
	}

	id := groupID(app, product, src.ID, seenID)
	if fileMode {
		f.Files = append(f.Files, config.FileGroup{ID: id, Paths: picked})
		return
	}
	rec := false
	g := config.DirGroup{
		ID:        id,
		Paths:     picked,
		Recursive: &rec,
	}
	if src.Filter != "" {
		g.FileFilter = &config.Filter{NameRegex: src.Filter}
	}
	f.Directories = append(f.Directories, g)
}
```

(The only behavior change for dir mode is the `exists != nil` guard, which protects hand-built `Env`s; `DefaultEnv` always sets both probes.)

In `DefaultEnv`, add the file probe after `Exists`:

```go
		ExistsFile: func(pathGlob string) bool {
			matches, err := filepath.Glob(pathGlob)
			if err != nil {
				return false
			}
			for _, m := range matches {
				if fi, err := os.Stat(m); err == nil && !fi.IsDir() {
					return true
				}
			}
			return false
		},
```

Also update the `DefaultEnv` doc comment to mention both probes:

```go
// DefaultEnv builds the live Env: real OS, home dir, environment, and
// existence probes that report whether a glob matches at least one directory
// (Exists) or regular file (ExistsFile).
```

- [ ] **Step 4: Run package tests, vet, race**

Run: `go test ./internal/catalog/ -v && go vet ./... && go test -race ./internal/catalog/`

Expected: ALL PASS, vet clean, race clean.

- [ ] **Step 5: Commit**

```bash
git add internal/catalog/resolve.go internal/catalog/resolve_test.go
git commit -m "feat(catalog): resolve file-mode sources to files: config groups"
```

---

### Task 3: Authoring guide `internal/catalog/CATALOG.md`

**Files:**
- Create: `internal/catalog/CATALOG.md`

No test — documentation only. `//go:embed catalog.yml` in `embed.go` embeds exactly that one file, so the guide is not bundled into the binary. **Do not modify `catalog.yml`.**

- [ ] **Step 1: Write the guide**

Create `internal/catalog/CATALOG.md` with exactly this content:

````markdown
# Authoring `catalog.yml`

`catalog.yml` is the bundled template catalog: `log-listener init <app>...`
resolves entries from it into a ready-to-run config for the current OS. The
bundled copy is parsed **strictly** — unknown keys and rule violations fail
`go test ./internal/catalog/` — while a downloaded (remote) catalog is parsed
leniently for forward compatibility.

## Top-level layout

```yaml
version: 1        # integer; compared against the online catalog
defaults:  { …. } # global output/tui blocks copied into generated configs
fragments: { … }  # reusable, parameterizable source bundles
apps:      { … }  # named templates the user can `init`
renderers: { … }  # reusable renderer library referenced by apps
bundles:   { … }  # named groups of app names
```

## Defaults

```yaml
defaults:
  output: { color: true, drop_unmatched: false }
  tui: { enabled: true, scrollback: 20000 }
```

Emitted verbatim into every generated config.

## Sources

A source is one discovery target. It comes in two modes:

### Directory mode — watch a directory, filter by filename

```yaml
sources:
  - id: main                          # part of the generated group id
    filter: '^(idea\.log(\.\d+)?)$'   # regex on FILENAMES in the directory
    locations:                        # ordered drift candidates, newest first
      - dir:
          linux:   '~/.cache/JetBrains/{product}*/log'
          darwin:  '~/Library/Logs/JetBrains/{product}*'
          windows: '%LOCALAPPDATA%/JetBrains/{product}*/log'
      - dir:
          linux:   '~/.local/share/JetBrains/Toolbox/apps/{product}*/log'
```

### File mode — name one log file explicitly

```yaml
sources:
  - id: main                          # no filter: the file is already named
    locations:
      - file:
          linux:   '~/.myapp/logs/app.log'
          darwin:  '~/Library/Logs/myapp/app.log'
          windows: '%LOCALAPPDATA%/myapp/app.log'
      - file:
          linux:   '~/.myapp-legacy/app.log'
```

A file-mode source resolves to a `files:` group in the generated config; a
dir-mode source resolves to a `directories:` group.

### Rules (enforced at parse time for the bundled catalog)

- Each location sets **exactly one** of `dir:` / `file:`.
- All locations of one source use the **same** mode — no mixing.
- `filter` is only valid on dir-mode sources. A file-mode source names its
  file explicitly, so a filename regex would be dead configuration.

### Path expansion

Paths may contain, in any mode:

| Token        | Expansion                                              |
|--------------|--------------------------------------------------------|
| `~`          | the user's home directory                              |
| `%VAR%`      | Windows-style environment variable                     |
| `$VAR`       | Unix-style environment variable                        |
| `{product}`  | the `product:` bound by the app's `use:` entry         |
| `*` (glob)   | passed through; matched against the filesystem         |

Unknown variables are left verbatim so a missing variable produces a
visibly-wrong path instead of a silently-empty one. Separators are
normalized to the target OS (`\` on Windows, `/` elsewhere).

### Drift candidates

`locations` is an ordered list of places the logs have lived across product
versions — newest scheme first. At `init` time every candidate that exists
on disk is kept; if none exist, the first candidate declared for the OS is
emitted as a best-effort fallback. An OS with no candidate paths emits
nothing.

## Fragments and `use:`

A fragment is a reusable bundle of sources, optionally parameterized by
`{product}`:

```yaml
fragments:
  jetbrains-base:
    sources:
      - id: main
        filter: '^(idea\.log(\.\d+)?)$'
        locations:
          - dir: { linux: '~/.cache/JetBrains/{product}*/log' }

apps:
  goland:
    use:
      - { frag: jetbrains-base, product: GoLand }
```

`use:` instantiates the fragment with `{product}` replaced everywhere.

## Apps

```yaml
apps:
  myapp:
    renderers: [json-line]      # names from the renderers: library
    use:                        # fragment instantiations
      - { frag: some-fragment, product: MyApp }
    sources: [ … ]              # inline sources, same shape as in fragments
```

Generated group ids are `app[-product][-sourceID]` — the product suffix is
dropped when it equals the app name, the source-id suffix when it is `main`.
Collisions get a numeric suffix.

## Renderers

```yaml
renderers:
  json-line:
    line_regex: '^\s*(\{.*\})\s*$'
    template: '$json($1)'
```

Referenced by name from `apps.<name>.renderers`; deduplicated across apps in
one `init` invocation.

## Bundles

```yaml
bundles:
  jetbrains: [goland, idea, pycharm, webstorm]
```

A bundle name given to `init` expands to its apps, deduplicated,
order-preserving. App and bundle names match case-insensitively.
````

- [ ] **Step 2: Verify the bundled catalog still parses and nothing else changed**

Run: `go test ./internal/catalog/ && git status --short`

Expected: tests PASS; `git status` shows only `?? internal/catalog/CATALOG.md` (in particular, **no** change to `catalog.yml`).

- [ ] **Step 3: Commit**

```bash
git add internal/catalog/CATALOG.md
git commit -m "docs(catalog): authoring guide for catalog.yml incl. file: sources"
```

---

### Task 4: Changelog + spec cross-reference + full verification

**Files:**
- Modify: `CHANGELOG.md` (new entry at the top of the unreleased/latest section, matching the file's existing entry style)
- Modify: `docs/superpowers/specs/2026-06-03-template-auto-config-design.md` (one-line addendum)

- [ ] **Step 1: Add the changelog entry**

Open `CHANGELOG.md`, find the newest section, and add an entry following the file's existing format/voice:

```markdown
- **Catalog file-based sources.** Catalog sources can now declare `file:`
  locations (symmetric with `dir:`) that resolve to `files:` groups in the
  generated config — for apps whose log is a single well-known file. The
  bundled catalog is validated strictly at parse time (exactly one of
  `dir`/`file` per location, uniform mode per source, no `filter` on file
  sources). New authoring guide: `internal/catalog/CATALOG.md`. The bundled
  `catalog.yml` itself is unchanged.
```

(Adjust heading placement to match how the latest entries are organized — read the top of the file first.)

- [ ] **Step 2: Cross-reference from the original template design spec**

At the top of `docs/superpowers/specs/2026-06-03-template-auto-config-design.md`, directly under the title/status line, add:

```markdown
> **Extension (2026-06-12):** file-based sources (`file:` locations →
> `files:` config groups) — see
> `2026-06-12-catalog-file-sources-design.md`.
```

- [ ] **Step 3: Full verification**

Run: `go test ./... && go vet ./... && go test -race ./...`

Expected: ALL PASS across the repo, vet clean, race clean.

- [ ] **Step 4: Commit**

```bash
git add CHANGELOG.md docs/superpowers/specs/2026-06-03-template-auto-config-design.md
git commit -m "docs: changelog + spec cross-ref for catalog file sources"
```
