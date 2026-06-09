package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

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

// snapshotSelection returns the visual selection's rows as plain export text
// (full prefixes, styling stripped) via plainExportLine, in display order.
// Visual mode guarantees len(m.lines) > 0 and a valid [lo, hi] from
// selectionBounds.
func (m *model) snapshotSelection() []string {
	lo, hi := m.selectionBounds()
	out := make([]string, 0, hi-lo+1)
	for i := lo; i <= hi; i++ {
		out = append(out, plainExportLine(m.lines[i]))
	}
	return out
}

// saveResultMsg reports the outcome of a background export write.
type saveResultMsg struct {
	path string
	n    int
	err  error
}

// saveCmd captures the already-computed export lines and writes them off the
// model goroutine, yielding a saveResultMsg. The snapshot is taken by the
// caller (in Update) because m.lines is owned by the model goroutine.
func (m *model) saveCmd(lines []string) tea.Cmd {
	dir := m.saveDir
	return func() tea.Msg {
		path, err := writeExport(dir, lines, time.Now())
		return saveResultMsg{path: path, n: len(lines), err: err}
	}
}

// writeExport writes lines to screen-log-listener-<timestamp>.txt in dir (the
// current working directory when dir == ""), never overwriting an existing
// file: on a name clash it appends -1, -2, … before the extension. Returns the
// final path. now is injected so the name is deterministic in tests.
func writeExport(dir string, lines []string, now time.Time) (string, error) {
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		dir = wd
	}
	base := "screen-log-listener-" + now.Format("20060102-150405")
	path := filepath.Join(dir, base+".txt")
	for i := 1; ; i++ {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			break
		}
		path = filepath.Join(dir, fmt.Sprintf("%s-%d.txt", base, i))
	}
	content := strings.Join(lines, "\n")
	if len(lines) > 0 {
		content += "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return path, nil
}
