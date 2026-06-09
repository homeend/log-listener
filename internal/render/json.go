package render

import (
	"encoding/json"
	"strings"
)

type jsonRender struct{}

func init() { registerRenderFunc(jsonRender{}) }

func (jsonRender) Name() string { return "json" }

// Parse decodes the capture as JSON. ok=false means it is not valid JSON (the
// renderer falls through). Empty input is an empty text Part.
func (jsonRender) Parse(raw string) (Part, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return Part{Type: "text", Value: ""}, true
	}
	var v interface{}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return Part{Type: "text", Value: s}, false
	}
	return Part{Type: "json", Value: v}, true
}

// Lines pretty-prints the decoded value into block rows. A marshal failure
// (essentially unreachable for a value that came from Unmarshal) yields no rows.
func (jsonRender) Lines(v interface{}) []string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil
	}
	return strings.Split(string(b), "\n")
}
