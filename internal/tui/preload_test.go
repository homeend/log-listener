package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/homeend/log-listener/internal/preload"
	"github.com/homeend/log-listener/internal/render"
)

// TestCaptureRoundTripIsIdempotent is the centerpiece: save → import → save is a
// fixed point, and exception blocks re-flag after the round trip.
func TestCaptureRoundTripIsIdempotent(t *testing.T) {
	m := newModel(1000)
	m.appendEvent(render.Event{Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "starting up"}}})
	m.appendEvent(render.Event{Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "payload"}, {Type: "json", Value: map[string]any{"x": float64(1)}}}})
	m.appendEvent(render.Event{Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "java.lang.NullPointerException: boom"}}})
	m.appendEvent(render.Event{Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "\tat com.foo.Bar.baz(Bar.java:42)"}}})

	cap1 := m.snapshotScrollback()

	m2 := newModel(1000)
	for _, ev := range preload.ParseCapture(cap1) {
		m2.appendEvent(ev)
	}
	cap2 := m2.snapshotScrollback()

	if !reflect.DeepEqual(cap1, cap2) {
		t.Errorf("round trip not idempotent:\n cap1=%#v\n cap2=%#v", cap1, cap2)
	}

	m2.ensureBlocks()
	frameIdx := -1
	for i, ln := range cap2 {
		if strings.Contains(ln, "Bar.java:42") {
			frameIdx = i
		}
	}
	if frameIdx < 0 || !m2.inExceptionBlock(frameIdx) {
		t.Errorf("stack frame (idx %d) should be in an exception block after round trip", frameIdx)
	}
}

func TestInitialEventsSeedBuffer(t *testing.T) {
	m := newModel(100)
	for _, ev := range []render.Event{
		{Group: "g", File: "/a.log", Rendered: []render.Part{{Type: "text", Value: "SEEDED-A"}}},
		{Group: "g", File: "/a.log", Rendered: []render.Part{{Type: "text", Value: "SEEDED-B"}}},
	} {
		m.appendEvent(ev)
	}
	got := m.snapshotScrollback()
	if len(got) != 2 || !strings.Contains(got[0], "SEEDED-A") || !strings.Contains(got[1], "SEEDED-B") {
		t.Errorf("seeded buffer = %#v", got)
	}
}
