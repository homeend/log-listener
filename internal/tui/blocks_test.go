package tui

import (
	"testing"

	"github.com/homeend/log-listener/internal/render"
)

func TestEnsureBlocksRecomputesAfterAppend(t *testing.T) {
	m := newModel(100)
	m.appendEvent(render.Event{Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "panic: boom"}}})
	m.appendEvent(render.Event{Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "goroutine 1 [running]:"}}})
	m.ensureBlocks()
	if len(m.blocks) != 1 {
		t.Fatalf("want 1 block, got %d", len(m.blocks))
	}
	if m.blocks[0].Exception == nil || m.blocks[0].Exception.Language != "go" {
		t.Errorf("block not flagged go: %+v", m.blocks[0])
	}
}

func TestAppendSetsBlocksDirty(t *testing.T) {
	m := newModel(100)
	m.ensureBlocks() // clean
	m.appendEvent(render.Event{Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "x"}}})
	if !m.blocksDirty {
		t.Error("appendEvent must set blocksDirty")
	}
}

func TestInExceptionBlock(t *testing.T) {
	m := newModel(100)
	m.appendEvent(render.Event{Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "Traceback (most recent call last):"}}})
	m.appendEvent(render.Event{Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "  File \"a.py\", line 1, in <module>"}}})
	m.ensureBlocks()
	if !m.inExceptionBlock(0) || !m.inExceptionBlock(1) {
		t.Errorf("both rows of a python traceback should be in an exception block")
	}
}
