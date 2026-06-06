// Package config defines the Config struct and CLI parser for Phase 1.
// YAML loading is added in Phase 2.
package config

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/homeend/log-listener/internal/discover"
	"github.com/homeend/log-listener/internal/timeparse"
)

// Config is the merged CLI + YAML configuration.
// Groups is the unified list of directory and file groups in declaration
// order — this is the order Assign and the renderer-pipeline (later phases)
// use for first-match-wins semantics.
type Config struct {
	Groups       []*discover.Group
	GlobalFilter *discover.FileFilter

	Once       bool
	NoTUI      bool
	NoColor    bool
	SSEAddr    string
	ConfigFile string

	// SourcePath is the absolute/relative path of the YAML file that was
	// actually loaded (resolved from --config or the default lookup), or ""
	// if no YAML was loaded. Used by the config-reload watcher.
	SourcePath string

	// Populated only via YAML in Phase 2; CLI has no flags for them yet.
	DropUnmatched bool
	TUIScrollback int // 0 = use the default (10000); set by tui.scrollback in YAML
	RendererSpecs []RendererSpec
	Matchers      map[string]MatcherSpec
	MuteSpecs     []MuteSpec
	Keybindings   *Keybindings // raw YAML key override layers; resolved in cmd

	indexDir    map[string]int
	indexFile   map[string]int
	cliExplicit map[string]bool // tracks which scalar fields CLI set, so YAML merge skips them
}

func newConfig() *Config {
	return &Config{
		indexDir:    map[string]int{},
		indexFile:   map[string]int{},
		cliExplicit: map[string]bool{},
	}
}

var flagRE = regexp.MustCompile(`^-([drf])(\d*)$`)

// ParseArgs parses CLI args (excluding program name). now anchors relative
// durations in rules (older: 1h, younger: 2d).
func ParseArgs(args []string, now time.Time) (*Config, error) {
	cfg := newConfig()
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "--config":
			v, next, err := requireValue(args, i, "--config")
			if err != nil {
				return nil, err
			}
			cfg.ConfigFile = v
			i = next
		case a == "--once":
			cfg.Once = true
			cfg.cliExplicit["once"] = true
			i++
		case a == "--no-tui":
			cfg.NoTUI = true
			cfg.cliExplicit["no_tui"] = true
			i++
		case a == "--no-color":
			cfg.NoColor = true
			cfg.cliExplicit["no_color"] = true
			i++
		case a == "--sse":
			v, next, err := requireValue(args, i, "--sse")
			if err != nil {
				return nil, err
			}
			cfg.SSEAddr = v
			cfg.cliExplicit["sse_addr"] = true
			i = next
		case a == "-R":
			vals, next := slurpValues(args, i+1)
			if err := applyRules(&cfg.GlobalFilter, vals, now); err != nil {
				return nil, fmt.Errorf("-R: %w", err)
			}
			// CLI-presence detection for the global filter is via the
			// "GlobalFilter != nil" check in mergeYAMLInto; no cliExplicit
			// entry needed here.
			i = next
		case flagRE.MatchString(a):
			m := flagRE.FindStringSubmatch(a)
			kind, id := m[1], m[2]
			if id == "" {
				id = "default"
			}
			vals, next := slurpValues(args, i+1)
			switch kind {
			case "d":
				if len(vals) == 0 {
					return nil, fmt.Errorf("%s: needs at least one path", a)
				}
				g := cfg.ensureDirGroup(id)
				g.Paths = append(g.Paths, vals...)
			case "r":
				g := cfg.ensureDirGroup(id)
				if err := applyRules(&g.Filter, vals, now); err != nil {
					return nil, fmt.Errorf("%s: %w", a, err)
				}
			case "f":
				if len(vals) == 0 {
					return nil, fmt.Errorf("%s: needs at least one path", a)
				}
				g := cfg.ensureFileGroup(id)
				g.Paths = append(g.Paths, vals...)
			}
			i = next
		default:
			return nil, fmt.Errorf("unknown flag or stray arg: %q", a)
		}
	}
	return cfg, nil
}

func requireValue(args []string, i int, name string) (string, int, error) {
	if i+1 >= len(args) {
		return "", 0, fmt.Errorf("%s: missing value", name)
	}
	return args[i+1], i + 2, nil
}

func slurpValues(args []string, start int) ([]string, int) {
	end := start
	for end < len(args) {
		if strings.HasPrefix(args[end], "-") {
			break
		}
		end++
	}
	return args[start:end], end
}

func (c *Config) ensureDirGroup(id string) *discover.Group {
	if i, ok := c.indexDir[id]; ok {
		return c.Groups[i]
	}
	g := &discover.Group{ID: id, Kind: discover.GroupDir, Recursive: true}
	c.indexDir[id] = len(c.Groups)
	c.Groups = append(c.Groups, g)
	return g
}

func (c *Config) ensureFileGroup(id string) *discover.Group {
	if i, ok := c.indexFile[id]; ok {
		return c.Groups[i]
	}
	g := &discover.Group{ID: id, Kind: discover.GroupFile}
	c.indexFile[id] = len(c.Groups)
	c.Groups = append(c.Groups, g)
	return g
}

// Validate checks structural completeness. Call after CLI + YAML merge.
func (c *Config) Validate() error {
	if len(c.Groups) == 0 {
		return fmt.Errorf("no directories (-d) or files (-f) given")
	}
	for _, g := range c.Groups {
		if len(g.Paths) == 0 {
			kind := "directory"
			if g.Kind == discover.GroupFile {
				kind = "file"
			}
			return fmt.Errorf("%s group %q has no paths", kind, g.ID)
		}
	}
	return nil
}

func applyRules(target **discover.FileFilter, tokens []string, now time.Time) error {
	if *target == nil {
		*target = &discover.FileFilter{}
	}
	f := *target
	for _, tok := range tokens {
		idx := strings.IndexByte(tok, ':')
		if idx <= 0 {
			return fmt.Errorf("bad rule token %q: expected key:value", tok)
		}
		key, val := tok[:idx], tok[idx+1:]
		switch key {
		case "name":
			re, err := regexp.Compile(val)
			if err != nil {
				return fmt.Errorf("name: %w", err)
			}
			f.NameRegex = re
		case "exclude":
			re, err := regexp.Compile(val)
			if err != nil {
				return fmt.Errorf("exclude: %w", err)
			}
			f.ExcludeRegex = re
		case "older":
			t, err := timeparse.Parse(val, now)
			if err != nil {
				return err
			}
			f.Older = t
		case "younger":
			t, err := timeparse.Parse(val, now)
			if err != nil {
				return err
			}
			f.Younger = t
		default:
			return fmt.Errorf("unknown rule key %q", key)
		}
	}
	return nil
}
