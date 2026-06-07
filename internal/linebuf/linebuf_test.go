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

func TestRangeInclusiveAndOrderTolerant(t *testing.T) {
	b := New(100, decomp)
	for _, s := range []string{"a", "b", "c", "d"} {
		b.Append(ev("g", "/x.log", s))
	}
	got := b.Range("L1", "L3") // b,c,d
	if len(got) != 3 || got[0].Lines[0].Text != "b" || got[2].Lines[0].Text != "d" {
		t.Fatalf("range L1..L3: %+v", got)
	}
	rev := b.Range("L3", "L1") // same span, reversed args
	if len(rev) != 3 || rev[0].Lines[0].Text != "b" {
		t.Fatalf("reversed args should normalise: %+v", rev)
	}
}

func TestContextBounds(t *testing.T) {
	b := New(100, decomp)
	for _, s := range []string{"a", "b", "c", "d", "e"} {
		b.Append(ev("g", "/x.log", s))
	}
	got := b.Context("L2", 1, 1) // b,c,d
	if len(got) != 3 || got[0].Lines[0].Text != "b" || got[2].Lines[0].Text != "d" {
		t.Fatalf("context L2 ±1: %+v", got)
	}
}

func TestSearchSubstringAndRegexAndLimit(t *testing.T) {
	b := New(100, decomp)
	for _, s := range []string{"alpha", "beta", "gamma alpha", "delta"} {
		b.Append(ev("g", "/x.log", s))
	}
	hits, err := b.Search("alpha", false, 10)
	if err != nil || len(hits) != 2 {
		t.Fatalf("substring hits: %+v err=%v", hits, err)
	}
	if hits[0].ID != "L2" { // newest-first
		t.Errorf("want newest first (L2), got %s", hits[0].ID)
	}
	rx, err := b.Search("^a", true, 10)
	if err != nil || len(rx) != 1 || rx[0].ID != "L0" {
		t.Fatalf("regex hits: %+v err=%v", rx, err)
	}
	lim, _ := b.Search("a", false, 1)
	if len(lim) != 1 {
		t.Errorf("limit not honoured: %d", len(lim))
	}
}

func TestRecentPagination(t *testing.T) {
	b := New(100, decomp)
	for _, s := range []string{"a", "b", "c", "d"} {
		b.Append(ev("g", "/x.log", s))
	}
	got := b.Recent(2, 0) // last 2, chronological: c,d
	if len(got) != 2 || got[0].Lines[0].Text != "c" || got[1].Lines[0].Text != "d" {
		t.Fatalf("recent(2,0): %+v", got)
	}
}
