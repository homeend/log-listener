// Package watch streams new lines from log files and detects rotation.
package watch

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"syscall"
)

// Tailer follows a single file. Tick() is called by the watcher loop on
// fsnotify events (or periodically) to read newly appended bytes, split them
// into complete lines, and detect rotation/truncation.
type Tailer struct {
	path  string
	file  *os.File
	buf   bytes.Buffer
	inode uint64
	pos   int64
}

// NewTailer opens path. If fromStart is true, reading begins at offset 0;
// otherwise reading starts from EOF (the typical tail -f behavior).
func NewTailer(path string, fromStart bool) (*Tailer, error) {
	t := &Tailer{path: path}
	if err := t.open(fromStart); err != nil {
		return nil, err
	}
	return t, nil
}

func (t *Tailer) open(fromStart bool) error {
	f, err := os.Open(t.path)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	ino, err := inodeOf(fi)
	if err != nil {
		f.Close()
		return err
	}
	t.file = f
	t.inode = ino
	if fromStart {
		t.pos = 0
		return nil
	}
	t.pos = fi.Size()
	_, err = f.Seek(t.pos, io.SeekStart)
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
	default:
		ino, e := inodeOf(fi)
		if e != nil {
			return nil, false, e
		}
		if ino != t.inode {
			didRotate = true
		} else if fi.Size() < t.pos {
			didTruncate = true
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
		t.pos = 0
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
	buf := make([]byte, 32*1024)
	for {
		n, err := t.file.Read(buf)
		if n > 0 {
			t.pos += int64(n)
			t.buf.Write(buf[:n])
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
	}
}

// Path returns the path the tailer follows.
func (t *Tailer) Path() string { return t.path }

// Close releases the underlying fd.
func (t *Tailer) Close() error {
	if t.file != nil {
		err := t.file.Close()
		t.file = nil
		return err
	}
	return nil
}

// inodeOf returns the inode number from a FileInfo. Linux-specific (Phase 1).
func inodeOf(fi fs.FileInfo) (uint64, error) {
	stat, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, errors.New("watch: cannot read inode from FileInfo")
	}
	return stat.Ino, nil
}
