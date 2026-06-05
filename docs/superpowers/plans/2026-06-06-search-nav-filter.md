# TUI Search Filter / Nav / Auto-Scroll / Repeat — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enhance TUI search with a whole-entry "filter to matches" mode (`t`), Up/Down hit navigation, horizontal auto-scroll to the matched term, and `/`+Enter to repeat the last term.

**Architecture:** All changes live in `internal/tui` (`app.go`, `search.go`). The filter view is computed on the fly from `m.entries` (`filteredIndices`) and rendered through the existing `streamTop` anchor — no parallel scroll state. Hit jumps gain horizontal panning (`adjustHorizToHit`/`hitColumn`). Tests are model-level (no TTY).

**Tech Stack:** Go 1.26, bubbletea, standard `testing`.

**Spec:** `docs/superpowers/specs/2026-06-05-search-nav-filter-design.md`

---

## Baseline (current code, for reference)

`internal/tui/search.go`:
```go
func (m *model) clearSearch() {
	m.searchTerm = ""
	m.searchQuery = ""
	m.searchHit = -1
	m.wrapPrompt = 0
}

func (m *model) commitSearch() {
	q := strings.TrimSpace(m.searchQuery)
	if q == "" {
		m.clearSearch()
		return
	}
	m.searchTerm = strings.ToLower(q)
	start := m.streamTop
	if m.tailMode {
		start = len(m.lines) - 1
		hit := m.findHit(start, -1)
		if hit >= 0 {
			m.jumpToHit(hit)
			return
		}
		hit = m.findHit(0, +1)
		if hit >= 0 {
			m.jumpToHit(hit)
		}
		return
	}
	hit := m.findHit(start, +1)
	if hit >= 0 {
		m.jumpToHit(hit)
		return
	}
	hit = m.findHit(start-1, -1)
	if hit >= 0 {
		m.jumpToHit(hit)
	}
}

func (m *model) jumpToHit(idx int) {
	if idx < 0 || idx >= len(m.lines) {
		return
	}
	m.searchHit = idx
	m.tailMode = false
	rows := m.contentHeight()
	top := idx - rows/2
	if top < 0 {
		top = 0
	}
	if top > len(m.lines)-1 {
		top = len(m.lines) - 1
	}
	m.streamTop = top
}
```

`internal/tui/app.go` `lineEnabled`:
```go
func (m *model) lineEnabled(dl displayLine) bool {
	if m.collapseMultiline && isContinuation(dl) {
		return false
	}
	if dl.group == "" {
		return true
	}
	enabled, known := m.groupEnabled[dl.group]
	if !known {
		return true
	}
	return enabled
}
```

Test helpers (in `internal/tui/search_test.go`): `seedSearchModel(t, n, hitIdxs map[int]bool)` appends n single-line events where `hitIdxs[i]` adds `" needle here"` to the body, sizes the window 80x20, returns `*model`. `typeQuery(t, m, q)` sends `/`, the chars of q, then Enter.

---

## Task 1: `/`+Enter repeats last term

**Files:**
- Modify: `internal/tui/app.go` (model struct — add `filterMode`, `lastQuery`)
- Modify: `internal/tui/search.go` (`commitSearch`, `clearSearch`)
- Test: `internal/tui/search_test.go`

- [ ] **Step 1: Write the failing tests** — append to `internal/tui/search_test.go`:

```go
func TestSearchRepeatLastTerm(t *testing.T) {
	m := seedSearchModel(t, 5, map[int]bool{2: true, 4: true})
	m = typeQuery(t, m, "needle")
	if m.searchTerm != "needle" {
		t.Fatalf("term = %q", m.searchTerm)
	}
	m.clearSearch()
	if m.searchTerm != "" {
		t.Fatal("clear should drop the active term")
	}
	// "/" then Enter with nothing typed re-runs the last term.
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = m2.(*model)
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m2.(*model)
	if m.searchTerm != "needle" {
		t.Fatalf("empty-Enter should repeat last term, got %q", m.searchTerm)
	}
	if m.searchQuery != "needle" {
		t.Fatalf("footer query should reflect the repeated term, got %q", m.searchQuery)
	}
}

func TestSearchEmptyEnterNoPriorTermClears(t *testing.T) {
	m := seedSearchModel(t, 3, nil)
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = m2.(*model)
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m2.(*model)
	if m.searchTerm != "" {
		t.Fatalf("empty Enter with no prior term should set no term, got %q", m.searchTerm)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test -run 'TestSearchRepeatLastTerm|TestSearchEmptyEnterNoPriorTermClears' ./internal/tui/`
Expected: FAIL — `lastQuery` field missing / empty-Enter clears instead of repeating.

- [ ] **Step 3: Add model fields.** In `internal/tui/app.go`, in the `model` struct, immediately after the `wrapPrompt rune` field in the Search state block, add:

```go
	// lastQuery is the most recently committed query (original case),
	// preserved across clears so "/"+Enter repeats it. filterMode is the
	// `t` "show only matching entries" toggle.
	lastQuery  string
	filterMode bool
```

- [ ] **Step 4: Update `commitSearch` and `clearSearch`** in `internal/tui/search.go`.

Replace the head of `commitSearch` (the empty-query guard and the term assignment) so it reads:

```go
func (m *model) commitSearch() {
	q := strings.TrimSpace(m.searchQuery)
	if q == "" {
		if m.lastQuery == "" {
			m.clearSearch()
			return
		}
		q = m.lastQuery // "/"+Enter repeats the last term
	}
	m.lastQuery = q
	m.searchQuery = q
	m.searchTerm = strings.ToLower(q)
	start := m.streamTop
	// ... (rest of commitSearch unchanged) ...
```

(Leave everything from `start := m.streamTop` onward exactly as it is.)

Replace `clearSearch` with (adds `filterMode` reset; does NOT touch `lastQuery`):

```go
// clearSearch wipes the active search state — term, hit pointer, pending
// wrap prompt, and the filter toggle — so highlights vanish on the next
// render. lastQuery is intentionally preserved so "/"+Enter can repeat it.
func (m *model) clearSearch() {
	m.searchTerm = ""
	m.searchQuery = ""
	m.searchHit = -1
	m.wrapPrompt = 0
	m.filterMode = false
}
```

- [ ] **Step 5: Run to verify pass**

Run: `go test -run 'TestSearchRepeatLastTerm|TestSearchEmptyEnterNoPriorTermClears' ./internal/tui/` → PASS
Run: `go test ./internal/tui/` → all PASS. `go vet ./internal/tui/` → clean.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/app.go internal/tui/search.go internal/tui/search_test.go
git commit -m "phase 1: /+Enter repeats last search term

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 2: Up/Down navigate hits when a term is active

**Files:**
- Modify: `internal/tui/app.go` (the `up`/`k` and `down`/`j` key cases)
- Test: `internal/tui/search_test.go`

- [ ] **Step 1: Write the failing tests** — append to `internal/tui/search_test.go`:

```go
func TestSearchUpDownNavigateHits(t *testing.T) {
	m := seedSearchModel(t, 6, map[int]bool{1: true, 3: true, 5: true})
	m = typeQuery(t, m, "needle") // tail-mode commit lands on the last hit (5)
	if m.searchHit < 0 {
		t.Fatal("expected a current hit after commit")
	}
	// Up -> previous hit (earlier index)
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = m2.(*model)
	prev := m.searchHit
	if prev >= 5 {
		t.Fatalf("Up should move to an earlier hit, got %d", prev)
	}
	// Down -> next hit (later index)
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = m2.(*model)
	if m.searchHit <= prev {
		t.Fatalf("Down should advance to a later hit, got %d (prev %d)", m.searchHit, prev)
	}
}

func TestUpDownScrollWhenNoSearch(t *testing.T) {
	m := newModel(1000)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	for i := 0; i < 50; i++ {
		m.appendEvent(render.Event{Group: "g", File: "/x.log",
			Rendered: []render.Part{{Type: "text", Value: fmt.Sprintf("line %d", i)}}})
	}
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = m2.(*model)
	m.tailMode = false
	m.streamTop = 20
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = m2.(*model)
	if m.streamTop != 19 {
		t.Fatalf("Up with no active search should scroll one line: streamTop=%d want 19", m.streamTop)
	}
}
```

(`search_test.go` already imports `fmt` and `render`; if not, add them.)

- [ ] **Step 2: Run to verify failure**

Run: `go test -run 'TestSearchUpDownNavigateHits|TestUpDownScrollWhenNoSearch' ./internal/tui/`
Expected: FAIL — Up/Down currently scroll, so `searchHit` doesn't change on Up/Down.

- [ ] **Step 3: Update the up/down handlers.** In `internal/tui/app.go`, replace the `case "up", "k":` and `case "down", "j":` blocks with:

```go
		// Vertical: one row
		case "up", "k":
			if m.showFiles {
				if m.filesScroll > 0 {
					m.filesScroll--
				}
			} else if m.searchTerm != "" {
				m.searchPrev()
			} else {
				m.unstickFromTail()
				m.streamTop--
				if m.streamTop < 0 {
					m.streamTop = 0
				}
			}
		case "down", "j":
			if m.showFiles {
				if m.filesScroll < len(m.files)-1 {
					m.filesScroll++
				}
			} else if m.searchTerm != "" {
				m.searchNext()
			} else if !m.tailMode {
				m.streamTop++
				m.maybeReStick()
			}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test -run 'TestSearchUpDownNavigateHits|TestUpDownScrollWhenNoSearch' ./internal/tui/` → PASS
Run: `go test ./internal/tui/` → all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/app.go internal/tui/search_test.go
git commit -m "phase 2: Up/Down navigate hits while a search term is active

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 3: Horizontal auto-scroll to the hit

**Files:**
- Modify: `internal/tui/search.go` (`jumpToHit`; add `hitColumn`, `adjustHorizToHit`)
- Modify: `internal/tui/app.go` (add `hitMargin` const near the other horiz consts)
- Test: `internal/tui/search_test.go`

- [ ] **Step 1: Write the failing tests** — append to `internal/tui/search_test.go`:

```go
func TestJumpToHitScrollsHorizontallyToMatch(t *testing.T) {
	m := newModel(1000)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	m.showGroup = false
	m.showFile = false
	body := strings.Repeat("x", 100) + "NEEDLE" + strings.Repeat("y", 20)
	m.appendEvent(render.Event{Group: "g", File: "/x.log",
		Rendered: []render.Part{{Type: "text", Value: body}}})
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	m = m2.(*model)
	m = typeQuery(t, m, "needle") // match starts at column 100, far right
	if m.horizScroll == 0 {
		t.Fatal("expected horizScroll to move to the off-screen match")
	}
	if 100 < m.horizScroll || 100 >= m.horizScroll+m.width {
		t.Fatalf("match col 100 not in view [%d,%d)", m.horizScroll, m.horizScroll+m.width)
	}
}

func TestJumpToHitKeepsHorizWhenMatchVisible(t *testing.T) {
	m := newModel(1000)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	m.showGroup = false
	m.showFile = false
	m.appendEvent(render.Event{Group: "g", File: "/x.log",
		Rendered: []render.Part{{Type: "text", Value: "early NEEDLE here"}}})
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	m = typeQuery(t, m, "needle")
	if m.horizScroll != 0 {
		t.Fatalf("match already visible; horizScroll should stay 0, got %d", m.horizScroll)
	}
}

func TestHitColumnAccountsForPrefix(t *testing.T) {
	m := newModel(1000)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	m.showGroup = true // prefix "[g] "
	m.showFile = false
	m.appendEvent(render.Event{Group: "g", File: "/x.log",
		Rendered: []render.Part{{Type: "text", Value: "NEEDLE"}}})
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	m.searchTerm = "needle"
	// "[g] " is 4 visible columns, then NEEDLE at col 4.
	if got := m.hitColumn(0); got != 4 {
		t.Fatalf("hitColumn with [g] prefix = %d, want 4", got)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test -run 'TestJumpToHit|TestHitColumn' ./internal/tui/`
Expected: FAIL — `hitColumn` undefined; horizScroll not adjusted.

- [ ] **Step 3: Add the `hitMargin` const.** In `internal/tui/app.go`, in the `const (...)` block with `horizStep`/`horizFastStep`/`vertFastStep`, add:

```go
	hitMargin = horizStep / 2 // left-margin columns when panning to a hit
```

- [ ] **Step 4: Add `hitColumn` and `adjustHorizToHit`, and call the latter from `jumpToHit`.** In `internal/tui/search.go`, add these two functions:

```go
// hitColumn returns the on-screen column (visible rune offset) of the first
// occurrence of the search term on line idx, accounting for the
// "[group] file:" prefix on head lines (blocks have no prefix). Returns -1
// if the term is not present on that line.
func (m *model) hitColumn(idx int) int {
	if idx < 0 || idx >= len(m.lines) || m.searchTerm == "" {
		return -1
	}
	dl := m.lines[idx]
	body := dl.body
	if dl.isBlock {
		body = stripANSI(body)
	}
	bi := strings.Index(strings.ToLower(body), m.searchTerm)
	if bi < 0 {
		return -1
	}
	col := runeLen(body[:bi])
	if !dl.isBlock {
		if m.showGroup {
			col += runeLen(dl.group) + 3 // "[" id "]" + space
		}
		if m.showFile {
			col += runeLen(dl.file) + 2 // ": "
		}
	}
	return col
}

// adjustHorizToHit pans the view horizontally so the match on line idx is
// visible. If the match already lies within the current window it is left
// alone; otherwise horizScroll moves so the match starts a small margin in
// from the left edge.
func (m *model) adjustHorizToHit(idx int) {
	if m.width <= 0 {
		return
	}
	start := m.hitColumn(idx)
	if start < 0 {
		return
	}
	end := start + runeLen(m.searchTerm)
	if start < m.horizScroll || end > m.horizScroll+m.width {
		ns := start - hitMargin
		if ns < 0 {
			ns = 0
		}
		m.horizScroll = ns
	}
}
```

Then add a call at the end of `jumpToHit` (just before its closing brace, after `m.streamTop = top`):

```go
	m.adjustHorizToHit(idx)
}
```

- [ ] **Step 5: Run to verify pass**

Run: `go test -run 'TestJumpToHit|TestHitColumn' ./internal/tui/` → PASS
Run: `go test ./internal/tui/` → all PASS. `go vet ./internal/tui/` → clean.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/app.go internal/tui/search.go internal/tui/search_test.go
git commit -m "phase 3: pan horizontally to an off-screen hit

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 4: `t` filter to matching entries (whole-entry)

**Files:**
- Modify: `internal/tui/app.go` (`t` key case; `filteredIndices`; `groupEnabledLine` refactor of `lineEnabled`; `collectVisible` filter branch)
- Modify: `internal/tui/search.go` (`jumpToHit` filter-aware vertical centering)
- Test: `internal/tui/search_test.go`

- [ ] **Step 1: Write the failing tests** — append to `internal/tui/search_test.go`:

```go
func TestFilterShowsWholeMatchingEntries(t *testing.T) {
	m := newModel(1000)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	// entry 0: plain, no match
	m.appendEvent(render.Event{Group: "g", File: "/x.log",
		Rendered: []render.Part{{Type: "text", Value: "boring line"}}})
	// entry 1: head + JSON block; match is INSIDE the json block
	m.appendEvent(render.Event{Group: "g", File: "/x.log",
		Rendered: []render.Part{
			{Type: "text", Value: "request received"},
			{Type: "json", Value: map[string]any{"userId": 42}},
		}})
	// entry 2: plain, matches
	m.appendEvent(render.Event{Group: "g", File: "/x.log",
		Rendered: []render.Part{{Type: "text", Value: "userId in plain line"}}})
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = m2.(*model)
	m = typeQuery(t, m, "userId")
	m.filterMode = true

	e0 := len(m.entries[0].lines)
	e1 := len(m.entries[1].lines)
	e2 := len(m.entries[2].lines)
	if e1 < 2 {
		t.Fatalf("setup: entry1 should have a multi-line json block, got %d lines", e1)
	}
	fil := m.filteredIndices()
	if len(fil) != e1+e2 {
		t.Fatalf("filtered = %d, want %d (whole entry1 %d + entry2 %d)", len(fil), e1+e2, e1, e2)
	}
	for _, idx := range fil {
		if idx < e0 {
			t.Fatalf("entry0 (no match) line %d must be excluded", idx)
		}
	}
}

func TestFilterTNoopWithoutTerm(t *testing.T) {
	m := seedSearchModel(t, 3, map[int]bool{1: true})
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	m = m2.(*model)
	if m.filterMode {
		t.Fatal("t must be a no-op with no active search term")
	}
	m = typeQuery(t, m, "needle")
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	m = m2.(*model)
	if !m.filterMode {
		t.Fatal("t should enable the filter when a term is active")
	}
}

func TestCollectVisibleFilterModeOnlyShowsFiltered(t *testing.T) {
	m := newModel(1000)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	m.appendEvent(render.Event{Group: "g", File: "/x.log",
		Rendered: []render.Part{{Type: "text", Value: "boring"}}})
	m.appendEvent(render.Event{Group: "g", File: "/x.log",
		Rendered: []render.Part{{Type: "text", Value: "needle one"}}})
	m.appendEvent(render.Event{Group: "g", File: "/x.log",
		Rendered: []render.Part{{Type: "text", Value: "needle two"}}})
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = m2.(*model)
	m = typeQuery(t, m, "needle")
	m.filterMode = true
	m.streamTop = 0
	vis := m.collectVisible(m.contentHeight())
	filSet := map[int]bool{}
	for _, i := range m.filteredIndices() {
		filSet[i] = true
	}
	if len(vis) == 0 {
		t.Fatal("expected visible filtered rows")
	}
	for _, i := range vis {
		if !filSet[i] {
			t.Fatalf("visible idx %d is not in the filtered set", i)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test -run 'TestFilter|TestCollectVisibleFilterMode' ./internal/tui/`
Expected: FAIL — `filteredIndices` undefined; `t` does nothing; `collectVisible` shows all lines.

- [ ] **Step 3: Add the `t` key case.** In `internal/tui/app.go`, add this case right after `case "p":` (the search prev key):

```go
		case "t":
			if m.searchTerm != "" {
				m.filterMode = !m.filterMode
				if m.filterMode {
					m.tailMode = false
				}
			}
```

- [ ] **Step 4: Refactor `lineEnabled` to expose a group-only check, and add `filteredIndices`.** In `internal/tui/app.go`, replace `lineEnabled` with:

```go
// groupEnabledLine reports whether dl's group is enabled (ignores the
// collapse-multiline toggle). Used by the search filter, which shows whole
// matching entries including their block lines.
func (m *model) groupEnabledLine(dl displayLine) bool {
	if dl.group == "" {
		return true
	}
	enabled, known := m.groupEnabled[dl.group]
	if !known {
		return true // unknown groups (shouldn't happen) default to visible
	}
	return enabled
}

// lineEnabled reports whether dl appears in the normal stream window given
// the per-group toggles AND the multiline-collapse toggle.
func (m *model) lineEnabled(dl displayLine) bool {
	if m.collapseMultiline && isContinuation(dl) {
		return false
	}
	return m.groupEnabledLine(dl)
}

// filteredIndices returns the absolute m.lines indices shown when the search
// filter is active: every group-enabled line of every entry that has at
// least one line containing the term. Whole entries are kept so a matched
// JSON/XML block appears in full alongside its head line. Returns nil when no
// term is set. Collapse-multiline is intentionally ignored here.
func (m *model) filteredIndices() []int {
	if m.searchTerm == "" {
		return nil
	}
	var out []int
	off := 0
	for _, e := range m.entries {
		n := len(e.lines)
		matched := false
		for _, dl := range e.lines {
			hay := dl.body
			if dl.isBlock {
				hay = stripANSI(hay)
			}
			if strings.Contains(strings.ToLower(hay), m.searchTerm) {
				matched = true
				break
			}
		}
		if matched {
			for k := 0; k < n; k++ {
				idx := off + k
				if m.groupEnabledLine(m.lines[idx]) {
					out = append(out, idx)
				}
			}
		}
		off += n
	}
	return out
}
```

(If `app.go` does not already import `strings`, add it — it is used elsewhere in the package so it should already be imported in `app.go`. Verify.)

- [ ] **Step 5: Add the filter branch to `collectVisible`.** In `internal/tui/app.go`, insert this block at the start of `collectVisible`, immediately after the `if rows <= 0 || len(m.lines) == 0 { return nil }` guard and before the `out := make(...)` / tailMode logic:

```go
	if m.filterMode {
		fil := m.filteredIndices()
		if len(fil) == 0 {
			return nil
		}
		start := 0
		for start < len(fil) && fil[start] < m.streamTop {
			start++
		}
		if start >= len(fil) {
			start = len(fil) - 1
		}
		end := start + rows
		if end > len(fil) {
			end = len(fil)
		}
		return append([]int(nil), fil[start:end]...)
	}
```

- [ ] **Step 6: Make `jumpToHit` filter-aware vertically.** In `internal/tui/search.go`, replace the whole `jumpToHit` with (this is the final version — it includes the `adjustHorizToHit` call added in Task 3):

```go
// jumpToHit positions the viewport on event index idx, exits tail mode, and
// pans horizontally so the match is visible. In filter mode it centers within
// the filtered list; otherwise it centers on the absolute index.
func (m *model) jumpToHit(idx int) {
	if idx < 0 || idx >= len(m.lines) {
		return
	}
	m.searchHit = idx
	m.tailMode = false
	rows := m.contentHeight()
	if m.filterMode {
		fil := m.filteredIndices()
		pos := -1
		for i, fi := range fil {
			if fi == idx {
				pos = i
				break
			}
		}
		if pos >= 0 {
			top := pos - rows/2
			if top < 0 {
				top = 0
			}
			if top > len(fil)-1 {
				top = len(fil) - 1
			}
			m.streamTop = fil[top]
		}
	} else {
		top := idx - rows/2
		if top < 0 {
			top = 0
		}
		if top > len(m.lines)-1 {
			top = len(m.lines) - 1
		}
		m.streamTop = top
	}
	m.adjustHorizToHit(idx)
}
```

- [ ] **Step 7: Run to verify pass**

Run: `go test -run 'TestFilter|TestCollectVisibleFilterMode' ./internal/tui/` → PASS
Run: `go test ./internal/tui/` → all PASS. `go vet ./internal/tui/` → clean.

- [ ] **Step 8: Commit**

```bash
git add internal/tui/app.go internal/tui/search.go internal/tui/search_test.go
git commit -m "phase 4: t filter shows whole matching entries

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 5: Footer + header indicators

**Files:**
- Modify: `internal/tui/app.go` (`renderFooter` search suffix; header string)
- Test: `internal/tui/search_test.go`

- [ ] **Step 1: Write the failing test** — append to `internal/tui/search_test.go`:

```go
func TestFooterShowsFilterTag(t *testing.T) {
	m := seedSearchModel(t, 3, map[int]bool{1: true})
	m = typeQuery(t, m, "needle")
	if strings.Contains(stripANSI(m.renderFooter()), "filter") {
		t.Fatal("filter tag should be absent when filterMode is off")
	}
	m.filterMode = true
	if !strings.Contains(stripANSI(m.renderFooter()), "filter") {
		t.Fatalf("expected filter tag in footer:\n%s", stripANSI(m.renderFooter()))
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test -run TestFooterShowsFilterTag ./internal/tui/`
Expected: FAIL — no `filter` text in the footer.

- [ ] **Step 3: Update the footer suffix.** In `internal/tui/app.go` `renderFooter`, replace the search-suffix block:

```go
	search := ""
	if m.searchTerm != "" {
		search = fmt.Sprintf(" · /%s", m.searchQuery)
	}
```

with:

```go
	search := ""
	if m.searchTerm != "" {
		search = fmt.Sprintf(" · /%s", m.searchQuery)
		if m.filterMode {
			search += " filter"
		}
	}
```

- [ ] **Step 4: Update the help header.** In `internal/tui/app.go` `View`, change the header string's trailing `· / search · n/p ` to `· / search · n/p · t filter `:

```go
	header := headerBg.Width(m.width).Render(" log-listener — q quit · Tab files · Ctrl+G groups · Ctrl+E rend · 1-9 grp · m collapse · Ctrl+P/L cols · Ctrl+R clear · / search · n/p · t filter ")
```

- [ ] **Step 5: Run to verify pass**

Run: `go test -run TestFooterShowsFilterTag ./internal/tui/` → PASS
Run: `go test ./internal/tui/` → all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/app.go internal/tui/search_test.go
git commit -m "phase 5: footer filter tag and t filter header hint

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 6: Docs + full verification

**Files:**
- Modify: `README.md`, `CHANGELOG.md`

- [ ] **Step 1: Document the keys.** In `README.md`, find the TUI key documentation (search for the `/ search` / `n`/`p` description and the keybinding list). Add entries describing:
  - `t` — toggle "filter to matches": shows only entries containing the term; a match inside a rendered JSON/XML block shows the whole block.
  - While a search term is active, `Up`/`Down` (and `k`/`j`) jump to the previous/next hit; `PgUp`/`PgDn` and `Ctrl+arrows` still scroll.
  - Jumping to a hit pans the view horizontally so the term is visible.
  - `/` then Enter (empty) repeats the last search term.

- [ ] **Step 2: Add a CHANGELOG entry.** In `CHANGELOG.md`, under `## [Unreleased]`, add a section:

```markdown
### TUI search: filter, hit navigation, auto-scroll, repeat
- **`t` filter**: show only entries containing the search term; a match inside
  a rendered JSON/XML block shows the whole block (whole-entry filtering).
- **Up/Down navigate hits**: while a term is active, Up/Down (and `k`/`j`) jump
  to the previous/next hit; PgUp/PgDn and Ctrl+arrows still scroll.
- **Horizontal auto-scroll**: jumping to a hit pans the view so an off-screen
  term becomes visible.
- **Repeat search**: `/` then Enter re-runs the last committed term (preserved
  across clears).
```

- [ ] **Step 3: Full verification**

Run:
```bash
go test ./...
go vet ./...
go test -race ./internal/tui/
```
Expected: all PASS / clean.

- [ ] **Step 4: Commit**

```bash
git add README.md CHANGELOG.md
git commit -m "phase 6: document TUI search filter/nav/auto-scroll/repeat

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Self-Review Notes

- **Spec coverage:** repeat-last-term (Task 1), Up/Down hit-nav + scroll-otherwise (Task 2), horizontal auto-scroll incl. prefix-aware `hitColumn` (Task 3), whole-entry `t` filter + `filteredIndices` + collapse-ignored + filter-aware `jumpToHit` vertical centering (Task 4), footer/header indicators (Task 5), docs (Task 6). Out-of-scope items (regex, case toggle, persistence) intentionally omitted.
- **Type/name consistency:** `filterMode`/`lastQuery` fields added in Task 1 and used in Tasks 4/5; `groupEnabledLine`, `filteredIndices`, `hitColumn`, `adjustHorizToHit`, `hitMargin` defined before use; `jumpToHit` final form in Task 4 supersedes the Task 3 incremental edit (both call `adjustHorizToHit`).
- **Match predicate** (lowercase `Contains`, `stripANSI` for blocks) is identical in `findHit`, `filteredIndices`, and `hitColumn`.
- **No placeholders.**
