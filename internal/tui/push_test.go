package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/render"
)

// Push must never block the pump even when nothing drains the signal, and many
// pushes must coalesce into a single pending reconcile.
func TestPushNonBlockingCoalesces(t *testing.T) {
	a := &App{sig: make(chan struct{}, 1), stop: make(chan struct{})}
	for i := 0; i < 1000; i++ {
		a.Push(render.Event{}) // no drainer: each call must return immediately
	}
	if len(a.sig) != 1 {
		t.Fatalf("signals should coalesce to 1 pending, got %d", len(a.sig))
	}
}

// After the program is done, Push is a no-op (no signal, no panic).
func TestPushAfterDoneIsNoop(t *testing.T) {
	a := &App{sig: make(chan struct{}, 1), stop: make(chan struct{})}
	a.mu.Lock()
	a.done = true
	a.mu.Unlock()
	a.Push(render.Event{})
	if len(a.sig) != 0 {
		t.Fatalf("Push after done should not signal, got %d", len(a.sig))
	}
}

// A reconcileMsg (what the forwarder emits per coalesced batch) makes the model
// pick up buffer appends the pump made without an inline reconcile.
func TestReconcileMsgPicksUpBufferAppend(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	before := len(m.lines)

	// As the pump does: append to the shared buffer, no inline reconcile.
	m.buf.Append(render.Event{Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "hello"}}})
	if len(m.lines) != before {
		t.Fatalf("buffer append must not reconcile on its own; lines moved %d -> %d", before, len(m.lines))
	}

	m3, _ := m.Update(reconcileMsg{})
	m = m3.(*model)
	if len(m.lines) <= before {
		t.Fatalf("reconcileMsg should pick up the append; lines %d -> %d", before, len(m.lines))
	}
}
