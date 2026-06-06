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
