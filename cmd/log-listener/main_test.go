package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
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
