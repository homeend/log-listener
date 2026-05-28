package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeYAML(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadFromExplicitConfig(t *testing.T) {
	dir := t.TempDir()
	yml := writeYAML(t, dir, "log.yml", `
directories:
  - id: default
    paths: [/var/log/a]
    recursive: true
    file_filter:
      name_regex: '\.log$'
      younger: 1h
  - id: errors
    paths: [/var/log/special]
files:
  - id: default
    paths: ['/tmp/output-*.log']
global_file_filter:
  younger: 7d
renderers:
  - name: app-json
    line_regex: '(\d{4}-\d{2}-\d{2}) \[(\w+)\] (\{.*\})'
    template: '$1 $2\njson($3)'
    applies_to:
      groups: [errors]
      paths: ['*.app.log']
output:
  color: false
  drop_unmatched: true
  sse:
    enabled: true
    addr: '127.0.0.1:9000'
tui:
  enabled: false
  scrollback: 5000
`)

	homeStub := func() (string, error) { return dir, nil }
	cfg, err := loadWithFS([]string{"--config", yml}, refNow, homeStub)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Groups) != 3 {
		t.Fatalf("want 3 groups, got %d", len(cfg.Groups))
	}
	d0 := cfg.Groups[0]
	if d0.ID != "default" || d0.Filter == nil || d0.Filter.NameRegex == nil {
		t.Fatalf("dir[0] misconfigured: %+v", d0)
	}
	if !d0.Filter.NameRegex.MatchString("foo.log") {
		t.Fatal("name regex not compiled")
	}
	if cfg.GlobalFilter == nil || cfg.GlobalFilter.Younger.IsZero() {
		t.Fatalf("global filter missing")
	}
	if !cfg.NoColor || !cfg.NoTUI {
		t.Fatalf("color/tui flags from YAML: NoColor=%v NoTUI=%v", cfg.NoColor, cfg.NoTUI)
	}
	if cfg.SSEAddr != "127.0.0.1:9000" {
		t.Fatalf("sse addr: %q", cfg.SSEAddr)
	}
	if !cfg.DropUnmatched {
		t.Fatal("drop_unmatched not propagated")
	}
	if cfg.TUIScrollback != 5000 {
		t.Fatalf("scrollback: %d", cfg.TUIScrollback)
	}
	if len(cfg.RendererSpecs) != 1 || cfg.RendererSpecs[0].Name != "app-json" {
		t.Fatalf("renderer not loaded: %+v", cfg.RendererSpecs)
	}
}

func TestLoadCLIOverridesYAML(t *testing.T) {
	dir := t.TempDir()
	yml := writeYAML(t, dir, "log.yml", `
directories:
  - id: default
    paths: [/var/log/yaml-side]
output:
  color: false
  sse:
    addr: '127.0.0.1:9000'
tui:
  enabled: false
`)

	homeStub := func() (string, error) { return dir, nil }
	// CLI: override default dir group + force SSE elsewhere + leave color/tui to YAML
	cfg, err := loadWithFS([]string{
		"--config", yml,
		"-d", "/cli-dir",
		"--sse", "127.0.0.1:8888",
	}, refNow, homeStub)
	if err != nil {
		t.Fatal(err)
	}
	// CLI's "default" dir wins; YAML's "default" is dropped
	if len(cfg.Groups) != 1 {
		t.Fatalf("want 1 group, got %d: %+v", len(cfg.Groups), cfg.Groups)
	}
	if cfg.Groups[0].Paths[0] != "/cli-dir" {
		t.Fatalf("CLI didn't override YAML default group: %v", cfg.Groups[0].Paths)
	}
	if cfg.SSEAddr != "127.0.0.1:8888" {
		t.Fatalf("CLI --sse should win: %q", cfg.SSEAddr)
	}
	// CLI didn't pass --no-color or --no-tui, so YAML applies
	if !cfg.NoColor || !cfg.NoTUI {
		t.Fatalf("YAML should set NoColor/NoTUI: %v/%v", cfg.NoColor, cfg.NoTUI)
	}
}

func TestLoadYAMLAppendsDifferentGroupIDs(t *testing.T) {
	dir := t.TempDir()
	yml := writeYAML(t, dir, "log.yml", `
directories:
  - id: errors
    paths: [/var/log/special]
`)
	homeStub := func() (string, error) { return dir, nil }
	cfg, err := loadWithFS([]string{"--config", yml, "-d", "/cli-dir"}, refNow, homeStub)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Groups) != 2 {
		t.Fatalf("want 2 groups (CLI default + YAML errors), got %d", len(cfg.Groups))
	}
	// CLI's default comes first (CLI parsed first; YAML appends)
	if cfg.Groups[0].ID != "default" || cfg.Groups[1].ID != "errors" {
		t.Fatalf("order: %v / %v", cfg.Groups[0].ID, cfg.Groups[1].ID)
	}
}

func TestLoadNoYAMLNoCLIErrorsAtValidate(t *testing.T) {
	dir := t.TempDir()
	homeStub := func() (string, error) { return dir, nil }
	_, err := loadWithFS([]string{}, refNow, homeStub)
	if err == nil {
		t.Fatal("want validate error (no groups), got nil")
	}
}

func TestLoadExplicitConfigMissing(t *testing.T) {
	homeStub := func() (string, error) { return os.TempDir(), nil }
	_, err := loadWithFS([]string{"--config", "/no/such/file.yml"}, refNow, homeStub)
	if err == nil {
		t.Fatal("expected error for missing --config file")
	}
}

func TestLoadFallsBackToCWDLogListenerYML(t *testing.T) {
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	writeYAML(t, dir, "log-listener.yml", `
directories:
  - id: default
    paths: [/var/log/from-cwd]
`)
	homeStub := func() (string, error) { return dir, nil } // home unused if CWD wins
	cfg, err := loadWithFS([]string{}, refNow, homeStub)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Groups) != 1 || cfg.Groups[0].Paths[0] != "/var/log/from-cwd" {
		t.Fatalf("CWD fallback failed: %+v", cfg.Groups)
	}
}

func TestLoadFallsBackToHomeDotFile(t *testing.T) {
	cwdDir := t.TempDir()
	homeDir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwdDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	writeYAML(t, homeDir, ".log-listener.yml", `
directories:
  - id: default
    paths: [/var/log/from-home]
`)
	homeStub := func() (string, error) { return homeDir, nil }
	cfg, err := loadWithFS([]string{}, refNow, homeStub)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Groups) != 1 || cfg.Groups[0].Paths[0] != "/var/log/from-home" {
		t.Fatalf("home fallback failed: %+v", cfg.Groups)
	}
}

func TestLoadBadYAML(t *testing.T) {
	dir := t.TempDir()
	yml := writeYAML(t, dir, "log.yml", "not valid yaml: [unclosed")
	homeStub := func() (string, error) { return dir, nil }
	_, err := loadWithFS([]string{"--config", yml}, refNow, homeStub)
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestLoadYAMLStrictUnknownKey(t *testing.T) {
	dir := t.TempDir()
	yml := writeYAML(t, dir, "log.yml", `
directorys:        # typo: should be "directories"
  - id: default
    paths: [/foo]
`)
	homeStub := func() (string, error) { return dir, nil }
	_, err := loadWithFS([]string{"--config", yml}, refNow, homeStub)
	if err == nil {
		t.Fatal("expected strict-mode error for unknown YAML key")
	}
}

func TestLoadYAMLDuplicateIDs(t *testing.T) {
	dir := t.TempDir()
	yml := writeYAML(t, dir, "log.yml", `
directories:
  - id: foo
    paths: [/a]
  - id: foo
    paths: [/b]
`)
	homeStub := func() (string, error) { return dir, nil }
	_, err := loadWithFS([]string{"--config", yml}, refNow, homeStub)
	if err == nil {
		t.Fatal("expected duplicate-id error")
	}
}

func TestLoadYAMLSSEEnabledDefaultsAddr(t *testing.T) {
	dir := t.TempDir()
	yml := writeYAML(t, dir, "log.yml", `
directories:
  - id: default
    paths: [/foo]
output:
  sse:
    enabled: true
`)
	homeStub := func() (string, error) { return dir, nil }
	cfg, err := loadWithFS([]string{"--config", yml}, refNow, homeStub)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SSEAddr != "127.0.0.1:8080" {
		t.Fatalf("want default localhost addr, got %q", cfg.SSEAddr)
	}
}

func TestLoadYAMLInvalidRegex(t *testing.T) {
	dir := t.TempDir()
	yml := writeYAML(t, dir, "log.yml", `
directories:
  - id: default
    paths: [/foo]
    file_filter:
      name_regex: '['
`)
	homeStub := func() (string, error) { return dir, nil }
	_, err := loadWithFS([]string{"--config", yml}, refNow, homeStub)
	if err == nil {
		t.Fatal("expected regex compile error")
	}
}
