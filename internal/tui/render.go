package tui

import (
	"path/filepath"
	"strings"

	"github.com/homeend/log-listener/internal/render"
)

// decomposeEvent splits one render.Event into the per-line display rows
// used by the model. Delegates to render.DecomposeLines for the plain-text
// splitting logic (shared with the MCP buffer), then applies TUI styling.
// The styled prefix is NOT baked in here so column toggles take effect
// without rebuilding the cache.
func decomposeEvent(ev render.Event) []displayLine {
	base := filepath.Base(ev.File)
	rows := render.DecomposeLines(ev)
	out := make([]displayLine, 0, len(rows))
	for _, r := range rows {
		body := r.Text
		if r.IsCont {
			body = dimStyle.Render(r.Text)
		}
		out = append(out, displayLine{
			group:     ev.Group,
			file:      base,
			body:      body,
			bodyWidth: dispWidth(r.Text),
			isBlock:   r.IsCont,
		})
	}
	return out
}

// renderDisplayLine assembles one terminal row from a displayLine using
// the model's current column toggles. Block lines never carry a prefix.
// Returns the styled string AND its visual width (runes) so clipLine can
// pad to terminal width without re-stripping ANSI.
//
// This variant takes no event index — it cannot apply the "current hit"
// background — and is used by the `$` widest-line walk and tests.
func (m *model) renderDisplayLine(dl displayLine) (string, int) {
	return m.renderDisplayLineCore(dl, false)
}

// renderDisplayLineAt is the on-screen variant that knows the line's
// absolute index, so it can apply the active-hit background when the
// row holds the current search hit and append the "[...]" suffix when
// collapsed-multiline mode is hiding continuation rows after this one.
// Falls through to the plain core otherwise.
func (m *model) renderDisplayLineAt(idx int) (string, int) {
	dl := m.lines[idx]
	isCurrent := m.matcher != nil && idx == m.searchHit
	if m.collapseMultiline && idx+1 < len(m.lines) && isContinuation(m.lines[idx+1]) {
		// Mutate the local copy so the marker shows on this row only.
		// dimStyle wraps the marker in ANSI; runeLen on the unstyled
		// text yields the correct visible width.
		const marker = " [...]"
		dl.body = dl.body + dimStyle.Render(marker)
		dl.bodyWidth += dispWidth(marker)
	}
	return m.renderDisplayLineCore(dl, isCurrent)
}

func (m *model) renderDisplayLineCore(dl displayLine, isCurrent bool) (string, int) {
	body := dl.body
	bodyWidth := dl.bodyWidth
	// When a search term is active, swap out the body for one with
	// highlighted matches. Block lines carry pre-styled ANSI so we
	// strip first; head lines are plain text already.
	if m.matcher != nil {
		plain := body
		if dl.isBlock {
			plain = stripANSI(body)
		}
		style := matchStyle.Render
		if isCurrent {
			style = currentMatchStyle.Render
		}
		newBody, newW := highlightMatches(plain, m.matcher, style)
		if newW != bodyWidth || newBody != plain {
			body = newBody
			bodyWidth = newW
		} else if dl.isBlock {
			// No match in a block: keep the original dim styling.
			body = dl.body
		}
	}
	if dl.isBlock {
		return body, bodyWidth
	}
	var sb strings.Builder
	visW := bodyWidth
	if m.showGroup {
		sb.WriteString(groupStyle.Render("[" + dl.group + "]"))
		sb.WriteByte(' ')
		visW += dispWidth(dl.group) + 3 // "[" + id + "]" + " "
	}
	if m.showFile {
		sb.WriteString(fileStyle.Render(dl.file))
		sb.WriteString(": ")
		visW += dispWidth(dl.file) + 2 // ": "
	}
	sb.WriteString(body)
	return sb.String(), visW
}

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
	if m.matcher == nil {
		return nil
	}
	var out []int
	off := 0
	for _, e := range m.visibleEntries() {
		dls := m.displayCache[e.ID]
		n := len(dls)
		matched := false
		for _, dl := range dls {
			if m.matcher.Match(matchHaystack(dl)) {
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

// isContinuation reports whether dl looks like a follow-on row of a
// multiline log entry — either a JSON/XML pretty-print block row, or
// a line whose body starts with whitespace (the convention Python
// tracebacks and many other multi-line log formats use). Empty bodies
// don't count.
func isContinuation(dl displayLine) bool {
	if dl.isBlock {
		return true
	}
	if len(dl.body) == 0 {
		return false
	}
	first := dl.body[0]
	return first == ' ' || first == '\t'
}
