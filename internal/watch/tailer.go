// Package watch streams new lines from log files and detects rotation.
package watch

import (
	"bytes"
	"errors"
	"io"
	"os"
	"sync/atomic"

	"github.com/homeend/log-listener/internal/diag"
)

// Tailer follows a single file. Tick() is called by the watcher loop on
// fsnotify events (or periodically) to read newly appended bytes, split them
// into complete lines, and detect rotation/truncation.
type Tailer struct {
	path    string
	file    *os.File
	buf     bytes.Buffer
	readBuf []byte // 32 KiB scratch buffer — reused across Tick calls
	inode   uint64
	// pos is the byte offset of the next unread byte. It is mutated on the
	// watcher's loop goroutine (Tick/open) but read from other goroutines for
	// lag reporting, so it is atomic.
	pos  atomic.Int64
	diag *diag.Logger // optional trace sink (set by Watcher.Add); nil-safe
}

// NewTailer opens path. If fromStart is true, reading begins at offset 0;
// otherwise reading starts from EOF (the typical tail -f behavior).
func NewTailer(path string, fromStart bool) (*Tailer, error) {
	t := &Tailer{
		path:    path,
		readBuf: make([]byte, 32*1024),
	}
	if err := t.open(fromStart); err != nil {
		return nil, err
	}
	return t, nil
}

func (t *Tailer) open(fromStart bool) error {
	f, err := openShared(t.path)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	ino, err := inodeOf(f, t.path)
	if err != nil {
		f.Close()
		return err
	}
	t.file = f
	t.inode = ino
	if fromStart {
		t.pos.Store(0)
		return nil
	}
	t.pos.Store(fi.Size())
	_, err = f.Seek(fi.Size(), io.SeekStart)
	return err
}

// Tick reads any new bytes, returns complete lines, and reports rotation.
// On rotation or truncation: drains the old fd, flushes any partial line as
// a final line for the old context, then re-opens the file at offset 0 and
// reads what's already there.
func (t *Tailer) Tick() (lines []string, rotated bool, err error) {
	fi, statErr := os.Stat(t.path)
	var (
		didRotate   bool
		didTruncate bool
	)
	switch {
	case statErr != nil:
		didRotate = true
		t.diag.Logf("ROTATE", "path=%s reason=stat_missing old_inode=%d old_pos=%d",
			t.path, t.inode, t.pos.Load())
	default:
		ino, e := inodeOf(nil, t.path)
		if e != nil {
			return nil, false, e
		}
		if ino != t.inode {
			didRotate = true
			t.diag.Logf("ROTATE", "path=%s reason=inode_changed old_inode=%d new_inode=%d old_pos=%d new_size=%d",
				t.path, t.inode, ino, t.pos.Load(), fi.Size())
		} else if fi.Size() < t.pos.Load() {
			didTruncate = true
			t.diag.Logf("TRUNCATE", "path=%s old_pos=%d new_size=%d",
				t.path, t.pos.Load(), fi.Size())
		}
	}

	// Drain whatever bytes remain on the current fd (the "old" file in the
	// rename-rotation case).
	drained, derr := t.readAvailable()
	lines = append(lines, drained...)
	if derr != nil {
		return lines, didRotate || didTruncate, derr
	}

	if !didRotate && !didTruncate {
		return lines, false, nil
	}

	// Flush partial line from the old context — it won't ever complete.
	if t.buf.Len() > 0 {
		lines = append(lines, t.buf.String())
		t.buf.Reset()
	}

	switch {
	case didRotate:
		if t.file != nil {
			t.file.Close()
			t.file = nil
		}
		// New file may not exist yet (e.g. mid-rotation); leave file nil and
		// the next Tick will retry.
		if statErr != nil {
			return lines, true, nil
		}
		if err := t.open(true); err != nil {
			return lines, true, err
		}
	case didTruncate:
		if _, err := t.file.Seek(0, io.SeekStart); err != nil {
			return lines, true, err
		}
		t.pos.Store(0)
	}

	more, merr := t.readAvailable()
	lines = append(lines, more...)
	return lines, true, merr
}

func (t *Tailer) readAvailable() ([]string, error) {
	if t.file == nil {
		return nil, nil
	}
	var lines []string
	for {
		n, err := t.file.Read(t.readBuf)
		if n > 0 {
			t.pos.Add(int64(n))
			t.buf.Write(t.readBuf[:n])
			for {
				data := t.buf.Bytes()
				i := bytes.IndexByte(data, '\n')
				if i < 0 {
					break
				}
				line := string(data[:i])
				// strip trailing \r for CRLF logs
				if l := len(line); l > 0 && line[l-1] == '\r' {
					line = line[:l-1]
				}
				lines = append(lines, line)
				t.buf.Next(i + 1)
			}
		}
		if errors.Is(err, io.EOF) || n == 0 {
			return lines, nil
		}
		if err != nil {
			return lines, err
		}
		// Refill on the next iteration. If we read a full buffer, loop and
		// drain more; if we read less, the next Read will hit EOF cheaply.
	}
}

// Path returns the path the tailer follows.
func (t *Tailer) Path() string { return t.path }

// Pos returns the byte offset of the next unread byte. Safe from any goroutine.
func (t *Tailer) Pos() int64 { return t.pos.Load() }

// skipToEOF fast-forwards the read position to the current end of the open
// file, discarding any unread bytes and partial line. It returns the number of
// bytes skipped (current size minus the old position, never negative). It
// mutates the file offset and so MUST be called on the same goroutine as Tick
// (the watcher loop) to avoid racing readAvailable. A nil/closed file skips 0.
func (t *Tailer) skipToEOF() (skipped int64, err error) {
	if t.file == nil {
		return 0, nil
	}
	fi, err := t.file.Stat()
	if err != nil {
		return 0, err
	}
	size := fi.Size()
	if _, err := t.file.Seek(size, io.SeekStart); err != nil {
		return 0, err
	}
	old := t.pos.Load()
	t.pos.Store(size)
	t.buf.Reset()
	if size > old {
		return size - old, nil
	}
	return 0, nil
}

// Close releases the underlying fd.
func (t *Tailer) Close() error {
	if t.file != nil {
		err := t.file.Close()
		t.file = nil
		return err
	}
	return nil
}
