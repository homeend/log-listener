package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/watch"
)

// debugDumpText assembles the on-demand diagnostic snapshot: the current view
// state, a duplicate-content scan of the shared buffer (the signature of the
// reload-duplication bug), a display-level duplicate scan of the visible lines,
// and the recent watch/reload event ring. Pressed live while a bug is on
// screen, it captures that exact moment — no startup flag needed.
func (m *model) debugDumpText(now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "log-listener debug dump — %s\n", now.Format(time.RFC3339))
	fmt.Fprintf(&b, "view: lines=%d window=%d tail=%v streamTop=%d lastGen=%d scrollback=%d wrap=%v filter=%v collapse=%v\n",
		len(m.lines), len(m.window), m.tailMode, m.streamTopRow(), m.lastGen, m.scrollback, m.wordWrap, m.filterMode, m.collapseMultiline)

	b.WriteString("\n== view modes & enable-state ==\n")
	b.WriteString(m.enableStateReport())

	b.WriteString("\n== shared buffer (duplicate scan) ==\n")
	if m.buf != nil {
		b.WriteString(m.buf.DuplicateReport())
	} else {
		b.WriteString("(no buffer)\n")
	}

	b.WriteString("\n== view lines (display duplicate scan) ==\n")
	b.WriteString(viewDuplicateReport(m.lines))

	b.WriteString("\n== tailer lag ==\n")
	b.WriteString(m.tailerLagReport())

	b.WriteString("\n== recent watch/reload events ==\n")
	if m.diagDump != nil {
		if ev := m.diagDump(); ev != "" {
			b.WriteString(ev)
		} else {
			b.WriteString("(no events recorded)\n")
		}
	} else {
		b.WriteString("(diagnostics not wired)\n")
	}
	return b.String()
}

// enableStateReport lists which groups/renderers are toggled off and how the
// disabled display lines are distributed — specifically the largest contiguous
// disabled run, the signature of the frozen-scroll bug (browse up-scroll stalls
// while crossing a run of hidden lines). collapseMultiline hiding continuation
// rows is the other disabled-line source.
func (m *model) enableStateReport() string {
	var sb strings.Builder

	offGroups := make([]string, 0)
	for _, gid := range m.groupOrder {
		if !m.groupEnabled[gid] {
			offGroups = append(offGroups, gid)
		}
	}
	fmt.Fprintf(&sb, "groups: %d total, %d off%s\n", len(m.groupOrder), len(offGroups), joinOff(offGroups))

	offRend := make([]string, 0)
	for i, name := range m.rendererOrder {
		if i < len(m.rendererEnabled) && !m.rendererEnabled[i] {
			offRend = append(offRend, name)
		}
	}
	fmt.Fprintf(&sb, "renderers: %d total, %d off%s\n", len(m.rendererOrder), len(offRend), joinOff(offRend))

	disabled, longest, runStart := 0, 0, -1
	cur, curStart := 0, 0
	for i, dl := range m.lines {
		if !m.lineEnabled(dl) {
			disabled++
			if cur == 0 {
				curStart = i
			}
			cur++
			if cur > longest {
				longest, runStart = cur, curStart
			}
		} else {
			cur = 0
		}
	}
	fmt.Fprintf(&sb, "display lines: %d total, %d disabled; longest contiguous disabled run = %d (starts at index %d)\n",
		len(m.lines), disabled, longest, runStart)
	return sb.String()
}

// tailerLagReport summarizes how far each tailer trails its file's end plus the
// pump-channel saturation — the concrete "how far behind, and is the pipeline
// backed up" evidence. Driven by the same lag source as the footer indicator.
func (m *model) tailerLagReport() string {
	if m.lag == nil {
		return "(lag diagnostics not wired)\n"
	}
	st := m.lag()
	var sb strings.Builder
	fmt.Fprintf(&sb, "events channel: %d/%d pending\n", st.Pending, st.PendingCap)
	fmt.Fprintf(&sb, "total lag = %d bytes across %d files\n", st.TotalBytes, len(st.Files))

	files := append([]watch.FileLag(nil), st.Files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Lag > files[j].Lag })
	nonzero := 0
	for _, f := range files {
		if f.Lag > 0 {
			nonzero++
		}
	}
	const topN = 20
	shown := 0
	for _, f := range files {
		if f.Lag == 0 {
			break // sorted desc — the remainder are all zero
		}
		if shown >= topN {
			fmt.Fprintf(&sb, "… and %d more file(s) with lag\n", nonzero-topN)
			break
		}
		fmt.Fprintf(&sb, "lag=%d pos=%d size=%d %s\n", f.Lag, f.Pos, f.Size, filepath.Base(f.Path))
		shown++
	}
	if shown == 0 {
		sb.WriteString("all tailers at EOF (no lag)\n")
	}
	return sb.String()
}

func joinOff(off []string) string {
	if len(off) == 0 {
		return ""
	}
	return " [" + strings.Join(off, ", ") + "]"
}

// viewDuplicateReport scans the rendered display lines for runs/repeats of the
// same content — the on-screen face of the duplication bug. Reports the first
// few repeated bodies with their counts.
func viewDuplicateReport(lines []displayLine) string {
	counts := map[string]int{}
	order := make([]string, 0, len(lines))
	for _, dl := range lines {
		body := stripANSI(dl.body)
		if _, seen := counts[body]; !seen {
			order = append(order, body)
		}
		counts[body]++
	}
	var sb strings.Builder
	dup := 0
	for _, body := range order {
		if counts[body] < 2 {
			continue
		}
		dup++
		if dup > 50 {
			continue
		}
		snippet := body
		if len(snippet) > 100 {
			snippet = snippet[:100] + "…"
		}
		fmt.Fprintf(&sb, "DUP x%d body=%q\n", counts[body], snippet)
	}
	if dup == 0 {
		sb.WriteString("no repeated display lines\n")
	} else {
		fmt.Fprintf(&sb, "distinct repeated display lines: %d\n", dup)
	}
	return sb.String()
}

// dumpCmd writes the debug snapshot off the model goroutine and reports the
// outcome as a saveResultMsg (reused so the footer flash path is shared).
func (m *model) dumpCmd(text string) tea.Cmd {
	dir := m.saveDir
	return func() tea.Msg {
		path, err := writeDump(dir, text, time.Now())
		return saveResultMsg{path: path, err: err, label: "debug dump → "}
	}
}

// writeDump writes the snapshot to debug-log-listener-<timestamp>.txt in dir
// (cwd when dir == ""), never overwriting (appends -1, -2, … on a clash).
func writeDump(dir, text string, now time.Time) (string, error) {
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		dir = wd
	}
	base := "debug-log-listener-" + now.Format("20060102-150405")
	path := filepath.Join(dir, base+".txt")
	for i := 1; ; i++ {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			break
		}
		path = filepath.Join(dir, fmt.Sprintf("%s-%d.txt", base, i))
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return "", err
	}
	return path, nil
}
