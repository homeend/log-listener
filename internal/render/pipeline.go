package render

import (
	"fmt"
	"path/filepath"
	"regexp"
	"time"

	"log-listener/internal/config"
)

// Renderer is a compiled rendering rule.
type Renderer struct {
	Name      string
	lineRegex *regexp.Regexp
	template  *Template
	// nil means "no group constraint"; otherwise the file's group ID must be
	// a key with value true.
	groups    map[string]bool
	pathGlobs []string
}

// Compile turns a config.RendererSpec into a runtime Renderer.
func Compile(spec config.RendererSpec) (*Renderer, error) {
	if spec.LineRegex == "" {
		return nil, fmt.Errorf("renderer %q: line_regex is required", spec.Name)
	}
	re, err := regexp.Compile(spec.LineRegex)
	if err != nil {
		return nil, fmt.Errorf("renderer %q: line_regex: %w", spec.Name, err)
	}
	tpl, err := ParseTemplate(spec.Template)
	if err != nil {
		return nil, fmt.Errorf("renderer %q: template: %w", spec.Name, err)
	}
	r := &Renderer{Name: spec.Name, lineRegex: re, template: tpl}
	if spec.AppliesTo != nil {
		if len(spec.AppliesTo.Groups) > 0 {
			r.groups = make(map[string]bool, len(spec.AppliesTo.Groups))
			for _, g := range spec.AppliesTo.Groups {
				r.groups[g] = true
			}
		}
		r.pathGlobs = append([]string(nil), spec.AppliesTo.Paths...)
	}
	return r, nil
}

// Applies reports whether the renderer is allowed to act on the given group
// and path. AND semantics: both groups (if set) and paths (if set) must
// match.
func (r *Renderer) Applies(group, path string) bool {
	if r.groups != nil && !r.groups[group] {
		return false
	}
	if len(r.pathGlobs) == 0 {
		return true
	}
	for _, g := range r.pathGlobs {
		if ok, _ := filepath.Match(g, path); ok {
			return true
		}
		if ok, _ := filepath.Match(g, filepath.Base(path)); ok {
			return true
		}
	}
	return false
}

// Match runs the line through the regex. Returns the capture slice (with
// index 0 = full match) or nil if no match.
func (r *Renderer) Match(line string) []string {
	return r.lineRegex.FindStringSubmatch(line)
}

// Event is the typed renderer output. It includes the original raw line plus
// the parts produced by the matching renderer (or a single text part holding
// the raw line if no renderer matched).
type Event struct {
	Ts       time.Time
	File     string
	Group    string
	Raw      string
	Renderer string
	Captures []string
	Rendered []Part
}

// Pipeline holds the ordered list of compiled renderers plus the
// drop-unmatched switch.
type Pipeline struct {
	renderers []*Renderer
	drop      bool
}

// NewPipeline compiles all specs into a Pipeline. The order is preserved as
// declared.
func NewPipeline(specs []config.RendererSpec, dropUnmatched bool) (*Pipeline, error) {
	p := &Pipeline{drop: dropUnmatched}
	for _, s := range specs {
		r, err := Compile(s)
		if err != nil {
			return nil, err
		}
		p.renderers = append(p.renderers, r)
	}
	return p, nil
}

// Render attempts to match line against each renderer in declaration order.
// First match wins. Returns ok=false when no renderer matched and the
// pipeline was configured to drop unmatched lines.
func (p *Pipeline) Render(now time.Time, group, path, raw string) (Event, bool) {
	ev := Event{Ts: now, File: path, Group: group, Raw: raw}
	for _, r := range p.renderers {
		if !r.Applies(group, path) {
			continue
		}
		caps := r.Match(raw)
		if caps == nil {
			continue
		}
		ev.Renderer = r.Name
		ev.Captures = caps
		ev.Rendered = r.template.Execute(caps)
		return ev, true
	}
	if p.drop {
		return Event{}, false
	}
	ev.Rendered = []Part{{Type: "text", Value: raw}}
	return ev, true
}
