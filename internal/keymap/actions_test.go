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
	if len(AllActions) != 41 {
		t.Errorf("expected 41 named actions, got %d", len(AllActions))
	}
}
