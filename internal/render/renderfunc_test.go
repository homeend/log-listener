package render

import "testing"

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
