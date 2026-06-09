package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/searchmatch"
)

// clearSearch wipes the active search state — term, hit pointer, pending
// wrap prompt, and the filter toggle — so highlights vanish on the next
// render. lastQuery is intentionally preserved so "/"+Enter can repeat it.
func (m *model) clearSearch() {
	m.matcher = nil
	m.searchQuery = ""
	m.searchRegex = false
	m.setSearchHitRow(-1)
	m.wrapPrompt = 0
	m.filterMode = false
}

// commitSearch turns the typed query into the active term and jumps to
// the first hit. The first hit is searched from the current viewport
// origin: in tail mode that's "from the end", in browse mode that's
// "from streamTop forward". If there's no hit anywhere in the buffer,
// the term stays committed (so n/p can prompt for wrap) but no jump
// happens.
func (m *model) commitSearch() bool {
	q := strings.TrimSpace(m.searchQuery)
	if q == "" {
		if m.lastQuery == "" {
			m.clearSearch()
			return true
		}
		q = m.lastQuery // "/"+Enter repeats the last term
	}
	m.lastQuery = q
	m.searchQuery = q
	mm, err := searchmatch.Compile(q, m.searchRegex)
	if err != nil {
		m.flash = "invalid search: " + err.Error()
		return false
	}
	m.matcher = mm
	start := m.streamTopRow()
	if m.tailMode {
		start = len(m.lines) - 1
		// In tail mode walk backward — most-recent match is what the
		// user expects to land on first.
		hit := m.findHit(start, -1)
		if hit >= 0 {
			m.jumpToHit(hit)
			return true
		}
		// Nothing earlier — try the buffer forward from the top as a
		// fallback so a brand-new search that misses below the tail
		// still surfaces older matches without an explicit wrap.
		hit = m.findHit(0, +1)
		if hit >= 0 {
			m.jumpToHit(hit)
		}
		return true
	}
	hit := m.findHit(start, +1)
	if hit >= 0 {
		m.jumpToHit(hit)
		return true
	}
	// Fallback: search before the cursor.
	hit = m.findHit(start-1, -1)
	if hit >= 0 {
		m.jumpToHit(hit)
	}
	return true
}

// searchNext advances to the next hit after the current one. If no
// hit exists between cursor+1 and end, sets wrapPrompt='n'.
func (m *model) searchNext() {
	if m.matcher == nil || len(m.lines) == 0 {
		return
	}
	from := m.searchHitRow() + 1
	if m.searchHitRow() < 0 {
		from = m.streamTopRow()
	}
	hit := m.findHit(from, +1)
	if hit >= 0 {
		m.jumpToHit(hit)
		return
	}
	m.wrapPrompt = 'n'
}

// searchPrev steps to the previous hit before the current one. If no
// hit exists between cursor-1 and start, sets wrapPrompt='p'.
func (m *model) searchPrev() {
	if m.matcher == nil || len(m.lines) == 0 {
		return
	}
	from := m.searchHitRow() - 1
	if m.searchHitRow() < 0 {
		from = m.streamTopRow()
	}
	if from < 0 {
		m.wrapPrompt = 'p'
		return
	}
	hit := m.findHit(from, -1)
	if hit >= 0 {
		m.jumpToHit(hit)
		return
	}
	m.wrapPrompt = 'p'
}

// handleSearchInputKey processes a single key while the search input
// line is active. Returns the (possibly same) model — there are no
// commands to issue from this path.
func (m *model) handleSearchInputKey(msg tea.KeyMsg) tea.Model {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.searchInput = false
		m.searchQuery = ""
		m.searchRegex = false // don't leak regex mode into the next search
		return m
	case tea.KeyEnter:
		if m.commitSearch() {
			m.searchInput = false
		}
		return m
	case tea.KeyCtrlR:
		m.searchRegex = !m.searchRegex
		return m
	case tea.KeyBackspace, tea.KeyDelete:
		if n := len(m.searchQuery); n > 0 {
			// Trim by rune so multibyte chars don't leave dangling bytes.
			r := []rune(m.searchQuery)
			m.searchQuery = string(r[:len(r)-1])
		}
		return m
	case tea.KeyRunes, tea.KeySpace:
		if msg.Type == tea.KeySpace {
			m.searchQuery += " "
		} else {
			m.searchQuery += string(msg.Runes)
		}
		return m
	}
	return m
}

// handleWrapPromptKey answers the wrap-around prompt. y/Y wraps from the
// opposite end; any other key (n, Esc, or anything else) dismisses the prompt
// without moving, so a stray keypress can't leave it stuck on screen.
func (m *model) handleWrapPromptKey(msg tea.KeyMsg) tea.Model {
	dir := m.wrapPrompt
	// Any key dismisses the prompt; only y/Y also performs the wrap. This way
	// an accidental keypress clears the prompt instead of leaving it stuck.
	m.wrapPrompt = 0
	if s := msg.String(); s == "y" || s == "Y" {
		var hit int
		if dir == 'n' {
			hit = m.findHit(0, +1)
		} else {
			hit = m.findHit(len(m.lines)-1, -1)
		}
		if hit >= 0 {
			m.jumpToHit(hit)
		}
	}
	return m
}

// matchHaystack returns the searchable text of dl: block bodies are
// dim-styled, so ANSI is stripped first to keep matching consistent with the
// plain-text heads. Shared by findHit, filteredIndices, and hitColumn so the
// match surface can't drift between them.
func matchHaystack(dl displayLine) string {
	if dl.isBlock {
		return stripANSI(dl.body)
	}
	return dl.body
}

// findHit returns the absolute index of the next event matching
// m.matcher, walking from `start` in direction `dir` (+1 forward,
// -1 backward). Returns -1 if no match exists in that range.
//
// Only enabled groups are considered: a hit hidden behind a disabled
// group toggle would jump the viewport to nothing, which is worse than
// reporting "no match".
//
// Both heads and block (JSON/XML) lines are searched — the user sees
// them both in the stream so they should both be reachable.
func (m *model) findHit(start, dir int) int {
	if m.matcher == nil || len(m.lines) == 0 {
		return -1
	}
	if dir == 0 {
		dir = 1
	}
	if start < 0 {
		start = 0
	}
	if start >= len(m.lines) {
		start = len(m.lines) - 1
	}
	for i := start; i >= 0 && i < len(m.lines); i += dir {
		ev := m.lines[i]
		if !m.lineEnabled(ev) {
			continue
		}
		if m.matcher.Match(matchHaystack(ev)) {
			return i
		}
	}
	return -1
}

// jumpToHit positions the viewport on event index idx, exits tail mode, and
// pans horizontally so the match is visible. In filter mode it centers within
// the filtered list; otherwise it centers on the absolute index.
func (m *model) jumpToHit(idx int) {
	if idx < 0 || idx >= len(m.lines) {
		return
	}
	m.setSearchHitRow(idx)
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
			m.setStreamTopRow(fil[top])
		}
	} else {
		top := idx - rows/2
		if top < 0 {
			top = 0
		}
		if top > len(m.lines)-1 {
			top = len(m.lines) - 1
		}
		m.setStreamTopRow(top)
	}
	m.adjustHorizToHit(idx)
}

// hitColumn returns the on-screen column (visible rune offset) of the first
// occurrence of the search term on line idx, accounting for the
// "[group] file:" prefix on head lines (blocks have no prefix). Returns -1
// if the term is not present on that line.
func (m *model) hitColumn(idx int) int {
	if idx < 0 || idx >= len(m.lines) || m.matcher == nil {
		return -1
	}
	dl := m.lines[idx]
	body := matchHaystack(dl)
	bi, _, ok := m.matcher.Find(body)
	if !ok {
		return -1
	}
	col := dispWidth(body[:bi])
	if !dl.isBlock {
		if m.showGroup {
			col += dispWidth(dl.group) + 3 // "[" id "]" + space
		}
		if m.showFile {
			col += dispWidth(dl.file) + 2 // ": "
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
	body := matchHaystack(m.lines[idx])
	bs, be, ok := m.matcher.Find(body)
	if !ok {
		return
	}
	end := start + dispWidth(body[bs:be])
	if start < m.horizScroll || end > m.horizScroll+m.width {
		ns := start - hitMargin
		if ns < 0 {
			ns = 0
		}
		m.horizScroll = ns
	}
}

// highlightMatches wraps every occurrence of mt's pattern in body with the
// supplied style. Returns the styled string and the total visual width
// (unstyled display width, identical to the original body's width since ANSI
// is zero-width). A nil matcher or empty body is returned as-is.
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
		if sp[0] < prev { // overlapping/contained — keep slicing valid
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
