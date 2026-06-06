package keymap

import (
	"strings"
	"testing"
)

func TestResolvePrecedence(t *testing.T) {
	// current-OS layer beats default layer beats app-default.
	userDefault := map[string][]string{"search": {"?"}}
	userOS := map[string][]string{"search": {":"}}
	km, err := Resolve("linux", userDefault, userOS)
	if err != nil {
		t.Fatal(err)
	}
	if got := km.Keys(ActionSearch); !equalSlice(got, []string{":"}) {
		t.Errorf("search keys = %v, want [:] (current-OS wins)", got)
	}
	// default layer applies when OS layer is silent.
	km2, _ := Resolve("linux", map[string][]string{"filter": {"F"}}, nil)
	if got := km2.Keys(ActionFilter); !equalSlice(got, []string{"F"}) {
		t.Errorf("filter keys = %v, want [F]", got)
	}
	// untouched action keeps app default.
	if got := km2.Keys(ActionQuit); !equalSlice(got, []string{"ctrl+c", "q"}) {
		t.Errorf("quit keys = %v, want app default", got)
	}
}

func TestResolveListReplaceAllowsClearingDefault(t *testing.T) {
	km, err := Resolve("linux", map[string][]string{"quit": {"ctrl+c"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := km.Keys(ActionQuit); !equalSlice(got, []string{"ctrl+c"}) {
		t.Errorf("quit keys = %v, want [ctrl+c] (q dropped)", got)
	}
}

func TestResolveCollisionIsError(t *testing.T) {
	// Rebind clear to n; next_match still owns n -> collision.
	_, err := Resolve("linux", map[string][]string{"clear": {"n"}}, nil)
	if err == nil {
		t.Fatal("expected collision error")
	}
	if !strings.Contains(err.Error(), "clear") || !strings.Contains(err.Error(), "next_match") || !strings.Contains(err.Error(), "n") {
		t.Errorf("collision error should name both actions and key: %v", err)
	}
}

func TestResolveUnknownActionIsError(t *testing.T) {
	_, err := Resolve("linux", map[string][]string{"frobnicate": {"x"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "frobnicate") {
		t.Fatalf("expected unknown-action error, got %v", err)
	}
}

func TestResolveBadKeyTokenIsError(t *testing.T) {
	_, err := Resolve("linux", map[string][]string{"search": {"ctrl+notakey"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "notakey") {
		t.Fatalf("expected key-token error, got %v", err)
	}
}

func TestResolveNormalizesUserKeys(t *testing.T) {
	km, err := Resolve("linux", map[string][]string{"search": {"Ctrl+A"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := km.Lookup("ctrl+a"); !ok {
		t.Errorf("normalized key ctrl+a should be looked up")
	}
}

func TestLookupAllDefaultKeys(t *testing.T) {
	km := Default("linux")
	for action, keys := range defaultFor("linux") {
		for _, k := range keys {
			got, ok := km.Lookup(k)
			if !ok || got != action {
				t.Errorf("Lookup(%q) = %q,%v; want %q", k, got, ok, action)
			}
		}
	}
}

func TestDefaultKeymapsHaveNoCollisions(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		if _, err := Resolve(goos, nil, nil); err != nil {
			t.Errorf("%s default keymap has a collision: %v", goos, err)
		}
	}
}
