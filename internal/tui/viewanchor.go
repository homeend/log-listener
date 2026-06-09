package tui

// rowAnchor is a stable pointer into the display stream: the ID of the entry
// that owns a row plus the row's offset within that entry's display lines. It
// survives head eviction (unlike an absolute m.lines index) because the entry
// ID is stable. The zero value (id == "") is the sentinel: an unresolvable or
// "no current value" anchor. Getters resolve a sentinel — or an anchor whose
// entry has left the window — to their per-value clamp result.
type rowAnchor struct {
	id  string
	off int
}

// rowForAnchor maps an anchor to an absolute m.lines index in the current
// reconcile window. ok=false when the anchor is the sentinel or its entry is
// no longer visible (evicted or scrolled out of the window). The offset is
// clamped into the entry's current row count, because a re-render can change
// how many display rows an entry owns. Walks m.window + m.displayCache exactly
// as entryIDForLine does (one consistent snapshot — never re-snapshot buf).
func (m *model) rowForAnchor(a rowAnchor) (int, bool) {
	if a.id == "" {
		return 0, false
	}
	off := 0
	for _, e := range m.visibleEntries() {
		n := len(m.displayCache[e.ID])
		if e.ID == a.id {
			if n == 0 {
				return off, true
			}
			o := a.off
			if o < 0 {
				o = 0
			}
			if o >= n {
				o = n - 1
			}
			return off + o, true
		}
		off += n
	}
	return 0, false
}

// anchorForRow is the inverse: the (entryID, rowOffset) owning absolute row idx
// in the current window. Index-domain rule:
//   - idx < 0            -> sentinel (preserves searchHit/visualAnchor's -1 unset)
//   - empty window       -> sentinel
//   - idx in [0, total)  -> the exact owning anchor
//   - idx >= total       -> clamp to the LAST row (a resolvable anchor), NOT the
//     sentinel. scrollBy(delta>0) intentionally over-scrolls past the end and
//     relies on maybeReStick to re-pin to tail; collapsing that to the sentinel
//     would resolve to row 0 and jump to the top instead of re-sticking.
func (m *model) anchorForRow(idx int) rowAnchor {
	if idx < 0 {
		return rowAnchor{}
	}
	off := 0
	any := false
	lastID := ""
	lastN := 0
	for _, e := range m.visibleEntries() {
		n := len(m.displayCache[e.ID])
		if idx < off+n {
			return rowAnchor{id: e.ID, off: idx - off}
		}
		off += n
		any, lastID, lastN = true, e.ID, n
	}
	if !any {
		return rowAnchor{} // empty window
	}
	o := lastN - 1
	if o < 0 {
		o = 0
	}
	return rowAnchor{id: lastID, off: o}
}

// streamTopRow returns the absolute m.lines index of the first visible row when
// browsing. Resolves the stored anchor against the current window; an evicted
// or unresolvable anchor clamps to row 0 (top of the now-shorter window) —
// exactly the old dragViewStateDown streamTop behavior.
func (m *model) streamTopRow() int {
	idx, ok := m.rowForAnchor(m.streamTopA)
	if !ok {
		return 0
	}
	return idx
}

// setStreamTopRow stores the first-visible-row position as a stable anchor. A
// past-end index clamps to the last row (so over-scroll re-sticks); a negative
// index or empty window stores the sentinel, which streamTopRow resolves to 0.
func (m *model) setStreamTopRow(i int) { m.streamTopA = m.anchorForRow(i) }

// searchHitRow returns the absolute m.lines index of the current search hit, or
// -1 when there is none or the hit's entry scrolled off (matching the old
// dragViewStateDown unset-on-eviction behavior).
func (m *model) searchHitRow() int {
	idx, ok := m.rowForAnchor(m.searchHitA)
	if !ok {
		return -1
	}
	return idx
}

// setSearchHitRow stores the hit position as a stable anchor. A negative or
// unresolvable index stores the sentinel, which searchHitRow resolves to -1.
func (m *model) setSearchHitRow(i int) { m.searchHitA = m.anchorForRow(i) }
