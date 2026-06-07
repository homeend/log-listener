package tui

import (
	"fmt"
	"os"

	"github.com/aymanbagabas/go-osc52/v2"
)

// entryIDForLine returns the ID of the entry that owns absolute m.lines index
// idx, or "" if out of range.
func (m *model) entryIDForLine(idx int) string {
	if idx < 0 {
		return ""
	}
	off := 0
	for _, e := range m.entries {
		n := len(e.lines)
		if idx < off+n {
			return e.id
		}
		off += n
	}
	return ""
}

// buildReference produces the paste-ready reference string by precedence:
//  1. search active + hit selected → line:<hit entry id>
//  2. cursor inside a multi-line block → range:<headEntry>..<endEntry>
//  3. else → range:<first visible entry>..<last visible entry>
func buildReference(m *model) string {
	if m.searchTerm != "" && m.searchHit >= 0 {
		if id := m.entryIDForLine(m.searchHit); id != "" {
			return "line:" + id
		}
	}
	cur := m.cursorIndex()
	if cur >= 0 {
		m.ensureBlocks()
		for _, b := range m.blocks {
			if cur >= b.Start && cur <= b.End && b.End > b.Start {
				head := m.entryIDForLine(b.Start)
				end := m.entryIDForLine(b.End)
				if head != "" && end != "" {
					return fmt.Sprintf("range:%s..%s", head, end)
				}
			}
		}
	}
	idxs := m.collectVisible(m.contentHeight())
	if len(idxs) == 0 {
		return ""
	}
	first := m.entryIDForLine(idxs[0])
	last := m.entryIDForLine(idxs[len(idxs)-1])
	if first == "" || last == "" {
		return ""
	}
	return fmt.Sprintf("range:%s..%s", first, last)
}

// osc52Copy writes ref to the terminal clipboard via the OSC 52 escape on
// stderr (stderr, not stdout, so it does not corrupt the stdout-driven render).
func osc52Copy(ref string) {
	_, _ = osc52.New(ref).WriteTo(os.Stderr)
}

// copyReference writes the reference to the terminal clipboard via OSC 52
// (to stderr, so it does not corrupt the stdout-driven render) and returns the
// reference string (empty if nothing to copy).
func copyReference(m *model) string {
	ref := buildReference(m)
	if ref == "" {
		return ""
	}
	osc52Copy(ref)
	return ref
}
