package catalog

import (
	"os"
	"regexp"
	"strings"
)

var winVarRE = regexp.MustCompile(`%([A-Za-z_][A-Za-z0-9_]*)%`)

// substituteProduct replaces every {product} placeholder with product.
func substituteProduct(raw, product string) string {
	return strings.ReplaceAll(raw, "{product}", product)
}

// normalizeSep rewrites path separators to the target OS's convention so a
// generated config reads naturally there: backslashes on Windows, forward
// slashes everywhere else. Expansion otherwise yields a mix — env-derived
// prefixes (the home dir, %LOCALAPPDATA%) carry the host's separator while
// catalog templates are authored with '/'. osKey is the resolution OS, not
// necessarily the host, so cross-OS resolution stays deterministic.
func normalizeSep(p, osKey string) string {
	if osKey == "windows" {
		return strings.ReplaceAll(p, "/", `\`)
	}
	return strings.ReplaceAll(p, `\`, "/")
}

// expandPath resolves ~, %WINVAR%, and $UNIXVAR against home/getenv. Unknown
// variables are left verbatim so a missing var produces a visibly-wrong path
// rather than a silently-empty one.
func expandPath(raw, home string, getenv func(string) string) string {
	if raw == "~" {
		return home
	}
	if strings.HasPrefix(raw, "~/") {
		raw = home + raw[1:]
	}
	raw = winVarRE.ReplaceAllStringFunc(raw, func(m string) string {
		name := m[1 : len(m)-1]
		if v := getenv(name); v != "" {
			return v
		}
		return m
	})
	raw = os.Expand(raw, func(name string) string {
		if v := getenv(name); v != "" {
			return v
		}
		return "${" + name + "}" // os.Expand strips $; restore unknowns approximately
	})
	return raw
}
