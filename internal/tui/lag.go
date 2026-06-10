package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/render"
	"github.com/homeend/log-listener/internal/watch"
)

// lagPollEvery is how often the TUI re-samples tailer read-lag for the footer
// indicator. Polling stat-s every watched file, so this stays coarse — the
// indicator is a "how far behind" gauge, not a live counter.
const lagPollEvery = time.Second

// lagTickMsg fires on the lag-poll timer; catchUpResultMsg carries the outcome
// of a manual catch-up back onto the model goroutine.
type (
	lagTickMsg       struct{}
	catchUpResultMsg struct{ stat watch.SkipStat }
)

// lagTickCmd schedules the next lag poll. Returns nil when no lag source is
// wired (tests / non-watch standalone), which leaves the timer dormant.
func (m *model) lagTickCmd() tea.Cmd {
	if m.lag == nil {
		return nil
	}
	return tea.Tick(lagPollEvery, func(time.Time) tea.Msg { return lagTickMsg{} })
}

// pollLag samples the current total read-lag into m.lagBytes (0 when no source).
func (m *model) pollLag() {
	if m.lag == nil {
		return
	}
	m.lagBytes = m.lag().TotalBytes
}

// catchUpCmd runs the catch-up off the model goroutine (it blocks until the
// watcher's loop goroutine services the skip) and reports the result back.
func (m *model) catchUpCmd() tea.Cmd {
	cu := m.catchUp
	if cu == nil {
		return nil
	}
	return func() tea.Msg { return catchUpResultMsg{stat: cu()} }
}

// applyCatchUp injects a marker line recording the skip and re-sticks to tail,
// then refreshes the lag gauge. The marker flows through the normal buffer so
// it is scrollback-visible, copyable, and searchable like any other line.
func (m *model) applyCatchUp(st watch.SkipStat) {
	if st.Bytes > 0 {
		marker := fmt.Sprintf("⤓ skipped %s across %d file(s) to catch up to live",
			humanBytes(st.Bytes), st.Files)
		// Build a rendered text part (DecomposeLines reads Rendered, not Raw).
		m.appendEvent(render.Event{Rendered: []render.Part{{Type: "text", Value: marker}}})
		m.flash = "caught up: skipped " + humanBytes(st.Bytes)
	} else {
		m.flash = "already at live (nothing to skip)"
	}
	m.tailMode = true
	m.pollLag()
}

// humanBytes formats a byte count as a short human-readable string.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
