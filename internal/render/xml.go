package render

import (
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

// renderXML pretty-prints the input XML. On parse failure it falls back to a
// text part containing the raw input.
func renderXML(s string) Part {
	s = strings.TrimSpace(s)
	if s == "" {
		return Part{Type: "text", Value: ""}
	}
	pretty, err := prettyXML(s)
	if err != nil {
		return Part{Type: "text", Value: s}
	}
	return Part{Type: "xml", Value: pretty}
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
