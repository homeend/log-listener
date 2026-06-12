package catalog

import (
	"strings"
	"testing"
)

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
  json-line: { line_regex: '^\s*(\{.*\})\s*$', template: '$json($1)' }
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
	if c.Renderers["json-line"].Template != "$json($1)" {
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

func TestParseFileLocation(t *testing.T) {
	c, err := Parse([]byte(`
version: 1
fragments:
  junie-logs:
    sources:
      - id: main
        locations:
          - file: { linux: '~/.junie/logs/agent.log' }
          - file: { linux: '~/.junie-local/logs/agent.log' }
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	locs := c.Fragments["junie-logs"].Sources[0].Locations
	if got := locs[0].File["linux"]; got != "~/.junie/logs/agent.log" {
		t.Errorf("file location = %q", got)
	}
	if locs[0].Dir != nil {
		t.Errorf("dir should be unset on a file location: %+v", locs[0].Dir)
	}
}

func TestParseRejectsLocationWithBothDirAndFile(t *testing.T) {
	_, err := Parse([]byte(`
version: 1
fragments:
  bad:
    sources:
      - id: main
        locations:
          - dir: { linux: '~/logs' }
            file: { linux: '~/logs/app.log' }
`))
	if err == nil {
		t.Fatal("expected error for location with both dir and file")
	} else if !strings.Contains(err.Error(), "both dir and file") {
		t.Fatalf("error should name the violated rule: %v", err)
	}
}

func TestParseRejectsLocationWithNeither(t *testing.T) {
	_, err := Parse([]byte(`
version: 1
fragments:
  bad:
    sources:
      - id: main
        locations:
          - {}
`))
	if err == nil {
		t.Fatal("expected error for location with neither dir nor file")
	} else if !strings.Contains(err.Error(), "neither dir nor file") {
		t.Fatalf("error should name the violated rule: %v", err)
	}
}

func TestParseRejectsMixedModeSource(t *testing.T) {
	_, err := Parse([]byte(`
version: 1
apps:
  bad:
    sources:
      - id: main
        locations:
          - dir: { linux: '~/logs' }
          - file: { linux: '~/logs/app.log' }
`))
	if err == nil {
		t.Fatal("expected error for source mixing dir and file locations")
	} else if !strings.Contains(err.Error(), "mixes dir and file") {
		t.Fatalf("error should name the violated rule: %v", err)
	}
}

func TestParseRejectsFilterOnFileSource(t *testing.T) {
	_, err := Parse([]byte(`
version: 1
apps:
  bad:
    sources:
      - id: main
        filter: '\.log$'
        locations:
          - file: { linux: '~/logs/app.log' }
`))
	if err == nil {
		t.Fatal("expected error for filter on a file-based source")
	} else if !strings.Contains(err.Error(), "filter is not allowed") {
		t.Fatalf("error should name the violated rule: %v", err)
	}
}

// TestParseLenientSkipsValidation pins the strict-vs-lenient split: the remote
// catalog (parseLenient) must stay usable even when it violates authoring
// rules a newer binary's validation would reject, mirroring how unknown keys
// are tolerated there.
func TestParseLenientSkipsValidation(t *testing.T) {
	_, err := parseLenient([]byte(`
version: 1
fragments:
  remote:
    sources:
      - id: main
        locations:
          - dir: { linux: '~/logs' }
            file: { linux: '~/logs/app.log' }
`))
	if err != nil {
		t.Fatalf("parseLenient must not validate: %v", err)
	}
}
