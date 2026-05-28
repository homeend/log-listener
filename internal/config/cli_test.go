package config

import (
	"testing"
	"time"

	"log-listener/internal/discover"
)

var refNow = time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)

func TestParseArgsBasicDir(t *testing.T) {
	cfg, err := ParseArgs([]string{"-d", "/var/log/a", "/var/log/b"}, refNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Groups) != 1 {
		t.Fatalf("want 1 group, got %d", len(cfg.Groups))
	}
	g := cfg.Groups[0]
	if g.ID != "default" || g.Kind != discover.GroupDir {
		t.Fatalf("group misconfigured: %+v", g)
	}
	if len(g.Paths) != 2 || g.Paths[0] != "/var/log/a" || g.Paths[1] != "/var/log/b" {
		t.Fatalf("paths: %v", g.Paths)
	}
}

func TestParseArgsNumberedGroups(t *testing.T) {
	cfg, err := ParseArgs([]string{
		"-d", "/a",
		"-d1", "/b",
		"-d2", "/c",
		"-f", "/x/*.log",
		"-f1", "/y.log",
	}, refNow)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs := []string{"default", "1", "2", "default", "1"}
	wantKinds := []discover.GroupKind{
		discover.GroupDir, discover.GroupDir, discover.GroupDir,
		discover.GroupFile, discover.GroupFile,
	}
	if len(cfg.Groups) != len(wantIDs) {
		t.Fatalf("want %d groups, got %d", len(wantIDs), len(cfg.Groups))
	}
	for i, g := range cfg.Groups {
		if g.ID != wantIDs[i] || g.Kind != wantKinds[i] {
			t.Fatalf("group %d: got id=%s kind=%v, want id=%s kind=%v",
				i, g.ID, g.Kind, wantIDs[i], wantKinds[i])
		}
	}
}

func TestParseArgsRulesPairedByID(t *testing.T) {
	cfg, err := ParseArgs([]string{
		"-d", "/a",
		"-r", "name:.log$", "younger:1h",
		"-d1", "/b",
		"-r1", "exclude:archive",
	}, refNow)
	if err != nil {
		t.Fatal(err)
	}
	gDefault := cfg.Groups[0]
	g1 := cfg.Groups[1]
	if gDefault.Filter == nil || gDefault.Filter.NameRegex == nil {
		t.Fatalf("default filter missing")
	}
	if !gDefault.Filter.NameRegex.MatchString("app.log") {
		t.Fatalf("name regex not compiled correctly")
	}
	if gDefault.Filter.Younger.IsZero() {
		t.Fatalf("younger not parsed")
	}
	if g1.Filter == nil || g1.Filter.ExcludeRegex == nil {
		t.Fatalf("g1 filter missing")
	}
}

func TestParseArgsGlobalRule(t *testing.T) {
	cfg, err := ParseArgs([]string{"-d", "/a", "-R", "younger:7d"}, refNow)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GlobalFilter == nil || cfg.GlobalFilter.Younger.IsZero() {
		t.Fatalf("global filter not set: %+v", cfg.GlobalFilter)
	}
}

func TestParseArgsBooleansAndOptions(t *testing.T) {
	cfg, err := ParseArgs([]string{
		"-d", "/a",
		"--once", "--no-tui", "--no-color",
		"--sse", "127.0.0.1:9000",
		"--config", "/etc/log-listener.yml",
	}, refNow)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Once || !cfg.NoTUI || !cfg.NoColor {
		t.Fatalf("bool flags: %+v", cfg)
	}
	if cfg.SSEAddr != "127.0.0.1:9000" {
		t.Fatalf("sse addr: %q", cfg.SSEAddr)
	}
	if cfg.ConfigFile != "/etc/log-listener.yml" {
		t.Fatalf("config file: %q", cfg.ConfigFile)
	}
}

func TestParseArgsErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"empty -d", []string{"-d"}},
		{"unknown flag", []string{"-d", "/a", "--bogus"}},
		{"bad rule token", []string{"-d", "/a", "-r", "nokeyvalue"}},
		{"bad rule key", []string{"-d", "/a", "-r", "foo:bar"}},
		{"bad regex", []string{"-d", "/a", "-r", "name:["}},
		{"bad time", []string{"-d", "/a", "-r", "older:nope"}},
		{"missing sse value", []string{"-d", "/a", "--sse"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseArgs(tc.args, refNow)
			if err == nil {
				t.Fatal("want error, got nil")
			}
		})
	}
}

func TestValidateErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"no groups", []string{}},
		{"-r without -d (no paths)", []string{"-r1", "name:.log"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseArgs(tc.args, refNow)
			if err != nil {
				t.Fatalf("ParseArgs unexpected error: %v", err)
			}
			if err := cfg.Validate(); err == nil {
				t.Fatal("Validate: want error, got nil")
			}
		})
	}
}
