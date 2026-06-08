package tui

import (
	"testing"

	"github.com/homeend/log-listener/internal/render"
)

// The load-bearing invariant: the ID the buffer assigns is the same ID the TUI
// entry carries. Here we simulate the fan-out — buffer assigns, the event is
// pushed to the model with that ID — and assert 1:1 parity.
func TestTUIEntryIDsMatchBufferIDs(t *testing.T) {
	m := newModel(100)
	events := []render.Event{
		{Group: "g", File: "/a.log", Raw: "one",
			Rendered: []render.Part{{Type: "text", Value: "one"}}},
		{Group: "g", File: "/a.log", Raw: "trace",
			Rendered: []render.Part{{Type: "text", Value: "panic: x\n  at y"}}},
	}
	// The TUI now sources records from its (owned) buffer, which is the ID
	// authority; appendEvent routes through it. Assert the visible entries
	// carry the buffer-assigned IDs L0, L1, ...
	for _, ev := range events {
		m.appendEvent(ev)
	}
	ve := m.visibleEntries()
	if len(ve) != len(events) {
		t.Fatalf("entry count: %d", len(ve))
	}
	for i, e := range ve {
		want := "L" + itoa36(i)
		if e.ID != want {
			t.Errorf("entry %d id = %q, want %q", i, e.ID, want)
		}
	}
}

func itoa36(i int) string {
	const d = "0123456789abcdefghijklmnopqrstuvwxyz"
	if i < 36 {
		return string(d[i])
	}
	return itoa36(i/36) + string(d[i%36])
}
