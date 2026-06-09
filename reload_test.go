package main

import (
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/homeend/log-listener/internal/config"
	"github.com/homeend/log-listener/internal/discover"
	"github.com/homeend/log-listener/internal/linebuf"
	"github.com/homeend/log-listener/internal/render"
	"github.com/homeend/log-listener/internal/watch"
)

func TestWatchSetOfIdentity(t *testing.T) {
	cfg := &config.Config{}
	a := []discover.Assignment{{Path: "/var/log/a.log", GroupID: "g1"}}
	b := []discover.Assignment{{Path: "/var/log/a.log", GroupID: "g1"}}
	if watchSetOf(a, cfg) != watchSetOf(b, cfg) {
		t.Fatal("identical assignments should produce an identical watch-set")
	}
	// Order-independence: same files, different order → same identity.
	c := []discover.Assignment{
		{Path: "/var/log/a.log", GroupID: "g1"},
		{Path: "/var/log/b.log", GroupID: "g2"},
	}
	d := []discover.Assignment{
		{Path: "/var/log/b.log", GroupID: "g2"},
		{Path: "/var/log/a.log", GroupID: "g1"},
	}
	if watchSetOf(c, cfg) != watchSetOf(d, cfg) {
		t.Fatal("watch-set must be order-independent")
	}
	// A different group on the same path changes the set.
	e := []discover.Assignment{{Path: "/var/log/a.log", GroupID: "OTHER"}}
	if watchSetOf(a, cfg) == watchSetOf(e, cfg) {
		t.Fatal("a different group should change the watch-set")
	}
}

func emptyPipe(t *testing.T) *render.Pipeline {
	t.Helper()
	p, err := render.NewPipeline(nil, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// The core of the fix: an unchanged watch-set must NOT rebuild the watcher —
// the same *watch.Watcher is returned, so every tailer keeps its position.
func TestApplyReloadWatcherKeepsWatcherWhenUnchanged(t *testing.T) {
	w, err := watch.New(nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	pipe := emptyPipe(t)
	rt := &runtime{cfg: &config.Config{}, pipeline: pipe, assignments: nil}
	buf := linebuf.New(100, func(render.Event) []linebuf.Line { return nil })
	var pp atomic.Pointer[render.Pipeline]
	pp.Store(pipe)

	curSet := watchSetOf(rt.assignments, rt.cfg)
	gotW, gotSet := applyReloadWatcher(w, curSet, rt, buf, &pp, nil, io.Discard)
	if gotW != w {
		t.Fatal("unchanged watch-set must keep the SAME watcher (no rebuild)")
	}
	if gotSet != curSet {
		t.Fatalf("watch-set should be unchanged: %q vs %q", gotSet, curSet)
	}
}

// When the watch-set changes, the watcher IS rebuilt (a different instance).
func TestApplyReloadWatcherRebuildsWhenChanged(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "a.log")
	if err := os.WriteFile(logPath, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := watch.New(nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	pipe := emptyPipe(t)
	rt := &runtime{
		cfg:         &config.Config{},
		pipeline:    pipe,
		assignments: []discover.Assignment{{Path: logPath, GroupID: "g"}},
	}
	buf := linebuf.New(100, func(render.Event) []linebuf.Line { return nil })
	var pp atomic.Pointer[render.Pipeline]
	pp.Store(pipe)

	curSet := "" // empty initial set differs from the one-file rt
	gotW, gotSet := applyReloadWatcher(w, curSet, rt, buf, &pp, nil, io.Discard)
	if gotW == w {
		t.Fatal("changed watch-set must rebuild the watcher (different instance)")
	}
	defer gotW.Close()
	if gotSet == curSet {
		t.Fatal("watch-set should have changed")
	}
}
