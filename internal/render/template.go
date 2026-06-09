// Package render compiles and applies the log-listener line renderer pipeline:
// regex-matched lines pass through a template DSL that produces typed Part
// outputs (text/json/xml). First match wins by declaration order.
package render

import (
	"fmt"
	"strconv"
	"strings"
)

// Part is a single typed segment of a rendered line.
//
//	"text" → Value is a string
//	"json" → Value is the decoded JSON value (interface{}/map/slice/...)
//	"xml"  → Value is a pretty-printed XML string
//
// When the json or xml decoder fails on its input, the part falls back to
// {Type:"text", Value:<raw input>} so output is never lost.
type Part struct {
	Type  string      `json:"type"`
	Value interface{} `json:"value"`
}

// Template is a parsed renderer template.
type Template struct {
	parts []templatePart
}

type templatePart struct {
	kind  partKind
	text  string
	group int
	rf    renderFunc // set when kind == partRender
}

type partKind int

const (
	partLiteral partKind = iota
	partCapture
	partRender
)

// ParseTemplate parses the template DSL: literal text + $N (capture group)
// + json($N) + xml($N). Backslash escapes: \n, \t, \\. Double-$ escapes a
// literal $.
func ParseTemplate(src string) (*Template, error) {
	t := &Template{}
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			t.parts = append(t.parts, templatePart{kind: partLiteral, text: lit.String()})
			lit.Reset()
		}
	}
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == '\\' && i+1 < len(src):
			switch src[i+1] {
			case 'n':
				lit.WriteByte('\n')
			case 't':
				lit.WriteByte('\t')
			case 'r':
				lit.WriteByte('\r')
			case '\\':
				lit.WriteByte('\\')
			default:
				lit.WriteByte(src[i+1])
			}
			i += 2
		case startsWith(src, i, "json("):
			flush()
			n, end, err := parseRenderCall(src, i+len("json("))
			if err != nil {
				return nil, err
			}
			t.parts = append(t.parts, templatePart{kind: partRender, group: n, rf: renderFuncs["json"]})
			i = end
		case startsWith(src, i, "xml("):
			flush()
			n, end, err := parseRenderCall(src, i+len("xml("))
			if err != nil {
				return nil, err
			}
			t.parts = append(t.parts, templatePart{kind: partRender, group: n, rf: renderFuncs["xml"]})
			i = end
		case c == '$' && i+1 < len(src):
			nx := src[i+1]
			switch {
			case nx == '$':
				lit.WriteByte('$')
				i += 2
			case nx >= '0' && nx <= '9':
				flush()
				j := i + 1
				for j < len(src) && src[j] >= '0' && src[j] <= '9' {
					j++
				}
				n, _ := strconv.Atoi(src[i+1 : j])
				t.parts = append(t.parts, templatePart{kind: partCapture, group: n})
				i = j
			default:
				return nil, fmt.Errorf("template: invalid escape $%c at %d", nx, i)
			}
		default:
			lit.WriteByte(c)
			i++
		}
	}
	flush()
	return t, nil
}

func startsWith(s string, i int, prefix string) bool {
	return i+len(prefix) <= len(s) && s[i:i+len(prefix)] == prefix
}

func parseRenderCall(src string, i int) (group, end int, err error) {
	if i >= len(src) || src[i] != '$' {
		return 0, 0, fmt.Errorf("template: expected $N inside renderer call at %d", i)
	}
	j := i + 1
	for j < len(src) && src[j] >= '0' && src[j] <= '9' {
		j++
	}
	if j == i+1 {
		return 0, 0, fmt.Errorf("template: expected digit after $ in renderer call at %d", i+1)
	}
	n, _ := strconv.Atoi(src[i+1 : j])
	if j >= len(src) || src[j] != ')' {
		return 0, 0, fmt.Errorf("template: missing ) in renderer call at %d", j)
	}
	return n, j + 1, nil
}

// Execute renders the template against the given regex captures. captures[0]
// is the full match; captures[1..N] are the parenthesized groups. Out-of-range
// $N references expand to empty string. ok=false means a json()/xml() call
// could not parse its capture — the caller should treat the renderer as not
// matching and fall through.
func (t *Template) Execute(captures []string) ([]Part, bool) {
	var parts []Part
	var text strings.Builder
	flushText := func() {
		if text.Len() > 0 {
			parts = append(parts, Part{Type: "text", Value: text.String()})
			text.Reset()
		}
	}
	capture := func(n int) string {
		if n < 0 || n >= len(captures) {
			return ""
		}
		return captures[n]
	}
	for _, p := range t.parts {
		switch p.kind {
		case partLiteral:
			text.WriteString(p.text)
		case partCapture:
			text.WriteString(capture(p.group))
		case partRender:
			flushText()
			part, ok := p.rf.Parse(capture(p.group))
			if !ok {
				return nil, false
			}
			parts = append(parts, part)
		}
	}
	flushText()
	return parts, true
}
