# Authoring `catalog.yml`

`catalog.yml` is the bundled template catalog: `log-listener init <app>...`
resolves entries from it into a ready-to-run config for the current OS. The
bundled copy is parsed **strictly** — unknown keys and rule violations fail
`go test ./internal/catalog/` — while a downloaded (remote) catalog is parsed
leniently for forward compatibility.

## Top-level layout

```yaml
version: 1        # integer; compared against the online catalog
defaults:  { … }  # global output/tui blocks copied into generated configs
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

Unknown variables are kept in the path (`%FOO%` stays verbatim, `$FOO`
becomes `${FOO}`) so a missing variable produces a visibly-wrong path
instead of a silently-empty one. Separators are normalized to the target
OS (`\` on Windows, `/` elsewhere).

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
lowercased, and dropped when it equals the app name (case-insensitively);
the source-id suffix is dropped when it is `main`. Collisions get a numeric
suffix.

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
