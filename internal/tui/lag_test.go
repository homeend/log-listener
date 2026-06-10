package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/watch"
)

func TestApplyCatchUpInjectsMarker(t *testing.T) {
	m := seedSearch(t, "one", "two")
	m.tailMode = false
	before := len(m.lines)

	m.applyCatchUp(watch.SkipStat{Files: 2, Bytes: 8_300_000})

	if len(m.lines) <= before {
		t.Fatalf("expected a marker line appended; lines %d -> %d", before, len(m.lines))
	}
	last := stripANSI(m.lines[len(m.lines)-1].body)
	if !strings.Contains(last, "skipped") || !strings.Contains(last, "catch up to live") {
		t.Fatalf("marker text unexpected: %q", last)
	}
	if !m.tailMode {
		t.Fatal("catch-up should re-stick to tail")
	}
}

func TestApplyCatchUpNoSkip(t *testing.T) {
	m := seedSearch(t, "one")
	before := len(m.lines)

	m.applyCatchUp(watch.SkipStat{}) // nothing was behind

	if len(m.lines) != before {
		t.Fatalf("no-skip must not append a marker; lines %d -> %d", before, len(m.lines))
	}
	if !strings.Contains(m.flash, "already at live") {
		t.Fatalf("flash: %q", m.flash)
	}
}

func TestCompactStatusShowsLag(t *testing.T) {
	m := seedSearch(t, "x")

	m.lagBytes = 0
	if strings.Contains(m.compactStatus(), "behind") {
		t.Fatal("no indicator expected at zero lag")
	}

	// Below the floor: a transient KB gap on healthy tailing must stay hidden.
	m.lagBytes = lagIndicatorFloor - 1
	if strings.Contains(m.compactStatus(), "behind") {
		t.Fatalf("sub-floor lag should not show the indicator: %q", m.compactStatus())
	}

	m.lagBytes = 8_300_000
	s := m.compactStatus()
	if !strings.Contains(s, "behind") || !strings.Contains(s, "MB") {
		t.Fatalf("expected lag indicator with MB, got %q", s)
	}
}

// End-to-end: pressing the catch-up key dispatches the action, runs the
// returned command (which calls the watcher stub), and the result message
// injects the marker — the whole UX path, not just applyCatchUp in isolation.
func TestCatchUpKeyInjectsMarker(t *testing.T) {
	m := seedSearch(t, "one", "two")
	m.catchUp = func() watch.SkipStat { return watch.SkipStat{Files: 1, Bytes: 5_000_000} }
	before := len(m.lines)

	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	m = m2.(*model)
	if cmd == nil {
		t.Fatal("catch-up key produced no command")
	}
	// Execute the off-loop command, then feed its message back like bubbletea.
	m3, _ := m.Update(cmd())
	m = m3.(*model)

	if len(m.lines) <= before {
		t.Fatalf("marker not injected via keypress: %d -> %d", before, len(m.lines))
	}
	last := stripANSI(m.lines[len(m.lines)-1].body)
	if !strings.Contains(last, "catch up to live") {
		t.Fatalf("marker text unexpected: %q", last)
	}
}

func TestDebugDumpHasTailerLagSection(t *testing.T) {
	m := seedSearch(t, "x")
	m.lag = func() watch.LagStat {
		return watch.LagStat{
			TotalBytes: 4096,
			Pending:    7,
			PendingCap: 1024,
			Files: []watch.FileLag{
				{Path: "/logs/big.log", Pos: 100, Size: 4196, Lag: 4096},
				{Path: "/logs/idle.log", Pos: 50, Size: 50, Lag: 0},
			},
		}
	}

	out := m.debugDumpText(time.Now())
	if !strings.Contains(out, "== tailer lag ==") {
		t.Fatal("missing tailer lag section")
	}
	if !strings.Contains(out, "events channel: 7/1024 pending") {
		t.Fatalf("missing channel-saturation line:\n%s", out)
	}
	if !strings.Contains(out, "big.log") {
		t.Fatal("missing top lagging file")
	}
	if strings.Contains(out, "idle.log") {
		t.Fatal("zero-lag file should be omitted from the report")
	}
}

func TestTailerLagReportNotWired(t *testing.T) {
	m := seedSearch(t, "x")
	m.lag = nil
	if !strings.Contains(m.tailerLagReport(), "not wired") {
		t.Fatal("nil lag source should report 'not wired'")
	}
}
