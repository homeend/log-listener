// Package configwatch watches a single config file for changes and emits a
// debounced "changed" signal. It is intentionally config-agnostic: it knows
// nothing about the YAML schema, so the reload caller owns parsing. Keeping
// fsnotify isolated here lets reload-orchestration logic be tested without
// real filesystem-event timing.
package configwatch

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher reports when the target file settles after a change.
type Watcher struct {
	fs        *fsnotify.Watcher
	target    string // absolute path of the watched file
	debounce  time.Duration
	changes   chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

// New starts watching the file at path. It watches the PARENT directory and
// filters by the file's absolute path, so editor "write temp then rename over
// the target" saves are still detected (a watch on the file inode itself goes
// deaf after such a save). Bursts of events from a single save are coalesced
// into one signal using the debounce window.
func New(path string, debounce time.Duration) (*Watcher, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := fw.Add(filepath.Dir(abs)); err != nil {
		fw.Close()
		return nil, err
	}
	w := &Watcher{
		fs:       fw,
		target:   abs,
		debounce: debounce,
		changes:  make(chan struct{}, 1),
		done:     make(chan struct{}),
	}
	go w.loop()
	return w, nil
}

// Changes delivers a signal each time the target file settles after a change.
// The channel has capacity 1; signals are dropped (not blocked) if the
// consumer hasn't drained the previous one, since a pending reload already
// covers any change.
func (w *Watcher) Changes() <-chan struct{} { return w.changes }

// Close stops the watcher and releases resources.
func (w *Watcher) Close() error {
	var err error
	w.closeOnce.Do(func() {
		close(w.done)
		err = w.fs.Close()
	})
	return err
}

func (w *Watcher) loop() {
	var timer *time.Timer
	var timerC <-chan time.Time
	arm := func() {
		if timer == nil {
			timer = time.NewTimer(w.debounce)
		} else {
			timer.Stop()
			timer.Reset(w.debounce)
		}
		timerC = timer.C
	}
	for {
		select {
		case <-w.done:
			if timer != nil {
				timer.Stop()
			}
			return
		case ev, ok := <-w.fs.Events:
			if !ok {
				return
			}
			abs, err := filepath.Abs(ev.Name)
			if err != nil || abs != w.target {
				continue
			}
			arm()
		case _, ok := <-w.fs.Errors:
			if !ok {
				return
			}
			// Best-effort: ignore watcher errors. A reload is not critical
			// enough to surface noise here.
		case <-timerC:
			timerC = nil
			// Confirm the file still exists before signaling; a transient
			// rename mid-save can briefly remove it.
			if _, err := os.Stat(w.target); err != nil {
				continue
			}
			select {
			case w.changes <- struct{}{}:
			default:
			}
		}
	}
}
