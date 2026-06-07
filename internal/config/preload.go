package config

import "strings"

// PreloadMode selects how a --preload file is interpreted.
type PreloadMode int

const (
	PreloadAuto    PreloadMode = iota // resolve by filename
	PreloadRaw                        // run lines through the pipeline
	PreloadCapture                    // reconstruct from [group] file: prefixes
)

// PreloadSpec is one --preload source. Group is the synthetic group for raw mode
// ("" → "preload"); it is ignored for capture mode (groups come from the file).
type PreloadSpec struct {
	Group string
	Path  string
	Mode  PreloadMode
}

// parsePreloadValue splits "[group=]path". A leading "group=" is recognized only
// when the part before the first '=' is non-empty and contains none of '/', '\',
// ':' — so Windows paths (C:\x) and group-prefixed Windows paths (api=C:\x) both
// parse correctly.
func parsePreloadValue(v string) (group, path string) {
	if eq := strings.IndexByte(v, '='); eq > 0 {
		if g := v[:eq]; !strings.ContainsAny(g, `/\:`) {
			return g, v[eq+1:]
		}
	}
	return "", v
}
