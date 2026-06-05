package render

import (
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

// renderXML pretty-prints the input XML. ok=false means the input is not valid
// XML (the caller should treat the renderer as non-matching). Empty input is ok.
func renderXML(s string) (Part, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Part{Type: "text", Value: ""}, true
	}
	pretty, err := prettyXML(s)
	if err != nil {
		return Part{Type: "text", Value: s}, false
	}
	return Part{Type: "xml", Value: pretty}, true
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
