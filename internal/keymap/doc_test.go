package keymap

import (
	"strings"
	"testing"
)

func TestRenderMarkdownDocContents(t *testing.T) {
	doc := RenderMarkdownDoc()
	for _, want := range []string{
		"# Keybindings",
		"| Action |",
		"Quit",
		"⌃C", // mac glyph column
		"Ctrl+C", // linux column
		"shift+down", // verification caveat mentions the mac fast-scroll key
		"keybindings:", // override section
		"do not edit", // generated banner
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("RenderMarkdownDoc missing %q", want)
		}
	}
}
