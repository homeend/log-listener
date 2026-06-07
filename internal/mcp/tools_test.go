package mcp

import (
	"context"
	"testing"

	"github.com/homeend/log-listener/internal/render"
)

func seed(s *Server, texts ...string) {
	for _, txt := range texts {
		s.buf.Append(render.Event{Group: "g", File: "/a.log", Raw: txt,
			Rendered: []render.Part{{Type: "text", Value: txt}}})
	}
}

func TestGetLineTool(t *testing.T) {
	s := New("127.0.0.1:0", newTestBuf())
	seed(s, "one", "two")
	_, out, err := s.getLine(context.Background(), nil, GetLineInput{ID: "L1"})
	if err != nil {
		t.Fatal(err)
	}
	if out.ID != "L1" || len(out.Lines) != 1 || out.Lines[0] != "two" {
		t.Fatalf("get_line: %+v", out)
	}
	if _, _, err := s.getLine(context.Background(), nil, GetLineInput{ID: "L99"}); err == nil {
		t.Error("unknown id should error")
	}
}

func TestGetRangeTool(t *testing.T) {
	s := New("127.0.0.1:0", newTestBuf())
	seed(s, "a", "b", "c", "d")
	_, out, err := s.getRange(context.Background(), nil, GetRangeInput{From: "L1", To: "L3"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Entries) != 3 || out.Entries[0].Lines[0] != "b" {
		t.Fatalf("get_range: %+v", out)
	}
}

func TestGetContextTool(t *testing.T) {
	s := New("127.0.0.1:0", newTestBuf())
	seed(s, "a", "b", "c", "d", "e")
	_, out, err := s.getContext(context.Background(), nil,
		GetContextInput{ID: "L2", Before: 1, After: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Entries) != 3 || out.Entries[0].Lines[0] != "b" {
		t.Fatalf("get_context: %+v", out)
	}
}

func TestGetScrollbackPaginates(t *testing.T) {
	s := New("127.0.0.1:0", newTestBuf())
	seed(s, "a", "b", "c", "d")
	_, out, err := s.getScrollback(context.Background(), nil,
		GetScrollbackInput{Limit: 2, Offset: 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Entries) != 2 || out.Entries[1].Lines[0] != "d" {
		t.Fatalf("get_scrollback: %+v", out)
	}
}

func TestSearchTool(t *testing.T) {
	s := New("127.0.0.1:0", newTestBuf())
	seed(s, "alpha", "beta", "gamma alpha")
	_, out, err := s.search(context.Background(), nil,
		SearchInput{Query: "alpha", Regex: false, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Hits) != 2 || out.Hits[0].ID != "L2" {
		t.Fatalf("search: %+v", out)
	}
}

func TestListExceptionsTool(t *testing.T) {
	s := New("127.0.0.1:0", newTestBuf())
	seed(s, "panic: boom", "goroutine 1 [running]:", "normal")
	_, out, err := s.listExceptions(context.Background(), nil, EmptyInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Exceptions) != 1 || out.Exceptions[0].From != "L0" ||
		out.Exceptions[0].Language != "go" {
		t.Fatalf("list_exceptions: %+v", out)
	}
}

func TestGetViewportAttached(t *testing.T) {
	s := New("127.0.0.1:0", newTestBuf())
	seed(s, "a", "b", "c")
	s.buf.SetViewport("L0", "L2")
	_, out, err := s.getViewport(context.Background(), nil, EmptyInput{})
	if err != nil {
		t.Fatal(err)
	}
	if out.From != "L0" || out.To != "L2" || len(out.Entries) != 3 {
		t.Fatalf("get_viewport: %+v", out)
	}
}

func TestGetViewportErrorsWhenNoTUI(t *testing.T) {
	s := New("127.0.0.1:0", newTestBuf())
	seed(s, "a", "b")
	if _, _, err := s.getViewport(context.Background(), nil, EmptyInput{}); err == nil {
		t.Error("get_viewport must error when no TUI has attached")
	}
}
