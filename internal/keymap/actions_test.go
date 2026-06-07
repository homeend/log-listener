package keymap

import "testing"

func TestAllActionsUniqueAndNonEmpty(t *testing.T) {
	seen := map[Action]bool{}
	for _, d := range AllActions {
		if d.Action == "" {
			t.Fatalf("empty action in AllActions")
		}
		if d.Title == "" {
			t.Errorf("action %q has empty Title", d.Action)
		}
		if seen[d.Action] {
			t.Errorf("duplicate action %q", d.Action)
		}
		seen[d.Action] = true
	}
	if len(AllActions) != 34 {
		t.Errorf("expected 34 named actions, got %d", len(AllActions))
	}
}
