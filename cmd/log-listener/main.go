// Command log-listener tails configured log files and directories.
// Phase 1 surface: raw line emission on stdout, no renderers, no TUI, no SSE.
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
	"syscall"
	"time"

	"log-listener/internal/config"
	"log-listener/internal/discover"
	"log-listener/internal/watch"
)

func main() {
	code := run(os.Args[1:], os.Stdout, os.Stderr)
	os.Exit(code)
}

// run is the testable entry point. Returns the process exit code.
func run(args []string, stdout, stderr io.Writer) int {
	cfg, err := config.ParseArgs(args, time.Now())
	if err != nil {
		fmt.Fprintln(stderr, "log-listener:", err)
		return 2
	}

	assignments, err := discover.Assign(cfg.Groups, cfg.GlobalFilter)
	if err != nil {
		fmt.Fprintln(stderr, "log-listener:", err)
		return 1
	}

	if cfg.Once {
		if err := runOnce(assignments, stdout); err != nil {
			fmt.Fprintln(stderr, "log-listener:", err)
			return 1
		}
		return 0
	}

	if err := runWatch(cfg, assignments, stdout, stderr); err != nil {
		fmt.Fprintln(stderr, "log-listener:", err)
		return 1
	}
	return 0
}

func runOnce(assignments []discover.Assignment, stdout io.Writer) error {
	for _, a := range assignments {
		f, err := os.Open(a.Path)
		if err != nil {
			return fmt.Errorf("open %s: %w", a.Path, err)
		}
		s := bufio.NewScanner(f)
		s.Buffer(make([]byte, 64*1024), 16*1024*1024)
		for s.Scan() {
			fmt.Fprintln(stdout, format(a.GroupID, a.Path, s.Text()))
		}
		if err := s.Err(); err != nil {
			f.Close()
			return fmt.Errorf("read %s: %w", a.Path, err)
		}
		f.Close()
	}
	return nil
}

func runWatch(cfg *config.Config, assignments []discover.Assignment, stdout, stderr io.Writer) error {
	matcher := makeNewFileMatcher(cfg)
	w, err := watch.New(matcher, 500*time.Millisecond)
	if err != nil {
		return err
	}
	defer w.Close()

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
			fmt.Fprintln(stdout, format(ev.Group, ev.Path, ev.Line))
		case e := <-w.Errors():
			fmt.Fprintf(stderr, "log-listener: %v\n", e)
		case <-ctx.Done():
			drainDeadline := time.After(200 * time.Millisecond)
			for {
				select {
				case ev := <-w.Events():
					fmt.Fprintln(stdout, format(ev.Group, ev.Path, ev.Line))
				case e := <-w.Errors():
					fmt.Fprintf(stderr, "log-listener: %v\n", e)
				case <-drainDeadline:
					return nil
				}
			}
		}
	}
}

func format(group, path, line string) string {
	return fmt.Sprintf("[%s] %s: %s", group, filepath.Base(path), line)
}

func makeNewFileMatcher(cfg *config.Config) watch.NewFileMatcher {
	return func(path string) (string, bool) {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			return "", false
		}
		for _, g := range cfg.Groups {
			if g.Kind != discover.GroupDir {
				continue
			}
			if !pathUnderAny(path, g.Paths, g.Recursive) {
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
	for _, g := range cfg.Groups {
		if g.Kind != discover.GroupDir {
			continue
		}
		for _, root := range g.Paths {
			if !g.Recursive {
				add(root)
				continue
			}
			_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
				if err != nil || !d.IsDir() {
					return nil
				}
				add(p)
				return nil
			})
		}
	}
	return out
}
