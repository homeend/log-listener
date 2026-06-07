package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRunOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"-d", dir, "--once"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code %d, stderr=%q", code, stderr.String())
	}
	out := stdout.String()
	if want := "[default] a.log: alpha\n"; !contains(out, want) {
		t.Fatalf("missing %q in:\n%s", want, out)
	}
	if want := "[default] a.log: beta\n"; !contains(out, want) {
		t.Fatalf("missing %q in:\n%s", want, out)
	}
}

func TestRunReportsParseErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--bogus"}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("want non-zero exit")
	}
	if stderr.Len() == 0 {
		t.Fatal("stderr empty")
	}
}

func TestPathUnderAny(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if !pathUnderAny(filepath.Join(dir, "a.log"), []string{dir}, false) {
		t.Fatal("direct child should match non-recursive")
	}
	if pathUnderAny(filepath.Join(sub, "a.log"), []string{dir}, false) {
		t.Fatal("nested file must not match non-recursive")
	}
	if !pathUnderAny(filepath.Join(sub, "a.log"), []string{dir}, true) {
		t.Fatal("nested file should match recursive")
	}
	if pathUnderAny("/var/log/foo", []string{dir}, true) {
		t.Fatal("unrelated path must not match")
	}
}

func contains(s, sub string) bool {
	return bytes.Contains([]byte(s), []byte(sub))
}

func TestLoadRuntime(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "app.log")
	if err := os.WriteFile(logPath, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	yml := "files:\n  - id: app\n    paths: [" + strconv.Quote(logPath) + "]\n" +
		"renderers:\n  - name: r1\n    line_regex: \".*\"\n    template: \"$0\"\n"
	cfgPath := filepath.Join(dir, "log-listener.yml")
	if err := os.WriteFile(cfgPath, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}

	rt, err := loadRuntime([]string{"--config", cfgPath}, false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if rt.pipeline.RendererCount() != 1 {
		t.Fatalf("RendererCount = %d, want 1", rt.pipeline.RendererCount())
	}
	if len(rt.assignments) != 1 || rt.assignments[0].Path != logPath {
		t.Fatalf("assignments = %+v, want one for %s", rt.assignments, logPath)
	}
	if rt.cfg.SourcePath != cfgPath {
		t.Fatalf("SourcePath = %q, want %q", rt.cfg.SourcePath, cfgPath)
	}
}

func TestLoadRuntimeForwardsDropUnmatched(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "app.log")
	if err := os.WriteFile(logPath, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// No renderers → every line is "unmatched", so drop behavior is observable.
	yml := "files:\n  - id: app\n    paths: [" + strconv.Quote(logPath) + "]\n"
	cfgPath := filepath.Join(dir, "log-listener.yml")
	if err := os.WriteFile(cfgPath, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}

	withDrop, err := loadRuntime([]string{"--config", cfgPath}, true, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := withDrop.pipeline.Render(time.Now(), "app", logPath, "x"); ok {
		t.Fatal("dropUnmatched=true should drop an unmatched line (want ok=false)")
	}

	withoutDrop, err := loadRuntime([]string{"--config", cfgPath}, false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := withoutDrop.pipeline.Render(time.Now(), "app", logPath, "x"); !ok {
		t.Fatal("dropUnmatched=false should emit an unmatched line as-is (want ok=true)")
	}
}

func TestRunOnceWithRenderer(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "a.log")
	if err := os.WriteFile(logPath, []byte(`2026-05-28 ERROR {"u":"bob"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	yml := filepath.Join(dir, "log.yml")
	cfg := `
directories:
  - id: default
    paths: [` + dir + `]
renderers:
  - name: app-json
    line_regex: '(\d{4}-\d{2}-\d{2}) (\w+) (\{.*\})'
    template: '$1 $2\njson($3)'
`
	if err := os.WriteFile(yml, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"--once", "--config", yml}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	if !contains(out, "[default] a.log: 2026-05-28 ERROR\n") {
		t.Fatalf("text part missing in:\n%s", out)
	}
	if !contains(out, `"u": "bob"`) {
		t.Fatalf("pretty-printed JSON missing in:\n%s", out)
	}
}

func TestKeybindingsDocFlagPrintsAndExitsZero(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"--keybindings-doc"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "# Keybindings") {
		t.Errorf("doc not printed; got: %q", out.String())
	}
}

func TestBadKeybindingExitsNonZero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log-listener.yml")
	yml := "files:\n  - id: a\n    paths: [\"/tmp/a.log\"]\nkeybindings:\n  default:\n    clear: [\"n\"]\n"
	if err := os.WriteFile(path, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := run([]string{"--no-tui", "--once", "--config", path}, &out, &errb)
	if code == 0 {
		t.Fatalf("expected non-zero exit for colliding keybinding")
	}
	if !strings.Contains(errb.String(), "clear") {
		t.Errorf("stderr should explain the collision; got %q", errb.String())
	}
}

func TestRunOnceWritesOutputFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.log"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Output file lives in a SEPARATE temp dir so it isn't itself discovered
	// and tailed by -d.
	out := filepath.Join(t.TempDir(), "capture.txt")
	if err := os.WriteFile(out, []byte("STALE\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"-d", dir, "--once", "--no-color", "-o", out}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d, stderr=%q", code, stderr.String())
	}

	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if strings.Contains(got, "STALE") {
		t.Fatalf("output file was not truncated: %q", got)
	}
	for _, want := range []string{"app.log: hello", "app.log: world"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in output file: %q", want, got)
		}
	}
	if strings.Contains(got, "\x1b[") {
		t.Fatalf("ANSI escape leaked into output file: %q", got)
	}
}
