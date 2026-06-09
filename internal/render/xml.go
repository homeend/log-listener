package render

import (
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

type xmlRender struct{}

func init() { registerRenderFunc(xmlRender{}) }

func (xmlRender) Name() string { return "xml" }

// Parse pretty-prints the capture as XML. ok=false means it is not valid XML
// (the renderer falls through). Empty input is an empty text Part.
func (xmlRender) Parse(raw string) (Part, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return Part{Type: "text", Value: ""}, true
	}
	pretty, err := prettyXML(s)
	if err != nil {
		return Part{Type: "text", Value: s}, false
	}
	return Part{Type: "xml", Value: pretty}, true
}

// Lines splits the pretty-printed XML string into block rows. A non-string
// value (shouldn't happen for an xml Part) yields no rows.
func (xmlRender) Lines(v interface{}) []string {
	s, ok := v.(string)
	if !ok {
		return nil
	}
	return strings.Split(s, "\n")
}

func prettyXML(in string) (string, error) {
	dec := xml.NewDecoder(strings.NewReader(in))
	var out strings.Builder
	enc := xml.NewEncoder(&out)
	enc.Indent("", "  ")
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		if err := enc.EncodeToken(tok); err != nil {
			return "", err
		}
	}
	if err := enc.Flush(); err != nil {
		return "", err
	}
	return out.String(), nil
}
