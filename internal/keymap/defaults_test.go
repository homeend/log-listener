package keymap

import "testing"

func TestDefaultForCoversEveryAction(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		dm := defaultFor(goos)
		for _, d := range AllActions {
			keys := dm[d.Action]
			if len(keys) == 0 {
				t.Errorf("%s: action %q has no default keys", goos, d.Action)
			}
		}
		if len(dm) != len(AllActions) {
			t.Errorf("%s: defaultFor has %d entries, want %d", goos, len(dm), len(AllActions))
		}
	}
}

func TestDarwinFastScrollAdvertisesShiftFirst(t *testing.T) {
	dm := defaultFor("darwin")
	if got := dm[ActionFastDown][0]; got != "shift+down" {
		t.Errorf("darwin fast_down primary = %q, want shift+down", got)
	}
	lin := defaultFor("linux")
	if got := lin[ActionFastDown][0]; got != "ctrl+down" {
		t.Errorf("linux fast_down primary = %q, want ctrl+down", got)
	}
	// Both forms remain bound on every platform.
	if !contains(dm[ActionFastDown], "ctrl+down") {
		t.Errorf("darwin fast_down must still bind ctrl+down")
	}
}

func TestWindowsEqualsLinux(t *testing.T) {
	win, lin := defaultFor("windows"), defaultFor("linux")
	for _, d := range AllActions {
		if !equalSlice(win[d.Action], lin[d.Action]) {
			t.Errorf("windows/linux differ for %q: %v vs %v", d.Action, win[d.Action], lin[d.Action])
		}
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSaveActionsHaveDefaults(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		dm := defaultFor(goos)
		if !equalSlice(dm[ActionSaveViewport], []string{"s"}) {
			t.Errorf("%s: save_viewport default = %v, want [s]", goos, dm[ActionSaveViewport])
		}
		if !equalSlice(dm[ActionSaveScrollback], []string{"S"}) {
			t.Errorf("%s: save_scrollback default = %v, want [S]", goos, dm[ActionSaveScrollback])
		}
	}
}

func TestBlockActionsHaveDefaults(t *testing.T) {
	want := map[Action]string{
		ActionNextBlock:            "]",
		ActionPrevBlock:            "[",
		ActionNextMarkedBlock:      "}",
		ActionPrevMarkedBlock:      "{",
		ActionToggleExceptionMarks: "e",
	}
	for _, goos := range []string{"linux", "darwin", "windows"} {
		dm := defaultFor(goos)
		for a, key := range want {
			if !equalSlice(dm[a], []string{key}) {
				t.Errorf("%s: %s default = %v, want [%s]", goos, a, dm[a], key)
			}
		}
	}
}
