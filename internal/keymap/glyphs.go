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

// normalizeKey canonicalizes a user-supplied key string to the vocabulary the
// dispatcher and bubbletea use. Modifiers (ctrl/alt/shift) are lowercased and
// ordered as written; the base is lowercased if it is a known named key, or
// kept verbatim if it is a single rune (so "G" stays "G"). The space key is
// the single string " " (also accepts "space"/"Space"). Unmappable tokens are
// an error — never a silent no-fire.
func normalizeKey(s string) (string, error) {
	if s == " " {
		return " ", nil
	}
	if strings.EqualFold(s, "space") {
		return " ", nil
	}
	parts := strings.Split(s, "+")
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
			parts[i] = lp
			continue
		}
		// base token
		lp := strings.ToLower(p)
		if knownBase[lp] {
			parts[i] = lp
		} else if len([]rune(p)) == 1 {
			if len(parts) > 1 {
				// With modifiers, lowercase the base (bubbletea convention: ctrl+i not ctrl+I).
				parts[i] = lp
			} else {
				parts[i] = p // standalone single rune: keep case ("G" vs "g")
			}
		} else {
			return "", fmt.Errorf("unknown key token %q in key %q", p, s)
		}
	}
	return strings.Join(parts, "+"), nil
}

var macGlyph = map[string]string{
	"ctrl": "⌃", "alt": "⌥", "shift": "⇧",
	"esc": "⎋", "tab": "⇥", "enter": "↩",
	"up": "↑", "down": "↓", "left": "←", "right": "→",
	"pgup": "PgUp", "pgdown": "PgDn", " ": "Space",
}

var textLabel = map[string]string{
	"esc": "Esc", "tab": "Tab", "enter": "Enter",
	"up": "↑", "down": "↓", "left": "←", "right": "→",
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
