package sink

import (
	"os"

	"github.com/homeend/log-listener/internal/render"
)

// FileSink writes rendered events to a file in the same plain-text format as a
// non-TTY Stdout sink ([group] basename: text + indented JSON/XML blocks, no
// ANSI color). It owns the underlying file and is the only sink that closes its
// writer. Not safe for concurrent Emit; callers fan out from a single goroutine.
type FileSink struct {
	f     *os.File
	inner *Stdout
}

// OpenFile creates (or truncates) path and returns a FileSink writing to it.
// Opened O_CREATE|O_WRONLY|O_TRUNC (0o644): each run starts with a fresh file.
func OpenFile(path string) (*FileSink, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	return &FileSink{f: f, inner: NewStdout(f, false)}, nil
}

// Emit writes the event in plain (no-color) format. Delegates to the embedded
// Stdout so the on-disk format always matches non-TTY stdout exactly.
func (s *FileSink) Emit(ev render.Event) { s.inner.Emit(ev) }

// Close closes the underlying file (FileSink owns it, unlike Stdout).
func (s *FileSink) Close() error { return s.f.Close() }
