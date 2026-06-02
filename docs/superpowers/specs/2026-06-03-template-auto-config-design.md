# Template-based Auto-Configuration — Design

**Date:** 2026-06-03
**Status:** Approved design, pre-implementation
**Feature:** `log-listener init <apps...>` — generate a `log-listener.yml`
from a catalog of per-application log templates, resolved for the current OS.

---

## 1. Problem & goal

Writing a `log-listener.yml` by hand is the main friction for a new user. For
common applications the log locations are well-known but OS-specific, version-
specific, and sometimes spread across *other* applications' directories (e.g.
the Junie agent logs that live inside an IntelliJ IDEA install).

`goland-logs.yml` in the repo root is a hand-written example of exactly the
artifact this feature should generate automatically.

**Goal:** `log-listener init goland junie` produces a ready-to-run
`./log-listener.yml` — correct paths for the current OS, the right renderers,
and sane output/TUI defaults — with no hand-editing required.

### Non-goals

- **Process / running-app detection.** The user's "based on the currently
  running app" concern is satisfied by path globbing + drift candidates, not by
  inspecting running processes. There is no process-inspection subsystem.
- A general config templating language. The catalog is a fixed schema, not a
  programmable templating engine.

---

## 2. Prior art

Filebeat's module `manifest.yml` is the closest established pattern: each
integration declares a `paths` variable with a generic `default` plus per-OS
overrides (`os.darwin`, `os.windows`), and the values are globs. This design
adopts that OS-map idea and generalizes it with two further axes (drift
candidates, catalog version) and composition (fragments).

Reference:
- Filebeat module dev guide — https://www.elastic.co/docs/extend/beats/filebeat-modules-devguide

---

## 3. The three axes (key concept)

"OS" and "version" are **separate axes**, plus a third that only matters for the
online-update phase. Keeping them separate is what prevents the format from
exploding combinatorially.

| Axis | Meaning | Mechanism |
|------|---------|-----------|
| **1 — OS** | Same log, different path on Linux/macOS/Windows | per-location `dir` map: `{ linux, darwin, windows }` |
| **2 — version drift** | The app's log *scheme* changed over its history (e.g. `~/.GoLand2019/system/log` → `~/.cache/JetBrains/GoLand2026.1/log`) | ordered `locations` list of candidate schemes, newest first |
| **3 — catalog version** | Bottled vs remote catalog freshness | single top-level integer `version:` — the **only** thing online-update compares |

The app *version* itself (`2026.1` vs `2025.3`) is **not** an axis — the glob
`{product}*` absorbs it, resolved at runtime by log-listener's existing
directory discovery. Each drift-candidate carries its own OS map, so axes 1 and
2 nest cleanly instead of multiplying.

---

## 4. Catalog data model — fragments + composition

Apps are **not** independent. The chosen model is **composition** (an app
*has* parts), not inheritance (an app *is a* parent). Rationale: the Junie
bridge logs belong to multiple apps at once and are even reused twice inside one
app (Junie→IntelliJ and Junie→GoLand) — a relationship single-inheritance
cannot express. "Prefer composition over inheritance" applies directly.

### Catalog schema

```yaml
version: 5                        # axis 3 — online-update compares only this

defaults:                         # global output/tui used when no selected app sets them
  output: { color: true, drop_unmatched: false }
  tui:    { enabled: true, scrollback: 20000 }

fragments:                        # reusable, parameterized source bundles
  jetbrains-base:                 # parameterized by {product}
    sources:
      - id: main
        filter: 'idea\.log(\.\d+)?$'
        locations:                # axis 2 — ordered drift candidates, newest first
          - dir: { linux:   '~/.cache/JetBrains/{product}*/log',
                   darwin:  '~/Library/Logs/JetBrains/{product}*',
                   windows: '%LOCALAPPDATA%/JetBrains/{product}*/log' }
          - dir: { linux:   '~/.{product}*/system/log' }      # legacy scheme
  junie-bridge:                   # Junie<->IDE comms, living inside a product dir
    sources:
      - id: junie
        filter: '\.log$'
        locations:
          - dir: { linux: '~/.cache/JetBrains/{product}*/log/junie' }
  junie-direct:
    sources:
      - id: agent
        filter: '\.log$'
        locations:
          - dir: { linux: '~/.config/junie/log' }

apps:                             # named app templates, composed from fragments
  goland:
    renderers: [ idea-trailing-json, json-line ]
    use:
      - { frag: jetbrains-base, product: GoLand }
    sources:                      # app-specific extras, same source schema as fragments
      - id: acp
        filter: '\.log$'
        locations:
          - dir: { linux: '~/.cache/JetBrains/GoLand*/log/acp' }
  idea:
    renderers: [ idea-trailing-json, json-line ]
    use:
      - { frag: jetbrains-base, product: IntelliJIdea }
      - { frag: junie-bridge,   product: IntelliJIdea }
  junie:
    use:
      - { frag: junie-direct }
      - { frag: junie-bridge, product: IntelliJIdea }
      - { frag: junie-bridge, product: GoLand }

renderers:                        # reusable renderer library, referenced by name
  idea-trailing-json: { line_regex: '^(.*?\s)(\{.+\})\s*$', template: '$1\njson($2)' }
  json-line:          { line_regex: '^\s*(\{.*\})\s*$',      template: 'json($1)' }

bundles:                          # select-many shortcuts
  jetbrains: [ goland, idea, pycharm, webstorm ]
```

### Schema element reference

- **`version`** *(int, required)* — catalog freshness counter (axis 3).
- **`defaults.output` / `defaults.tui`** — global blocks written into the
  generated config when no selected app supplies them.
- **`fragments.<name>.sources[]`** — reusable source definitions. A source has:
  - `id` *(string)* — local id, used to build the emitted group id.
  - `filter` *(regex string)* — becomes the directory group's `name_regex`.
  - `locations[]` — ordered drift candidates (axis 2). Each has a `dir` OS-map
    (axis 1) with at least a `linux` key; `darwin`/`windows` optional.
  - `{product}` placeholder — substituted from the `use:` entry's `product`.
- **`apps.<name>`** — a template. Has optional `renderers` (names into the
  `renderers` library), `use[]` (fragment references with params), and inline
  `sources[]` (same schema as fragment sources, for app-specific extras).
- **`renderers.<name>`** — `{ line_regex, template }`, the existing renderer DSL.
- **`bundles.<name>`** — list of app names expanded when the bundle is selected.

---

## 5. Resolution rules

Given `(targetOS, homeDir, env, fsProbe)`:

1. **Expand bundles** → flat app list. Unknown app/bundle name → hard error
   listing available names.
2. **Compose each app:** gather sources from each `use:` fragment (substituting
   `{product}`) plus the app's inline `sources`.
3. **Token expansion** for the target OS: `~` → home, `%LOCALAPPDATA%` /
   `%USERPROFILE%` / `$XDG_*` → env. Pick the OS key from each `dir` map
   (`linux`/`darwin`/`windows`); a candidate with no key for the target OS is
   skipped.
4. **Probe-and-pick** (axis 2): for each source, `fsProbe` (stat / glob) each
   candidate's expanded path; emit every candidate whose directory currently
   exists. If **none** exist, emit the newest (first) candidate as best-effort
   so the config still works once the app is installed. `fsProbe` is injected
   for testability.
5. **Group ids:** emitted as `<app>-<sourceid>` (e.g. `goland`, `goland-acp`,
   `idea-junie`) → guaranteed unique across multiple selected apps and readable
   in the TUI. The base/`main` source emits as just `<app>`.
6. **Emitted directory group:** `recursive: false`, `name_regex` = the source's
   `filter`, paths = the surviving candidate globs. (Matches `goland-logs.yml`.)
7. **Renderers:** collect the named renderers of all selected apps; a renderer
   referenced by multiple apps is emitted **once** (dedup by name).
8. **output / tui:** taken from catalog `defaults`. Per-app overrides are out of
   scope (apps would fight over global settings when several are selected).

---

## 6. CLI / UX

```
$ log-listener init goland junie
  Check GitHub for newer templates? [Y/n] y
  remote catalog v7 > bundled v5 → using remote (cached)
  resolved 2 apps → 5 sources, 2 renderers
  ./log-listener.yml exists. [o]verwrite / [m]erge / [c]ancel? m
  merged: +3 groups, +1 renderer (existing entries untouched)
  wrote ./log-listener.yml
```

- **Invocation:** `log-listener init <app|bundle>...`
- **`-o <path>`** *(default `./log-listener.yml`)* — output target. `-o -`
  writes the YAML to **stdout** with no prompts (composable).
- **Online prompt:** asked **every run** (per decision). `--online` / `--offline`
  flags force the choice and skip the prompt.
- **Existing file:** prompt `[o]verwrite / [m]erge / [c]ancel`.
  - **Merge** = append generated groups/renderers whose id/name is not already
    present; never modify or delete the user's existing entries; write
    `output`/`tui` only if absent.
- **Non-TTY** (piped stdout, or `-o -`): no prompts. Defaults to **offline**,
  and refuses to overwrite an existing file unless `--force`.

---

## 7. Online update

- **Remote URL:** a build-time constant pointing at GitHub raw
  `catalog/catalog.yml` on the repo's default branch. (Exact `owner/repo`
  filled in at implementation.)
- **Cache:** `~/.cache/log-listener/catalog.yml` (XDG-respecting).
- **Selection:** compare integer `version`; use whichever is higher (remote
  cached for next time).
- **Failure handling:** *any* network error, HTTP non-200, or parse failure
  silently falls back to the embedded bottled catalog. `init` never hard-fails
  because the network is down.
- **Trust:** remote catalog is parsed with the same strict loader
  (`KnownFields(true)`) and only ever yields paths + regexes — no code
  execution. Fetched over HTTPS from the official repo.

---

## 8. Architecture / module map

| Package | Role |
|---------|------|
| `internal/catalog` | Catalog schema types; embedded bottled catalog (`go:embed`); fragment composition; OS/token expansion; probe-and-pick resolution → produces a config document. |
| `internal/catalog` (remote, behind an interface) | Fetch remote catalog, version-compare, cache. Network isolated behind a `Fetcher` interface for testing. |
| `cmd/log-listener` `init` subcommand | Arg parsing; online prompt; resolve; marshal YAML; write/merge file or stdout. |
| `internal/config` *(small refactor)* | Export the YAML schema structs so loader and emitter share one source of truth (no schema drift between read and write). |

### Data flow

```
init goland junie
  → choose catalog source (online prompt → remote-or-bottled Fetcher)
  → expand bundles + validate names
  → per app: compose use:fragments + inline sources + renderers
  → expand tokens for current OS, probe-and-pick locations
  → assign unique group ids
  → build document (directories/files + deduped renderers + defaults output/tui)
  → marshal YAML
  → write/merge ./log-listener.yml (or stdout)
  → existing config.Load + auto-reload runs it
```

### Config schema sharing

The generator must emit YAML that the existing loader accepts. To avoid a second
copy of the schema drifting out of sync, the unexported YAML structs in
`internal/config/yaml.go` are promoted to exported types (`config.File`,
`config.DirGroup`, etc.) used by **both** the loader and the catalog emitter.
This is the one targeted refactor of existing code the feature requires.

---

## 9. Testing strategy

- **Catalog resolution** is pure given `(os, homeDir, env, fsProbe)` →
  table-driven tests covering: each app, each OS, drift-candidate selection
  (exists / not-exists / none-exist-fallback), `{product}` substitution, bundle
  expansion, unknown-name error, group-id uniqueness, renderer dedup.
- **Remote fetch** behind the `Fetcher` interface → fake-transport tests:
  remote-newer, remote-older, unreachable, HTTP-error, malformed-YAML — each
  asserting the correct catalog is chosen and failures fall back to bottled.
- **Catalog validity:** a test that parses the embedded bottled catalog with the
  strict loader and resolves every app on every OS (no panics, every source has
  a usable path) — guards the shipped catalog.
- **`init` e2e** in a temp dir: fresh write, overwrite, merge (existing entries
  untouched), `-o <path>`, `-o -` stdout, unknown-app error, non-TTY behavior,
  `--force`.

All must leave `go test ./...`, `go vet ./...`, `go test -race ./...` green,
per repo convention.

---

## 10. Phasing

The user opted to design all three concerns together; implementation can still
land incrementally:

1. **Catalog + resolution + `init` (bottled only).** The heart. Embedded
   catalog, composition, probe-and-pick, file write/merge, `-o`.
2. **Online update.** `Fetcher`, version compare, cache, prompt, fallback.
3. **Catalog content growth.** More apps/bundles in the shipped catalog
   (PyCharm, WebStorm, VS Code, Docker, systemd journ­al exports, …) — pure data,
   no code change.

---

## 11. Open implementation details (decided defaults, not blockers)

- Exact GitHub `owner/repo` for the remote URL constant.
- WSL: on Linux, the Windows-side install at
  `/mnt/c/Users/*/AppData/Local/JetBrains/{product}*/log` is simply an
  additional drift candidate — handled by existing machinery, no special case.
- Windows path separators in emitted YAML (forward slashes, per `goland-logs.yml`
  WSL examples and Go's path handling).
