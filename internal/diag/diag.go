// Package diag is an optional, low-overhead diagnostic trace writer. It records
// timestamped, greppable lines about the watch/reload lifecycle so an
// intermittent reload bug can be captured in the field and shared.
//
// A nil *Logger is a valid no-op: every method is nil-safe, so callers can pass
// a nil logger when --debug-log is not set without branching.
package diag

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// Logger appends one trace line per event to the debug file. Safe for
// concurrent use; a nil *Logger discards everything.
type Logger struct {
	mu  sync.Mutex
	f   *os.File
	now func() time.Time // injectable clock for tests; nil → time.Now
}

// New opens (creating/appending) the file at path for trace output.
func New(path string) (*Logger, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &Logger{f: f}, nil
}

// Logf writes one line: "<RFC3339Nano UTC> <kind> <message>". kind is a short
// uppercase tag (e.g. RELOAD, TAILER-OPEN, ROTATE) so the log is grep-friendly.
func (l *Logger) Logf(kind, format string, args ...any) {
	if l == nil {
		return
	}
	clock := time.Now
	if l.now != nil {
		clock = l.now
	}
	ts := clock().UTC().Format(time.RFC3339Nano)
	msg := fmt.Sprintf(format, args...)
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.f, "%s %s %s\n", ts, kind, msg)
}

// Close flushes and closes the underlying file. Nil-safe.
func (l *Logger) Close() error {
	if l == nil || l.f == nil {
		return nil
	}
	return l.f.Close()
}
