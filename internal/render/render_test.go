package render

import (
	"strings"
	"testing"
	"time"

	"log-listener/internal/config"
)

func TestParseTemplateBasic(t *testing.T) {
	tpl, err := ParseTemplate(`$1 $2\njson($3)`)
	if err != nil {
		t.Fatal(err)
	}
	parts := tpl.Execute([]string{"FULL", "2026-05-28", "ERROR", `{"u":"bob"}`})
	if len(parts) != 2 {
		t.Fatalf("want 2 parts, got %d: %+v", len(parts), parts)
	}
	if parts[0].Type != "text" || parts[0].Value.(string) != "2026-05-28 ERROR\n" {
		t.Fatalf("part 0: %+v", parts[0])
	}
	if parts[1].Type != "json" {
		t.Fatalf("part 1 type: %+v", parts[1])
	}
	m := parts[1].Value.(map[string]interface{})
	if m["u"].(string) != "bob" {
		t.Fatalf("parsed json: %+v", m)
	}
}

func TestParseTemplateEscapes(t *testing.T) {
	tpl, _ := ParseTemplate(`pre\\$1\tlit$$end`)
	parts := tpl.Execute([]string{"_", "X"})
	if len(parts) != 1 || parts[0].Type != "text" {
		t.Fatalf("parts: %+v", parts)
	}
	want := "pre\\X\tlit$end"
	if parts[0].Value.(string) != want {
		t.Fatalf("got %q want %q", parts[0].Value, want)
	}
}

func TestParseTemplateXMLCall(t *testing.T) {
	tpl, err := ParseTemplate(`xml($1)`)
	if err != nil {
		t.Fatal(err)
	}
	parts := tpl.Execute([]string{"_", `<a><b>1</b></a>`})
	if len(parts) != 1 || parts[0].Type != "xml" {
		t.Fatalf("parts: %+v", parts)
	}
	pretty := parts[0].Value.(string)
	if !strings.Contains(pretty, "<b>1</b>") {
		t.Fatalf("xml output: %s", pretty)
	}
}

func TestParseTemplateInvalidEscape(t *testing.T) {
	cases := []string{
		`$a`,           // $ followed by non-digit, non-$
		`json($)`,      // empty digit
		`json($1`,      // missing )
		`xml($`,        // unfinished
	}
	for _, c := range cases {
		_, err := ParseTemplate(c)
		if err == nil {
			t.Errorf("expected error for %q", c)
		}
	}
}

func TestRendererAppliesTo(t *testing.T) {
	spec := config.RendererSpec{
		Name:      "r",
		LineRegex: `(.*)`,
		Template:  `$1`,
		AppliesTo: &config.AppliesTo{
			Groups: []string{"d1"},
			Paths:  []string{"*.app.log"},
		},
	}
	r, err := Compile(spec)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		group, path string
		want        bool
	}{
		{"d1", "/var/log/x.app.log", true},
		{"d2", "/var/log/x.app.log", false}, // wrong group
		{"d1", "/var/log/x.log", false},     // wrong path
		{"d1", "x.app.log", true},           // basename match
	}
	for _, tc := range cases {
		if got := r.Applies(tc.group, tc.path); got != tc.want {
			t.Errorf("Applies(%q,%q)=%v want %v", tc.group, tc.path, got, tc.want)
		}
	}
}

func TestRendererAppliesToEmptyMeansGlobal(t *testing.T) {
	r, err := Compile(config.RendererSpec{Name: "g", LineRegex: `.`, Template: ``})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Applies("anything", "/anywhere") {
		t.Fatal("renderer with no applies_to must be global")
	}
}

func TestPipelineFirstMatchWins(t *testing.T) {
	specs := []config.RendererSpec{
		{Name: "first", LineRegex: `^ERROR`, Template: `[err]`},
		{Name: "second", LineRegex: `.*`, Template: `[any]`},
	}
	p, err := NewPipeline(specs, false)
	if err != nil {
		t.Fatal(err)
	}
	ev, ok := p.Render(time.Now(), "d", "/x", "ERROR boom")
	if !ok {
		t.Fatal("dropped")
	}
	if ev.Renderer != "first" {
		t.Fatalf("renderer=%q want first", ev.Renderer)
	}
	ev2, _ := p.Render(time.Now(), "d", "/x", "info ok")
	if ev2.Renderer != "second" {
		t.Fatalf("second-pass: renderer=%q", ev2.Renderer)
	}
}

func TestPipelineDropUnmatched(t *testing.T) {
	p, err := NewPipeline([]config.RendererSpec{
		{Name: "r", LineRegex: `^ERROR`, Template: `$0`},
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	_, ok := p.Render(time.Now(), "d", "/x", "info")
	if ok {
		t.Fatal("unmatched line should be dropped")
	}
	ev, ok := p.Render(time.Now(), "d", "/x", "ERROR boom")
	if !ok {
		t.Fatal("matched line should not be dropped")
	}
	if ev.Renderer != "r" {
		t.Fatalf("renderer=%q", ev.Renderer)
	}
}

func TestPipelineUnmatchedFallsThroughAsText(t *testing.T) {
	p, _ := NewPipeline(nil, false)
	ev, ok := p.Render(time.Now(), "d", "/x", "hello")
	if !ok {
		t.Fatal("non-drop mode: unmatched must still emit event")
	}
	if ev.Renderer != "" {
		t.Fatalf("Renderer should be empty for unmatched: %q", ev.Renderer)
	}
	if len(ev.Rendered) != 1 || ev.Rendered[0].Type != "text" || ev.Rendered[0].Value.(string) != "hello" {
		t.Fatalf("rendered: %+v", ev.Rendered)
	}
}

func TestJSONRendererInvalidFallsBackToText(t *testing.T) {
	tpl, _ := ParseTemplate(`json($1)`)
	parts := tpl.Execute([]string{"_", "not-json"})
	if len(parts) != 1 || parts[0].Type != "text" {
		t.Fatalf("invalid json must fall back to text: %+v", parts)
	}
}

func TestXMLRendererInvalidFallsBackToText(t *testing.T) {
	tpl, _ := ParseTemplate(`xml($1)`)
	parts := tpl.Execute([]string{"_", "<broken"})
	if len(parts) != 1 || parts[0].Type != "text" {
		t.Fatalf("invalid xml must fall back to text: %+v", parts)
	}
}

func TestCaptureOutOfRange(t *testing.T) {
	tpl, _ := ParseTemplate(`$5`)
	parts := tpl.Execute([]string{"only", "one"})
	if len(parts) != 0 && parts[0].Value.(string) != "" {
		t.Fatalf("out-of-range capture should expand to empty: %+v", parts)
	}
}
