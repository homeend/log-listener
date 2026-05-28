package discover

import (
	"path/filepath"
	"strings"
)

// HasMeta reports whether s contains any glob metacharacter recognized
// by path.Match: '*', '?', '['. Escaped meta (`\*`, `\?`) is ignored.
func HasMeta(s string) bool {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '*', '?', '[':
			return true
		case '\\':
			i++ // skip the next char so `\*` isn't treated as meta
		}
	}
	return false
}

// LiteralPrefix returns the longest leading literal directory prefix of
// pattern (everything before the first segment that contains a meta
// character). Used by the watcher to know which existing directory to
// add to fsnotify so future Creates of pattern-matching children fire.
//
//	"/tmp/acp-*/sub" → "/tmp"
//	"/a/b/c"         → "/a/b/c"
//	"/*"             → "/"
//	"rel/*"          → "rel"
//	"*"              → ""
func LiteralPrefix(pattern string) string {
	sep := string(filepath.Separator)
	segs := strings.Split(pattern, sep)
	for i, seg := range segs {
		if HasMeta(seg) {
			if i == 0 {
				if strings.HasPrefix(pattern, sep) {
					return sep
				}
				return ""
			}
			prefix := strings.Join(segs[:i], sep)
			if prefix == "" {
				// All preceding segments were empty — that's the
				// leading-"/" case (e.g. pattern "/*" → segs ["","*"]).
				return sep
			}
			return prefix
		}
	}
	return pattern
}

// MatchesPath reports whether path matches pattern segment-by-segment.
// Both must have the same number of non-empty segments. Differs from
// filepath.Match in that '*' never crosses a '/' boundary even when
// inside a character class.
func MatchesPath(pattern, path string) (bool, error) {
	pSegs := splitClean(pattern)
	aSegs := splitClean(path)
	if len(pSegs) != len(aSegs) {
		return false, nil
	}
	for i := range pSegs {
		m, err := filepath.Match(pSegs[i], aSegs[i])
		if err != nil {
			return false, err
		}
		if !m {
			return false, nil
		}
	}
	return true, nil
}

// PrefixMatchesPattern reports whether path matches the first N segments
// of pattern, where N is the number of segments in path. Used by the
// watcher to decide whether to add a watch on a newly-created directory:
// even if it's not (yet) a full pattern match, future descendants
// underneath it might be — so we need to watch it for child Creates.
//
// Examples (pattern = "/tmp/acp-*/sub"):
//
//	"/tmp"            → true   (segments [tmp] match pattern[0])
//	"/tmp/acp-NEW"    → true   (matches pattern[0..1])
//	"/tmp/acp-NEW/sub"→ true   (full match)
//	"/tmp/other"      → false  (pattern[1] = "acp-*" rejects "other")
//	"/var"            → false  (pattern[0] = "tmp" rejects "var")
//	"/tmp/a/b/c/d"    → false  (path deeper than pattern)
func PrefixMatchesPattern(pattern, path string) (bool, error) {
	pSegs := splitClean(pattern)
	aSegs := splitClean(path)
	if len(aSegs) > len(pSegs) {
		return false, nil
	}
	for i, aSeg := range aSegs {
		m, err := filepath.Match(pSegs[i], aSeg)
		if err != nil {
			return false, err
		}
		if !m {
			return false, nil
		}
	}
	return true, nil
}

func splitClean(p string) []string {
	parts := strings.Split(p, string(filepath.Separator))
	out := parts[:0]
	for _, s := range parts {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
