package render

// renderFunc is a named, pluggable render-call usable in the template DSL as
// $name($N). Name() is BOTH the DSL keyword and the Part.Type produced on a
// successful parse, so ParseTemplate, Execute, and DecomposeLines can all
// dispatch by name through the registry. Implementations live one-per-file and
// self-register via init().
type renderFunc interface {
	// Name is the DSL keyword (after '$') and the Part.Type emitted on a
	// successful non-empty parse. Must be ASCII [A-Za-z][A-Za-z0-9]*.
	Name() string

	// Parse turns a capture into a Part. ok=false means "not my type" — the
	// renderer falls through (first-match-wins). Empty/whitespace input returns
	// an empty text Part with ok=true. On success: Part{Type: Name(), Value}, true.
	Parse(raw string) (Part, bool)

	// Lines turns a successful Part's Value into finished display rows (already
	// split; every type-specific guard inside). nil/empty → no rows.
	Lines(v interface{}) []string
}

// renderFuncs is the name-keyed registry, populated by each render file's init().
var renderFuncs = map[string]renderFunc{}

// registerRenderFunc adds r; a duplicate name panics at init so a collision is
// caught at startup instead of being silently shadowed.
func registerRenderFunc(r renderFunc) {
	if _, dup := renderFuncs[r.Name()]; dup {
		panic("render: duplicate renderFunc " + r.Name())
	}
	renderFuncs[r.Name()] = r
}
