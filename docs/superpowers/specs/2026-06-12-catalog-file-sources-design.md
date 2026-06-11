# Catalog file-based sources

Date: 2026-06-12
Status: approved

## Problem

The template catalog (`internal/catalog/catalog.yml`) can only describe
directory-based discovery: every `Location` is a `dir:` map, and
`emitSource` always emits a `config.DirGroup`. The underlying config format
already supports watching explicit files via `files:` (`config.FileGroup`),
but `log-listener init <app>` cannot generate such entries. Apps whose log
is a single well-known file must be modeled as a directory plus a filename
filter, which is indirect and walks a directory it doesn't need to.

Separately, there is no authoring documentation next to `catalog.yml`
explaining how the file is constructed.

## Decision

1. Add a `file:` location type to the catalog schema, symmetric with `dir:`
   (Option A from the design discussion). A source whose locations use
   `file:` resolves to a `config.FileGroup` instead of a `config.DirGroup`.
2. Add an authoring guide `internal/catalog/CATALOG.md` next to
   `catalog.yml` with annotated examples of every construct, including both
   source forms.
3. **`catalog.yml` itself is not modified.** The current bundled catalog
   works and gains no artificial noise. The new capability is exercised by
   tests and documented by the guide.

## Schema change (`schema.go`)

```go
// Location is one drift candidate. Exactly one of Dir/File is set; both
// map an OS key (linux/darwin/windows) to a path that may contain ~,
// %VAR%, $VAR, and {product}.
type Location struct {
    Dir  map[string]string `yaml:"dir,omitempty"`
    File map[string]string `yaml:"file,omitempty"`
}
```

Authoring example:

```yaml
sources:
  - id: main
    locations:
      - file:
          linux:   '~/.myapp/logs/app.log'
          darwin:  '~/Library/Logs/myapp/app.log'
          windows: '%LOCALAPPDATA%/myapp/app.log'
```

### Validation rules

A new `validate()` pass runs from `Parse` (the strict, bundled-catalog
path) so authoring mistakes fail at build/test time:

- Every location has **exactly one** of `dir`/`file` — neither zero nor
  both.
- All locations within one source agree on the same mode. A source is
  uniformly dir-based or file-based; no mixing.
- A file-based source must not set `filter`. The filter is a filename
  regex applied to directory contents; for an explicit file it is
  meaningless, and silently ignoring it would hide an authoring error.

`parseLenient` (remote catalog, forward compatibility) does not run
validation — a newer published catalog must stay usable on an older
binary. `emitSource` skips malformed locations defensively at resolve
time instead.

## Resolution change (`resolve.go`)

`emitSource` branches on the source's mode:

- **dir mode** — unchanged: probe drift candidates with `Env.Exists`,
  emit `config.DirGroup` with `Recursive: false` and `FileFilter` from
  `filter`.
- **file mode** — expand each `file:` candidate with the same
  `{product}` substitution, `~`/`%VAR%`/`$VAR` expansion, and separator
  normalization; probe with the new `Env.ExistsFile`; emit
  `config.FileGroup{ID, Paths}` appended to `f.Files`.

The drift model is identical in both modes: keep every candidate that
exists on disk; if none exist, fall back to the first candidate declared
for the OS (best-effort); if the OS has no candidates at all, emit
nothing.

`groupID` is reused unchanged so file groups get the same readable,
deduplicated IDs (`app[-product][-sourceID]`).

### Env change

```go
type Env struct {
    OS         string
    Home       string
    Getenv     func(string) string
    Exists     func(dirGlob string) bool  // dir-glob matches an existing directory
    ExistsFile func(pathGlob string) bool // path-glob matches an existing file
}
```

`DefaultEnv` fills `ExistsFile` with `filepath.Glob` → `os.Stat` →
`!IsDir()`, mirroring the existing directory probe. Test fixtures that
construct `Env` by hand gain the extra field.

## Authoring guide (`internal/catalog/CATALOG.md`)

A markdown companion file documenting how to construct a catalog:

- top-level layout (`version`, `defaults`, `fragments`, `apps`,
  `renderers`, `bundles`);
- sources: `id`, `filter` (dir mode only), `locations` as ordered drift
  candidates, per-OS path maps, path expansion (`~`, `%VAR%`, `$VAR`,
  `{product}`), glob support;
- both `dir:` and `file:` location forms, with the validation rules
  spelled out;
- fragments and `use:`/`product` parameterization;
- apps, inline sources, renderer references, bundles;
- the strict-vs-lenient parse split (bundled vs remote catalog).

The guide is documentation only. `//go:embed catalog.yml` embeds exactly
one file, so the guide is not bundled into the binary.

## Testing

- `schema_test.go` — validation: rejects a location with both `dir` and
  `file`; rejects a location with neither; rejects mixed-mode sources;
  rejects `filter` on a file-based source; accepts a valid file source.
  The bundled catalog still parses (content test untouched).
- `resolve_test.go` — a file-mode source emits a `FileGroup` with
  expanded, OS-normalized paths; existence probing keeps only existing
  candidates; fallback to first declared candidate when none exist;
  dir-mode and file-mode sources coexist within one app; `{product}`
  substitution works in `file:` paths.

All of `go test ./...`, `go vet ./...`, `go test -race ./...` stay green.

## Docs

- `CHANGELOG.md` entry for the new `file:` source type and the authoring
  guide.
- `docs/superpowers/specs/2026-06-03-template-auto-config-design.md` gets
  a short note pointing at this spec for the file-source extension.

## Out of scope

- No change to `catalog.yml` content.
- No new CLI surface; `init` behavior is unchanged except that a catalog
  using `file:` sources now produces `files:` entries.
- No per-file filters, no mixed dir+file sources, no remote-catalog
  validation.
