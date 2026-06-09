package tui

import (
	"github.com/homeend/log-listener/internal/linebuf"
	"github.com/homeend/log-listener/internal/render"
)

// tuiDecompose adapts render.DecomposeLines to linebuf.Line for the owned
// buffer (mirrors main.go's bufDecompose).
func tuiDecompose(ev render.Event) []linebuf.Line {
	rows := render.DecomposeLines(ev)
	out := make([]linebuf.Line, len(rows))
	for i, r := range rows {
		out[i] = linebuf.Line{Text: r.Text, IsCont: r.IsCont}
	}
	return out
}

// appendEvent appends an event to the shared buffer and reconciles. In
// production the pump appends to the buffer and Push triggers a reconcile via
// EventMsg; this is the seed/test path (and any in-model append) doing both.
// The buffer assigns the ID (ev.ID is ignored).
func (m *model) appendEvent(ev render.Event) {
	if m.buf == nil {
		return
	}
	m.buf.Append(ev)
	m.reconcile()
}

// appendStored seeds a pre-built event by its source fields (tests). It routes
// through the shared buffer so the model stays single-sourced.
func (m *model) appendStored(e scrollbackEvent) {
	if m.buf == nil {
		return
	}
	m.buf.Append(render.Event{Group: e.group, File: e.file, Raw: e.raw})
	m.reconcile()
}

// displayLinesFromEntry builds the TUI display rows for a linebuf entry as a
// pure transform — no re-render. The entry already holds the basename File and
// the decomposed Lines (Text/IsCont); this mirrors decomposeEvent's row build.
func displayLinesFromEntry(e *linebuf.Entry) []displayLine {
	out := make([]displayLine, 0, len(e.Lines))
	for _, ln := range e.Lines {
		body := ln.Text
		if ln.IsCont {
			body = dimStyle.Render(ln.Text)
		}
		out = append(out, displayLine{
			group:     e.Group,
			file:      e.File,
			body:      body,
			bodyWidth: dispWidth(ln.Text),
			isBlock:   ln.IsCont,
		})
	}
	return out
}

// reconcile pulls a bounded snapshot from the shared buffer and rebuilds
// m.lines + the ID-keyed display cache, keeping only the tail that fits the
// scrollback display-line window. View-state indices are dragged down by the
// number of head rows evicted since the last reconcile (matching trimToCap).
// Coalesces: a no-op when the buffer generation is unchanged.
func (m *model) reconcile() {
	if m.buf == nil {
		return
	}
	if g := m.buf.Gen(); g == m.lastGen && m.lines != nil {
		return
	}
	snap, gen := m.buf.Snapshot(m.scrollback)
	type built struct {
		e     *linebuf.Entry
		lines []displayLine
	}
	rebuilt := make([]built, 0, len(snap))
	total := 0
	for _, e := range snap {
		if m.clearedSeq > 0 && e.Seq <= m.clearedSeq {
			continue // Clear floor: hide entries from before the last Clear
		}
		dls, ok := m.displayCache[e.ID]
		if !ok {
			dls = displayLinesFromEntry(e)
			m.displayCache[e.ID] = dls
		}
		rebuilt = append(rebuilt, built{e: e, lines: dls})
		total += len(dls)
	}
	startEntry := 0
	for m.scrollback > 0 && total > m.scrollback && startEntry < len(rebuilt) {
		total -= len(rebuilt[startEntry].lines)
		startEntry++
	}
	present := make(map[string]struct{}, len(rebuilt)-startEntry)
	for _, b := range rebuilt[startEntry:] {
		present[b.e.ID] = struct{}{}
	}
	dropped := 0
	for id, n := range m.prevIDLines {
		if _, keep := present[id]; !keep {
			dropped += n
		}
	}
	// Build m.lines, the window, and prevIDLines together from the SAME
	// snapshot, so every reader can index against m.window/displayCache without
	// re-snapshotting the concurrently-mutated buffer (which would drift).
	flat := make([]displayLine, 0, total)
	window := make([]*linebuf.Entry, 0, len(rebuilt)-startEntry)
	newPrev := make(map[string]int, len(rebuilt)-startEntry)
	for _, b := range rebuilt[startEntry:] {
		flat = append(flat, b.lines...)
		window = append(window, b.e)
		newPrev[b.e.ID] = len(b.lines)
	}
	for id := range m.displayCache {
		if _, keep := newPrev[id]; !keep {
			delete(m.displayCache, id)
		}
	}
	m.lines = flat
	m.window = window
	m.prevIDLines = newPrev
	m.lastGen = gen
	m.blocksDirty = true
	if dropped > 0 {
		m.dragViewStateDown(dropped)
	}
}

// dragViewStateDown shifts absolute m.lines indices down by `dropped` rows when
// head entries were evicted, preserving trimToCap's exact semantics: streamTop
// only when browsing; searchHit/visual anchors always; clamp at 0; unset on
// scroll-off; clear the focused-block indicator.
func (m *model) dragViewStateDown(dropped int) {
	m.blockFocused = false
}

// visibleEntries returns the entries currently in the display window, in order,
// exactly as captured by the last reconcile — so m.lines, m.window, and
// displayCache are one consistent set. Readers MUST use this (not a fresh
// buf.Snapshot), or their m.lines index mapping can drift when the pump appends/
// evicts between reconciles.
func (m *model) visibleEntries() []*linebuf.Entry {
	return m.window
}

// reRenderAll walks every stored entry through renderFn and rebuilds
// m.lines from the resulting display lines. Called when a renderer
// toggle changes how the pipeline dispatches lines. Index anchors
// (streamTop, searchHit) are clamped to the new flat-line range —
// the viewport may visibly jump if a long stack-trace block collapsed
// into a single raw line, which is the correct UX for "this is what
// this line looks like now."
//
// If renderFn is nil (no pipeline plumbed — early bootstrap, tests
// that bypass main.go) reRenderAll is a no-op.
func (m *model) reRenderAll() {
	if m.renderFn == nil || m.buf == nil {
		return
	}
	// Re-render the shared buffer under the current pipeline so linebuf.Entry
	// Lines reflect the new rendering (this also keeps MCP consistent), then
	// drop the stale display cache and reconcile.
	m.buf.Rerender(func(g, f, raw string) (render.Event, bool) {
		return m.renderFn(g, f, raw)
	})
	m.displayCache = map[string][]displayLine{}
	m.lastGen = 0 // force reconcile
	m.reconcile()
}
