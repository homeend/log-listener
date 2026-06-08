// Package searchmatch is the shared search predicate used by both the MCP-facing
// linebuf.Search and the interactive TUI search, so a query matches the same
// text the same way in both. Substring queries are smart-case (case-insensitive
// unless the query has an uppercase letter); regex queries are literal.
package searchmatch

import (
	"regexp"
	"strings"
	"unicode"
)

// Matcher is a compiled search predicate. A nil *Matcher, the zero value, and a
// Matcher from Compile("") all match nothing (neither literal nor re is set).
type Matcher struct {
	literal string         // case-sensitive substring path (non-empty when used)
	re      *regexp.Regexp // regex path, or the (?i)-wrapped fold path
}

// inactive reports whether the matcher matches nothing (nil / zero / empty
// query). Centralizes the guard so Match/Find/FindAll can never fall into a
// strings.Contains(text, "") == true / zero-width infinite-loop trap.
func (m *Matcher) inactive() bool {
	return m == nil || (m.re == nil && m.literal == "")
}

// Compile builds a Matcher. regex=true compiles query as a regexp (error on
// invalid). regex=false is smart-case substring: case-sensitive iff query has an
// uppercase letter, else case-insensitive (implemented as a (?i)-wrapped quoted
// regexp so Find offsets index the original text). Empty query matches nothing.
func Compile(query string, regex bool) (*Matcher, error) {
	if query == "" {
		return &Matcher{}, nil
	}
	if regex {
		re, err := regexp.Compile(query)
		if err != nil {
			return nil, err
		}
		return &Matcher{re: re}, nil
	}
	if hasUpper(query) {
		return &Matcher{literal: query}, nil
	}
	re, err := regexp.Compile("(?i)" + regexp.QuoteMeta(query))
	if err != nil {
		return nil, err
	}
	return &Matcher{re: re}, nil
}

func hasUpper(s string) bool {
	for _, r := range s {
		if unicode.IsUpper(r) {
			return true
		}
	}
	return false
}

// Match reports whether text matches.
func (m *Matcher) Match(text string) bool {
	if m.inactive() {
		return false
	}
	if m.re != nil {
		return m.re.MatchString(text)
	}
	return strings.Contains(text, m.literal)
}

// Find returns byte offsets [start,end) of the first match in text.
func (m *Matcher) Find(text string) (start, end int, ok bool) {
	if m.inactive() {
		return 0, 0, false
	}
	if m.re != nil {
		loc := m.re.FindStringIndex(text)
		if loc == nil {
			return 0, 0, false
		}
		return loc[0], loc[1], true
	}
	i := strings.Index(text, m.literal)
	if i < 0 {
		return 0, 0, false
	}
	return i, i + len(m.literal), true
}

// FindAll returns byte-offset [start,end) pairs for every (non-overlapping)
// match, advancing past zero-width matches so it always terminates.
func (m *Matcher) FindAll(text string) [][2]int {
	if m.inactive() {
		return nil
	}
	if m.re != nil {
		locs := m.re.FindAllStringIndex(text, -1)
		out := make([][2]int, 0, len(locs))
		for _, l := range locs {
			out = append(out, [2]int{l[0], l[1]})
		}
		return out
	}
	var out [][2]int
	off := 0
	for {
		i := strings.Index(text[off:], m.literal)
		if i < 0 {
			break
		}
		s := off + i
		e := s + len(m.literal)
		out = append(out, [2]int{s, e})
		off = e
	}
	return out
}
