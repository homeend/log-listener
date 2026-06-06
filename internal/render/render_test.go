package render

import (
	"strings"
	"testing"
	"time"

	"github.com/homeend/log-listener/internal/config"
)

func TestParseTemplateBasic(t *testing.T) {
	tpl, err := ParseTemplate(`$1 $2\njson($3)`)
	if err != nil {
		t.Fatal(err)
	}
	parts, _ := tpl.Execute([]string{"FULL", "2026-05-28", "ERROR", `{"u":"bob"}`})
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
	parts, _ := tpl.Execute([]string{"_", "X"})
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
	parts, _ := tpl.Execute([]string{"_", `<a><b>1</b></a>`})
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

func TestJSONRendererInvalidReportsNotOK(t *testing.T) {
	tpl, _ := ParseTemplate(`json($1)`)
	if _, ok := tpl.Execute([]string{"_", "not-json"}); ok {
		t.Fatal("unparseable JSON must make Execute report ok=false")
	}
}

func TestXMLRendererInvalidReportsNotOK(t *testing.T) {
	tpl, _ := ParseTemplate(`xml($1)`)
	if _, ok := tpl.Execute([]string{"_", "<broken"}); ok {
		t.Fatal("unparseable XML must make Execute report ok=false")
	}
}

func TestCaptureOutOfRange(t *testing.T) {
	tpl, _ := ParseTemplate(`pre-$5-post`)
	parts, _ := tpl.Execute([]string{"only", "one"})
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

func TestMuteDropsLine(t *testing.T) {
	matchers := map[string]config.MatcherSpec{"health": {LineRegex: "GET /health"}}
	mutes := []config.MuteSpec{{ID: "h", Matcher: "health"}}
	p, err := NewPipeline(nil, matchers, mutes, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.Render(time.Time{}, "g", "/f", "GET /health 200"); ok {
		t.Fatal("muted line should be dropped (ok=false)")
	}
	if _, ok := p.Render(time.Time{}, "g", "/f", "GET /api 200"); !ok {
		t.Fatal("non-muted line should pass through")
	}
}

func TestMuteInlineFields(t *testing.T) {
	mutes := []config.MuteSpec{{ID: "dbg", MatcherSpec: config.MatcherSpec{Line: "DEBUG"}}}
	p, _ := NewPipeline(nil, nil, mutes, false)
	if _, ok := p.Render(time.Time{}, "g", "/f", "DEBUG"); ok {
		t.Fatal("inline mute should drop exact line")
	}
	if _, ok := p.Render(time.Time{}, "g", "/f", "DEBUG: x"); !ok {
		t.Fatal("exact-literal mute must not drop substring")
	}
}

func TestMuteAppliesToScopesByGroup(t *testing.T) {
	mutes := []config.MuteSpec{{
		ID:          "dbg",
		MatcherSpec: config.MatcherSpec{LineRegex: "DEBUG"},
		AppliesTo:   &config.AppliesToSpec{Groups: []string{"app"}},
	}}
	p, _ := NewPipeline(nil, nil, mutes, false)
	if _, ok := p.Render(time.Time{}, "app", "/f", "DEBUG x"); ok {
		t.Fatal("DEBUG in group app should be muted")
	}
	if _, ok := p.Render(time.Time{}, "other", "/f", "DEBUG x"); !ok {
		t.Fatal("DEBUG outside group app should NOT be muted")
	}
}

func TestMuteAppliesToScopesByPathGlob(t *testing.T) {
	mutes := []config.MuteSpec{{
		ID:          "noise",
		MatcherSpec: config.MatcherSpec{LineRegex: "DEBUG"},
		AppliesTo:   &config.AppliesToSpec{Paths: []string{"*.app.log"}},
	}}
	p, _ := NewPipeline(nil, nil, mutes, false)
	// Glob is tried against the basename, so a full path under any dir matches.
	if _, ok := p.Render(time.Time{}, "g", "/var/log/x.app.log", "DEBUG x"); ok {
		t.Fatal("DEBUG in *.app.log should be muted")
	}
	if _, ok := p.Render(time.Time{}, "g", "/var/log/other.txt", "DEBUG x"); !ok {
		t.Fatal("DEBUG outside the path glob should NOT be muted")
	}
}

func TestMutePrecedesDropUnmatched(t *testing.T) {
	mutes := []config.MuteSpec{{ID: "h", MatcherSpec: config.MatcherSpec{LineRegex: "X"}}}
	p, _ := NewPipeline(nil, nil, mutes, true)
	if _, ok := p.Render(time.Time{}, "g", "/f", "X"); ok {
		t.Fatal("muted line dropped")
	}
}

func TestMuteRequiresExactlyOneOfRefOrInline(t *testing.T) {
	both := []config.MuteSpec{{ID: "x", Matcher: "m", MatcherSpec: config.MatcherSpec{Line: "y"}}}
	if _, err := NewPipeline(nil, map[string]config.MatcherSpec{"m": {Line: "z"}}, both, false); err == nil {
		t.Fatal("expected error: both ref and inline set")
	}
	neither := []config.MuteSpec{{ID: "x"}}
	if _, err := NewPipeline(nil, nil, neither, false); err == nil {
		t.Fatal("expected error: neither ref nor inline set")
	}
}

func TestMuteUnknownMatcherRef(t *testing.T) {
	mutes := []config.MuteSpec{{ID: "x", Matcher: "ghost"}}
	if _, err := NewPipeline(nil, nil, mutes, false); err == nil {
		t.Fatal("expected error for unknown matcher reference")
	}
}

func TestPipelineRendererFallsThroughOnUnparseableJSON(t *testing.T) {
	specs := []config.RendererSpec{
		{Name: "trailing-json", LineRegex: `^(.*?\s)(\{.+\})\s*$`, Template: `$1\njson($2)`},
	}
	p, err := NewPipeline(specs, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	ev, ok := p.Render(time.Now(), "g", "/idea.log", "msg: {DB=C:\\x}")
	if !ok {
		t.Fatal("non-drop mode must still emit the raw line")
	}
	if ev.Renderer != "" {
		t.Fatalf("renderer should fall through on unparseable JSON, got %q", ev.Renderer)
	}
	if len(ev.Rendered) != 1 || ev.Rendered[0].Type != "text" ||
		ev.Rendered[0].Value.(string) != "msg: {DB=C:\\x}" {
		t.Fatalf("expected raw passthrough, got %+v", ev.Rendered)
	}
	ev, ok = p.Render(time.Now(), "g", "/idea.log", `msg: {"a":1}`)
	if !ok || ev.Renderer != "trailing-json" {
		t.Fatalf("valid JSON should render, got ok=%v renderer=%q", ok, ev.Renderer)
	}
}

func TestPipelineUnparseableJSONDroppedWhenDropUnmatched(t *testing.T) {
	specs := []config.RendererSpec{
		{Name: "trailing-json", LineRegex: `^(.*?\s)(\{.+\})\s*$`, Template: `$1\njson($2)`},
	}
	p, _ := NewPipeline(specs, nil, nil, true)
	if _, ok := p.Render(time.Now(), "g", "/idea.log", "msg: {DB=x}"); ok {
		t.Fatal("regex-matched but unparseable render-call must drop under drop_unmatched")
	}
}
