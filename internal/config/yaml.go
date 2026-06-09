package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/homeend/log-listener/internal/discover"
	"github.com/homeend/log-listener/internal/timeparse"
)

// RendererSpec is the renderer definition as loaded from YAML. Phase 3 will
// compile these into the actual rendering pipeline; for now they are carried
// through so Load() is forward-compatible with Phase 3.
type RendererSpec struct {
	Name      string
	LineRegex string
	Template  string
	Matcher   string // optional: name of a matcher in the matchers library
	AppliesTo *AppliesTo
	// StartOff is the soft-disable flag from YAML's `off: true`. The
	// renderer is registered in the pipeline but its atomic-enabled flag
	// is initialized to false, so it's skipped during first-match-wins
	// dispatch until the user toggles it on via the TUI.
	StartOff bool
}

// AppliesTo is the renderer scope filter; semantics: a renderer applies to
// a file if (Groups is empty OR file's group is in Groups) AND
//
//	(Paths is empty OR file matches some glob in Paths).
type AppliesTo struct {
	Groups []string
	Paths  []string
}

// File is the YAML config schema, shared by the loader (readYAMLFile /
// mergeYAMLInto) and the emitter (emit.go). One struct set = no read/write drift.
type File struct {
	Directories      []DirGroup             `yaml:"directories,omitempty"`
	Files            []FileGroup            `yaml:"files,omitempty"`
	GlobalFileFilter *Filter                `yaml:"global_file_filter,omitempty"`
	Matchers         map[string]MatcherSpec `yaml:"matchers,omitempty"`
	Mute             []MuteSpec             `yaml:"mute,omitempty"`
	Renderers        []Renderer             `yaml:"renderers,omitempty"`
	Output           *Output                `yaml:"output,omitempty"`
	TUI              *TUI                   `yaml:"tui,omitempty"`
	Keybindings      *Keybindings           `yaml:"keybindings,omitempty"`
}

type DirGroup struct {
	ID         string   `yaml:"id"`
	Paths      []string `yaml:"paths,omitempty"`
	Recursive  *bool    `yaml:"recursive,omitempty"`
	FileFilter *Filter  `yaml:"file_filter,omitempty"`
	// disabled: true -> entry filtered out entirely at load time.
	// off: true      -> entry loaded, but its TUI toggle starts off.
	// If both are set, disabled wins and off is ignored.
	Disabled bool `yaml:"disabled,omitempty"`
	Off      bool `yaml:"off,omitempty"`
}

type FileGroup struct {
	ID       string   `yaml:"id"`
	Paths    []string `yaml:"paths,omitempty"`
	Disabled bool     `yaml:"disabled,omitempty"`
	Off      bool     `yaml:"off,omitempty"`
}

type Filter struct {
	NameRegex    string `yaml:"name_regex,omitempty"`
	ExcludeRegex string `yaml:"exclude_regex,omitempty"`
	Older        string `yaml:"older,omitempty"`
	Younger      string `yaml:"younger,omitempty"`
}

type Renderer struct {
	Name      string         `yaml:"name"`
	LineRegex string         `yaml:"line_regex,omitempty"`
	Template  string         `yaml:"template"`
	Matcher   string         `yaml:"matcher,omitempty"`
	AppliesTo *AppliesToSpec `yaml:"applies_to,omitempty"`
	Disabled  bool           `yaml:"disabled,omitempty"`
	Off       bool           `yaml:"off,omitempty"`
}

// AppliesToSpec is the YAML form of a renderer scope (distinct from the
// compiled AppliesTo type carried into the render pipeline).
type AppliesToSpec struct {
	Groups []string `yaml:"groups,omitempty"`
	Paths  []string `yaml:"paths,omitempty"`
}

// MatcherSpec is a reusable matcher definition from the `matchers:` map. For
// each dimension set either the literal key or the *_regex key.
type MatcherSpec struct {
	Line      string `yaml:"line,omitempty"`
	LineRegex string `yaml:"line_regex,omitempty"`
	Name      string `yaml:"name,omitempty"`
	NameRegex string `yaml:"name_regex,omitempty"`
	Path      string `yaml:"path,omitempty"`
	PathRegex string `yaml:"path_regex,omitempty"`
}

// MuteSpec is one entry in the `mute:` list. It sets exactly one of `matcher`
// (a named reference) or inline matcher fields (embedded MatcherSpec). `id` is
// an optional identity used in diagnostic messages; it is named `id` (not
// `name`) to avoid colliding with the matcher's inline `name` field.
type MuteSpec struct {
	ID          string `yaml:"id,omitempty"`
	Matcher     string `yaml:"matcher,omitempty"`
	MatcherSpec `yaml:",inline"`
	AppliesTo   *AppliesToSpec `yaml:"applies_to,omitempty"`
}

type Output struct {
	Color         *bool `yaml:"color,omitempty"`
	DropUnmatched *bool `yaml:"drop_unmatched,omitempty"`
	SSE           *SSE  `yaml:"sse,omitempty"`
}

type SSE struct {
	Enabled *bool  `yaml:"enabled,omitempty"`
	Addr    string `yaml:"addr,omitempty"`
}

type TUI struct {
	Enabled           *bool `yaml:"enabled,omitempty"`
	Scrollback        *int  `yaml:"scrollback,omitempty"`
	TruncateFilenames *bool `yaml:"truncate_filenames,omitempty"`
	FilenameWidth     *int  `yaml:"filename_width,omitempty"`
	WordWrap          *bool `yaml:"word_wrap,omitempty"`
}

// Keybindings is the raw YAML override layers for TUI keys. Action names and
// key strings are validated later by keymap.Resolve (cmd wiring), not here, so
// config stays decoupled from the keymap package.
type Keybindings struct {
	Default map[string][]string `yaml:"default,omitempty"`
	Darwin  map[string][]string `yaml:"darwin,omitempty"`
	Linux   map[string][]string `yaml:"linux,omitempty"`
	Windows map[string][]string `yaml:"windows,omitempty"`
}

// Load parses CLI args, resolves the YAML config (if any), and merges them
// with CLI taking precedence over YAML. Resolution order for the YAML path:
// args' --config, then ./log-listener.yml, then ~/.log-listener.yml.
func Load(args []string, now time.Time) (*Config, error) {
	return loadWithFS(args, now, defaultHomeDir)
}

// loadWithFS is the testable variant; homeDir is injectable.
func loadWithFS(args []string, now time.Time, homeDir func() (string, error)) (*Config, error) {
	cfg, err := ParseArgs(args, now)
	if err != nil {
		return nil, err
	}

	yamlPath, err := resolveYAMLPath(cfg.ConfigFile, homeDir)
	if err != nil {
		return nil, err
	}
	cfg.SourcePath = yamlPath
	if yamlPath == "" {
		if err := cfg.Validate(); err != nil {
			return nil, err
		}
		return cfg, nil
	}

	yc, err := readYAMLFile(yamlPath)
	if err != nil {
		return nil, fmt.Errorf("config: %s: %w", yamlPath, err)
	}
	if err := mergeYAMLInto(cfg, yc, now); err != nil {
		return nil, fmt.Errorf("config: %s: %w", yamlPath, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func defaultHomeDir() (string, error) { return os.UserHomeDir() }

// resolveYAMLPath returns the YAML path to load, or "" if none should be
// loaded. An explicit --config that does not exist is an error; the default
// fallback paths silently skip when missing.
func resolveYAMLPath(explicit string, homeDir func() (string, error)) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("config: %s: %w", explicit, err)
		}
		return explicit, nil
	}
	if _, err := os.Stat("log-listener.yml"); err == nil {
		return "log-listener.yml", nil
	}
	home, err := homeDir()
	if err != nil || home == "" {
		return "", nil
	}
	candidate := filepath.Join(home, ".log-listener.yml")
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}
	return "", nil
}

func readYAMLFile(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var yc File
	if len(data) == 0 {
		return &yc, nil
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true) // strict: unknown YAML keys are an error so typos surface
	if err := dec.Decode(&yc); err != nil {
		return nil, err
	}
	return &yc, nil
}

// mergeYAMLInto applies yc to cfg with CLI-precedence semantics:
//   - scalar fields: applied only if CLI didn't set them explicitly
//   - groups: CLI-supplied (kind, id) groups win entirely; YAML-only groups
//     are appended in YAML declaration order (after CLI groups for that kind)
//   - global_file_filter: applied only if CLI didn't supply -R (any token)
//   - renderers: always taken from YAML (CLI has no renderer flags)
func mergeYAMLInto(cfg *Config, yc *File, now time.Time) error {
	// global_file_filter — CLI -R wins entirely if present
	if cfg.GlobalFilter == nil && yc.GlobalFileFilter != nil {
		gf, err := yamlFilterToDiscover(yc.GlobalFileFilter, now)
		if err != nil {
			return fmt.Errorf("global_file_filter: %w", err)
		}
		cfg.GlobalFilter = gf
	}

	// directories — append YAML groups whose (kind=dir, id) isn't already CLI
	yamlDirSeen := map[string]struct{}{}
	for _, ydg := range yc.Directories {
		id := ydg.ID
		if id == "" {
			id = "default"
		}
		if _, dup := yamlDirSeen[id]; dup {
			return fmt.Errorf("directories: duplicate id %q", id)
		}
		yamlDirSeen[id] = struct{}{}
		// disabled: true — drop the entry entirely. Skip the CLI-already-
		// exists check too (a disabled YAML entry does not shadow CLI).
		if ydg.Disabled {
			continue
		}
		if _, exists := cfg.indexDir[id]; exists {
			continue // CLI wins
		}
		filter, err := yamlFilterToDiscover(ydg.FileFilter, now)
		if err != nil {
			return fmt.Errorf("directories[%s]: %w", id, err)
		}
		recursive := true
		if ydg.Recursive != nil {
			recursive = *ydg.Recursive
		}
		g := &discover.Group{
			ID:        id,
			Kind:      discover.GroupDir,
			Paths:     append([]string(nil), ydg.Paths...),
			Recursive: recursive,
			Filter:    filter,
			StartOff:  ydg.Off,
		}
		cfg.indexDir[id] = len(cfg.Groups)
		cfg.Groups = append(cfg.Groups, g)
	}

	// files — same pattern
	yamlFileSeen := map[string]struct{}{}
	for _, yfg := range yc.Files {
		id := yfg.ID
		if id == "" {
			id = "default"
		}
		if _, dup := yamlFileSeen[id]; dup {
			return fmt.Errorf("files: duplicate id %q", id)
		}
		yamlFileSeen[id] = struct{}{}
		if yfg.Disabled {
			continue
		}
		if _, exists := cfg.indexFile[id]; exists {
			continue
		}
		g := &discover.Group{
			ID:       id,
			Kind:     discover.GroupFile,
			Paths:    append([]string(nil), yfg.Paths...),
			StartOff: yfg.Off,
		}
		cfg.indexFile[id] = len(cfg.Groups)
		cfg.Groups = append(cfg.Groups, g)
	}

	// renderers — Phase 2 just carries these through
	for _, yr := range yc.Renderers {
		// disabled: true — drop entirely. After this filter, the loaded
		// order == panel order == toggle-key index, so we don't need a
		// "loaded but inactive" tier.
		if yr.Disabled {
			continue
		}
		spec := RendererSpec{
			Name:      yr.Name,
			LineRegex: yr.LineRegex,
			Template:  yr.Template,
			Matcher:   yr.Matcher,
			StartOff:  yr.Off,
		}
		if yr.AppliesTo != nil {
			spec.AppliesTo = &AppliesTo{
				Groups: append([]string(nil), yr.AppliesTo.Groups...),
				Paths:  append([]string(nil), yr.AppliesTo.Paths...),
			}
		}
		cfg.RendererSpecs = append(cfg.RendererSpecs, spec)
	}

	// matchers / mute — YAML-only, carried through verbatim (validated at
	// pipeline build). Map decoding already rejects duplicate matcher names.
	if yc.Matchers != nil {
		cfg.Matchers = make(map[string]MatcherSpec, len(yc.Matchers))
		for k, v := range yc.Matchers {
			cfg.Matchers[k] = v
		}
	}
	cfg.MuteSpecs = append(cfg.MuteSpecs, yc.Mute...)

	// output
	if yc.Output != nil {
		o := yc.Output
		if o.Color != nil && !cfg.cliExplicit["no_color"] {
			if !*o.Color {
				cfg.NoColor = true
			}
		}
		if o.DropUnmatched != nil && !cfg.cliExplicit["drop_unmatched"] {
			cfg.DropUnmatched = *o.DropUnmatched
		}
		if o.SSE != nil && !cfg.cliExplicit["sse_addr"] {
			switch {
			case o.SSE.Enabled != nil && !*o.SSE.Enabled:
				cfg.SSEAddr = ""
			case o.SSE.Addr != "":
				cfg.SSEAddr = o.SSE.Addr
			case o.SSE.Enabled != nil && *o.SSE.Enabled:
				// enabled: true without addr → default localhost binding
				cfg.SSEAddr = "127.0.0.1:8080"
			}
		}
	}

	// tui
	if yc.TUI != nil {
		t := yc.TUI
		if t.Enabled != nil && !cfg.cliExplicit["no_tui"] {
			if !*t.Enabled {
				cfg.NoTUI = true
			}
		}
		if t.Scrollback != nil && !cfg.cliExplicit["tui_scrollback"] {
			cfg.TUIScrollback = *t.Scrollback
		}
		if t.TruncateFilenames != nil {
			cfg.TUITruncateFilenames = *t.TruncateFilenames
		}
		if t.FilenameWidth != nil {
			cfg.TUIFilenameWidth = *t.FilenameWidth
		}
		if t.WordWrap != nil {
			cfg.TUIWordWrap = *t.WordWrap
		}
	}

	// keybindings — carried through verbatim; resolved+validated in cmd
	// (needs runtime.GOOS). YAML-only, no CLI flags.
	if yc.Keybindings != nil {
		cfg.Keybindings = yc.Keybindings
	}

	return nil
}

func yamlFilterToDiscover(yf *Filter, now time.Time) (*discover.FileFilter, error) {
	if yf == nil {
		return nil, nil
	}
	f := &discover.FileFilter{}
	if yf.NameRegex != "" {
		re, err := regexp.Compile(yf.NameRegex)
		if err != nil {
			return nil, fmt.Errorf("name_regex: %w", err)
		}
		f.NameRegex = re
	}
	if yf.ExcludeRegex != "" {
		re, err := regexp.Compile(yf.ExcludeRegex)
		if err != nil {
			return nil, fmt.Errorf("exclude_regex: %w", err)
		}
		f.ExcludeRegex = re
	}
	if yf.Older != "" {
		t, err := timeparse.Parse(yf.Older, now)
		if err != nil {
			return nil, fmt.Errorf("older: %w", err)
		}
		f.Older = t
	}
	if yf.Younger != "" {
		t, err := timeparse.Parse(yf.Younger, now)
		if err != nil {
			return nil, fmt.Errorf("younger: %w", err)
		}
		f.Younger = t
	}
	return f, nil
}
