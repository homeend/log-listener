package linebuf

import (
	"testing"

	"github.com/homeend/log-listener/internal/render"
)

// decomp is a test decomposer mirroring render.DecomposeLines via the adapter.
func decomp(ev render.Event) []Line {
	out := make([]Line, 0)
	for _, r := range render.DecomposeLines(ev) {
		out = append(out, Line{Text: r.Text, IsCont: r.IsCont})
	}
	return out
}

func ev(group, file, text string) render.Event {
	return render.Event{Group: group, File: file, Raw: text,
		Rendered: []render.Part{{Type: "text", Value: text}}}
}

func TestAppendAssignsSequentialIDs(t *testing.T) {
	b := New(100, decomp)
	id0 := b.Append(ev("g", "/a.log", "one"))
	id1 := b.Append(ev("g", "/a.log", "two"))
	if id0 != "L0" || id1 != "L1" {
		t.Fatalf("ids: %q %q", id0, id1)
	}
	e, ok := b.Get("L1")
	if !ok || e.Lines[0].Text != "two" {
		t.Fatalf("get L1: %+v ok=%v", e, ok)
	}
}

func TestAppendEvictsOldest(t *testing.T) {
	b := New(2, decomp)
	b.Append(ev("g", "/a.log", "one"))
	b.Append(ev("g", "/a.log", "two"))
	b.Append(ev("g", "/a.log", "three"))
	if _, ok := b.Get("L0"); ok {
		t.Error("L0 should have been evicted")
	}
	if _, ok := b.Get("L2"); !ok {
		t.Error("L2 should be present")
	}
}
