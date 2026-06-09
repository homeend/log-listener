package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMergeFilesAppendsOnlyNew(t *testing.T) {
	rec := false
	existing := &File{
		Directories: []DirGroup{{ID: "goland", Paths: []string{"/a"}, Recursive: &rec}},
		Renderers:   []Renderer{{Name: "json-line", LineRegex: "x", Template: "y"}},
		Output:      &Output{}, // already present -> must NOT be overwritten by defaults
	}
	gen := &File{
		Directories: []DirGroup{
			{ID: "goland", Paths: []string{"/SHOULD-BE-IGNORED"}}, // dup id -> skipped
			{ID: "idea", Paths: []string{"/b"}},                   // new -> appended
		},
		Renderers: []Renderer{
			{Name: "json-line", LineRegex: "NEW", Template: "NEW"}, // dup name -> skipped
			{Name: "idea-trailing-json", LineRegex: "p", Template: "q"},
		},
		Output: &Output{}, // existing wins -> ignored
		TUI:    &TUI{},    // existing has none -> set
	}
	out := MergeFiles(existing, gen)

	if len(out.Directories) != 2 || out.Directories[0].ID != "goland" || out.Directories[1].ID != "idea" {
		t.Fatalf("dirs = %+v", out.Directories)
	}
	if out.Directories[0].Paths[0] != "/a" {
		t.Errorf("existing goland clobbered: %+v", out.Directories[0])
	}
	if len(out.Renderers) != 2 || out.Renderers[1].Name != "idea-trailing-json" {
		t.Errorf("renderers = %+v", out.Renderers)
	}
	if out.Renderers[0].LineRegex != "x" {
		t.Errorf("existing renderer clobbered: %+v", out.Renderers[0])
	}
	if out.Output != existing.Output {
		t.Errorf("Output should be the existing pointer (unchanged)")
	}
	if out.TUI != gen.TUI {
		t.Errorf("TUI should be filled from gen when existing had none")
	}
}

func TestFileMarshalLoadsBackThroughLoad(t *testing.T) {
	rec := false
	f := &File{
		Directories: []DirGroup{{
			ID:         "goland",
			Paths:      []string{"/tmp/does-not-matter/log"},
			Recursive:  &rec,
			FileFilter: &Filter{NameRegex: `idea\.log$`},
		}},
		Renderers: []Renderer{{
			Name: "json-line", LineRegex: `^\s*(\{.*\})\s*$`, Template: "$json($1)",
		}},
	}
	data, err := f.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "log-listener.yml")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load([]string{"--config", path}, time.Now())
	if err != nil {
		t.Fatalf("Load(emitted): %v\n---\n%s", err, data)
	}
	if len(cfg.Groups) != 1 || cfg.Groups[0].ID != "goland" {
		t.Errorf("groups = %+v", cfg.Groups)
	}
	if len(cfg.RendererSpecs) != 1 || cfg.RendererSpecs[0].Name != "json-line" {
		t.Errorf("renderers = %+v", cfg.RendererSpecs)
	}
}
