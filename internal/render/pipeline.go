package render

import (
	"fmt"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/homeend/log-listener/internal/config"
	"github.com/homeend/log-listener/internal/match"
)

// toMatchSpec converts a config matcher spec into a match.Spec.
func toMatchSpec(s config.MatcherSpec) match.Spec {
	return match.Spec{
		Line: s.Line, LineRegex: s.LineRegex,
		Name: s.Name, NameRegex: s.NameRegex,
		Path: s.Path, PathRegex: s.PathRegex,
	}
}

// Renderer is a compiled rendering rule. The matcher provides the content
// match + template captures; applies_to (groups/pathGlobs) scopes it.
type Renderer struct {
	Name      string
	matcher   *match.Matcher
	template  *Template
	groups    map[string]bool
	pathGlobs []string
}

// Compile turns a config.RendererSpec into a runtime Renderer. Exactly one of
// LineRegex or Matcher must be set. A matcher used here must carry a
// line_regex (captures feed the template).
func Compile(spec config.RendererSpec, matchers map[string]config.MatcherSpec) (*Renderer, error) {
	hasLine := spec.LineRegex != ""
	hasMatcher := spec.Matcher != ""
	if hasLine == hasMatcher {
		return nil, fmt.Errorf("renderer %q: set exactly one of line_regex or matcher", spec.Name)
	}

	var ms match.Spec
	if hasMatcher {
		cm, ok := matchers[spec.Matcher]
		if !ok {
			return nil, fmt.Errorf("renderer %q: unknown matcher %q", spec.Name, spec.Matcher)
		}
		ms = toMatchSpec(cm)
	} else {
		ms = match.Spec{LineRegex: spec.LineRegex}
	}

	m, err := match.Compile(ms)
	if err != nil {
		return nil, fmt.Errorf("renderer %q: %w", spec.Name, err)
	}
	if !m.HasLineRegex() {
		return nil, fmt.Errorf("renderer %q: matcher %q has no line_regex (nothing to capture)", spec.Name, spec.Matcher)
	}

	tpl, err := ParseTemplate(spec.Template)
	if err != nil {
		return nil, fmt.Errorf("renderer %q: template: %w", spec.Name, err)
	}

	r := &Renderer{Name: spec.Name, matcher: m, template: tpl}
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

// Applies reports whether the renderer's applies_to scope admits group+path.
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

// Match runs the matcher against path+line. Returns the capture slice
// (index 0 = full match) or nil if it does not match.
func (r *Renderer) Match(path, line string) []string {
	caps, ok := r.matcher.Match(path, line)
	if !ok {
		return nil
	}
	return caps
}

// Event is the typed renderer output. It includes the original raw line plus
// the parts produced by the matching renderer (or a single text part holding
// the raw line if no renderer matched).
type Event struct {
	ID       string    `json:"id,omitempty"`
	Ts       time.Time `json:"ts"`
	File     string    `json:"file"`
	Group    string    `json:"group"`
	Raw      string    `json:"raw"`
	Renderer string    `json:"renderer,omitempty"`
	Captures []string  `json:"captures,omitempty"`
	Rendered []Part    `json:"rendered"`
}

// Pipeline holds the ordered list of compiled renderers plus the
// drop-unmatched switch. Each renderer has a parallel atomic.Bool
// (`enabled`) so the TUI can flip individual renderers on/off
// concurrently with Render without locking.
type Pipeline struct {
	renderers []*Renderer
	enabled   []*atomic.Bool
	mutes     []*MuteRule
	drop      bool
}

// NewPipeline compiles renderer specs (resolving matcher references against
// the matchers library) and mute specs into a Pipeline. Renderer order is
// preserved. Each renderer starts enabled unless its spec sets StartOff.
func NewPipeline(specs []config.RendererSpec, matchers map[string]config.MatcherSpec, mutes []config.MuteSpec, dropUnmatched bool) (*Pipeline, error) {
	p := &Pipeline{drop: dropUnmatched}
	for _, s := range specs {
		r, err := Compile(s, matchers)
		if err != nil {
			return nil, err
		}
		flag := &atomic.Bool{}
		flag.Store(!s.StartOff)
		p.renderers = append(p.renderers, r)
		p.enabled = append(p.enabled, flag)
	}
	for _, ms := range mutes {
		mr, err := compileMute(ms, matchers)
		if err != nil {
			return nil, err
		}
		p.mutes = append(p.mutes, mr)
	}
	return p, nil
}

// Render attempts to match line against each renderer in declaration order.
// First match wins. Disabled renderers are skipped, so a line that would
// have been handled by a disabled renderer falls through to the next
// matching one (or to raw text, or to drop). Returns ok=false when no
// renderer matched and the pipeline was configured to drop unmatched lines.
func (p *Pipeline) Render(now time.Time, group, path, raw string) (Event, bool) {
	for _, mr := range p.mutes {
		if mr.Mutes(group, path, raw) {
			return Event{}, false
		}
	}
	ev := Event{Ts: now, File: path, Group: group, Raw: raw}
	for i, r := range p.renderers {
		if !p.enabled[i].Load() {
			continue
		}
		if !r.Applies(group, path) {
			continue
		}
		caps := r.Match(path, raw)
		if caps == nil {
			continue
		}
		parts, ok := r.template.Execute(caps)
		if !ok {
			// A json()/xml() call couldn't parse — the renderer doesn't apply.
			continue
		}
		ev.Renderer = r.Name
		ev.Captures = caps
		ev.Rendered = parts
		return ev, true
	}
	if p.drop {
		return Event{}, false
	}
	ev.Rendered = []Part{{Type: "text", Value: raw}}
	return ev, true
}

// SetRendererEnabled toggles the i-th renderer's enable flag. Indices
// out of range are silent no-ops so callers can pass through user-
// supplied indices without bounds-checking.
func (p *Pipeline) SetRendererEnabled(i int, on bool) {
	if i < 0 || i >= len(p.enabled) {
		return
	}
	p.enabled[i].Store(on)
}

// IsEnabled reports the current enable state of the i-th renderer.
// Out-of-range indices return false.
func (p *Pipeline) IsEnabled(i int) bool {
	if i < 0 || i >= len(p.enabled) {
		return false
	}
	return p.enabled[i].Load()
}

// RendererCount returns the number of compiled renderers.
func (p *Pipeline) RendererCount() int { return len(p.renderers) }

// RendererName returns the i-th renderer's name. Empty for out-of-range.
func (p *Pipeline) RendererName(i int) string {
	if i < 0 || i >= len(p.renderers) {
		return ""
	}
	return p.renderers[i].Name
}

// MuteRule drops a line before rendering when its matcher matches and its
// applies_to scope (group ids + path globs, AND) admits the line.
type MuteRule struct {
	id        string
	matcher   *match.Matcher
	groups    map[string]bool
	pathGlobs []string
}

// Mutes reports whether the rule drops the given line.
func (mr *MuteRule) Mutes(group, path, line string) bool {
	if mr.groups != nil && !mr.groups[group] {
		return false
	}
	if len(mr.pathGlobs) > 0 {
		matched := false
		for _, g := range mr.pathGlobs {
			if ok, _ := filepath.Match(g, path); ok {
				matched = true
				break
			}
			if ok, _ := filepath.Match(g, filepath.Base(path)); ok {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	_, ok := mr.matcher.Match(path, line)
	return ok
}

func inlineEmpty(s config.MatcherSpec) bool {
	return s.Line == "" && s.LineRegex == "" &&
		s.Name == "" && s.NameRegex == "" &&
		s.Path == "" && s.PathRegex == ""
}

func compileMute(ms config.MuteSpec, matchers map[string]config.MatcherSpec) (*MuteRule, error) {
	id := ms.ID
	if id == "" {
		id = "(unnamed)"
	}
	hasRef := ms.Matcher != ""
	hasInline := !inlineEmpty(ms.MatcherSpec)
	if hasRef == hasInline {
		return nil, fmt.Errorf("mute %q: set exactly one of matcher (reference) or inline matcher fields", id)
	}

	var spec match.Spec
	if hasRef {
		cm, ok := matchers[ms.Matcher]
		if !ok {
			return nil, fmt.Errorf("mute %q: unknown matcher %q", id, ms.Matcher)
		}
		spec = toMatchSpec(cm)
	} else {
		spec = toMatchSpec(ms.MatcherSpec)
	}

	m, err := match.Compile(spec)
	if err != nil {
		return nil, fmt.Errorf("mute %q: %w", id, err)
	}

	mr := &MuteRule{id: ms.ID, matcher: m}
	if ms.AppliesTo != nil {
		if len(ms.AppliesTo.Groups) > 0 {
			mr.groups = make(map[string]bool, len(ms.AppliesTo.Groups))
			for _, g := range ms.AppliesTo.Groups {
				mr.groups[g] = true
			}
		}
		mr.pathGlobs = append([]string(nil), ms.AppliesTo.Paths...)
	}
	return mr, nil
}
