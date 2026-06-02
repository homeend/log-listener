package catalog

import (
	"testing"
)

func testCatalog(t *testing.T) *Catalog {
	t.Helper()
	c, err := Parse([]byte(`
version: 1
defaults:
  output: { color: true, drop_unmatched: false }
  tui: { enabled: true, scrollback: 20000 }
fragments:
  jetbrains-base:
    sources:
      - id: main
        filter: 'idea\.log(\.\d+)?$'
        locations:
          - dir: { linux: '~/.cache/JetBrains/{product}*/log' }
          - dir: { linux: '~/.{product}*/system/log' }
  junie-bridge:
    sources:
      - id: junie
        filter: '\.log$'
        locations:
          - dir: { linux: '~/.cache/JetBrains/{product}*/log/junie' }
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
  idea:
    renderers: [json-line]
    use:
      - { frag: jetbrains-base, product: IntelliJIdea }
      - { frag: junie-bridge,   product: IntelliJIdea }
renderers:
  json-line: { line_regex: '^\s*(\{.*\})\s*$', template: 'json($1)' }
bundles:
  jetbrains: [goland, idea]
`))
	if err != nil {
		t.Fatalf("parse test catalog: %v", err)
	}
	return c
}

func TestResolveGoland_NewSchemeExists(t *testing.T) {
	c := testCatalog(t)
	exists := func(p string) bool {
		return p == "/home/me/.cache/JetBrains/GoLand*/log" ||
			p == "/home/me/.cache/JetBrains/GoLand*/log/acp"
	}
	env := Env{OS: "linux", Home: "/home/me", Getenv: func(string) string { return "" }, Exists: exists}

	f, err := c.Resolve([]string{"goland"}, env)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(f.Directories) != 2 {
		t.Fatalf("dirs = %+v", f.Directories)
	}
	if f.Directories[0].ID != "goland" || f.Directories[0].Paths[0] != "/home/me/.cache/JetBrains/GoLand*/log" {
		t.Errorf("base group = %+v", f.Directories[0])
	}
	if f.Directories[0].FileFilter == nil || f.Directories[0].FileFilter.NameRegex != `idea\.log(\.\d+)?$` {
		t.Errorf("base filter = %+v", f.Directories[0].FileFilter)
	}
	if f.Directories[1].ID != "goland-acp" {
		t.Errorf("acp group id = %q", f.Directories[1].ID)
	}
	if len(f.Renderers) != 1 || f.Renderers[0].Name != "json-line" {
		t.Errorf("renderers = %+v", f.Renderers)
	}
	if f.Output == nil || f.Output.Color == nil || !*f.Output.Color {
		t.Errorf("output defaults missing: %+v", f.Output)
	}
	if f.TUI == nil || f.TUI.Scrollback == nil || *f.TUI.Scrollback != 20000 {
		t.Errorf("tui defaults missing: %+v", f.TUI)
	}
}

func TestResolveFallbackToNewestWhenNoneExist(t *testing.T) {
	c := testCatalog(t)
	env := Env{OS: "linux", Home: "/home/me", Getenv: func(string) string { return "" },
		Exists: func(string) bool { return false }}
	f, err := c.Resolve([]string{"goland"}, env)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if f.Directories[0].Paths[0] != "/home/me/.cache/JetBrains/GoLand*/log" {
		t.Errorf("fallback path = %v", f.Directories[0].Paths)
	}
}

func TestResolveBothCandidatesExistEmitsBoth(t *testing.T) {
	c := testCatalog(t)
	env := Env{OS: "linux", Home: "/home/me", Getenv: func(string) string { return "" },
		Exists: func(string) bool { return true }}
	f, _ := c.Resolve([]string{"goland"}, env)
	if len(f.Directories[0].Paths) != 2 {
		t.Errorf("want both candidate paths, got %v", f.Directories[0].Paths)
	}
}

func TestResolveBundleAndDedup(t *testing.T) {
	c := testCatalog(t)
	env := Env{OS: "linux", Home: "/home/me", Getenv: func(string) string { return "" },
		Exists: func(string) bool { return true }}
	f, err := c.Resolve([]string{"jetbrains"}, env)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(f.Renderers) != 1 {
		t.Errorf("dedup failed: %+v", f.Renderers)
	}
	ids := map[string]bool{}
	for _, d := range f.Directories {
		if ids[d.ID] {
			t.Errorf("duplicate group id %q", d.ID)
		}
		ids[d.ID] = true
	}
	if !ids["idea-intellijidea-junie"] {
		t.Errorf("missing idea junie-bridge group; ids=%v", ids)
	}
}

func TestResolveUnknownName(t *testing.T) {
	c := testCatalog(t)
	env := Env{OS: "linux", Home: "/home/me", Getenv: func(string) string { return "" },
		Exists: func(string) bool { return true }}
	if _, err := c.Resolve([]string{"nope"}, env); err == nil {
		t.Fatal("expected error for unknown name")
	}
}
