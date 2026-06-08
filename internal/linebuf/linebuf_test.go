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

func TestExceptionsMapsBlockToEntries(t *testing.T) {
	b := New(100, decomp)
	b.Append(ev("g", "/a.log", "panic: boom"))            // L0 (head)
	b.Append(ev("g", "/a.log", "goroutine 1 [running]:")) // L1 (continuation entry)
	b.Append(ev("g", "/a.log", "ordinary line"))          // L2
	exc := b.Exceptions()
	if len(exc) != 1 {
		t.Fatalf("want 1 exception block, got %d: %+v", len(exc), exc)
	}
	if exc[0].HeadID != "L0" {
		t.Errorf("head: %s", exc[0].HeadID)
	}
	if exc[0].Exception == nil || exc[0].Exception.Language != "go" {
		t.Errorf("language: %+v", exc[0].Exception)
	}
	if got := b.BlockOf("L0"); got == nil || got.HeadID != "L0" {
		t.Errorf("BlockOf(L0): %+v", got)
	}
}

func TestRerenderNoRaceWithEscapedPointer(t *testing.T) {
	b := New(100, decomp)
	for i := 0; i < 50; i++ {
		b.Append(ev("g", "/a.log", "line"))
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			// Simulate an MCP handler: grab an escaped *Entry, then read its
			// Lines without holding the buffer lock.
			if e, ok := b.Get("L0"); ok {
				_ = len(e.Lines)
				for _, ln := range e.Lines {
					_ = ln.Text
				}
			}
		}
	}()
	for i := 0; i < 200; i++ {
		b.Rerender(func(g, f, raw string) (render.Event, bool) {
			return render.Event{Group: g, File: f, Raw: raw,
				Rendered: []render.Part{{Type: "text", Value: "RE"}}}, true
		})
	}
	<-done
}

func TestViewportSlotRoundTrip(t *testing.T) {
	b := New(100, decomp)
	if _, _, attached := b.Viewport(); attached {
		t.Error("fresh buffer must report not-attached")
	}
	b.SetViewport("L0", "L5")
	from, to, attached := b.Viewport()
	if !attached || from != "L0" || to != "L5" {
		t.Fatalf("viewport = %q..%q attached=%v", from, to, attached)
	}
}

func TestViewportConcurrentAccess(t *testing.T) {
	b := New(100, decomp)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 2000; i++ {
			b.SetViewport("L0", "L1")
		}
		close(done)
	}()
	for i := 0; i < 2000; i++ {
		_, _, _ = b.Viewport()
	}
	<-done
}

func TestRerenderKeepsIDsChangesContent(t *testing.T) {
	b := New(100, decomp)
	b.Append(ev("g", "/a.log", "original"))
	b.Rerender(func(group, file, raw string) (render.Event, bool) {
		return render.Event{Group: group, File: file, Raw: raw,
			Rendered: []render.Part{{Type: "text", Value: "RE:" + raw}}}, true
	})
	e, ok := b.Get("L0")
	if !ok {
		t.Fatal("L0 must survive rerender")
	}
	if e.Lines[0].Text != "RE:original" {
		t.Errorf("content not re-rendered: %q", e.Lines[0].Text)
	}
	if e.Seq != 0 {
		t.Errorf("seq must be preserved: %d", e.Seq)
	}
}

func TestGenBumpsOnAppendAndRerender(t *testing.T) {
	b := New(10, func(ev render.Event) []Line { return []Line{{Text: ev.Raw}} })
	g0 := b.Gen()
	b.Append(render.Event{Raw: "a"})
	g1 := b.Gen()
	if g1 == g0 {
		t.Fatal("gen did not bump on Append")
	}
	b.Rerender(func(g, f, raw string) (render.Event, bool) {
		return render.Event{Raw: raw + "!"}, true
	})
	if b.Gen() == g1 {
		t.Fatal("gen did not bump on Rerender")
	}
}

func TestGenBumpsOnEviction(t *testing.T) {
	b := New(1, func(ev render.Event) []Line { return []Line{{Text: ev.Raw}} })
	b.Append(render.Event{Raw: "a"})
	g := b.Gen()
	b.Append(render.Event{Raw: "b"}) // evicts "a"
	if b.Gen() <= g {
		t.Fatalf("gen did not bump on eviction: %d <= %d", b.Gen(), g)
	}
	if snap, _ := b.Snapshot(0); len(snap) != 1 || snap[0].Raw != "b" {
		t.Fatalf("after eviction Snapshot = %v, want [b]", snapRaws(snap))
	}
}

func TestSnapshotReturnsLastLimitAndGen(t *testing.T) {
	b := New(100, func(ev render.Event) []Line { return []Line{{Text: ev.Raw}} })
	for _, s := range []string{"a", "b", "c", "d"} {
		b.Append(render.Event{Raw: s})
	}
	snap, gen := b.Snapshot(2)
	if gen != b.Gen() {
		t.Fatalf("snapshot gen %d != Gen() %d", gen, b.Gen())
	}
	if len(snap) != 2 || snap[0].Raw != "c" || snap[1].Raw != "d" {
		t.Fatalf("Snapshot(2) = %v, want [c d]", snapRaws(snap))
	}
	all, _ := b.Snapshot(0)
	if len(all) != 4 {
		t.Fatalf("Snapshot(0) len = %d, want 4 (all)", len(all))
	}
}

func snapRaws(es []*Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Raw
	}
	return out
}

func TestSnapshotIsStableAcrossConcurrentAppend(t *testing.T) {
	b := New(1000, func(ev render.Event) []Line { return []Line{{Text: ev.Raw}} })
	for i := 0; i < 50; i++ {
		b.Append(render.Event{Raw: "x"})
	}
	done := make(chan struct{})
	go func() {
		for i := 0; i < 500; i++ {
			b.Append(render.Event{Raw: "y"})
		}
		close(done)
	}()
	for i := 0; i < 500; i++ {
		snap, _ := b.Snapshot(100)
		for _, e := range snap {
			_ = e.Raw
			_ = e.Lines
		}
	}
	<-done
}
