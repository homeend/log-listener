package preload

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/homeend/log-listener/internal/config"
	"github.com/homeend/log-listener/internal/render"
)

func TestResolveModeByFilename(t *testing.T) {
	if ResolveMode(config.PreloadAuto, "/x/screen-log-listener-20260607.txt") != config.PreloadCapture {
		t.Error("screen-log-listener-* should auto-detect as capture")
	}
	if ResolveMode(config.PreloadAuto, "/x/app.log") != config.PreloadRaw {
		t.Error("ordinary file should auto-detect as raw")
	}
	if ResolveMode(config.PreloadRaw, "/x/screen-log-listener-x.txt") != config.PreloadRaw {
		t.Error("explicit raw must not be overridden")
	}
}

func TestParseCaptureHeadsAndContinuations(t *testing.T) {
	evs := ParseCapture([]string{
		"[g] a.log: hello",
		"[g] a.log:     at Foo(Bar.java:1)",
		"[h] b.log: msg",
		"{",
		"  \"x\": 1",
		"}",
	})
	if len(evs) != 3 {
		t.Fatalf("want 3 events, got %d: %+v", len(evs), evs)
	}
	if evs[0].Group != "g" || evs[0].File != "a.log" || evs[0].Rendered[0].Value.(string) != "hello" {
		t.Errorf("ev0 = %+v", evs[0])
	}
	if evs[1].Rendered[0].Value.(string) != "    at Foo(Bar.java:1)" {
		t.Errorf("ev1 body = %q", evs[1].Rendered[0].Value)
	}
	if got := evs[2].Rendered[0].Value.(string); got != "msg\n{\n  \"x\": 1\n}" {
		t.Errorf("ev2 folded text = %q", got)
	}
}

func TestParseCaptureBodyWithColonSpaceSurvives(t *testing.T) {
	evs := ParseCapture([]string{"[g] a.log: key: value"})
	if got := evs[0].Rendered[0].Value.(string); got != "key: value" {
		t.Errorf("body with ': ' = %q, want 'key: value'", got)
	}
}

func TestLoadCaptureSkipsPipeline(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "screen-log-listener-x.txt")
	if err := os.WriteFile(p, []byte("[g] a.log: hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	called := false
	rf := func(g, f, l string) (render.Event, bool) { called = true; return render.Event{}, true }
	evs, err := Load(config.PreloadSpec{Path: p}, config.PreloadCapture, rf)
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("capture mode must not call the pipeline")
	}
	if len(evs) != 1 || evs[0].Group != "g" {
		t.Errorf("evs = %+v", evs)
	}
}

func TestLoadRawUsesRenderFnAndSyntheticGroup(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "app.log")
	if err := os.WriteFile(p, []byte("line one\nline two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rf := func(g, f, l string) (render.Event, bool) {
		if g != "preload" || f != "app.log" {
			t.Errorf("group/file = %q/%q, want preload/app.log", g, f)
		}
		return render.Event{Group: g, File: f, Rendered: []render.Part{{Type: "text", Value: l}}}, true
	}
	evs, err := Load(config.PreloadSpec{Path: p}, config.PreloadRaw, rf)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 {
		t.Errorf("want 2 events, got %d", len(evs))
	}
}

func TestLoadMissingFileErrors(t *testing.T) {
	if _, err := Load(config.PreloadSpec{Path: "/nonexistent/x.log"}, config.PreloadRaw, nil); err == nil {
		t.Error("expected error for missing file")
	}
}
