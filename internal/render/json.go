package render

import (
	"encoding/json"
	"strings"
)

// renderJSON parses the input as JSON. On success the Part.Value holds the
// decoded value (Go map/slice/primitive). On parse failure it falls back to
// a text part with the raw input, so output is never dropped.
func renderJSON(s string) Part {
	s = strings.TrimSpace(s)
	if s == "" {
		return Part{Type: "text", Value: ""}
	}
	var v interface{}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return Part{Type: "text", Value: s}
	}
	return Part{Type: "json", Value: v}
}
