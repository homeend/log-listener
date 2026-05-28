# PLAN — pattern-based directory matching with new-dir detection

## Goal

Let users pass glob-style patterns to `-d` (and YAML `directories`),
and have log-listener:

1. **At startup** — expand the pattern to all currently-matching
   directories and watch them.
2. **At runtime** — when a *new* directory that matches the pattern is
   created later, automatically pick it up and tail any files that
   pass the group's `file_filter`.

The same dynamic behaviour applies to `-f` (`files:`) glob paths whose
parent directory doesn't exist yet.

Concrete user case (already-tested static form):

```
-f '/tmp/acp-logs-*/acp/acp-transport-memory.log'
```

works for files that exist at startup. After this change, a brand-new
`/tmp/acp-logs-<NEW>/` appearing after startup will also be picked up.

---

## Scope

### Supported pattern shapes (MVP)

- **Wildcard in any path segment** — `*`, `?`, `[abc]` per `path.Match`
  semantics.
- **Suffix after the wildcard segment** — e.g.
  `/tmp/acp-logs-*/acp` or `/tmp/acp-logs-*/acp/*.log`. The wildcard
  doesn't have to be the last segment.
- **Multiple wildcards** — supported by deferring to `filepath.Glob`
  for static expansion, plus segment-by-segment matching for the
  runtime watcher.

### Explicitly out of scope (for this iteration)

- `**` recursive globs (Go's `filepath.Glob` doesn't support them
  anyway; users can fall back to `recursive: true` on the dir group).
- Symlink loops (we don't follow symlinks today; staying conservative).
- Pattern *removal* on Delete events — we don't drop tailers when a
  watched dir vanishes; rotation already handles missing files.
  Deferred.

---

## Design

### New module: `internal/discover/pattern.go`

```go
// HasMeta reports whether s contains any glob metacharacter.
func HasMeta(s string) bool

// LiteralPrefix returns the longest leading literal prefix of pattern
// (everything before the first segment that contains a metachar).
// Example: "/tmp/acp-logs-*/acp" → "/tmp".
func LiteralPrefix(pattern string) string

// MatchesPath reports whether path matches pattern in the
// path.Match sense, segment by segment. Differs from path.Match
// in that we explicitly allow leading "/" and ignore trailing "/".
func MatchesPath(pattern, path string) (bool, error)
```

### Discovery changes

`internal/discover/discover.go`:

- `ListCandidates` is already glob-aware for `GroupFile`. Extend
  `GroupDir` walk to:
  - If any path contains glob meta, call `filepath.Glob(path)` first
    and walk each matching directory.
  - Else, walk the literal path as today.

- Add helper:
  ```go
  // MatchesGroup reports whether path could be assigned to g (Dir or
  // File). Used by the watcher's NewDirMatcher/NewFileMatcher.
  func MatchesGroup(g *Group, path string, info fs.FileInfo,
      global *FileFilter) bool
  ```

### Watcher changes — `internal/watch/watcher.go`

Add a `NewDirMatcher`:

```go
// NewDirMatcher decides whether a newly-created directory should be
// scanned for files matching some group. Called from handleFsEvent
// when fsnotify reports a Create event on a path that's a directory.
type NewDirMatcher func(path string) (accept bool)
```

`handleFsEvent` becomes:

```go
on Create event for abs P:
    info, _ := os.Stat(P)
    if info.IsDir():
        // 1. Watch the new dir so future child Creates fire.
        WatchDir(P)
        // 2. Recursively scan for matching files.
        if dirMatcher != nil && dirMatcher(P):
            walk P, for each file f:
                if newFileMatcher(f) accepts: Add(f, gid, true)
    else:
        // existing path: call newFileMatcher
```

### Wiring — `cmd/log-listener/main.go`

`makeNewFileMatcher` and a new `makeNewDirMatcher` both close over
`cfg.Groups` so they share the pattern logic:

```go
func makeNewDirMatcher(cfg *config.Config) watch.NewDirMatcher {
    return func(path string) bool {
        for _, g := range cfg.Groups {
            if g.Kind != discover.GroupDir { continue }
            for _, p := range g.Paths {
                if pat, ok := dirPattern(p); ok {
                    if pat.MatchesPath(path) { return true }
                } else {
                    // literal prefix: any subdir of p matches if recursive
                    if g.Recursive && pathUnderAny(path, []string{p}, true) {
                        return true
                    }
                }
            }
        }
        return false
    }
}
```

At startup, for each pattern path:

```go
// 1. Expand to current matches, register each.
matches, _ := filepath.Glob(patternPath)
for each match: WatchDir(match) + walk + tail files

// 2. Watch the literal prefix so Creates fire when new dirs appear.
prefix := discover.LiteralPrefix(patternPath)
WatchDir(prefix)
```

### Edge cases the watcher must handle

- **Nested pattern with suffix.** Pattern `/tmp/acp-logs-*/acp`. When
  `/tmp/acp-logs-NEW/` is created, the watcher must:
  - Add a watch on `/tmp/acp-logs-NEW/`
  - Check if `acp/` already exists inside; if yes, walk it.
  - If `acp/` is created LATER, the watch on the parent fires Create
    → recurse.

  Solution: when `dirMatcher` accepts a new directory, ALSO walk it
  to look for further pattern segments. Easier alternative for MVP:
  use `discover.Assign` re-run on the affected subtree to find any
  new files matching ANY group's pattern.

- **Filter still applies.** New dirs/files honour `file_filter`,
  `global_file_filter`, and first-match-wins assignment.

- **Group attribution.** When a new file is found in a runtime-
  discovered dir, the matcher must return the group ID of the
  first group whose pattern matches the file's path.

### Config / CLI surface

No new flags. Existing `-d '/tmp/acp-logs-*/acp'` and YAML
`directories: paths: ['/tmp/acp-logs-*/acp']` just start working.

---

## Test plan

### New unit tests (`internal/discover/pattern_test.go`)

| Test | Asserts |
|---|---|
| `TestHasMeta` | `*`, `?`, `[abc]` → true; literal → false |
| `TestLiteralPrefix` | `/tmp/acp-*/sub` → `/tmp`; `/a/b/c` → `/a/b/c`; `/*` → `/` |
| `TestMatchesPath` | segment-by-segment match; rejects mismatched depth |

### New unit tests (`internal/discover/discover_test.go`)

| Test | Asserts |
|---|---|
| `TestListCandidatesDirGlob` | `-d /tmp/foo-*/sub` finds all matching subdirs and the files inside them |
| `TestAssignDirGlobFirstMatchWins` | overlapping glob dir groups still respect first-match-wins |

### New e2e tests (`cmd/log-listener/e2e_test.go`)

| Test | Asserts |
|---|---|
| `TestE2EStaticDirGlobAtStartup` | `-d /tmp/<tmpdir>-*/sub` with two existing matching dirs picks up both at startup |
| `TestE2ENewDirMatchingPattern` | start with one matching dir, create a second at runtime → its files are tailed within ~1s |
| `TestE2ENewDirWithDelayedSubdir` | pattern `/tmp/<tmpdir>-*/sub/file.log`: at runtime create `/tmp/<tmpdir>-NEW/` then `sub/` then `file.log` → all three Creates lead to the file being tailed |
| `TestE2EFileGlobPicksUpNewDirs` | `-f '/tmp/<tmpdir>-*/sub/*.log'` runtime: new matching file in new dir is tailed |

### Existing tests — audit results

What's already covered (regression coverage if I refactor `discover` /
`watch`):

| Test | What it locks down |
|---|---|
| `internal/discover.TestListCandidatesDirRecursive` | recursive walk of a literal dir |
| `internal/discover.TestListCandidatesDirNonRecursive` | non-recursive walk only emits direct children |
| `internal/discover.TestListCandidatesFileGlob` | `*.log` glob inside a literal dir |
| `internal/discover.TestAssignFirstMatchWins` | overlapping groups: first declared wins |
| `internal/discover.TestAssignAppliesGlobalFilter` | global filter applies on top of group filter |
| `internal/discover.TestAssignFileGroupBypassesFilters` | file groups skip filters |
| `internal/watch.TestWatcherEmitsAppendedLines` | tailer + fsnotify Write event |
| `internal/watch.TestWatcherPicksUpNewFiles` | `NewFileMatcher` is consulted on Create in a watched dir |
| `internal/watch.TestWatcherIgnoresUnmatchedNewFiles` | Create for a non-matching new file is dropped |
| `internal/watch.TestTailer*` | per-file rotation/truncate/CRLF handling |
| `cmd/log-listener.TestE2ELiveTailingAppend` | end-to-end append on existing file |
| `cmd/log-listener.TestE2ELiveTailingNewFile` | end-to-end Create in existing watched dir |
| `cmd/log-listener.TestE2ELiveTailingRotation` | end-to-end rename rotation |
| `cmd/log-listener.TestE2ELiveTailingFileGroup` | `-f` literal path live tail |

**Gaps the existing tests do NOT cover** — and which the new feature
needs:

1. No test exercises a glob meta-character in a `Group.Paths` for
   `GroupDir` (only `GroupFile`). The change to `ListCandidates`
   touches this path — needs coverage.
2. No test exercises a brand-new SUBDIRECTORY being created in a
   watched parent (`TestWatcherPicksUpNewFiles` covers new *files*).
3. No test exercises a multi-hop create chain (parent dir, then
   subdir, then file all appearing live).

These gaps are exactly the new `TestE2ENewDir*` tests above.

---

## Implementation order

1. **Plan acknowledged** (this doc).
2. `internal/discover/pattern.go` + unit tests for `HasMeta`,
   `LiteralPrefix`, `MatchesPath`.
3. Extend `ListCandidates` for `GroupDir` to glob-expand path roots
   that contain meta. Add unit test.
4. Add `MatchesGroup` helper.
5. `internal/watch`: introduce `NewDirMatcher`; in `handleFsEvent`,
   on Create-of-dir, recursively scan + register. Add unit test
   (`TestWatcherPicksUpNewSubdir`).
6. `cmd/log-listener/main.go`: wire `makeNewDirMatcher`, also watch
   `LiteralPrefix` of each pattern path at startup.
7. Four new e2e tests in `cmd/log-listener/e2e_test.go`.
8. Update README — remove "no recursive subdir creation handling"
   from Limitations; add a "Pattern paths" section under Concepts.
9. Single commit `feat: pattern-based directory matching with
   runtime new-dir detection` + follow-up review-fix commit per the
   per-phase workflow.

---

## Open questions before I implement

1. **MVP scope** — implement the full thing (incl. multi-hop nested
   patterns like `/tmp/acp-logs-*/acp/file.log`), OR start with the
   single-wildcard-last-segment case (`/tmp/acp-logs-*`) which is
   simpler and covers most logger-output-dir setups?

2. **Delete handling** — when a watched matched directory disappears
   (logger rolled it away), tailers inside go to rotation-not-found
   state. That's already harmless. OK to defer "clean removal of
   watch + tailer state" to a later iteration?

3. **Watch budget** — fsnotify watches consume inotify slots
   (default ~8K on Linux). Patterns with very broad matching could
   blow that. Want me to add a configurable watch cap, or rely on
   the kernel error and surface it on `Errors()`?
