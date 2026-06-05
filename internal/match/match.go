// Package match provides a reusable predicate over a log line's content, the
// source file's basename, and its full path. Each dimension is matched either
// by an exact literal or by a regular expression; at least one dimension must
// be set, and all set dimensions must match (AND).
package match

import (
	"fmt"
	"path/filepath"
	"regexp"
)

// Spec is the YAML-agnostic matcher definition. For each dimension set either
// the literal field OR the *Regex field, never both. At least one must be set.
type Spec struct {
	Line, LineRegex string
	Name, NameRegex string
	Path, PathRegex string
}

// Matcher is a compiled Spec.
type Matcher struct {
	line       string
	lineRE     *regexp.Regexp
	name       string
	nameRE     *regexp.Regexp
	path       string
	pathRE     *regexp.Regexp
	hasLineLit bool
	hasNameLit bool
	hasPathLit bool
}

// Compile validates and compiles a Spec.
func Compile(s Spec) (*Matcher, error) {
	m := &Matcher{}
	set := 0

	if s.Line != "" && s.LineRegex != "" {
		return nil, fmt.Errorf("matcher: set only one of line or line_regex")
	}
	if s.Line != "" {
		m.line, m.hasLineLit = s.Line, true
		set++
	}
	if s.LineRegex != "" {
		re, err := regexp.Compile(s.LineRegex)
		if err != nil {
			return nil, fmt.Errorf("matcher: line_regex: %w", err)
		}
		m.lineRE = re
		set++
	}

	if s.Name != "" && s.NameRegex != "" {
		return nil, fmt.Errorf("matcher: set only one of name or name_regex")
	}
	if s.Name != "" {
		m.name, m.hasNameLit = s.Name, true
		set++
	}
	if s.NameRegex != "" {
		re, err := regexp.Compile(s.NameRegex)
		if err != nil {
			return nil, fmt.Errorf("matcher: name_regex: %w", err)
		}
		m.nameRE = re
		set++
	}

	if s.Path != "" && s.PathRegex != "" {
		return nil, fmt.Errorf("matcher: set only one of path or path_regex")
	}
	if s.Path != "" {
		m.path, m.hasPathLit = s.Path, true
		set++
	}
	if s.PathRegex != "" {
		re, err := regexp.Compile(s.PathRegex)
		if err != nil {
			return nil, fmt.Errorf("matcher: path_regex: %w", err)
		}
		m.pathRE = re
		set++
	}

	if set == 0 {
		return nil, fmt.Errorf("matcher: at least one of line/name/path (or their *_regex form) must be set")
	}
	return m, nil
}

// HasLineRegex reports whether a line_regex criterion is set (used to validate
// renderer references, which need capture groups).
func (m *Matcher) HasLineRegex() bool { return m.lineRE != nil }

// Match reports whether path+line satisfy every set criterion (AND). caps
// holds the line_regex submatches (caps[0] is the whole match) when a line
// regex is set and matched; otherwise caps is nil.
func (m *Matcher) Match(path, line string) (caps []string, ok bool) {
	base := filepath.Base(path)

	if m.hasNameLit && base != m.name {
		return nil, false
	}
	if m.nameRE != nil && !m.nameRE.MatchString(base) {
		return nil, false
	}
	if m.hasPathLit && path != m.path {
		return nil, false
	}
	if m.pathRE != nil && !m.pathRE.MatchString(path) {
		return nil, false
	}
	if m.hasLineLit && line != m.line {
		return nil, false
	}
	if m.lineRE != nil {
		caps = m.lineRE.FindStringSubmatch(line)
		if caps == nil {
			return nil, false
		}
	}
	return caps, true
}
