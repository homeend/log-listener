// Package catalog turns an embedded (or online-updated) catalog of per-app
// log templates into a config.File for the current OS. See
// docs/superpowers/specs/2026-06-03-template-auto-config-design.md.
package catalog

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// Catalog is the top-level catalog document.
type Catalog struct {
	Version   int                 `yaml:"version"`
	Defaults  Defaults            `yaml:"defaults"`
	Fragments map[string]Fragment `yaml:"fragments"`
	Apps      map[string]App      `yaml:"apps"`
	Renderers map[string]Renderer `yaml:"renderers"`
	Bundles   map[string][]string `yaml:"bundles"`
}

// Defaults supplies the global output/tui blocks when no selected app sets them.
type Defaults struct {
	Output OutputDefaults `yaml:"output"`
	TUI    TUIDefaults    `yaml:"tui"`
}

// OutputDefaults holds the global output settings emitted into generated configs.
type OutputDefaults struct {
	Color         bool `yaml:"color"`
	DropUnmatched bool `yaml:"drop_unmatched"`
}

// TUIDefaults holds the global TUI settings emitted into generated configs.
type TUIDefaults struct {
	Enabled    bool `yaml:"enabled"`
	Scrollback int  `yaml:"scrollback"`
}

// Fragment is a reusable bundle of sources, optionally parameterized by {product}.
type Fragment struct {
	Sources []Source `yaml:"sources"`
}

// Source is one discovery target: a filter plus ordered drift candidates.
type Source struct {
	ID        string     `yaml:"id"`
	Filter    string     `yaml:"filter"`
	Locations []Location `yaml:"locations"`
}

// Location is one drift candidate. Exactly one of Dir/File is set; both map
// an OS key (linux/darwin/windows) to a path that may contain ~, %VAR%,
// $VAR, and {product}. Dir names a directory to watch (paired with the
// source's filter); File names one log file explicitly.
type Location struct {
	Dir  map[string]string `yaml:"dir,omitempty"`
	File map[string]string `yaml:"file,omitempty"`
}

// App is a named template composed from fragments plus inline sources.
type App struct {
	Renderers []string `yaml:"renderers"`
	Use       []Use    `yaml:"use"`
	Sources   []Source `yaml:"sources"`
}

// Use references a fragment and binds its {product} parameter.
type Use struct {
	Frag    string `yaml:"frag"`
	Product string `yaml:"product"`
}

// Renderer is a named entry in the catalog's reusable renderer library.
type Renderer struct {
	LineRegex string `yaml:"line_regex"`
	Template  string `yaml:"template"`
}

// Parse decodes a catalog document strictly (unknown keys are an error). Used
// for the bundled catalog so authoring typos surface at build/test time.
func Parse(data []byte) (*Catalog, error) {
	var c Catalog
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, err
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// parseLenient decodes a catalog WITHOUT strict unknown-key checking. Used for
// the remote catalog (see Select) so a newer published catalog that adds fields
// an older binary doesn't recognize is still usable — forward compatibility,
// mirroring the strict config.Load vs lenient config.ParseFile split. The remote
// catalog is our own CI-validated artifact, so leniency here is safe.
func parseLenient(data []byte) (*Catalog, error) {
	var c Catalog
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// validate enforces authoring rules the YAML schema alone cannot express:
// each location carries exactly one of dir/file, all locations of a source
// agree on one mode, and file-based sources have no filter (a filename regex
// is meaningless when the file is named explicitly). Run only from Parse —
// the strict bundled-catalog path — so the remote catalog stays lenient.
func (c *Catalog) validate() error {
	for name, frag := range c.Fragments {
		if err := validateSources("fragment "+name, frag.Sources); err != nil {
			return err
		}
	}
	for name, app := range c.Apps {
		if err := validateSources("app "+name, app.Sources); err != nil {
			return err
		}
	}
	return nil
}

// validateSources checks the per-source location rules for one owner's source
// list. A source with no locations has no mode and passes untouched — it is
// inert at resolve time, so there is nothing to enforce.
func validateSources(owner string, srcs []Source) error {
	for _, src := range srcs {
		mode := ""
		for i, loc := range src.Locations {
			hasDir, hasFile := len(loc.Dir) > 0, len(loc.File) > 0
			var m string
			switch {
			case hasDir && hasFile:
				return fmt.Errorf("%s: source %q: location %d sets both dir and file", owner, src.ID, i)
			case hasDir:
				m = "dir"
			case hasFile:
				m = "file"
			default:
				return fmt.Errorf("%s: source %q: location %d sets neither dir nor file", owner, src.ID, i)
			}
			if mode == "" {
				mode = m
			} else if mode != m {
				return fmt.Errorf("%s: source %q: location %d mixes dir and file locations", owner, src.ID, i)
			}
		}
		if mode == "file" && src.Filter != "" {
			return fmt.Errorf("%s: source %q: filter is not allowed on a file-based source", owner, src.ID)
		}
	}
	return nil
}
