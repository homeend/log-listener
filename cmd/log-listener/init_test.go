package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitWritesFile(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "log-listener.yml")
	var stdout, stderr bytes.Buffer

	code := runInit([]string{"goland", "-o", out, "--offline"}, &stdout, &stderr)
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
	code := runInit([]string{"goland", "-o", "-", "--offline"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "directories:") {
		t.Errorf("stdout not YAML:\n%s", stdout.String())
	}
}

func TestInitUnknownApp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runInit([]string{"nope", "-o", "-", "--offline"}, &stdout, &stderr)
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
	// non-TTY buffers => no prompt => refuse without --force
	code := runInit([]string{"goland", "-o", out, "--offline"}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("expected refusal to overwrite without --force")
	}
}

func TestInitForceMerge(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "log-listener.yml")
	if err := os.WriteFile(out, []byte("directories:\n  - id: mine\n    paths: [/x]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := runInit([]string{"goland", "-o", out, "--offline", "--force", "--merge"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	data, _ := os.ReadFile(out)
	s := string(data)
	if !strings.Contains(s, "id: mine") || !strings.Contains(s, "id: goland") {
		t.Errorf("merge dropped an entry:\n%s", s)
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
