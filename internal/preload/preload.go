// Package preload seeds the log-listener buffer from a file before tailing
// starts: raw log lines run through the renderer pipeline, or a saved
// screen-log-listener-* capture reconstructed faithfully (no re-render).
package preload

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/homeend/log-listener/internal/config"
	"github.com/homeend/log-listener/internal/render"
)

// RenderFunc renders one raw line (a closure over the pipeline, supplied by main).
type RenderFunc func(group, file, line string) (render.Event, bool)

// ResolveMode maps PreloadAuto to Raw/Capture by basename. The S export always
// writes screen-log-listener-<ts>.txt, so that prefix is the reliable signal
// (content detection misfires on common bracket-prefixed logs).
func ResolveMode(m config.PreloadMode, path string) config.PreloadMode {
	if m != config.PreloadAuto {
		return m
	}
	if strings.HasPrefix(filepath.Base(path), "screen-log-listener-") {
		return config.PreloadCapture
	}
	return config.PreloadRaw
}

// captureHeadRE matches a saved head row "[group] file: body". The file group is
// non-greedy, so it stops at the FIRST ": " (a body containing ": " survives).
var captureHeadRE = regexp.MustCompile(`^\[([^\]]*)\] (.+?): (.*)$`)

// ParseCapture reconstructs events from saved-capture lines: head rows open a
// new event; other rows are no-prefix continuations folded into the open event
// as embedded newlines (so appendEvent re-decomposes them into block rows).
func ParseCapture(lines []string) []render.Event {
	var events []render.Event
	open := -1
	for _, ln := range lines {
		if m := captureHeadRE.FindStringSubmatch(ln); m != nil {
			events = append(events, render.Event{
				Group: m[1], File: m[2], Raw: m[3],
				Rendered: []render.Part{{Type: "text", Value: m[3]}},
			})
			open = len(events) - 1
			continue
		}
		if open < 0 {
			events = append(events, render.Event{
				Rendered: []render.Part{{Type: "text", Value: ln}},
			})
			open = len(events) - 1
			continue
		}
		txt := events[open].Rendered[0].Value.(string) + "\n" + ln
		events[open].Rendered[0].Value = txt
		events[open].Raw = txt
	}
	return events
}

// Load reads spec.Path and returns the events to seed. mode must be resolved
// (Raw or Capture). Raw lines go through renderFn; capture lines reconstruct.
func Load(spec config.PreloadSpec, mode config.PreloadMode, renderFn RenderFunc) ([]render.Event, error) {
	data, err := os.ReadFile(spec.Path)
	if err != nil {
		return nil, fmt.Errorf("preload %s: %w", spec.Path, err)
	}
	lines := splitLines(string(data))
	if mode == config.PreloadCapture {
		return ParseCapture(lines), nil
	}
	group := spec.Group
	if group == "" {
		group = "preload"
	}
	base := filepath.Base(spec.Path)
	var events []render.Event
	for _, ln := range lines {
		if ev, ok := renderFn(group, base, ln); ok {
			events = append(events, ev)
		}
	}
	return events, nil
}

// splitLines normalizes CRLF and drops a single trailing newline, so a file
// ending in "\n" doesn't yield a trailing empty line.
func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
