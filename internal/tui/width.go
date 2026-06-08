package tui

import (
	"regexp"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

// ansiRE matches CSI / OSC escape sequences emitted by lipgloss. Used both
// to strip styling (stripANSI) and to walk it while preserving it during
// horizontal-scroll slicing (clipANSIWindow).
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

func runeLen(s string) int { return utf8.RuneCountInString(s) }

// dispWidth is the terminal cell width of s — wide (CJK) runes count as 2,
// zero-width/combining as 0 — with any ANSI stripped first. Width/clip math
// must use this, not runeLen: a rune is not always one column, and counting it
// as one makes a row of wide characters overflow and wrap.
func dispWidth(s string) int { return runewidth.StringWidth(stripANSI(s)) }

// runeWidth is the cell width of a single rune (0, 1, or 2). A table lookup —
// allocation-free, so it's cheap on the per-rune clip hot path.
func runeWidth(r rune) int { return runewidth.RuneWidth(r) }
