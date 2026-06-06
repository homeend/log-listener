package keymap_test

import (
	"os"
	"strings"
	"testing"

	"log-listener/internal/keymap"
)

// TestDocsUpToDate fails if KEYBINDINGS.md drifts from RenderMarkdownDoc.
// Regenerate with: ./build.sh keybindings-docs
func TestDocsUpToDate(t *testing.T) {
	const path = "../../KEYBINDINGS.md"
	got := keymap.RenderMarkdownDoc()
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v (run `./build.sh keybindings-docs`)", path, err)
	}
	norm := func(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }
	if norm(string(want)) != norm(got) {
		t.Errorf("%s is stale — run `./build.sh keybindings-docs`", path)
	}
}
