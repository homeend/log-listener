package tui

// plainExportLine renders one displayLine to plain (unstyled) export text.
// Head lines always carry the "[group] file: " prefix — even when the on-screen
// group/file columns are toggled off — because the export is a complete record,
// not a WYSIWYG screenshot. Continuation / JSON / XML block rows carry no prefix
// and keep their own indentation, with styling stripped.
func plainExportLine(dl displayLine) string {
	if dl.isBlock {
		return stripANSI(dl.body)
	}
	return "[" + dl.group + "] " + dl.file + ": " + stripANSI(dl.body)
}

// snapshotViewport returns the rows currently visible on screen as plain text —
// honoring browse position, group disable, collapse, and filter mode (via
// collectVisible), minus styling, plus full prefixes.
func (m *model) snapshotViewport() []string {
	idxs := m.collectVisible(m.contentHeight())
	out := make([]string, 0, len(idxs))
	for _, i := range idxs {
		out = append(out, plainExportLine(m.lines[i]))
	}
	return out
}

// snapshotScrollback returns the entire buffer as plain text, in order,
// ignoring transient view toggles (collapse/filter) and group enable/disable.
// "Save scrollback" means the whole buffer, period.
func (m *model) snapshotScrollback() []string {
	out := make([]string, 0, len(m.lines))
	for i := range m.lines {
		out = append(out, plainExportLine(m.lines[i]))
	}
	return out
}
