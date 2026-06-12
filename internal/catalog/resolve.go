package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/homeend/log-listener/internal/config"
)

// Env carries the host facts resolution depends on. Injected for testability;
// DefaultEnv builds the live one.
type Env struct {
	OS         string                     // "linux" | "darwin" | "windows"
	Home       string                     // user home directory
	Getenv     func(string) string        // environment lookup
	Exists     func(dirGlob string) bool  // true if the dir-glob matches an existing directory
	ExistsFile func(pathGlob string) bool // true if the path-glob matches an existing regular file
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
// User-supplied names are matched case-insensitively (see resolveName), so
// `init GoLand` resolves the same app as `init goland`.
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
		canon, isApp, isBundle := c.resolveName(n)
		switch {
		case isBundle:
			for _, app := range c.Bundles[canon] {
				appCanon, appIsApp, _ := c.resolveName(app)
				if !appIsApp {
					return nil, fmt.Errorf("bundle %q references unknown app %q", canon, app)
				}
				add(appCanon)
			}
		case isApp:
			add(canon)
		default:
			return nil, fmt.Errorf("unknown app or bundle %q (see `log-listener init --list`)", n)
		}
	}
	return out, nil
}

// resolveName maps a user-supplied app/bundle name to its canonical catalog
// key, case-insensitively. An exact match wins so a catalog that (unusually)
// declares case-variant keys stays deterministic; otherwise the first
// case-insensitive match is taken. Bundles take precedence over apps, matching
// expandNames' resolution order.
func (c *Catalog) resolveName(n string) (canonical string, isApp, isBundle bool) {
	if c.Bundles[n] != nil {
		return n, false, true
	}
	if _, ok := c.Apps[n]; ok {
		return n, true, false
	}
	for name := range c.Bundles {
		if strings.EqualFold(name, n) {
			return name, false, true
		}
	}
	for name := range c.Apps {
		if strings.EqualFold(name, n) {
			return name, true, false
		}
	}
	return "", false, false
}

// emitSource probe-and-picks a source's drift candidates and appends a
// directory group (dir-mode source) or a file group (file-mode source) to f.
func (c *Catalog) emitSource(f *config.File, app, product string, src Source, key string, env Env, seenID map[string]bool) {
	// A source is file-mode when any location declares file:. Parse-time
	// validation guarantees uniformity for the bundled catalog; for a lenient
	// remote catalog, off-mode locations simply contribute no candidate below.
	fileMode := false
	for _, loc := range src.Locations {
		if len(loc.File) > 0 {
			fileMode = true
			break
		}
	}
	exists := env.Exists
	if fileMode {
		exists = env.ExistsFile
	}

	var picked []string
	// firstCandidate is the first location that has a path for this OS, in
	// declaration order (newest scheme first). It is the best-effort fallback
	// emitted when no candidate currently exists on disk.
	var firstCandidate string
	for _, loc := range src.Locations {
		m := loc.Dir
		if fileMode {
			m = loc.File
		}
		raw, ok := m[key]
		if !ok {
			continue
		}
		p := normalizeSep(expandPath(substituteProduct(raw, product), env.Home, env.Getenv), key)
		if firstCandidate == "" {
			firstCandidate = p
		}
		if exists != nil && exists(p) {
			picked = append(picked, p)
		}
	}
	if len(picked) == 0 {
		if firstCandidate == "" {
			return
		}
		picked = []string{firstCandidate}
	}

	id := groupID(app, product, src.ID, seenID)
	if fileMode {
		f.Files = append(f.Files, config.FileGroup{ID: id, Paths: picked})
		return
	}
	rec := false
	g := config.DirGroup{
		ID:        id,
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

// DefaultEnv builds the live Env: real OS, home dir, environment, and
// existence probes that report whether a glob matches at least one directory
// (Exists) or regular file (ExistsFile).
func DefaultEnv() Env {
	home, _ := os.UserHomeDir()
	return Env{
		OS:     runtime.GOOS,
		Home:   home,
		Getenv: os.Getenv,
		Exists: func(dirGlob string) bool {
			matches, err := filepath.Glob(dirGlob)
			if err != nil {
				return false
			}
			for _, m := range matches {
				if fi, err := os.Stat(m); err == nil && fi.IsDir() {
					return true
				}
			}
			return false
		},
		ExistsFile: func(pathGlob string) bool {
			matches, err := filepath.Glob(pathGlob)
			if err != nil {
				return false
			}
			for _, m := range matches {
				if fi, err := os.Stat(m); err == nil && !fi.IsDir() {
					return true
				}
			}
			return false
		},
	}
}
