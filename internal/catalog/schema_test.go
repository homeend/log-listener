package catalog

import "testing"

func TestParseMinimalCatalog(t *testing.T) {
	src := []byte(`
version: 3
defaults:
  output: { color: true, drop_unmatched: false }
  tui: { enabled: true, scrollback: 20000 }
fragments:
  jetbrains-base:
    sources:
      - id: main
        filter: 'idea\.log$'
        locations:
          - dir: { linux: '~/.cache/JetBrains/{product}*/log' }
apps:
  goland:
    renderers: [json-line]
    use:
      - { frag: jetbrains-base, product: GoLand }
    sources:
      - id: acp
        filter: '\.log$'
        locations:
          - dir: { linux: '~/.cache/JetBrains/GoLand*/log/acp' }
renderers:
  json-line: { line_regex: '^\s*(\{.*\})\s*$', template: 'json($1)' }
bundles:
  jetbrains: [goland]
`)
	c, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Version != 3 {
		t.Errorf("Version = %d, want 3", c.Version)
	}
	if got := c.Fragments["jetbrains-base"].Sources[0].Locations[0].Dir["linux"]; got != "~/.cache/JetBrains/{product}*/log" {
		t.Errorf("fragment dir = %q", got)
	}
	app := c.Apps["goland"]
	if len(app.Use) != 1 || app.Use[0].Frag != "jetbrains-base" || app.Use[0].Product != "GoLand" {
		t.Errorf("app.Use = %+v", app.Use)
	}
	if app.Sources[0].ID != "acp" {
		t.Errorf("app inline source = %+v", app.Sources)
	}
	if c.Renderers["json-line"].Template != "json($1)" {
		t.Errorf("renderer = %+v", c.Renderers["json-line"])
	}
	if got := c.Bundles["jetbrains"]; len(got) != 1 || got[0] != "goland" {
		t.Errorf("bundle = %v", got)
	}
	if !c.Defaults.TUI.Enabled || c.Defaults.TUI.Scrollback != 20000 {
		t.Errorf("defaults.tui = %+v", c.Defaults.TUI)
	}
	if !c.Defaults.Output.Color || c.Defaults.Output.DropUnmatched {
		t.Errorf("defaults.output = %+v", c.Defaults.Output)
	}
}

func TestParseRejectsUnknownKey(t *testing.T) {
	_, err := Parse([]byte("version: 1\nbogus_key: true\n"))
	if err == nil {
		t.Fatal("expected error for unknown key, got nil")
	}
}
