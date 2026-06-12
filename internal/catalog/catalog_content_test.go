package catalog

import "testing"

func TestBundledResolvesEveryAppOnEveryOS(t *testing.T) {
	c, err := Bundled()
	if err != nil {
		t.Fatalf("Bundled: %v", err)
	}
	if len(c.Apps) == 0 {
		t.Fatal("bundled catalog has no apps")
	}
	env := func(os string) Env {
		return Env{OS: os, Home: "/home/u", Getenv: func(string) string { return "C:/AppData" },
			Exists:     func(string) bool { return false }, // force best-effort path on all
			ExistsFile: func(string) bool { return false }}
	}
	for name := range c.Apps {
		for _, os := range []string{"linux", "darwin", "windows"} {
			f, err := c.Resolve([]string{name}, env(os))
			if err != nil {
				t.Errorf("Resolve(%q, %s): %v", name, os, err)
				continue
			}
			for _, d := range f.Directories {
				if len(d.Paths) == 0 {
					t.Errorf("%q/%s: group %q has no paths", name, os, d.ID)
				}
			}
			for _, fg := range f.Files {
				if len(fg.Paths) == 0 {
					t.Errorf("%q/%s: file group %q has no paths", name, os, fg.ID)
				}
			}
		}
	}
	if c.Bundles["jetbrains"] == nil {
		t.Error("expected a 'jetbrains' bundle")
	}

	// Guard against a source being silently dropped on an OS that lacks a
	// dir key: goland (jetbrains-base main + inline acp) must emit exactly two
	// groups on every OS. A dropped source would show up here.
	for _, os := range []string{"linux", "darwin", "windows"} {
		f, err := c.Resolve([]string{"goland"}, env(os))
		if err != nil {
			t.Fatalf("Resolve(goland, %s): %v", os, err)
		}
		if len(f.Directories) != 2 {
			t.Errorf("goland/%s: got %d groups, want 2 (a source was dropped?)", os, len(f.Directories))
		}
	}
}
