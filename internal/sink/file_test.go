package sink

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/homeend/log-listener/internal/render"
)

func TestOpenFileTruncates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "o.txt")
	if err := os.WriteFile(path, []byte("OLD CONTENT"), 0o644); err != nil {
		t.Fatal(err)
	}
	fs, err := OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected truncated empty file, got %q", got)
	}
}

func TestFileSinkMatchesStdout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "o.txt")
	fs, err := OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ev := makeEvent(render.Part{Type: "text", Value: "hello world"})
	fs.Emit(ev)
	if err := fs.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var buf strings.Builder
	NewStdout(&buf, false).Emit(ev)
	if string(got) != buf.String() {
		t.Fatalf("file %q != non-TTY stdout %q", got, buf.String())
	}
}

func TestFileSinkJSONBlockNoANSI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "o.txt")
	fs, err := OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ev := makeEvent(
		render.Part{Type: "text", Value: "evt"},
		render.Part{Type: "json", Value: map[string]any{"k": "v"}},
	)
	fs.Emit(ev)
	if err := fs.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, "[d1] app.log: evt\n") {
		t.Fatalf("text line missing: %q", s)
	}
	if !strings.Contains(s, `"k": "v"`) {
		t.Fatalf("json block missing: %q", s)
	}
	if strings.Contains(s, "\x1b[") {
		t.Fatalf("ANSI escape leaked into file: %q", s)
	}
}
