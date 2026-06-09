package render

import (
	"strings"
	"testing"
)

// fakeRF is a throwaway renderFunc for registry tests.
type fakeRF struct{ name string }

func (f fakeRF) Name() string                { return f.name }
func (fakeRF) Parse(raw string) (Part, bool) { return Part{Type: "text", Value: raw}, true }
func (fakeRF) Lines(v interface{}) []string  { return nil }

func TestRegisterAndLookup(t *testing.T) {
	registerRenderFunc(fakeRF{name: "test_reg_lookup"})
	if renderFuncs["test_reg_lookup"] == nil {
		t.Fatal("registered renderFunc not found in registry")
	}
}

func TestDuplicateRegistrationPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate registration should panic")
		}
	}()
	registerRenderFunc(fakeRF{name: "test_dup"})
	registerRenderFunc(fakeRF{name: "test_dup"}) // must panic
}

// upRender is a throwaway renderFunc proving pluggability: $up($N) uppercases
// the capture into a text Part, with ZERO edits to ParseTemplate/Execute.
type upRender struct{}

func (upRender) Name() string { return "up" }
func (upRender) Parse(raw string) (Part, bool) {
	return Part{Type: "text", Value: strings.ToUpper(raw)}, true
}
func (upRender) Lines(v interface{}) []string { return nil }

func TestPluggableRenderFuncParsesAndExecutes(t *testing.T) {
	registerRenderFunc(upRender{})
	tpl, err := ParseTemplate(`$up($1)`)
	if err != nil {
		t.Fatalf("ParseTemplate($up): %v", err)
	}
	parts, ok := tpl.Execute([]string{"full", "hello"})
	if !ok || len(parts) != 1 || parts[0].Value != "HELLO" {
		t.Fatalf("pluggable func: got %+v ok=%v", parts, ok)
	}
}

func TestUnknownRenderFuncErrors(t *testing.T) {
	if _, err := ParseTemplate(`$jsno($1)`); err == nil {
		t.Fatal("unknown render function must error (typo detection)")
	}
}

func TestLiteralWordsAreNotRenderCalls(t *testing.T) {
	for _, src := range []string{`format($1)`, `jsonish($1)`, `level: json`} {
		tpl, err := ParseTemplate(src)
		if err != nil {
			t.Fatalf("%q should parse as literal, got error %v", src, err)
		}
		parts, ok := tpl.Execute([]string{"full", "X"})
		if !ok {
			t.Fatalf("%q execute failed", src)
		}
		for _, p := range parts {
			if p.Type == "json" || p.Type == "xml" {
				t.Fatalf("%q must not produce a render part, got %+v", src, parts)
			}
		}
	}
}
