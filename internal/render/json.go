package render

import (
	"encoding/json"
	"strings"
)

// renderJSON parses the input as JSON. ok=false means the input is not valid
// JSON (the caller should treat the renderer as non-matching). Empty input is
// ok (an empty text part).
func renderJSON(s string) (Part, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Part{Type: "text", Value: ""}, true
	}
	var v interface{}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return Part{Type: "text", Value: s}, false
	}
	return Part{Type: "json", Value: v}, true
}
