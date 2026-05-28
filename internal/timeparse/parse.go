// Package timeparse turns user-supplied date strings and relative durations
// into a time.Time anchor used for file mtime filtering.
package timeparse

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var absoluteLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

var relativeRE = regexp.MustCompile(`^(\d+)([smhdw])$`)

// Parse interprets s as either an absolute date/datetime or a relative
// duration. Relative durations (e.g. "1h", "2d", "5w") are subtracted from
// now to yield the anchor time. Supported units: s, m, h, d (days), w (weeks).
func Parse(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("timeparse: empty input")
	}

	if m := relativeRE.FindStringSubmatch(s); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return time.Time{}, fmt.Errorf("timeparse: %q: %w", s, err)
		}
		var d time.Duration
		switch m[2] {
		case "s":
			d = time.Duration(n) * time.Second
		case "m":
			d = time.Duration(n) * time.Minute
		case "h":
			d = time.Duration(n) * time.Hour
		case "d":
			d = time.Duration(n) * 24 * time.Hour
		case "w":
			d = time.Duration(n) * 7 * 24 * time.Hour
		}
		return now.Add(-d), nil
	}

	for _, layout := range absoluteLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("timeparse: cannot parse %q as date or duration", s)
}
