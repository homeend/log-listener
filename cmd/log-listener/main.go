// Command log-listener tails configured log files and directories.
// Output destinations (auto-selected): colorized stdout when piped or with
// --no-tui, an interactive TUI when stdout is a TTY, and an optional SSE
// broadcast on http://<addr>/stream (enabled by --sse or output.sse in YAML).
// See README.md for the full reference and PLAN.md for the architecture.
package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"log-listener/internal/config"
	"log-listener/internal/configwatch"
	"log-listener/internal/discover"
	"log-listener/internal/render"
	"log-listener/internal/sink"
	"log-listener/internal/tui"
	"log-listener/internal/watch"
)

func main() {
	code := run(os.Args[1:], os.Stdout, os.Stderr)
	os.Exit(code)
}

// run is the testable entry point. Returns the process exit code.
func run(args []string, stdout, stderr io.Writer) int {
	cfg, err := config.Load(args, time.Now())
	if err != nil {
		fmt.Fprintln(stderr, "log-listener:", err)
		return 2
	}

	assignments, err := discover.Assign(cfg.Groups, cfg.GlobalFilter)
	if err != nil {
		fmt.Fprintln(stderr, "log-listener:", err)
		return 1
	}

	pipeline, err := render.NewPipeline(cfg.RendererSpecs, cfg.DropUnmatched)
	if err != nil {
		fmt.Fprintln(stderr, "log-listener:", err)
		return 2
	}

	var pipePtr atomic.Pointer[render.Pipeline]
	pipePtr.Store(pipeline)

	// Color is on only when (a) the user didn't pass --no-color AND (b) the
	// output is a real TTY. Non-*os.File writers (e.g. a test bytes.Buffer)
	// are treated as non-TTY.
	useColor := !cfg.NoColor
	if useColor {
		f, ok := stdout.(*os.File)
		if !ok || !sink.IsTTY(f) {
			useColor = false
		}
	}
	stdoutSink := sink.NewStdout(stdout, useColor)

	var sseHub *sink.SSEHub
	if cfg.SSEAddr != "" {
		sseHub = sink.NewSSEHub(cfg.SSEAddr)
		if err := sseHub.Start(); err != nil {
			fmt.Fprintln(stderr, "log-listener: sse:", err)
			return 1
		}
		defer sseHub.Close()
	}

	if cfg.Once {
		if err := runOnce(assignments, pipeline, stdoutSink, sseHub); err != nil {
			fmt.Fprintln(stderr, "log-listener:", err)
			return 1
		}
		return 0
	}

	// TUI mode requires a TTY and --no-tui not set; --once already returned.
	useTUI := !cfg.NoTUI
	if useTUI {
		f, ok := stdout.(*os.File)
		if !ok || !sink.IsTTY(f) {
			useTUI = false
		}
	}

	if useTUI {
		if err := runWatchTUI(cfg, assignments, &pipePtr, sseHub, stderr); err != nil {
			fmt.Fprintln(stderr, "log-listener:", err)
			return 1
		}
		return 0
	}

	if err := runWatch(cfg, args, cfg.DropUnmatched, assignments, &pipePtr, stdoutSink, sseHub, stderr); err != nil {
		fmt.Fprintln(stderr, "log-listener:", err)
		return 1
	}
	return 0
}

// runtime bundles the per-config-load derived state: the parsed config, the
// compiled renderer pipeline, and the file→group assignments. Built once at
// startup and rebuilt on every config reload.
type runtime struct {
	cfg         *config.Config
	pipeline    *render.Pipeline
	assignments []discover.Assignment
}

// loadRuntime parses args (re-reading the YAML file), assigns files to groups,
// and compiles the renderer pipeline. It is the RELOAD seam: on a config
// reload the watcher and pipeline are rebuilt from a fresh runtime. Startup
// (run) intentionally inlines the same three calls instead of using this,
// because it already parses cfg once to make mode decisions (SSE/TUI/color)
// and would otherwise parse the file twice.
//
// dropUnmatched is passed explicitly so a reload keeps the STARTUP drop
// setting rather than the reloaded file's value (output settings are out of
// scope for reload). Pure and side-effect-free — the unit-testable seam.
func loadRuntime(args []string, dropUnmatched bool, now time.Time) (*runtime, error) {
	cfg, err := config.Load(args, now)
	if err != nil {
		return nil, err
	}
	assignments, err := discover.Assign(cfg.Groups, cfg.GlobalFilter)
	if err != nil {
		return nil, err
	}
	pipeline, err := render.NewPipeline(cfg.RendererSpecs, dropUnmatched)
	if err != nil {
		return nil, err
	}
	return &runtime{cfg: cfg, pipeline: pipeline, assignments: assignments}, nil
}

// buildWatcher constructs a fresh watch.Watcher wired with matcher closures
// over cfg, registers every assignment as a tailer (fromStart=false → start at
// EOF, so neither startup nor reload replays existing file content), and adds directory watches.
// Per-file/dir failures are logged to stderr but do not abort.
func buildWatcher(cfg *config.Config, assignments []discover.Assignment, stderr io.Writer) (*watch.Watcher, error) {
	w, err := watch.New(makeNewFileMatcher(cfg), 2*time.Second)
	if err != nil {
		return nil, err
	}
	w.SetDirMatcher(makeNewDirMatcher(cfg))
	for _, a := range assignments {
		if err := w.Add(a.Path, a.GroupID, false); err != nil {
			fmt.Fprintf(stderr, "log-listener: cannot tail %s: %v\n", a.Path, err)
		}
	}
	for _, d := range dirsToWatch(cfg) {
		if err := w.WatchDir(d); err != nil {
			fmt.Fprintf(stderr, "log-listener: cannot watch %s: %v\n", d, err)
		}
	}
	return w, nil
}

// runOnce uses the concrete pipeline directly — --once exits before the
// watcher or reload machinery starts, so no swap can occur. That's why it
// can't share emit(), which loads through the atomic pointer.
func runOnce(assignments []discover.Assignment, pipeline *render.Pipeline, stdoutSink *sink.Stdout, sseHub *sink.SSEHub) error {
	for _, a := range assignments {
		f, err := os.Open(a.Path)
		if err != nil {
			return fmt.Errorf("open %s: %w", a.Path, err)
		}
		s := bufio.NewScanner(f)
		s.Buffer(make([]byte, 64*1024), 16*1024*1024)
		for s.Scan() {
			ev, ok := pipeline.Render(time.Now(), a.GroupID, a.Path, s.Text())
			if ok {
				stdoutSink.Emit(ev)
				if sseHub != nil {
					sseHub.Emit(ev)
				}
			}
		}
		if err := s.Err(); err != nil {
			f.Close()
			return fmt.Errorf("read %s: %w", a.Path, err)
		}
		f.Close()
	}
	return nil
}

func runWatch(cfg *config.Config, args []string, dropUnmatched bool, assignments []discover.Assignment, pipePtr *atomic.Pointer[render.Pipeline], stdoutSink *sink.Stdout, sseHub *sink.SSEHub, stderr io.Writer) error {
	w, err := buildWatcher(cfg, assignments, stderr)
	if err != nil {
		return err
	}
	defer w.Close()

	var cfgChanges <-chan struct{}
	if cfg.SourcePath != "" {
		cw, err := configwatch.New(cfg.SourcePath, 300*time.Millisecond)
		if err != nil {
			fmt.Fprintf(stderr, "log-listener: config watch disabled: %v\n", err)
		} else {
			defer cw.Close()
			cfgChanges = cw.Changes()
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 4)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
		// Any subsequent signal hard-exits, no matter how long shutdown
		// takes. The handler must keep listening forever — otherwise
		// signal.Notify suppresses the default SIGINT handler and the
		// process becomes unkillable via Ctrl+C.
		for range sigCh {
			os.Exit(130)
		}
	}()

	for {
		select {
		case ev := <-w.Events():
			emit(pipePtr, stdoutSink, sseHub, ev.Group, ev.Path, ev.Line)
		case e := <-w.Errors():
			fmt.Fprintf(stderr, "log-listener: %v\n", e)
		case <-cfgChanges:
			rt, err := loadRuntime(args, dropUnmatched, time.Now())
			if err != nil {
				continue // silent: keep the last-good config running
			}
			newW, err := buildWatcher(rt.cfg, rt.assignments, stderr)
			if err != nil {
				continue
			}
			pipePtr.Store(rt.pipeline)
			w.Close()
			w = newW
		case <-ctx.Done():
			drainDeadline := time.After(200 * time.Millisecond)
			for {
				select {
				case ev := <-w.Events():
					emit(pipePtr, stdoutSink, sseHub, ev.Group, ev.Path, ev.Line)
				case e := <-w.Errors():
					fmt.Fprintf(stderr, "log-listener: %v\n", e)
				case <-drainDeadline:
					return nil
				}
			}
		}
	}
}

// emit routes a raw line through the renderer pipeline then fans out to the
// stdout sink and (if running) the SSE broadcast hub.
func emit(pipePtr *atomic.Pointer[render.Pipeline], stdoutSink *sink.Stdout, sseHub *sink.SSEHub, group, path, line string) {
	ev, ok := pipePtr.Load().Render(time.Now(), group, path, line)
	if !ok {
		return
	}
	stdoutSink.Emit(ev)
	if sseHub != nil {
		sseHub.Emit(ev)
	}
}

// runWatchTUI is the TUI variant of runWatch. The bubbletea program owns the
// terminal on the main goroutine, while a background goroutine pumps watcher
// events through the renderer pipeline into app.Push() and (if configured)
// the SSE hub.
func runWatchTUI(cfg *config.Config, assignments []discover.Assignment, pipePtr *atomic.Pointer[render.Pipeline], sseHub *sink.SSEHub, stderr io.Writer) error {
	w, err := buildWatcher(cfg, assignments, stderr)
	if err != nil {
		return err
	}
	defer w.Close()

	// Initial file list, groups, and renderers — passed through tui.New
	// so the model is seeded before bubbletea starts. Calling SetFiles
	// before Run would deadlock: bubbletea's msgs channel is unbuffered
	// and Run hasn't started reading from it yet.
	initial := make([]tui.FileEntry, 0, len(assignments))
	for _, a := range assignments {
		initial = append(initial, tui.FileEntry{Path: a.Path, Group: a.GroupID})
	}
	groups := make([]tui.GroupInfo, 0, len(cfg.Groups))
	for _, g := range cfg.Groups {
		groups = append(groups, tui.GroupInfo{ID: g.ID, StartOff: g.StartOff})
	}
	p0 := pipePtr.Load()
	renderers := make([]tui.RendererInfo, p0.RendererCount())
	for i := range renderers {
		renderers[i] = tui.RendererInfo{
			Name:     p0.RendererName(i),
			StartOff: !p0.IsEnabled(i),
		}
	}
	app := tui.New(tui.Options{
		Scrollback:   cfg.TUIScrollback,
		InitialFiles: initial,
		Groups:       groups,
		Renderers:    renderers,
		SetRendererOn: func(i int, on bool) { pipePtr.Load().SetRendererEnabled(i, on) },
		RenderFn: func(group, file, raw string) (render.Event, bool) {
			return pipePtr.Load().Render(time.Now(), group, file, raw)
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 4)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		app.Quit()
		for range sigCh {
			os.Exit(130)
		}
	}()

	go func() {
		for {
			select {
			case ev := <-w.Events():
				rev, ok := pipePtr.Load().Render(time.Now(), ev.Group, ev.Path, ev.Line)
				if !ok {
					continue
				}
				app.Push(rev)
				if sseHub != nil {
					sseHub.Emit(rev)
				}
			case <-w.Errors():
				// Errors go to /dev/null in TUI mode for now — a future
				// phase could surface them in a status bar.
			case <-ctx.Done():
				return
			}
		}
	}()

	return app.Run()
}

func makeNewFileMatcher(cfg *config.Config) watch.NewFileMatcher {
	return func(path string) (string, bool) {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			return "", false
		}
		for _, g := range cfg.Groups {
			if g.Kind == discover.GroupFile {
				// File groups: literal exact match or glob pattern match
				// on the full path. No filter applied (file groups are
				// always unfiltered, like the static -f expansion).
				for _, p := range g.Paths {
					absP, _ := filepath.Abs(p)
					absFile, _ := filepath.Abs(path)
					if discover.HasMeta(absP) {
						if m, _ := filepath.Match(absP, absFile); m {
							return g.ID, true
						}
					} else if absP == absFile {
						return g.ID, true
					}
				}
				continue
			}
			// Dir group: path must lie under one of the configured paths
			// (literal or pattern), then satisfy the file filter.
			if !fileBelongsToDirGroup(g, path) {
				continue
			}
			if !g.Filter.Allow(info.Name(), info.ModTime()) {
				continue
			}
			if !cfg.GlobalFilter.Allow(info.Name(), info.ModTime()) {
				continue
			}
			return g.ID, true
		}
		return "", false
	}
}

// makeNewDirMatcher accepts a newly-appeared directory if any group's
// configured path (literal recursive dir, or pattern) plausibly leads to
// log files inside that directory tree. Used by the watcher to decide
// whether to add an fsnotify watch on the new dir and scan it.
func makeNewDirMatcher(cfg *config.Config) watch.NewDirMatcher {
	return func(path string) bool {
		absPath, err := filepath.Abs(path)
		if err != nil {
			return false
		}
		for _, g := range cfg.Groups {
			for _, p := range g.Paths {
				absP, err := filepath.Abs(p)
				if err != nil {
					continue
				}
				if discover.HasMeta(absP) {
					// Pattern: watch if absPath is a prefix-match. Covers
					// the multi-hop case (e.g. /tmp/acp-*/sub where we
					// need to start watching /tmp/acp-NEW before /sub is
					// created inside it).
					if m, _ := discover.PrefixMatchesPattern(absP, absPath); m {
						return true
					}
				} else if g.Kind == discover.GroupDir && g.Recursive {
					// Recursive literal dir: any descendant matters.
					if pathUnderAny(absPath, []string{absP}, true) {
						return true
					}
				}
			}
		}
		return false
	}
}

// fileBelongsToDirGroup reports whether filePath is under some entry in
// g.Paths, honouring g.Recursive. Pattern entries are matched
// segment-wise; literal entries fall through to pathUnderAny.
func fileBelongsToDirGroup(g *discover.Group, filePath string) bool {
	absFile, err := filepath.Abs(filePath)
	if err != nil {
		return false
	}
	for _, p := range g.Paths {
		absP, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		if discover.HasMeta(absP) {
			if fileMatchesDirPattern(absP, absFile, g.Recursive) {
				return true
			}
		} else if pathUnderAny(absFile, []string{absP}, g.Recursive) {
			return true
		}
	}
	return false
}

// fileMatchesDirPattern reports whether file is inside some directory
// that matches dirPattern. recursive=false → file's immediate parent
// must match; recursive=true → any ancestor may match.
func fileMatchesDirPattern(dirPattern, file string, recursive bool) bool {
	fileDir := filepath.Dir(file)
	dir := fileDir
	for {
		if m, _ := discover.MatchesPath(dirPattern, dir); m {
			if !recursive {
				return dir == fileDir
			}
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

func pathUnderAny(path string, roots []string, recursive bool) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	for _, root := range roots {
		rootAbs, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(rootAbs, abs)
		if err != nil || strings.HasPrefix(rel, "..") || rel == "." {
			continue
		}
		if recursive {
			return true
		}
		if !strings.ContainsRune(rel, filepath.Separator) {
			return true
		}
	}
	return false
}

func dirsToWatch(cfg *config.Config) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(d string) {
		abs, err := filepath.Abs(d)
		if err != nil {
			return
		}
		if _, dup := seen[abs]; dup {
			return
		}
		seen[abs] = struct{}{}
		out = append(out, abs)
	}

	walkDirIntoWatches := func(root string, recursive bool) {
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			return
		}
		add(root)
		if !recursive {
			return
		}
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil || !d.IsDir() {
				return nil
			}
			add(p)
			return nil
		})
	}

	for _, g := range cfg.Groups {
		for _, root := range g.Paths {
			if discover.HasMeta(root) {
				// Pattern path: watch the literal prefix so future dirs
				// matching the pattern fire Create events. Also walk all
				// currently-matching expansions so their existing subtrees
				// are watched (dir group recurses; file group only needs
				// the matched dir for its file events).
				if prefix := discover.LiteralPrefix(root); prefix != "" {
					add(prefix)
				}
				matches, _ := filepath.Glob(root)
				for _, m := range matches {
					if g.Kind == discover.GroupDir {
						walkDirIntoWatches(m, g.Recursive)
					} else {
						add(filepath.Dir(m))
					}
				}
				continue
			}

			// Literal path. Dir groups → walk; file groups → parent dir.
			if g.Kind == discover.GroupDir {
				walkDirIntoWatches(root, g.Recursive)
			} else {
				add(filepath.Dir(root))
			}
		}
	}
	return out
}
