package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
			Name: "json-line", LineRegex: `^\s*(\{.*\})\s*$`, Template: "json($1)",
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
