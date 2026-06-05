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
	r, err := Compile(spec, nil)
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
	r, err := Compile(config.RendererSpec{Name: "g", LineRegex: `.`, Template: ``}, nil)
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
	p, err := NewPipeline(specs, nil, nil, false)
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
	}, nil, nil, true)
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
	p, _ := NewPipeline(nil, nil, nil, false)
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
	tpl, _ := ParseTemplate(`pre-$5-post`)
	parts := tpl.Execute([]string{"only", "one"})
	if len(parts) != 1 || parts[0].Type != "text" {
		t.Fatalf("expected single text part: %+v", parts)
	}
	if parts[0].Value.(string) != "pre--post" {
		t.Fatalf("out-of-range capture should expand to empty, got %q", parts[0].Value)
	}
}

func TestPipelineRendererScopedByAppliesTo(t *testing.T) {
	specs := []config.RendererSpec{
		{
			Name: "d1-only", LineRegex: `.*`, Template: `[d1]`,
			AppliesTo: &config.AppliesTo{Groups: []string{"d1"}},
		},
		{
			Name: "app-files-only", LineRegex: `.*`, Template: `[app]`,
			AppliesTo: &config.AppliesTo{Paths: []string{"*.app.log"}},
		},
		{Name: "fallback", LineRegex: `.*`, Template: `[any]`},
	}
	p, err := NewPipeline(specs, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		group, path, wantRenderer string
	}{
		{"d1", "/var/log/other.log", "d1-only"},      // group match wins
		{"d2", "/var/log/x.app.log", "app-files-only"}, // path match wins
		{"d2", "/var/log/other.log", "fallback"},     // neither
	}
	for _, tc := range cases {
		t.Run(tc.group+"-"+tc.path, func(t *testing.T) {
			ev, _ := p.Render(time.Now(), tc.group, tc.path, "anything")
			if ev.Renderer != tc.wantRenderer {
				t.Fatalf("got %q want %q", ev.Renderer, tc.wantRenderer)
			}
		})
	}
}

func TestPipelineSetRendererEnabledFallsToNextMatch(t *testing.T) {
	specs := []config.RendererSpec{
		{Name: "first", LineRegex: `^X`, Template: `[first]`},
		{Name: "second", LineRegex: `^X`, Template: `[second]`},
	}
	p, err := NewPipeline(specs, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	ev, _ := p.Render(time.Now(), "d", "/x", "X line")
	if ev.Renderer != "first" {
		t.Fatalf("baseline: renderer=%q want first", ev.Renderer)
	}

	p.SetRendererEnabled(0, false)
	if p.IsEnabled(0) {
		t.Fatal("renderer 0 should be off after SetRendererEnabled(0, false)")
	}
	ev2, _ := p.Render(time.Now(), "d", "/x", "X line")
	if ev2.Renderer != "second" {
		t.Fatalf("after disabling first: renderer=%q want second", ev2.Renderer)
	}

	// Re-enable — first wins again.
	p.SetRendererEnabled(0, true)
	ev3, _ := p.Render(time.Now(), "d", "/x", "X line")
	if ev3.Renderer != "first" {
		t.Fatalf("after re-enabling: renderer=%q want first", ev3.Renderer)
	}
}

func TestPipelineDisabledFallsThroughToRaw(t *testing.T) {
	p, err := NewPipeline([]config.RendererSpec{
		{Name: "only", LineRegex: `.*`, Template: `[styled]`},
	}, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	p.SetRendererEnabled(0, false)
	ev, ok := p.Render(time.Now(), "d", "/x", "hello")
	if !ok {
		t.Fatal("non-drop pipeline must not drop a disabled-renderer line")
	}
	if ev.Renderer != "" {
		t.Fatalf("renderer name should be empty on raw fallthrough, got %q", ev.Renderer)
	}
	if len(ev.Rendered) != 1 || ev.Rendered[0].Value != "hello" {
		t.Fatalf("expected raw text part, got %+v", ev.Rendered)
	}
}

func TestPipelineStartOffHonored(t *testing.T) {
	p, _ := NewPipeline([]config.RendererSpec{
		{Name: "sleeping", LineRegex: `.*`, Template: `[s]`, StartOff: true},
	}, nil, nil, false)
	if p.IsEnabled(0) {
		t.Fatal("StartOff=true must initialize renderer disabled")
	}
	ev, _ := p.Render(time.Now(), "d", "/x", "hello")
	if ev.Renderer != "" {
		t.Fatalf("disabled renderer should not run; got %q", ev.Renderer)
	}
}

func TestPipelineRendererAccessors(t *testing.T) {
	p, _ := NewPipeline([]config.RendererSpec{
		{Name: "a", LineRegex: `.*`, Template: `$0`},
		{Name: "b", LineRegex: `.*`, Template: `$0`},
	}, nil, nil, false)
	if p.RendererCount() != 2 {
		t.Fatalf("count=%d want 2", p.RendererCount())
	}
	if p.RendererName(0) != "a" || p.RendererName(1) != "b" {
		t.Fatalf("names: %q %q", p.RendererName(0), p.RendererName(1))
	}
	// Out-of-range stays silent.
	p.SetRendererEnabled(99, false)
	if p.IsEnabled(99) {
		t.Fatal("out-of-range IsEnabled must return false")
	}
	if p.RendererName(99) != "" {
		t.Fatal("out-of-range name must be empty")
	}
}

func TestRendererViaMatcherCaptures(t *testing.T) {
	matchers := map[string]config.MatcherSpec{
		"json-on-idea": {Name: "idea.log", LineRegex: `^\s*(\{.*\})\s*$`},
	}
	specs := []config.RendererSpec{
		{Name: "idea-json", Matcher: "json-on-idea", Template: "json($1)"},
	}
	p, err := NewPipeline(specs, matchers, nil, false)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	ev, ok := p.Render(time.Time{}, "g", "/var/log/idea.log", `{"a":1}`)
	if !ok || ev.Renderer != "idea-json" {
		t.Fatalf("expected idea-json render, got ok=%v renderer=%q", ok, ev.Renderer)
	}
	ev, ok = p.Render(time.Time{}, "g", "/var/log/other.log", `{"a":1}`)
	if !ok || ev.Renderer != "" {
		t.Fatalf("expected raw passthrough for other.log, got renderer=%q", ev.Renderer)
	}
}

func TestRendererMatcherWithoutLineRegexIsError(t *testing.T) {
	matchers := map[string]config.MatcherSpec{"nameonly": {Name: "idea.log"}}
	specs := []config.RendererSpec{{Name: "r", Matcher: "nameonly", Template: "x"}}
	if _, err := NewPipeline(specs, matchers, nil, false); err == nil {
		t.Fatal("expected error: matcher used by renderer has no line_regex")
	}
}

func TestRendererRequiresExactlyOneOfLineRegexOrMatcher(t *testing.T) {
	both := []config.RendererSpec{{Name: "r", LineRegex: "x", Matcher: "m", Template: "t"}}
	if _, err := NewPipeline(both, map[string]config.MatcherSpec{"m": {LineRegex: "y"}}, nil, false); err == nil {
		t.Fatal("expected error when both line_regex and matcher set")
	}
	neither := []config.RendererSpec{{Name: "r", Template: "t"}}
	if _, err := NewPipeline(neither, nil, nil, false); err == nil {
		t.Fatal("expected error when neither line_regex nor matcher set")
	}
}

func TestRendererUnknownMatcherRef(t *testing.T) {
	specs := []config.RendererSpec{{Name: "r", Matcher: "ghost", Template: "t"}}
	if _, err := NewPipeline(specs, nil, nil, false); err == nil {
		t.Fatal("expected error for unknown matcher reference")
	}
}
