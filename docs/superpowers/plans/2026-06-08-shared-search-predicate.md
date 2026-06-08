# Shared Search Predicate + Smart-Case + TUI Regex (5-2) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** One shared `searchmatch.Matcher` defines match semantics; `linebuf.Search` (MCP) and the TUI both use it, so a substring query returns the same (smart-case) matches in both, and the TUI gains a regex toggle.

**Architecture:** New pure `internal/searchmatch` package: smart-case substring or regexp, with `Match`/`Find`/`FindAll`. `linebuf.Search` builds a Matcher (its substring path becomes smart-case). The TUI replaces its lowercase `searchTerm` with a compiled `matcher`, highlights via `Find`/`FindAll`, and adds a `Ctrl+R` regex toggle with invalid-regex feedback.

**Tech Stack:** Go 1.26 stdlib (`regexp`, `strings`, `unicode`). No new deps.

**Spec:** `docs/superpowers/specs/2026-06-08-shared-search-predicate-design.md`

---

## Key facts (verified)

- `linebuf.Search(query string, regex bool, limit int) ([]SearchHit, error)` inlines `re.MatchString` (regex) / `strings.Contains(ln.Text, query)` (case-sensitive substring) over `e.Lines[].Text`, newest-first, one hit per entry, limited.
- TUI model fields (`app.go`): `searchInput bool`, `searchQuery string` (raw typed), `searchTerm string` (committed, lowercased; "" = inactive). `searchTerm != ""` is used as the "search active" guard in app.go (filter/highlight/footer at lines ~570,581,617,632,1060,1078,1144,1154,1307) AND copyref.go:33 / copytext.go:41.
- search.go uses `searchTerm`: `clearSearch` (13), `commitSearch` (37 `strings.ToLower`), `findHit` (184,201), `hitColumn` (255,260), `adjustHorizToHit` (288), `highlightMatches(body, m.searchTerm, style)` called at app.go:1087. `highlightMatches` (search.go:303) loops `strings.Index(lower, tl)` styling each case-insensitive occurrence.
- The search prompt renders at app.go:1271 (`" /" + m.searchQuery + "_"`); footer at app.go:1307-1308 (`· /<query>`).
- `handleSearchInputKey` (search.go:112) handles Esc/Enter/Backspace/Runes; a new `Ctrl+R` case fits here (search-input key, NOT a keymap Action).
- MCP `search` tool (`internal/mcp/tools.go`) forwards `query`/`regex` to `linebuf.Search`; e2e/unit tests may assert case-sensitive substring — update to smart-case.

---

## Task 1: `internal/searchmatch` package

**Files:** Create `internal/searchmatch/searchmatch.go`, `internal/searchmatch/searchmatch_test.go`.

- [ ] **Step 1: Write failing tests**

`internal/searchmatch/searchmatch_test.go`:

```go
package searchmatch

import "testing"

func TestSmartCaseFoldsWhenLowercase(t *testing.T) {
	m, err := Compile("error", false)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Match("an ERROR here") || !m.Match("Error") || !m.Match("error") {
		t.Fatal("lowercase query should fold case")
	}
}

func TestSmartCaseSensitiveWhenUppercase(t *testing.T) {
	m, _ := Compile("Error", false)
	if !m.Match("Error here") {
		t.Fatal("should match exact case")
	}
	if m.Match("an error here") {
		t.Fatal("uppercase query must be case-sensitive (should not match 'error')")
	}
}

func TestFindOffsetsOriginalText(t *testing.T) {
	m, _ := Compile("err", false) // folds
	s, e, ok := m.Find("an ERR x")
	if !ok || s != 3 || e != 6 {
		t.Fatalf("Find = (%d,%d,%v), want (3,6,true) into original text", s, e, ok)
	}
}

func TestFindMultibyteOffsets(t *testing.T) {
	m, _ := Compile("café", false)
	s, e, ok := m.Find("a café x") // 'é' is 2 bytes; offsets are byte offsets
	if !ok || "a café x"[s:e] != "café" {
		t.Fatalf("Find slice = %q, want café (s=%d e=%d ok=%v)", "a café x"[s:e], s, e, ok)
	}
}

func TestRegexMatchAndFindAll(t *testing.T) {
	m, err := Compile("a.c", true)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Match("xabcx") || m.Match("xyz") {
		t.Fatal("regex match wrong")
	}
	all := m.FindAll("abc-aXc")
	if len(all) != 2 {
		t.Fatalf("FindAll = %v, want 2 matches", all)
	}
}

func TestInvalidRegexErrors(t *testing.T) {
	if _, err := Compile("a(", true); err == nil {
		t.Fatal("invalid regex should error")
	}
}

func TestEmptyQueryMatchesNothing(t *testing.T) {
	m, _ := Compile("", false)
	if m.Match("anything") {
		t.Fatal("empty query must match nothing")
	}
	if all := m.FindAll("anything"); len(all) != 0 {
		t.Fatalf("FindAll on empty query = %v, want none", all)
	}
}

func TestFindAllZeroWidthRegexTerminates(t *testing.T) {
	m, _ := Compile("x*", true) // can match empty
	_ = m.FindAll("axbx")       // must not infinite-loop
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/searchmatch/ -v`
Expected: FAIL — package/symbols undefined.

- [ ] **Step 3: Implement**

`internal/searchmatch/searchmatch.go`:

```go
// Package searchmatch is the shared search predicate used by both the MCP-facing
// linebuf.Search and the interactive TUI search, so a query matches the same
// text the same way in both. Substring queries are smart-case (case-insensitive
// unless the query has an uppercase letter); regex queries are literal.
package searchmatch

import (
	"regexp"
	"strings"
	"unicode"
)

// Matcher is a compiled search predicate. The zero value matches nothing.
type Matcher struct {
	empty   bool
	literal string         // case-sensitive substring path (non-empty when used)
	re      *regexp.Regexp // regex path, or the (?i)-wrapped fold path
}

// Compile builds a Matcher. regex=true compiles query as a regexp (error on
// invalid). regex=false is smart-case substring: case-sensitive iff query has an
// uppercase letter, else case-insensitive (implemented as a (?i)-wrapped quoted
// regexp so Find offsets index the original text). Empty query matches nothing.
func Compile(query string, regex bool) (*Matcher, error) {
	if query == "" {
		return &Matcher{empty: true}, nil
	}
	if regex {
		re, err := regexp.Compile(query)
		if err != nil {
			return nil, err
		}
		return &Matcher{re: re}, nil
	}
	if hasUpper(query) {
		return &Matcher{literal: query}, nil
	}
	// Fold: (?i) over the quoted literal keeps Find offsets in the original text.
	re, err := regexp.Compile("(?i)" + regexp.QuoteMeta(query))
	if err != nil { // QuoteMeta output is always valid; defensive
		return nil, err
	}
	return &Matcher{re: re}, nil
}

func hasUpper(s string) bool {
	for _, r := range s {
		if unicode.IsUpper(r) {
			return true
		}
	}
	return false
}

// Match reports whether text matches.
func (m *Matcher) Match(text string) bool {
	if m.empty {
		return false
	}
	if m.re != nil {
		return m.re.MatchString(text)
	}
	return strings.Contains(text, m.literal)
}

// Find returns byte offsets [start,end) of the first match in text.
func (m *Matcher) Find(text string) (start, end int, ok bool) {
	if m.empty {
		return 0, 0, false
	}
	if m.re != nil {
		loc := m.re.FindStringIndex(text)
		if loc == nil {
			return 0, 0, false
		}
		return loc[0], loc[1], true
	}
	i := strings.Index(text, m.literal)
	if i < 0 {
		return 0, 0, false
	}
	return i, i + len(m.literal), true
}

// FindAll returns byte-offset [start,end) pairs for every (non-overlapping)
// match, advancing past zero-width matches so it always terminates.
func (m *Matcher) FindAll(text string) [][2]int {
	if m.empty {
		return nil
	}
	if m.re != nil {
		locs := m.re.FindAllStringIndex(text, -1)
		out := make([][2]int, 0, len(locs))
		for _, l := range locs {
			out = append(out, [2]int{l[0], l[1]})
		}
		return out
	}
	var out [][2]int
	off := 0
	for {
		i := strings.Index(text[off:], m.literal)
		if i < 0 {
			break
		}
		s := off + i
		e := s + len(m.literal)
		out = append(out, [2]int{s, e})
		off = e
	}
	return out
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/searchmatch/ -v && go vet ./internal/searchmatch/`
Expected: PASS, vet clean.

- [ ] **Step 5: Commit**

```bash
git add internal/searchmatch/
git commit -m "feat(searchmatch): shared smart-case/regex search predicate"
```

---

## Task 2: `linebuf.Search` uses the shared predicate (substring → smart-case)

**Files:** Modify `internal/linebuf/linebuf.go`; update `internal/linebuf/linebuf_test.go`; update MCP tests (`e2e_mcp_tools_test.go` and/or `internal/mcp/*_test.go`) for smart-case.

- [ ] **Step 1: Add/adjust a linebuf.Search test for smart-case**

In `internal/linebuf/linebuf_test.go`, add:

```go
func TestSearchSubstringIsSmartCase(t *testing.T) {
	b := New(100, func(ev render.Event) []Line { return []Line{{Text: ev.Raw}} })
	b.Append(render.Event{Raw: "an ERROR line"})
	b.Append(render.Event{Raw: "a warning line"})
	// lowercase query folds → matches the ERROR line.
	hits, err := b.Search("error", false, 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("smart-case fold: hits=%d err=%v, want 1", len(hits), err)
	}
	// uppercase query is case-sensitive → does NOT match 'an ERROR' via 'Error'.
	hits, _ = b.Search("Error", false, 10)
	if len(hits) != 0 {
		t.Fatalf("smart-case sensitive: hits=%d, want 0 (no exact 'Error')", len(hits))
	}
}
```

Run: `go test ./internal/linebuf/ -run TestSearchSubstring -v` → FAIL (currently case-sensitive: "error" finds nothing).

- [ ] **Step 2: Rewrite `linebuf.Search` to use `searchmatch`**

Replace the body's compile + per-line match. New version:

```go
func (b *Buffer) Search(query string, regex bool, limit int) ([]SearchHit, error) {
	if limit <= 0 {
		limit = 50
	}
	matcher, err := searchmatch.Compile(query, regex)
	if err != nil {
		return nil, err
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	var out []SearchHit
	for i := len(b.entries) - 1; i >= 0 && len(out) < limit; i-- {
		e := b.entries[i]
		for li, ln := range e.Lines {
			if matcher.Match(ln.Text) {
				out = append(out, SearchHit{ID: e.ID, Group: e.Group,
					File: e.File, Snippet: ln.Text, MatchedLine: li})
				break
			}
		}
	}
	return out, nil
}
```

Add the import `"github.com/homeend/log-listener/internal/searchmatch"`; remove the now-unused `regexp`/`strings` imports IF no longer used elsewhere in the file (verify with the compiler — `strings` is likely still used by other funcs; only remove what's actually unused).

- [ ] **Step 3: Run linebuf tests**

Run: `go test ./internal/linebuf/ -v && go test -race ./internal/linebuf/`
Expected: PASS (smart-case test passes; regex/ordering/limit tests unchanged).

- [ ] **Step 4: Update MCP tests for smart-case**

Run `go test ./... 2>&1 | grep -i mcp` and inspect any MCP search test (`e2e_mcp_tools_test.go`, `internal/mcp/*_test.go`) that searches a substring expecting case-sensitive behavior. Update expectations to smart-case (a lowercase query now folds; an uppercase query stays exact). If a test specifically wanted case-sensitive substring, change it to use an uppercase query (still exact under smart-case) or `regex: true` with `(?i)` as appropriate. Do not weaken assertions — re-express them for the new, intended semantics.

- [ ] **Step 5: Full suite + commit**

Run: `go test ./... && go vet ./...`
```bash
git add internal/linebuf/ e2e_mcp_tools_test.go
# plus any internal/mcp/*_test.go you changed
git commit -m "feat(linebuf): Search substring is now smart-case via searchmatch"
```

---

## Task 3: TUI search uses the shared matcher (smart-case; no regex yet)

Behavior-preserving except substring becomes smart-case. The regex toggle is Task 4. The `appendEvent`-style trick here is the `matcher != nil` guard replacing `searchTerm != ""`.

**Files:** Modify `internal/tui/app.go`, `internal/tui/search.go`, `internal/tui/copyref.go`, `internal/tui/copytext.go`.

- [ ] **Step 1: Swap model state**

In `app.go` model: remove `searchTerm string`; add `matcher *searchmatch.Matcher`. Keep `searchInput`, `searchQuery`. Add the import `"github.com/homeend/log-listener/internal/searchmatch"`. Update the field doc comment (the `searchTerm` line).

- [ ] **Step 2: Replace the active-guard everywhere**

Replace every `m.searchTerm != ""` with `m.matcher != nil` and every `m.searchTerm == ""` with `m.matcher == nil`, across `app.go` (lines ~570,581,617,632,1060,1078,1144,1154), `search.go` (72,90,184,255), `copyref.go:33`, `copytext.go:41`. (These are all "is search active?" guards.)

- [ ] **Step 3: `clearSearch` / `commitSearch`**

`clearSearch`: replace `m.searchTerm = ""` with `m.matcher = nil`.

`commitSearch`: replace `m.searchTerm = strings.ToLower(q)` with:

```go
	mm, err := searchmatch.Compile(q, false) // Task 4 threads searchRegex here
	if err != nil {
		m.flash = "invalid search: " + err.Error()
		return
	}
	m.matcher = mm
```

(With `regex=false`, `Compile` never errors, but keep the check — Task 4 reuses this path with regex.)

- [ ] **Step 4: `findHit` / `filteredIndices` use the matcher**

In `search.go findHit` (line ~201) replace
`strings.Contains(strings.ToLower(matchHaystack(ev)), m.searchTerm)` with
`m.matcher.Match(matchHaystack(ev))`.

In `app.go filteredIndices` (line ~1154) replace
`strings.Contains(strings.ToLower(matchHaystack(dl)), m.searchTerm)` with
`m.matcher.Match(matchHaystack(dl))`.

- [ ] **Step 5: `hitColumn` / `adjustHorizToHit` / `highlightMatches` via Find/FindAll**

`hitColumn` (search.go:254): replace the `strings.Index(strings.ToLower(body), m.searchTerm)` with the matcher:

```go
	body := matchHaystack(dl)
	bi, _, ok := m.matcher.Find(body)
	if !ok {
		return -1
	}
	col := dispWidth(body[:bi])
	// ... unchanged group/file column offset ...
```

`adjustHorizToHit` (search.go:280): replace `end := start + dispWidth(m.searchTerm)` with the actual match span:

```go
	body := matchHaystack(m.lines[idx])
	bs, be, ok := m.matcher.Find(body)
	if !ok {
		return
	}
	start := m.hitColumn(idx)
	if start < 0 {
		return
	}
	end := start + dispWidth(body[bs:be])
```

`highlightMatches`: change signature to take the matcher and use `FindAll`:

```go
func highlightMatches(body string, mt *searchmatch.Matcher, style func(strs ...string) string) (string, int) {
	if mt == nil || body == "" {
		return body, dispWidth(body)
	}
	spans := mt.FindAll(body)
	if len(spans) == 0 {
		return body, dispWidth(body)
	}
	var sb strings.Builder
	prev := 0
	for _, sp := range spans {
		if sp[0] < prev { // overlapping/contained — skip to keep slicing valid
			continue
		}
		sb.WriteString(body[prev:sp[0]])
		sb.WriteString(style(body[sp[0]:sp[1]]))
		prev = sp[1]
	}
	sb.WriteString(body[prev:])
	out := sb.String()
	return out, dispWidth(out)
}
```

Update the call site `app.go:1087`: `highlightMatches(plain, m.matcher, style)`.

- [ ] **Step 6: Build + update TUI search tests**

Run: `go build ./... && go test ./internal/tui/`
Expected: PASS after migrating any test that referenced `m.searchTerm`. Tests that typed a query and asserted hits should still pass (smart-case is a superset for lowercase queries). If a test asserted that an UPPERCASE query matched lowercase text (old case-insensitive behavior), update it to smart-case expectations. Search for `searchTerm` in `internal/tui/*_test.go` and migrate (set queries via the input path / `commitSearch`, assert via hits/highlight).

- [ ] **Step 7: Full suite + commit**

Run: `go test ./... && go vet ./... && go test -race ./internal/tui/`
```bash
git add internal/tui/
git commit -m "feat(tui): search uses shared searchmatch matcher (smart-case)"
```

---

## Task 4: TUI regex toggle (`Ctrl+R`)

**Files:** Modify `internal/tui/app.go`, `internal/tui/search.go`; Test `internal/tui/search_test.go` (or a new `regex_test.go`).

- [ ] **Step 1: Write failing tests**

Add (e.g. `internal/tui/regex_test.go`):

```go
package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/render"
)

func seedSearch(t *testing.T, vals ...string) *model {
	t.Helper()
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	for _, v := range vals {
		m.appendEvent(render.Event{Group: "g", File: "/a.log",
			Rendered: []render.Part{{Type: "text", Value: v}}})
	}
	return m
}

func TestSearchRegexToggleMatches(t *testing.T) {
	m := seedSearch(t, "user-42", "user-7", "admin")
	m.searchInput = true
	m.searchQuery = "user-[0-9]+"
	m = key(m, tea.KeyMsg{Type: tea.KeyCtrlR}) // toggle regex on
	if !m.searchRegex {
		t.Fatal("Ctrl+R should enable regex")
	}
	m = key(m, tea.KeyMsg{Type: tea.KeyEnter}) // commit
	if m.matcher == nil || !m.matcher.Match("user-42") || m.matcher.Match("admin") {
		t.Fatal("regex matcher did not compile/behave as expected")
	}
}

func TestSearchInvalidRegexStaysInInputWithFlash(t *testing.T) {
	m := seedSearch(t, "x")
	m.searchInput = true
	m.searchQuery = "a("
	m.searchRegex = true
	m = key(m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.searchInput {
		t.Fatal("invalid regex must keep the input box open")
	}
	if m.matcher != nil {
		t.Fatal("invalid regex must not set a matcher")
	}
	if !strings.Contains(m.flash, "invalid") {
		t.Fatalf("expected invalid-regex flash, got %q", m.flash)
	}
}
```

(`key` helper exists in `visual_test.go`.)

- [ ] **Step 2: Run → fail**

Run: `go test ./internal/tui/ -run 'TestSearchRegex|TestSearchInvalid' -v`
Expected: FAIL — `searchRegex` undefined / no toggle.

- [ ] **Step 3: Add `searchRegex` + toggle + thread into commit**

In the model: add `searchRegex bool`. In `clearSearch`: also `m.searchRegex = false`.

In `handleSearchInputKey`, add a case (before the `KeyRunes` case):

```go
	case tea.KeyCtrlR:
		m.searchRegex = !m.searchRegex
		return m
```

In `commitSearch`, thread the flag: `searchmatch.Compile(q, m.searchRegex)`. Keep `m.searchInput = true` on error (the handler sets `m.searchInput = false` BEFORE calling `commitSearch` today — change `handleSearchInputKey`'s Enter case so it only clears `searchInput` when commit succeeds):

```go
	case tea.KeyEnter:
		if m.commitSearch() { // returns true on success
			m.searchInput = false
		}
		return m
```

Change `commitSearch` to return `bool` (true on success / cleared; false on invalid-regex so the box stays open). Update its other callers/`return` paths accordingly (the empty-query clear path returns true).

- [ ] **Step 4: Prompt shows regex + invalid state**

In the search-prompt render (`app.go:1271`), include an indicator when `m.searchRegex`:

```go
	prefix := " /"
	if m.searchRegex {
		prefix = " /(regex) "
	}
	return headerBg.Width(m.width).MaxHeight(1).Render(prefix + m.searchQuery + "_")
```

(Invalid-regex feedback is already surfaced via `m.flash` from `commitSearch`; no extra prompt state needed.)

- [ ] **Step 5: Run tests + full suite**

Run: `go test ./internal/tui/ -run 'TestSearchRegex|TestSearchInvalid' -v && go test ./... && go vet ./... && go test -race ./internal/tui/`
Expected: all PASS.

- [ ] **Step 6: Docs + commit**

Update `README.md` (TUI search section) and `CHANGELOG.md` `[Unreleased]`: smart-case search shared with MCP, and the `Ctrl+R` regex toggle. Regenerate `KEYBINDINGS.md` only if the toggle became a keymap Action (it did NOT — it's a search-input key — so no regen needed; mention `Ctrl+R` in the README search prose).

```bash
git add internal/tui/ README.md CHANGELOG.md
git commit -m "feat(tui): Ctrl+R regex search toggle with invalid-regex feedback"
```

---

## Self-Review

**1. Spec coverage:**
- `searchmatch` package (Match/Find/FindAll, smart-case, regex, empty, offsets) → Task 1. ✓ (FindAll added for highlight-all; consistent with spec's "highlight via Find".)
- `linebuf.Search` uses it; substring → smart-case → Task 2. ✓
- MCP behavior shift + test updates → Task 2 S4. ✓
- TUI matcher migration (guards, findHit, filter, hitColumn, adjustHoriz, highlight) → Task 3. ✓
- Smart-case applies to substring; regex literal → Task 1 (Compile) + Task 4 threads regex flag. ✓
- TUI `Ctrl+R` toggle + invalid-regex stays-in-box + flash → Task 4. ✓
- Prompt `[regex]`/feedback → Task 4 S4. ✓
- Non-goals (no keymap Action, no MCP shape change, view-state index-based untouched) respected. ✓

**2. Placeholder scan:** Task 2 S4 / Task 3 S6 describe test migrations as "find the sites and re-express for smart-case" rather than giving every final test body, because the exact pre-existing tests must be read in place; the new semantics and the migration rule (lowercase folds; uppercase exact) are explicit. No TBD/"handle later".

**3. Type consistency:** `searchmatch.Compile(query string, regex bool) (*Matcher, error)`, `Match(string) bool`, `Find(string)(int,int,bool)`, `FindAll(string)[][2]int`, model `matcher *searchmatch.Matcher` + `searchRegex bool`, `commitSearch() bool`, `highlightMatches(body string, mt *searchmatch.Matcher, style …)` — consistent across tasks.
