package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/keymap"
)

// helpRow is one line of the help overlay: the resolved key display for an
// action plus its title and description.
type helpRow struct {
	keys, title, desc string
}

// helpRows builds the help list from keymap.AllActions in order, resolving keys
// for the current OS via the same Display the KEYBINDINGS.md doc uses. When
// helpQuery is set, only rows whose keys+title+desc contain it (case-insensitive)
// are kept.
func (m *model) helpRows() []helpRow {
	q := strings.ToLower(m.helpQuery)
	out := make([]helpRow, 0, len(keymap.AllActions))
	for _, d := range keymap.AllActions {
		r := helpRow{
			keys:  m.resolvedKM().Display(d.Action),
			title: d.Title,
			desc:  d.Desc,
		}
		if q != "" {
			hay := strings.ToLower(r.keys + " " + r.title + " " + r.desc)
			if !strings.Contains(hay, q) {
				continue
			}
		}
		out = append(out, r)
	}
	return out
}

// renderHelp draws the modal help overlay into rows display lines, mirroring the
// files overlay: a header, the filtered rows windowed by helpScroll, then blank
// fill. The header echoes the active filter.
func (m *model) renderHelp(rows int) string {
	all := m.helpRows()

	avail := rows - 1
	if avail < 1 {
		avail = 1
	}
	if m.helpScroll > len(all)-avail {
		m.helpScroll = len(all) - avail
	}
	if m.helpScroll < 0 {
		m.helpScroll = 0
	}

	out := make([]string, 0, rows)
	title := " Help — type to filter · j/k scroll · " +
		m.keyDisplay(keymap.ActionHelp) + "/" + m.keyDisplay(keymap.ActionCloseOverlay) + " close "
	if m.helpQuery != "" {
		title = " Help — /" + m.helpQuery + " "
	}
	out = append(out, headerBg.Width(m.width).MaxHeight(1).Render(title))

	if len(all) == 0 {
		out = append(out, m.padRow(dimStyle.Render("  (no matching keys)")))
		for i := 2; i < rows; i++ {
			out = append(out, m.blankRow())
		}
		return strings.Join(out, "\n")
	}

	start := m.helpScroll
	end := start + avail
	if end > len(all) {
		end = len(all)
	}
	for i := start; i < end; i++ {
		r := all[i]
		out = append(out, m.padRow(fmt.Sprintf("  %-22s  %-28s  %s",
			r.keys, r.title, dimStyle.Render(r.desc))))
	}
	for i := end - start; i < avail; i++ {
		out = append(out, m.blankRow())
	}
	return strings.Join(out, "\n")
}

// handleHelpKey processes keys while the help overlay is open. It is fully
// modal: esc/? close, j/k/arrows scroll, backspace trims the filter, and any
// other printable rune extends the filter. Everything else is ignored.
func (m *model) handleHelpKey(msg tea.KeyMsg) *model {
	switch msg.String() {
	case "esc", "?":
		m.showHelp = false
		m.helpQuery = ""
		return m
	case "up", "k":
		m.helpScroll--
		if m.helpScroll < 0 {
			m.helpScroll = 0
		}
		return m
	case "down", "j":
		m.helpScroll++
		return m
	case "backspace":
		if r := []rune(m.helpQuery); len(r) > 0 {
			m.helpQuery = string(r[:len(r)-1])
			m.helpScroll = 0
		}
		return m
	}
	if msg.Type == tea.KeyRunes && len(msg.Runes) == 1 {
		m.helpQuery += string(msg.Runes)
		m.helpScroll = 0
	}
	return m
}
