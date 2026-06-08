# Shared Search Predicate + Smart-Case + TUI Regex (slice 5-2) — Design

**Date:** 2026-06-08
**Status:** Approved (design)
**Scope:** new `internal/searchmatch`, `internal/linebuf` (Search), `internal/tui` (search), `internal/mcp` (e2e/tests only).

## Context

Slice **5-2** of cycle #5 (TUI on the shared model — see
[[plugin-architecture-roadmap]]). Slice 5-1 put the TUI and MCP on one record
store. They still **search differently**:

- **TUI search** (`internal/tui/search.go`): case-INsensitive substring
  (`strings.ToLower`), no regex; walks display rows for highlight/navigation.
- **`linebuf.Search`** (MCP): case-SENSITIVE substring **or** regex; returns one
  entry-level hit per entry, newest-first, limited.

So `search("Error")` matches `error` in the TUI but not via MCP — divergent
results for the same query. The result *shapes* differ inherently (interactive
row navigation vs retrieval) and stay separate; the unifiable thing is the
**match predicate** — "does this text match this query." This slice extracts one
shared predicate, makes substring matching **smart-case** in both, and adds a
**regex toggle to the TUI** so a human can run the same query an agent can.

## Decisions (locked)

- **Smart-case** for substring: case-insensitive unless the query contains an
  uppercase letter, then case-sensitive (vim/ripgrep style). Applies to
  **substring only**; regex is literal (the author writes `(?i)` for insensitive).
- The TUI gains a **regex toggle** (`Ctrl+R` in search-input mode).
- Result shapes are NOT unified — only the predicate.

## Design

### 1. `internal/searchmatch` — the shared predicate

A new, pure package (imports only `regexp`/`strings`/`unicode`; no `linebuf`/`tui`
import, so no cycles):

```go
package searchmatch

// Matcher is a compiled search predicate: smart-case substring, or a regexp.
type Matcher struct { /* re *regexp.Regexp; or literal string + fold bool */ }

// Compile builds a Matcher. regex=true compiles query as a regexp (returns an
// error on invalid syntax). regex=false is smart-case substring: case-sensitive
// iff the query contains an uppercase letter, else case-insensitive. An empty
// query yields a Matcher that matches nothing.
func Compile(query string, regex bool) (*Matcher, error)

// Match reports whether text matches.
func (m *Matcher) Match(text string) bool

// Find returns the byte offsets [start,end) of the first match in text (ok=false
// if none). Used for highlight; offsets index the ORIGINAL text so callers can
// map to display columns Unicode-safely.
func (m *Matcher) Find(text string) (start, end int, ok bool)
```

- **Smart-case detection:** the query contains an uppercase letter
  (`unicode.IsUpper` over its runes)?
  - **Yes → case-sensitive literal:** `Match` = `strings.Contains(text, query)`;
    `Find` = `strings.Index(text, query)` + `len(query)` (exact byte offsets).
  - **No → case-insensitive:** compile `regexp.Compile("(?i)" +
    regexp.QuoteMeta(query))` ONCE at `Compile` time; `Match` = `re.MatchString`,
    `Find` = `re.FindStringIndex`. Using a `(?i)`-wrapped quoted regexp (rather
    than lowercasing the text) guarantees `Find` returns offsets into the
    **original** text, avoiding the `ToLower`-length-change pitfall for non-ASCII.
- **Regex (`regex=true`):** `re := regexp.Compile(query)` (error on invalid);
  `Match` = `re.MatchString`; `Find` = `re.FindStringIndex`.
- **Empty query:** `Match` always false, `Find` ok=false. (Callers already guard
  the empty case; this is belt-and-suspenders.)

### 2. `linebuf.Search` uses the shared predicate

`Search(query string, regex bool, limit int)` builds
`searchmatch.Compile(query, regex)` (propagating the compile error) and replaces
its inline `re.MatchString` / `strings.Contains(ln.Text, query)` with
`matcher.Match(ln.Text)`. Net behavior change: the **substring path becomes
smart-case** (was case-sensitive). Entry-walk, newest-first ordering, per-entry
single hit, and the `SearchHit` shape are unchanged.

### 3. MCP

No API/schema change — the `search` tool already forwards `query`/`regex` to
`linebuf.Search`. Its substring behavior shifts to smart-case (the intended
unification). MCP e2e/unit tests that assumed case-sensitive substring are
updated to smart-case expectations.

### 4. TUI search (`internal/tui/search.go`)

State:
- Remove `searchTerm string` (the pre-lowercased query). Add
  `matcher *searchmatch.Matcher` (compiled at commit) and `searchRegex bool`
  (toggle state). Keep `searchQuery string` (raw input buffer).
- `clearSearch` sets `matcher = nil`, `searchRegex = false`.

Matching & navigation:
- All guards `if m.searchTerm == ""` become `if m.matcher == nil`.
- `findHit` / `filteredIndices`: `m.matcher.Match(matchHaystack(dl))` instead of
  `strings.Contains(strings.ToLower(matchHaystack(dl)), m.searchTerm)`.
- `hitColumn` and the highlight: use `m.matcher.Find(body)` to get the match
  start + width per row (regex match length varies row to row), feeding the
  existing Unicode-safe `dispWidth` slicing. (Today these assume a fixed
  `searchTerm` width — replace with the `Find` span.)

Commit & errors:
- `commitSearch`: `mm, err := searchmatch.Compile(m.searchQuery, m.searchRegex)`.
  On `err` (invalid regex): keep `m.searchInput = true` (stay in the box), set a
  flash/inline error ("invalid regex: <msg>"), do NOT set `m.matcher`. On
  success: `m.matcher = mm` and proceed (jump to first hit) as today.

Regex toggle:
- In `handleSearchInputKey`, add `case tea.KeyCtrlR: m.searchRegex = !m.searchRegex`
  (re-renders the prompt; does not leave input mode). It's a search-input key
  like Enter/Esc — **no keymap Action** added.
- The search prompt shows a `[regex]` indicator when `searchRegex` is on, and an
  "invalid regex" hint when the last compile failed.

### 5. Result shapes stay separate

The TUI keeps row-level navigation/highlight over `m.lines`; MCP keeps
entry-level retrieval. Only `searchmatch` is shared, guaranteeing a query matches
the same text the same way in both.

## Non-goals

- View-state stays index-based (slice 5-3).
- No change to MCP's `search` result shape or tool schema.
- No new keymap Action; the regex toggle is a search-input-mode key.
- Smart-case does NOT apply to regex (regex is literal; use `(?i)`).

## Testing

- **`searchmatch` unit tests:** smart-case boundaries (`error` matches `Error`;
  `Error` does not match `error`); regex match + `Find` offsets; invalid regex
  returns an error; empty query matches nothing; multibyte/Unicode `Find` offsets
  correct.
- **`linebuf.Search`:** substring query is now smart-case (add/adjust a test);
  regex unchanged; ordering/limit unchanged.
- **TUI:** smart-case hits via the model; `Ctrl+R` toggles regex and a regex
  query matches+highlights; invalid regex shows feedback and does not commit;
  highlight span correct for a regex match whose length differs from the query.
- **MCP e2e/unit:** update any case-sensitive substring expectation to smart-case.
- `go test ./...`, `go vet ./...`, `go test -race ./...` green.

## Success criteria

- One `searchmatch.Matcher` defines match semantics; `linebuf.Search` and the TUI
  both use it — a substring query returns the same matches (smart-case) in the
  TUI and via MCP.
- The TUI can toggle regex (`Ctrl+R`) and run the same regex an agent can, with
  correct highlight and graceful invalid-regex handling.
- No regression in TUI search navigation/highlight or MCP search retrieval;
  full suite + vet + race green.
