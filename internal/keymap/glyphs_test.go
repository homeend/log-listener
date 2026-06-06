package keymap

import "testing"

func TestNormalizeKey(t *testing.T) {
	cases := map[string]string{
		"Ctrl+I": "ctrl+i",
		"ctrl+i": "ctrl+i",
		"Esc":    "esc",
		"Tab":    "tab",
		"Space":  " ",
		" ":      " ",
		"Shift+Up": "shift+up",
		"PgUp":   "pgup",
		"G":      "G",
		"/":      "/",
		"CTRL+ALT+DELETE": "", // delete not a known base -> error
	}
	for in, want := range cases {
		got, err := normalizeKey(in)
		if want == "" {
			if err == nil {
				t.Errorf("normalizeKey(%q): want error, got %q", in, got)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("normalizeKey(%q) = %q,%v; want %q", in, got, err, want)
		}
	}
}

func TestNormalizeKeyCanonicalModifierOrder(t *testing.T) {
	// Bubbletea's canonical modifier order is alt → ctrl → shift (base last).
	// Confirmed from bubbletea@v1.3.10 key.go:70 (alt prefix written first in
	// Key.String()) and key.go:311-312 (keyNames entries "ctrl+shift+home/end").
	cases := map[string]string{
		"shift+ctrl+up":       "ctrl+shift+up",
		"shift+alt+up":        "alt+shift+up",
		"ctrl+alt+up":         "alt+ctrl+up",
		"shift+alt+ctrl+up":   "alt+ctrl+shift+up",
		"shift+ctrl+alt+up":   "alt+ctrl+shift+up",
		// Already-canonical forms must be unchanged.
		"ctrl+shift+up":       "ctrl+shift+up",
		"alt+ctrl+shift+up":   "alt+ctrl+shift+up",
	}
	for in, want := range cases {
		got, err := normalizeKey(in)
		if err != nil {
			t.Errorf("normalizeKey(%q) unexpected error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("normalizeKey(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestDisplayHomeEndConsistent(t *testing.T) {
	// home and end must display as "Home"/"End" on all platforms (not HOME/END on darwin).
	for _, goos := range []string{"darwin", "linux", "windows"} {
		if got := Display([]string{"home"}, goos); got != "Home" {
			t.Errorf("Display([home], %q) = %q; want Home", goos, got)
		}
		if got := Display([]string{"end"}, goos); got != "End" {
			t.Errorf("Display([end], %q) = %q; want End", goos, got)
		}
	}
}

func TestDisplayPerOS(t *testing.T) {
	mac := Display([]string{"ctrl+i", "tab"}, "darwin")
	if mac != "⌃I / ⇥" {
		t.Errorf("darwin display = %q, want ⌃I / ⇥", mac)
	}
	lin := Display([]string{"ctrl+i", "tab"}, "linux")
	if lin != "Ctrl+I / Tab" {
		t.Errorf("linux display = %q, want Ctrl+I / Tab", lin)
	}
	if got := Display([]string{"shift+down"}, "darwin"); got != "⇧↓" {
		t.Errorf("darwin shift+down = %q, want ⇧↓", got)
	}
}
