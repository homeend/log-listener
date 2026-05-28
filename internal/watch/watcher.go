package watch

import (
	"errors"
	"io/fs"
	"os"
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

// NewDirMatcher decides whether a newly-created directory is interesting
// enough to add a watch on (so its children fire fsnotify events) and to
// recursively scan for matching files. Used to pick up pattern-based dir
// matches (-d '/tmp/acp-logs-*/sub') and the parent-of-glob case for files
// (-f '/tmp/acp-logs-*/sub/*.log').
type NewDirMatcher func(path string) (accept bool)

// Watcher fans fsnotify events out to per-file Tailers and forwards their
// lines on Events(). New files appearing in a watched directory are matched
// via NewFileMatcher; if accepted, a Tailer is created for them.
type Watcher struct {
	fs         *fsnotify.Watcher
	matcher    NewFileMatcher
	dirMatcher NewDirMatcher
	mu         sync.Mutex
	tailers    map[string]*Tailer  // path → tailer
	groups     map[string]string   // path → group ID
	watched    map[string]struct{} // directory paths we've added to fsnotify
	events     chan Event
	errs       chan error
	done       chan struct{}
	closeOnce  sync.Once
	pollEvery  time.Duration
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

// SetDirMatcher installs a NewDirMatcher that is consulted whenever a
// directory appears (Create event on a watched parent). If it accepts the
// new dir, the watcher adds an fsnotify watch on it AND recursively scans
// it for files; each file is offered to the NewFileMatcher.
func (w *Watcher) SetDirMatcher(m NewDirMatcher) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.dirMatcher = m
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

	if !ev.Has(fsnotify.Create) {
		return
	}

	// Stat to find out whether this is a file or a directory. A miss is
	// fine (created-then-removed race) — just bail.
	info, statErr := os.Stat(abs)
	if statErr != nil {
		return
	}
	if info.IsDir() {
		w.handleNewDir(abs)
		return
	}
	if w.matcher != nil {
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

// handleNewDir runs when a Create event fires for a path that turned out to
// be a directory. If the dirMatcher accepts the new dir, we add a watch on
// it (so future child Creates fire) and recursively scan it for files —
// each file is offered to the file matcher. The dir matcher's "accept"
// semantics are "prefix match of some configured pattern" so intermediate
// dirs in a multi-segment pattern (e.g. /tmp/acp-*/sub) get watched too.
func (w *Watcher) handleNewDir(abs string) {
	w.mu.Lock()
	accept := w.dirMatcher != nil && w.dirMatcher(abs)
	matcher := w.matcher
	w.mu.Unlock()
	if !accept {
		return
	}
	if err := w.WatchDir(abs); err != nil && !errors.Is(err, fsnotify.ErrEventOverflow) {
		w.pushErr(err)
	}
	// Walk for any pre-existing files / subdirs that already lived under
	// this dir at Create time (rare for log dirs but cheap to handle).
	_ = filepath.WalkDir(abs, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if p == abs {
				return nil
			}
			// Recurse into descendant dirs the same way: if the dirMatcher
			// would accept them, watch them so their children fire events.
			w.mu.Lock()
			subAccept := w.dirMatcher != nil && w.dirMatcher(p)
			w.mu.Unlock()
			if subAccept {
				_ = w.WatchDir(p)
			}
			return nil
		}
		if matcher == nil {
			return nil
		}
		gid, ok := matcher(p)
		if !ok {
			return nil
		}
		_ = w.Add(p, gid, true)
		return nil
	})
}

func (w *Watcher) tickAll() {
	// Snapshot under the lock so a slow consumer on w.events doesn't stall
	// Add/WatchDir/Close while we're ticking.
	type pair struct {
		path string
		t    *Tailer
	}
	w.mu.Lock()
	snap := make([]pair, 0, len(w.tailers))
	for path, t := range w.tailers {
		snap = append(snap, pair{path, t})
	}
	w.mu.Unlock()
	for _, p := range snap {
		w.tickOne(p.path, p.t)
	}
}

// tickOne is the unlocked version of tickLocked used by tickAll's snapshot
// iteration. It must not be called concurrently for the same Tailer.
func (w *Watcher) tickOne(path string, t *Tailer) {
	lines, _, err := t.Tick()
	if err != nil {
		w.pushErr(err)
	}
	w.mu.Lock()
	gid := w.groups[path]
	w.mu.Unlock()
	for _, l := range lines {
		select {
		case w.events <- Event{Path: path, Group: gid, Line: l}:
		case <-w.done:
			return
		}
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
