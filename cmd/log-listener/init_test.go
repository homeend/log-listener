package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"log-listener/internal/catalog"
	"log-listener/internal/config"
	"log-listener/internal/render"
)

// TestInitOutputLoadsAndBuildsPipeline is the end-to-end guard: a generated
// config must load through the real (strict) config.Load AND its catalog
// filter/renderer regexes must compile into a real render pipeline. This is the
// only test that compiles the bundled catalog's regex strings, so a bad regex
// or schema drift in catalog.yml fails here instead of at a user's runtime.
func TestInitOutputLoadsAndBuildsPipeline(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "log-listener.yml")
	var stdout, stderr bytes.Buffer
	// the whole jetbrains bundle + junie exercises every fragment and renderer
	code := runInit([]string{"jetbrains", "junie", "-o", out, "--offline"}, strings.NewReader(""), false, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("init exit %d: %s", code, stderr.String())
	}

	cfg, err := config.Load([]string{"--config", out}, time.Now())
	if err != nil {
		data, _ := os.ReadFile(out)
		t.Fatalf("generated config failed to load: %v\n---\n%s", err, data)
	}
	if len(cfg.Groups) == 0 {
		t.Fatal("generated config has no groups")
	}
	if _, err := render.NewPipeline(cfg.RendererSpecs, cfg.DropUnmatched); err != nil {
		t.Fatalf("generated renderers failed to compile: %v", err)
	}
}

func TestInitWritesFile(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "log-listener.yml")
	var stdout, stderr bytes.Buffer

	code := runInit([]string{"goland", "-o", out, "--offline"}, strings.NewReader(""), false, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d, stderr=%s", code, stderr.String())
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "id: goland") {
		t.Errorf("missing goland group:\n%s", data)
	}
	if !strings.Contains(string(data), "json-line") {
		t.Errorf("missing renderer:\n%s", data)
	}
}

func TestInitStdout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runInit([]string{"goland", "-o", "-", "--offline"}, strings.NewReader(""), false, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "directories:") {
		t.Errorf("stdout not YAML:\n%s", stdout.String())
	}
}

func TestInitUnknownApp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runInit([]string{"nope", "-o", "-", "--offline"}, strings.NewReader(""), false, &stdout, &stderr)
	if code == 0 {
		t.Fatal("expected non-zero exit for unknown app")
	}
	if !strings.Contains(stderr.String(), "nope") {
		t.Errorf("stderr should name the bad app: %s", stderr.String())
	}
}

func TestInitNoOverwriteWithoutForce(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "log-listener.yml")
	if err := os.WriteFile(out, []byte("directories: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	// non-interactive => no prompt => refuse without --force
	code := runInit([]string{"goland", "-o", out, "--offline"}, strings.NewReader(""), false, &stdout, &stderr)
	if code == 0 {
		t.Fatal("expected refusal to overwrite without --force")
	}
}

func TestInitInteractiveMerge(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "log-listener.yml")
	if err := os.WriteFile(out, []byte("directories:\n  - id: mine\n    paths: [/x]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	// interactive: reply "m" at the overwrite/merge/cancel prompt
	code := runInit([]string{"goland", "-o", out, "--offline"}, strings.NewReader("m\n"), true, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	s, _ := os.ReadFile(out)
	if !strings.Contains(string(s), "id: mine") || !strings.Contains(string(s), "id: goland") {
		t.Errorf("interactive merge dropped an entry:\n%s", s)
	}
	if !strings.Contains(stdout.String(), "[o]verwrite") {
		t.Errorf("expected the prompt to be shown; stdout=%s", stdout.String())
	}
}

func TestInitInteractiveCancel(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "log-listener.yml")
	orig := []byte("directories:\n  - id: mine\n    paths: [/x]\n")
	if err := os.WriteFile(out, orig, 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := runInit([]string{"goland", "-o", out, "--offline"}, strings.NewReader("c\n"), true, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	after, _ := os.ReadFile(out)
	if string(after) != string(orig) {
		t.Errorf("cancel should leave the file untouched; got:\n%s", after)
	}
}

func TestInitForceMerge(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "log-listener.yml")
	if err := os.WriteFile(out, []byte("directories:\n  - id: mine\n    paths: [/x]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := runInit([]string{"goland", "-o", out, "--offline", "--force", "--merge"}, strings.NewReader(""), false, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	data, _ := os.ReadFile(out)
	s := string(data)
	if !strings.Contains(s, "id: mine") || !strings.Contains(s, "id: goland") {
		t.Errorf("merge dropped an entry:\n%s", s)
	}
}

func TestInitForceOverwrite(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "log-listener.yml")
	if err := os.WriteFile(out, []byte("directories:\n  - id: mine\n    paths: [/x]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := runInit([]string{"goland", "-o", out, "--offline", "--force"}, strings.NewReader(""), false, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	s, _ := os.ReadFile(out)
	// overwrite (no --merge) replaces: the old "mine" group must be gone.
	if strings.Contains(string(s), "id: mine") {
		t.Errorf("overwrite should drop the existing entry:\n%s", s)
	}
	if !strings.Contains(string(s), "id: goland") {
		t.Errorf("overwrite missing new content:\n%s", s)
	}
}

func TestRunDispatchesInit(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"init", "goland", "-o", "-", "--offline"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "directories:") {
		t.Errorf("init not dispatched:\n%s", stdout.String())
	}
}

func TestInitList(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runInit([]string{"--list"}, strings.NewReader(""), false, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	s := stdout.String()
	if !strings.Contains(s, "goland") || !strings.Contains(s, "jetbrains") {
		t.Errorf("--list should show apps and bundles:\n%s", s)
	}
}

func TestPromptOverwriteMapsReplies(t *testing.T) {
	cases := map[string]string{
		"o\n": "overwrite", "overwrite\n": "overwrite",
		"m\n": "merge", "merge\n": "merge",
		"c\n": "cancel", "\n": "cancel", "x\n": "cancel",
	}
	for reply, want := range cases {
		var w bytes.Buffer
		if got := promptOverwrite(&w, strings.NewReader(reply), "x.yml"); got != want {
			t.Errorf("promptOverwrite(%q) = %q, want %q", reply, got, want)
		}
		if !strings.Contains(w.String(), "[o]verwrite") {
			t.Errorf("prompt text missing for reply %q: %q", reply, w.String())
		}
	}
}

func TestInitOnlineUsesFetcher(t *testing.T) {
	prev := initFetcher
	t.Cleanup(func() { initFetcher = prev })
	// Key the location to the host OS so the source resolves on every
	// platform (resolution is OS-aware; a linux-only fixture would emit
	// nothing on windows/darwin).
	initFetcher = func() catalog.Fetcher {
		return stubFetcher([]byte(fmt.Sprintf(`
version: 9999
defaults: {output: {color: true, drop_unmatched: false}, tui: {enabled: true, scrollback: 1}}
fragments: {}
renderers: {}
bundles: {}
apps:
  zzz-remote-only:
    use: []
    sources:
      - id: main
        filter: '\.log$'
        locations: [ { dir: { %s: '/var/log/zzz' } } ]
`, goruntime.GOOS)))
	}
	var stdout, stderr bytes.Buffer
	code := runInit([]string{"zzz-remote-only", "-o", "-", "--online"}, strings.NewReader(""), false, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "zzz-remote-only") {
		t.Errorf("remote app not resolved:\n%s", stdout.String())
	}
}

type stubFetcher []byte

func (s stubFetcher) Fetch() ([]byte, error) { return []byte(s), nil }
