package discover

import "testing"

func TestHasMeta(t *testing.T) {
	cases := map[string]bool{
		"":           false,
		"foo":        false,
		"foo*":       true,
		"foo?":       true,
		"foo[abc]":   true,
		"/tmp/a-b-c": false,
		`\*literal`:  false, // escaped star
		"a/b*/c":     true,
	}
	for s, want := range cases {
		if got := HasMeta(s); got != want {
			t.Errorf("HasMeta(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestLiteralPrefix(t *testing.T) {
	cases := map[string]string{
		"/tmp/acp-*/sub":     "/tmp",
		"/tmp/*/sub":         "/tmp",
		"/a/b/c":             "/a/b/c",
		"/tmp":               "/tmp",
		"/*":                 "/",
		"rel/*":              "rel",
		"*":                  "",
		"a/b/c-*/end":        "a/b",
	}
	for in, want := range cases {
		if got := LiteralPrefix(in); got != want {
			t.Errorf("LiteralPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMatchesPath(t *testing.T) {
	cases := []struct {
		pat, path string
		want      bool
	}{
		{"/tmp/acp-*/sub", "/tmp/acp-123/sub", true},
		{"/tmp/acp-*/sub", "/tmp/acp-123/other", false},
		{"/tmp/acp-*/sub", "/tmp/acp-123/sub/deeper", false},
		{"/tmp/acp-*/sub", "/tmp/acp-123", false},
		{"/tmp/*", "/tmp/foo", true},
		{"/tmp/*", "/tmp/foo/bar", false},
		{"/tmp", "/tmp", true},
		{"/tmp", "/tmp/x", false},
		{"*", "anything", true},
		{"/tmp/[abc]-*/log", "/tmp/a-1/log", true},
		{"/tmp/[abc]-*/log", "/tmp/d-1/log", false},
	}
	for _, c := range cases {
		got, err := MatchesPath(c.pat, c.path)
		if err != nil {
			t.Errorf("MatchesPath(%q, %q) err: %v", c.pat, c.path, err)
			continue
		}
		if got != c.want {
			t.Errorf("MatchesPath(%q, %q) = %v, want %v", c.pat, c.path, got, c.want)
		}
	}
}

func TestPrefixMatchesPattern(t *testing.T) {
	cases := []struct {
		pat, path string
		want      bool
	}{
		// pattern = /tmp/acp-*/sub
		{"/tmp/acp-*/sub", "/tmp", true},
		{"/tmp/acp-*/sub", "/tmp/acp-NEW", true},
		{"/tmp/acp-*/sub", "/tmp/acp-NEW/sub", true},
		{"/tmp/acp-*/sub", "/tmp/other", false},
		{"/tmp/acp-*/sub", "/var", false},
		{"/tmp/acp-*/sub", "/tmp/a/b/c/d", false}, // deeper than pattern
		// pattern = /tmp/acp-*/acp/acp.log
		{"/tmp/acp-*/acp/acp.log", "/tmp/acp-1", true},
		{"/tmp/acp-*/acp/acp.log", "/tmp/acp-1/acp", true},
		{"/tmp/acp-*/acp/acp.log", "/tmp/acp-1/acp/acp.log", true},
		{"/tmp/acp-*/acp/acp.log", "/tmp/acp-1/acp/other.log", false},
		// root match
		{"/*", "/anything", true},
		{"/*", "/", true}, // empty segments
	}
	for _, c := range cases {
		got, err := PrefixMatchesPattern(c.pat, c.path)
		if err != nil {
			t.Errorf("PrefixMatchesPattern(%q, %q) err: %v", c.pat, c.path, err)
			continue
		}
		if got != c.want {
			t.Errorf("PrefixMatchesPattern(%q, %q) = %v, want %v", c.pat, c.path, got, c.want)
		}
	}
}
