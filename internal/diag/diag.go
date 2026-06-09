// Package diag is an always-on, low-overhead diagnostic recorder for the
// watch/reload lifecycle. Events are kept in a bounded in-memory ring so they
// can be dumped ON DEMAND (e.g. from a TUI key) at the moment an intermittent
// bug is observed — without needing a flag set at startup. An optional file
// mirror (--debug-log) additionally streams every event to disk.
//
// A nil *Logger is a valid no-op: every method is nil-safe.
package diag

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// Logger records timestamped trace lines into a bounded ring (always) and an
// optional file (when a path was given). Safe for concurrent use.
type Logger struct {
	mu   sync.Mutex
	f    *os.File // optional file mirror; nil = ring-only
	ring []string
	cap  int
	next int
	full bool
	now  func() time.Time // injectable clock for tests; nil → time.Now
}

// New creates a recorder keeping the last ringCap events in memory. If filePath
// is non-empty it also appends every event to that file (the --debug-log
// mirror); a file-open error is returned but the ring still works if you ignore
// it. ringCap <= 0 defaults to 2048.
func New(ringCap int, filePath string) (*Logger, error) {
	if ringCap <= 0 {
		ringCap = 2048
	}
	l := &Logger{ring: make([]string, ringCap), cap: ringCap}
	if filePath != "" {
		f, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return l, err
		}
		l.f = f
	}
	return l, nil
}

// Logf records one line: "<RFC3339Nano UTC> <kind> <message>". kind is a short
// uppercase tag (RELOAD, TAILER-OPEN, ROTATE, TRUNCATE) so dumps are greppable.
func (l *Logger) Logf(kind, format string, args ...any) {
	if l == nil {
		return
	}
	clock := time.Now
	if l.now != nil {
		clock = l.now
	}
	line := fmt.Sprintf("%s %s %s", clock().UTC().Format(time.RFC3339Nano), kind, fmt.Sprintf(format, args...))
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ring[l.next] = line
	l.next = (l.next + 1) % l.cap
	if l.next == 0 {
		l.full = true
	}
	if l.f != nil {
		fmt.Fprintln(l.f, line)
	}
}

// Dump returns the recorded events oldest-first, one per line. Nil-safe.
func (l *Logger) Dump() string {
	if l == nil {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var b strings.Builder
	emit := func(i int) {
		if l.ring[i] != "" {
			b.WriteString(l.ring[i])
			b.WriteByte('\n')
		}
	}
	if l.full {
		for i := l.next; i < l.cap; i++ {
			emit(i)
		}
	}
	for i := 0; i < l.next; i++ {
		emit(i)
	}
	return b.String()
}

// Close closes the optional file mirror. The ring remains usable. Nil-safe.
func (l *Logger) Close() error {
	if l == nil || l.f == nil {
		return nil
	}
	return l.f.Close()
}
