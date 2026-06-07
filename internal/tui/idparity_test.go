package tui

import (
	"testing"

	"github.com/homeend/log-listener/internal/linebuf"
	"github.com/homeend/log-listener/internal/render"
)

// The load-bearing invariant: the ID the buffer assigns is the same ID the TUI
// entry carries. Here we simulate the fan-out — buffer assigns, the event is
// pushed to the model with that ID — and assert 1:1 parity.
func TestTUIEntryIDsMatchBufferIDs(t *testing.T) {
	decomp := func(ev render.Event) []linebuf.Line {
		out := make([]linebuf.Line, 0)
		for _, r := range render.DecomposeLines(ev) {
			out = append(out, linebuf.Line{Text: r.Text, IsCont: r.IsCont})
		}
		return out
	}
	buf := linebuf.New(100, decomp)
	m := newModel(100)
	events := []render.Event{
		{Group: "g", File: "/a.log", Raw: "one",
			Rendered: []render.Part{{Type: "text", Value: "one"}}},
		{Group: "g", File: "/a.log", Raw: "trace",
			Rendered: []render.Part{{Type: "text", Value: "panic: x\n  at y"}}},
	}
	for _, ev := range events {
		ev.ID = buf.Append(ev) // fan-out: buffer is the authority
		m.appendEvent(ev)
	}
	if len(m.entries) != len(events) {
		t.Fatalf("entry count: %d", len(m.entries))
	}
	for i, e := range m.entries {
		want := "L" + itoa36(i)
		if e.id != want {
			t.Errorf("entry %d id = %q, want %q", i, e.id, want)
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
