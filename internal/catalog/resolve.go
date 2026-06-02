package catalog

import (
	"fmt"
	"runtime"
	"strings"

	"log-listener/internal/config"
)

// Env carries the host facts resolution depends on. Injected for testability;
// DefaultEnv builds the live one.
type Env struct {
	OS     string                    // "linux" | "darwin" | "windows"
	Home   string                    // user home directory
	Getenv func(string) string       // environment lookup
	Exists func(dirGlob string) bool // true if the dir-glob matches an existing directory
}

func osKey(os string) string {
	if os == "" {
		return runtime.GOOS
	}
	return os
}

// Resolve composes the named apps/bundles into a config.File for env's OS.
func (c *Catalog) Resolve(names []string, env Env) (*config.File, error) {
	apps, err := c.expandNames(names)
	if err != nil {
		return nil, err
	}
	key := osKey(env.OS)

	f := &config.File{}
	seenID := map[string]bool{}
	seenRend := map[string]bool{}

	for _, appName := range apps {
		app := c.Apps[appName] // existence guaranteed by expandNames

		for _, u := range app.Use {
			frag, ok := c.Fragments[u.Frag]
			if !ok {
				return nil, fmt.Errorf("app %q: unknown fragment %q", appName, u.Frag)
			}
			for _, src := range frag.Sources {
				c.emitSource(f, appName, u.Product, src, key, env, seenID)
			}
		}
		for _, src := range app.Sources {
			c.emitSource(f, appName, "", src, key, env, seenID)
		}
		for _, rn := range app.Renderers {
			if seenRend[rn] {
				continue
			}
			r, ok := c.Renderers[rn]
			if !ok {
				return nil, fmt.Errorf("app %q: unknown renderer %q", appName, rn)
			}
			f.Renderers = append(f.Renderers, config.Renderer{
				Name: rn, LineRegex: r.LineRegex, Template: r.Template,
			})
			seenRend[rn] = true
		}
	}

	color := c.Defaults.Output.Color
	drop := c.Defaults.Output.DropUnmatched
	f.Output = &config.Output{Color: &color, DropUnmatched: &drop}
	enabled := c.Defaults.TUI.Enabled
	scroll := c.Defaults.TUI.Scrollback
	f.TUI = &config.TUI{Enabled: &enabled, Scrollback: &scroll}

	return f, nil
}

// expandNames flattens bundles into a deduped, order-preserving app list.
func (c *Catalog) expandNames(names []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	add := func(app string) {
		if !seen[app] {
			seen[app] = true
			out = append(out, app)
		}
	}
	for _, n := range names {
		switch {
		case c.Bundles[n] != nil:
			for _, app := range c.Bundles[n] {
				if _, ok := c.Apps[app]; !ok {
					return nil, fmt.Errorf("bundle %q references unknown app %q", n, app)
				}
				add(app)
			}
		case func() bool { _, ok := c.Apps[n]; return ok }():
			add(n)
		default:
			return nil, fmt.Errorf("unknown app or bundle %q (see `log-listener init --list`)", n)
		}
	}
	return out, nil
}

// emitSource probe-and-picks a source's drift candidates and appends a
// directory group to f.
func (c *Catalog) emitSource(f *config.File, app, product string, src Source, key string, env Env, seenID map[string]bool) {
	var picked []string
	var newest string
	for _, loc := range src.Locations {
		raw, ok := loc.Dir[key]
		if !ok {
			continue
		}
		p := expandPath(substituteProduct(raw, product), env.Home, env.Getenv)
		if newest == "" {
			newest = p
		}
		if env.Exists(p) {
			picked = append(picked, p)
		}
	}
	if len(picked) == 0 {
		if newest == "" {
			return
		}
		picked = []string{newest}
	}
	rec := false
	g := config.DirGroup{
		ID:        groupID(app, product, src.ID, seenID),
		Paths:     picked,
		Recursive: &rec,
	}
	if src.Filter != "" {
		g.FileFilter = &config.Filter{NameRegex: src.Filter}
	}
	f.Directories = append(f.Directories, g)
}

// groupID builds a unique, readable directory-group id.
func groupID(app, product, sourceID string, seen map[string]bool) string {
	id := app
	if product != "" && !strings.EqualFold(product, app) {
		id += "-" + strings.ToLower(product)
	}
	if sourceID != "" && sourceID != "main" {
		id += "-" + sourceID
	}
	base := id
	for n := 2; seen[id]; n++ {
		id = fmt.Sprintf("%s-%d", base, n)
	}
	seen[id] = true
	return id
}
