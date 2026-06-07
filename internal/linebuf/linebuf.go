// Package linebuf is a concurrency-safe ring of log records with stable opaque
// IDs. It is fed at the pump fan-out point (parallel to the TUI and SSE) so an
// embedded MCP server can resolve a user-copied reference to exactly the
// records the user is watching. It depends only on internal/render and
// internal/blocks — never on internal/tui.
package linebuf

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/homeend/log-listener/internal/blocks"
	"github.com/homeend/log-listener/internal/render"
)

// Line is one decomposed plain display row of an entry.
type Line struct {
	Text   string
	IsCont bool
}

// Entry is one log record — the external, copyable unit. Its ID is stable for
// the entry's lifetime even when a config reload re-renders Lines.
type Entry struct {
	ID    string
	Seq   uint64
	Group string
	File  string
	Ts    time.Time
	Raw   string
	Lines []Line
}

// Block is a contiguous run of entries the segmenter grouped (or a single
// multi-row entry); identity is the head entry.
type Block struct {
	HeadID    string
	EndID     string
	EntryIDs  []string
	Exception *blocks.ExceptionInfo
}

// Buffer is the shared ring. All methods are safe for concurrent use.
type Buffer struct {
	mu        sync.RWMutex
	cap       int
	seq       uint64
	entries   []*Entry
	byID      map[string]*Entry
	blocks    []Block
	blockOf   map[string]int
	dirty     bool
	decompose func(render.Event) []Line
}

// New returns a Buffer holding at most cap entries, decomposing events with
// the supplied function (an adapter over render.DecomposeLines).
func New(cap int, decompose func(render.Event) []Line) *Buffer {
	if cap <= 0 {
		cap = 10000
	}
	return &Buffer{
		cap:       cap,
		byID:      map[string]*Entry{},
		blockOf:   map[string]int{},
		decompose: decompose,
	}
}

// Append assigns the next ID+Seq, stores the entry, evicts the oldest if over
// cap, and returns the assigned ID. Single write path.
func (b *Buffer) Append(ev render.Event) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := "L" + strconv.FormatUint(b.seq, 36)
	e := &Entry{
		ID: id, Seq: b.seq, Group: ev.Group, File: baseName(ev.File),
		Ts: ev.Ts, Raw: ev.Raw, Lines: b.decompose(ev),
	}
	b.seq++
	b.entries = append(b.entries, e)
	b.byID[id] = e
	if len(b.entries) > b.cap {
		drop := b.entries[0]
		b.entries = b.entries[1:]
		delete(b.byID, drop.ID)
	}
	b.dirty = true
	return id
}

// Get returns the entry for id.
func (b *Buffer) Get(id string) (*Entry, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	e, ok := b.byID[id]
	return e, ok
}

func baseName(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// Range returns entries between fromID and toID inclusive, in seq order,
// tolerant of argument order. If one ID was evicted, the resident sub-span is
// returned; if both are unknown, nil.
func (b *Buffer) Range(fromID, toID string) []*Entry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	from, okF := b.byID[fromID]
	to, okT := b.byID[toID]
	if !okF && !okT {
		return nil
	}
	lo, hi := uint64(0), ^uint64(0)
	if okF {
		lo = from.Seq
	}
	if okT {
		hi = to.Seq
	}
	if lo > hi {
		lo, hi = hi, lo
	}
	var out []*Entry
	for _, e := range b.entries {
		if e.Seq >= lo && e.Seq <= hi {
			out = append(out, e)
		}
	}
	return out
}

// Context returns up to `before` entries before id and `after` after it,
// inclusive of id.
func (b *Buffer) Context(id string, before, after int) []*Entry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	idx := -1
	for i, e := range b.entries {
		if e.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil
	}
	lo := idx - before
	if lo < 0 {
		lo = 0
	}
	hi := idx + after
	if hi >= len(b.entries) {
		hi = len(b.entries) - 1
	}
	out := make([]*Entry, 0, hi-lo+1)
	for i := lo; i <= hi; i++ {
		out = append(out, b.entries[i])
	}
	return out
}
