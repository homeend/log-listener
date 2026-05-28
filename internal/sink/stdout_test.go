package sink

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"log-listener/internal/render"
)

func makeEvent(parts ...render.Part) render.Event {
	return render.Event{
		Ts:       time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC),
		File:     "/var/log/app.log",
		Group:    "d1",
		Raw:      "raw line",
		Renderer: "test",
		Rendered: parts,
	}
}

func TestStdoutPlain(t *testing.T) {
	var buf bytes.Buffer
	s := NewStdout(&buf, false)
	s.Emit(makeEvent(render.Part{Type: "text", Value: "hello world"}))
	got := buf.String()
	if got != "[d1] app.log: hello world\n" {
		t.Fatalf("unexpected: %q", got)
	}
}

func TestStdoutColor(t *testing.T) {
	var buf bytes.Buffer
	s := NewStdout(&buf, true)
	s.Emit(makeEvent(render.Part{Type: "text", Value: "hi"}))
	got := buf.String()
	if !strings.Contains(got, ansiCyan) || !strings.Contains(got, ansiReset) {
		t.Fatalf("color codes missing: %q", got)
	}
}

func TestStdoutJSONBlock(t *testing.T) {
	var buf bytes.Buffer
	s := NewStdout(&buf, false)
	s.Emit(makeEvent(
		render.Part{Type: "text", Value: "INFO\n"},
		render.Part{Type: "json", Value: map[string]interface{}{"k": "v"}},
	))
	got := buf.String()
	if !strings.Contains(got, "INFO\n") {
		t.Fatalf("text part missing: %q", got)
	}
	if !strings.Contains(got, `"k": "v"`) {
		t.Fatalf("pretty json missing: %q", got)
	}
	if strings.Count(got, "\n\n") > 0 {
		t.Fatalf("unexpected blank line between text and json: %q", got)
	}
}

func TestStdoutXMLBlock(t *testing.T) {
	var buf bytes.Buffer
	s := NewStdout(&buf, false)
	s.Emit(makeEvent(
		render.Part{Type: "text", Value: "RAW: "},
		render.Part{Type: "xml", Value: "<a>\n  <b>1</b>\n</a>"},
	))
	got := buf.String()
	if !strings.Contains(got, "<b>1</b>") {
		t.Fatalf("xml block missing: %q", got)
	}
}
