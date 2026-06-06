package keymap_test

import (
	"os"
	"testing"

	"log-listener/internal/keymap"
)

// TestDocsUpToDate fails if docs/KEYBINDINGS.md drifts from RenderMarkdownDoc.
// Regenerate with: ./build.sh keybindings-docs
func TestDocsUpToDate(t *testing.T) {
	const path = "../../docs/KEYBINDINGS.md"
	got := keymap.RenderMarkdownDoc()
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v (run `./build.sh keybindings-docs`)", path, err)
	}
	if string(want) != got {
		t.Errorf("%s is stale — run `./build.sh keybindings-docs`", path)
	}
}
