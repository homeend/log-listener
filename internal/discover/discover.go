// Package discover finds candidate log files from configured directory and
// file groups, applies file-level filters, and produces first-match-wins
// assignments of paths to groups.
package discover

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

// GroupKind distinguishes directory groups (recursive walk + filter) from
// file groups (glob expansion, unfiltered).
type GroupKind int

const (
	GroupDir GroupKind = iota
	GroupFile
)

// FileFilter constrains which files within a directory group are accepted.
// A zero-value field means "no constraint" for that dimension.
type FileFilter struct {
	NameRegex    *regexp.Regexp
	ExcludeRegex *regexp.Regexp
	Older        time.Time
	Younger      time.Time
}

// Allow reports whether a file with the given basename and mtime passes the
// filter. A nil filter accepts everything.
func (f *FileFilter) Allow(name string, mtime time.Time) bool {
	if f == nil {
		return true
	}
	if f.NameRegex != nil && !f.NameRegex.MatchString(name) {
		return false
	}
	if f.ExcludeRegex != nil && f.ExcludeRegex.MatchString(name) {
		return false
	}
	if !f.Older.IsZero() && !mtime.Before(f.Older) {
		return false
	}
	if !f.Younger.IsZero() && !mtime.After(f.Younger) {
		return false
	}
	return true
}

// Group is a directory or file group from configuration.
type Group struct {
	ID        string
	Kind      GroupKind
	Paths     []string
	Recursive bool
	Filter    *FileFilter
}

// Assignment is a single file's binding to its owning group.
type Assignment struct {
	Path      string
	GroupID   string
	GroupKind GroupKind
	ModTime   time.Time
}

// ListCandidates returns absolute paths to all candidate files for the group,
// before any filter is applied. Directory groups are walked (recursively if
// configured); file groups are glob-expanded.
func ListCandidates(g Group) ([]candidate, error) {
	out := []candidate{}
	seen := map[string]struct{}{}

	add := func(path string, info fs.FileInfo) {
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		if _, dup := seen[abs]; dup {
			return
		}
		seen[abs] = struct{}{}
		out = append(out, candidate{path: abs, mod: info.ModTime(), name: info.Name()})
	}

	switch g.Kind {
	case GroupDir:
		for _, root := range g.Paths {
			info, err := statPath(root)
			if err != nil {
				return nil, fmt.Errorf("discover: %s: %w", root, err)
			}
			if !info.IsDir() {
				return nil, fmt.Errorf("discover: %s is not a directory", root)
			}
			walkFn := func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					if !g.Recursive && p != root {
						return fs.SkipDir
					}
					return nil
				}
				fi, ferr := d.Info()
				if ferr != nil {
					return ferr
				}
				add(p, fi)
				return nil
			}
			if err := filepath.WalkDir(root, walkFn); err != nil {
				return nil, fmt.Errorf("discover: walk %s: %w", root, err)
			}
		}
	case GroupFile:
		for _, pat := range g.Paths {
			matches, err := filepath.Glob(pat)
			if err != nil {
				return nil, fmt.Errorf("discover: bad glob %q: %w", pat, err)
			}
			for _, m := range matches {
				info, err := statPath(m)
				if err != nil {
					continue
				}
				if info.IsDir() {
					continue
				}
				add(m, info)
			}
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out, nil
}

type candidate struct {
	path string
	name string
	mod  time.Time
}

// Assign walks groups in declaration order and assigns each candidate file to
// the first group whose filter accepts it (combined with the global filter,
// for directory groups). File groups are accepted unconditionally.
func Assign(groups []*Group, global *FileFilter) ([]Assignment, error) {
	owned := map[string]struct{}{}
	out := []Assignment{}

	for _, g := range groups {
		cands, err := ListCandidates(*g)
		if err != nil {
			return nil, err
		}
		for _, c := range cands {
			if _, taken := owned[c.path]; taken {
				continue
			}
			if g.Kind == GroupDir {
				if !g.Filter.Allow(c.name, c.mod) {
					continue
				}
				if !global.Allow(c.name, c.mod) {
					continue
				}
			}
			owned[c.path] = struct{}{}
			out = append(out, Assignment{
				Path: c.path, GroupID: g.ID, GroupKind: g.Kind, ModTime: c.mod,
			})
		}
	}
	return out, nil
}
