package keymap

import (
	"fmt"
	"sort"
)

// Keymap is a resolved, validated action↔keys mapping plus a reverse index
// for dispatch. Construct with Resolve or Default; do not build literally.
type Keymap struct {
	goos     string
	bindings map[Action][]string
	lookup   map[string]Action
}

// Default returns the built-in keymap for goos with no user overrides.
func Default(goos string) *Keymap {
	km, err := Resolve(goos, nil, nil)
	if err != nil {
		// Built-in defaults are collision-free by construction and covered by
		// TestDefaultKeymapsHaveNoCollisions; a failure here is a programmer bug.
		panic(fmt.Sprintf("keymap: built-in default for %q invalid: %v", goos, err))
	}
	return km
}

// Resolve merges user override layers over the built-in OS default, per action
// (first defining layer wins; the list REPLACES the lower layer — no key-by-key
// merge, so a user can clear a default by giving a shorter explicit list).
// Precedence per action: userOS > userDefault > app default. User keys are
// normalized; unknown action names, unmappable key tokens, and post-merge key
// collisions are all errors.
func Resolve(goos string, userDefault, userOS map[string][]string) (*Keymap, error) {
	bindings := defaultFor(goos)

	apply := func(layer map[string][]string) error {
		// Deterministic order for stable error messages.
		names := make([]string, 0, len(layer))
		for k := range layer {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, name := range names {
			if !IsAction(name) {
				return fmt.Errorf("keybindings: unknown action %q", name)
			}
			raw := layer[name]
			norm := make([]string, 0, len(raw))
			for _, k := range raw {
				nk, err := normalizeKey(k)
				if err != nil {
					return fmt.Errorf("keybindings.%s: %w", name, err)
				}
				norm = append(norm, nk)
			}
			bindings[Action(name)] = norm
		}
		return nil
	}

	// Lower precedence first; userOS applied last so it wins.
	if err := apply(userDefault); err != nil {
		return nil, err
	}
	if err := apply(userOS); err != nil {
		return nil, err
	}

	lookup, err := buildLookup(bindings)
	if err != nil {
		return nil, err
	}
	return &Keymap{goos: goos, bindings: bindings, lookup: lookup}, nil
}

func buildLookup(bindings map[Action][]string) (map[string]Action, error) {
	// Deterministic iteration so collision errors are stable.
	actions := make([]Action, 0, len(bindings))
	for a := range bindings {
		actions = append(actions, a)
	}
	sort.Slice(actions, func(i, j int) bool { return actions[i] < actions[j] })

	lookup := map[string]Action{}
	for _, a := range actions {
		for _, k := range bindings[a] {
			if other, dup := lookup[k]; dup && other != a {
				return nil, fmt.Errorf("keybindings: key %q is bound to both %q and %q", k, other, a)
			}
			lookup[k] = a
		}
	}
	return lookup, nil
}

// Lookup maps a bubbletea key string to its action.
func (k *Keymap) Lookup(key string) (Action, bool) {
	a, ok := k.lookup[key]
	return a, ok
}

// Keys returns the bound keys for an action (display order).
func (k *Keymap) Keys(a Action) []string { return k.bindings[a] }

// Display returns the per-OS label for an action's keys, e.g. "⌃G".
func (k *Keymap) Display(a Action) string { return Display(k.bindings[a], k.goos) }

// GOOS returns the platform this keymap was resolved for.
func (k *Keymap) GOOS() string { return k.goos }
