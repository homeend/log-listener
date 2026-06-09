package tui

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/render"
)

func TestDebugDumpTextFlagsBufferDuplicate(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	// Two entries with identical Raw → buffer duplicate.
	for i := 0; i < 2; i++ {
		m.appendEvent(render.Event{Group: "g", File: "/a.log", Raw: "same line",
			Rendered: []render.Part{{Type: "text", Value: "same line"}}})
	}
	m.diagDump = func() string { return "2026-06-09T20:00:00Z RELOAD rebuilt=false files=1 dirs=1\n" }

	dump := m.debugDumpText(time.Date(2026, 6, 9, 20, 0, 0, 0, time.UTC))
	for _, want := range []string{
		"DUP x2",    // buffer duplicate scan caught it
		"same line", // the offending content
		"recent watch/reload events",
		"RELOAD rebuilt=false", // the event ring is included
	} {
		if !strings.Contains(dump, want) {
			t.Fatalf("dump missing %q:\n%s", want, dump)
		}
	}
}

func TestDumpKeyWritesFile(t *testing.T) {
	dir := t.TempDir()
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m = m2.(*model)
	m.saveDir = dir
	m.appendEvent(render.Event{Group: "g", File: "/a.log", Raw: "x",
		Rendered: []render.Part{{Type: "text", Value: "x"}}})

	// ctrl+d dispatches the dump command.
	m3, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = m3.(*model)
	if cmd == nil {
		t.Fatal("ctrl+d should return a dump command")
	}
	msg := cmd() // run the write off-goroutine command synchronously
	res, ok := msg.(saveResultMsg)
	if !ok {
		t.Fatalf("dump cmd should yield saveResultMsg, got %T", msg)
	}
	if res.err != nil {
		t.Fatalf("dump write failed: %v", res.err)
	}
	if !strings.Contains(res.path, "debug-log-listener-") {
		t.Fatalf("dump path should be debug-log-listener-*, got %s", res.path)
	}
	data, err := os.ReadFile(res.path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "log-listener debug dump") {
		t.Fatalf("dump file missing header:\n%s", data)
	}
}
