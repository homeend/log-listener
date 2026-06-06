package keymap

import (
	"fmt"
	"strings"
)

// knownBase is the set of multi-char base tokens. Single runes are also
// valid bases (letters, digits, punctuation) and pass through verbatim.
var knownBase = map[string]bool{
	"up": true, "down": true, "left": true, "right": true,
	"home": true, "end": true, "pgup": true, "pgdown": true,
	"tab": true, "esc": true, "enter": true,
}

// modOrder defines bubbletea's canonical modifier order: alt → ctrl → shift.
// Confirmed from bubbletea@v1.3.10 key.go:70 (Key.String writes "alt+" as an
// unconditional leading prefix) and key.go:311-312 (keyNames entries for
// KeyCtrlShiftHome/End are "ctrl+shift+…", alt never appears between ctrl and shift).
var modOrder = map[string]int{"alt": 0, "ctrl": 1, "shift": 2}

// normalizeKey canonicalizes a user-supplied key string to the vocabulary the
// dispatcher and bubbletea use. Modifiers (ctrl/alt/shift) are lowercased and
// sorted into bubbletea's canonical order (alt → ctrl → shift); the base is
// lowercased if it is a known named key, or kept verbatim if it is a single
// rune (so "G" stays "G"). The space key is the single string " " (also
// accepts "space"/"Space"). Unmappable tokens are an error — never a silent
// no-fire.
func normalizeKey(s string) (string, error) {
	if s == " " {
		return " ", nil
	}
	if strings.EqualFold(s, "space") {
		return " ", nil
	}
	parts := strings.Split(s, "+")
	var mods []string
	var base string
	for i, p := range parts {
		if p == "" {
			return "", fmt.Errorf("invalid key %q (empty token)", s)
		}
		isLast := i == len(parts)-1
		if !isLast {
			lp := strings.ToLower(p)
			if lp != "ctrl" && lp != "alt" && lp != "shift" {
				return "", fmt.Errorf("invalid modifier %q in key %q", p, s)
			}
			mods = append(mods, lp)
			continue
		}
		// base token
		lp := strings.ToLower(p)
		if knownBase[lp] {
			base = lp
		} else if len([]rune(p)) == 1 {
			if len(parts) > 1 {
				// With modifiers, lowercase the base (bubbletea convention: ctrl+i not ctrl+I).
				base = lp
			} else {
				base = p // standalone single rune: keep case ("G" vs "g")
			}
		} else {
			return "", fmt.Errorf("unknown key token %q in key %q", p, s)
		}
	}
	if len(mods) == 0 {
		return base, nil
	}
	// Sort modifiers into bubbletea's canonical order (alt → ctrl → shift).
	// Use a simple insertion sort — at most 3 elements.
	for i := 1; i < len(mods); i++ {
		for j := i; j > 0 && modOrder[mods[j]] < modOrder[mods[j-1]]; j-- {
			mods[j], mods[j-1] = mods[j-1], mods[j]
		}
	}
	return strings.Join(append(mods, base), "+"), nil
}

var macGlyph = map[string]string{
	"ctrl": "⌃", "alt": "⌥", "shift": "⇧",
	"esc": "⎋", "tab": "⇥", "enter": "↩",
	"up": "↑", "down": "↓", "left": "←", "right": "→",
	"home": "Home", "end": "End",
	"pgup": "PgUp", "pgdown": "PgDn", " ": "Space",
}

var textLabel = map[string]string{
	"esc": "Esc", "tab": "Tab", "enter": "Enter",
	"up": "↑", "down": "↓", "left": "←", "right": "→",
	"home": "Home", "end": "End",
	"pgup": "PgUp", "pgdown": "PgDn", " ": "Space",
}

// Display renders one action's key list to a per-OS label, e.g.
// ["ctrl+i","tab"] -> "⌃I / ⇥" on darwin, "Ctrl+I / Tab" elsewhere.
func Display(keys []string, goos string) string {
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, displayKey(k, goos))
	}
	return strings.Join(parts, " / ")
}

func displayKey(key string, goos string) string {
	mac := goos == "darwin"
	// Whole-key specials (space).
	if key == " " {
		return "Space"
	}
	toks := strings.Split(key, "+")
	var b strings.Builder
	for i, tok := range toks {
		last := i == len(toks)-1
		if mac {
			if g, ok := macGlyph[tok]; ok {
				b.WriteString(g)
			} else {
				b.WriteString(strings.ToUpper(tok))
			}
			// Mac glyphs are written tight (⌃I), no "+".
			continue
		}
		// linux/windows
		if !last {
			b.WriteString(capitalize(tok)) // Ctrl/Alt/Shift
			b.WriteString("+")
			continue
		}
		if lbl, ok := textLabel[tok]; ok {
			b.WriteString(lbl)
		} else if len([]rune(tok)) == 1 {
			b.WriteString(strings.ToUpper(tok))
		} else {
			b.WriteString(capitalize(tok))
		}
	}
	return b.String()
}

// capitalize upper-cases the first rune of s and returns the result.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = []rune(strings.ToUpper(string(runes[0])))[0]
	return string(runes)
}
