package watch

import (
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Event is a single line emitted by the watcher.
type Event struct {
	Path  string
	Group string
	Line  string
}

// NewFileMatcher decides whether a newly-discovered file should be tailed and
// which group it belongs to.
type NewFileMatcher func(path string) (groupID string, accept bool)

// Watcher fans fsnotify events out to per-file Tailers and forwards their
// lines on Events(). New files appearing in a watched directory are matched
// via NewFileMatcher; if accepted, a Tailer is created for them.
type Watcher struct {
	fs       *fsnotify.Watcher
	matcher  NewFileMatcher
	mu       sync.Mutex
	tailers  map[string]*Tailer // path → tailer
	groups   map[string]string  // path → group ID
	watched  map[string]struct{} // directory paths we've added to fsnotify
	events   chan Event
	errs     chan error
	done     chan struct{}
	closeOnce sync.Once
	pollEvery time.Duration
}

// New creates a Watcher. matcher may be nil; in that case new files are
// ignored. pollEvery is the periodic safety-net interval (e.g. fsnotify can
// occasionally miss events under load); use 0 to disable polling.
func New(matcher NewFileMatcher, pollEvery time.Duration) (*Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{
		fs:        fw,
		matcher:   matcher,
		tailers:   map[string]*Tailer{},
		groups:    map[string]string{},
		watched:   map[string]struct{}{},
		events:    make(chan Event, 1024),
		errs:      make(chan error, 8),
		done:      make(chan struct{}),
		pollEvery: pollEvery,
	}
	go w.loop()
	return w, nil
}

// Add registers a file for tailing. fromStart controls whether the existing
// content is read from offset 0 (true, useful for --once) or only future
// appends are emitted (false, the default for live tailing).
func (w *Watcher) Add(path, groupID string, fromStart bool) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, dup := w.tailers[abs]; dup {
		return nil
	}
	t, err := NewTailer(abs, fromStart)
	if err != nil {
		return err
	}
	w.tailers[abs] = t
	w.groups[abs] = groupID
	parent := filepath.Dir(abs)
	if _, w0 := w.watched[parent]; !w0 {
		if err := w.fs.Add(parent); err != nil {
			delete(w.tailers, abs)
			delete(w.groups, abs)
			t.Close()
			return err
		}
		w.watched[parent] = struct{}{}
	}
	if fromStart {
		w.tickLocked(abs, t)
	}
	return nil
}

// WatchDir adds a directory to be watched for new file creation. Without this,
// new files created in dirs that aren't already a parent of some Added file
// won't be observed.
func (w *Watcher) WatchDir(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.watched[abs]; ok {
		return nil
	}
	if err := w.fs.Add(abs); err != nil {
		return err
	}
	w.watched[abs] = struct{}{}
	return nil
}

// Events returns the channel of new lines.
func (w *Watcher) Events() <-chan Event { return w.events }

// Errors returns the channel of background errors.
func (w *Watcher) Errors() <-chan error { return w.errs }

// Close stops the watcher and releases resources.
func (w *Watcher) Close() error {
	var err error
	w.closeOnce.Do(func() {
		close(w.done)
		err = w.fs.Close()
		w.mu.Lock()
		defer w.mu.Unlock()
		for _, t := range w.tailers {
			t.Close()
		}
	})
	return err
}

func (w *Watcher) loop() {
	var tick <-chan time.Time
	if w.pollEvery > 0 {
		tk := time.NewTicker(w.pollEvery)
		defer tk.Stop()
		tick = tk.C
	}
	for {
		select {
		case <-w.done:
			return
		case ev, ok := <-w.fs.Events:
			if !ok {
				return
			}
			w.handleFsEvent(ev)
		case err, ok := <-w.fs.Errors:
			if !ok {
				return
			}
			if err != nil {
				w.pushErr(err)
			}
		case <-tick:
			w.tickAll()
		}
	}
}

func (w *Watcher) handleFsEvent(ev fsnotify.Event) {
	abs, err := filepath.Abs(ev.Name)
	if err != nil {
		w.pushErr(err)
		return
	}
	w.mu.Lock()
	t, known := w.tailers[abs]
	w.mu.Unlock()

	if known {
		w.mu.Lock()
		w.tickLocked(abs, t)
		w.mu.Unlock()
		return
	}

	if ev.Has(fsnotify.Create) && w.matcher != nil {
		gid, ok := w.matcher(abs)
		if !ok {
			return
		}
		// Newly-discovered files are read from offset 0: the Create event
		// arrives before writes, so we'd otherwise skip the initial content.
		if err := w.Add(abs, gid, true); err != nil && !errors.Is(err, fsnotify.ErrEventOverflow) {
			w.pushErr(err)
		}
	}
}

func (w *Watcher) tickAll() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for path, t := range w.tailers {
		w.tickLocked(path, t)
	}
}

// tickLocked must be called with w.mu held.
func (w *Watcher) tickLocked(path string, t *Tailer) {
	lines, _, err := t.Tick()
	if err != nil {
		w.pushErr(err)
	}
	gid := w.groups[path]
	for _, l := range lines {
		select {
		case w.events <- Event{Path: path, Group: gid, Line: l}:
		case <-w.done:
			return
		}
	}
}

func (w *Watcher) pushErr(err error) {
	select {
	case w.errs <- err:
	default:
	}
}
