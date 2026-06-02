package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mattn/go-isatty"
	"log-listener/internal/catalog"
	"log-listener/internal/config"
)

// runInit implements `log-listener init <app|bundle>... [flags]`.
// Flags: -o <path|-> (default ./log-listener.yml), --offline/--online,
// --force (overwrite/merge non-interactively), --merge (merge vs overwrite),
// --list (print available apps/bundles).
func runInit(args []string, stdout, stderr io.Writer) int {
	var names []string
	outPath := "log-listener.yml"
	var offline, force, merge, list bool
	online := false

	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "-o", "--output":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "log-listener init: -o needs a value")
				return 2
			}
			outPath = args[i+1]
			i++
		case "--offline":
			offline = true
		case "--online":
			online = true
		case "--force":
			force = true
		case "--merge":
			merge = true
		case "--list":
			list = true
		default:
			if strings.HasPrefix(a, "-") {
				fmt.Fprintf(stderr, "log-listener init: unknown flag %q\n", a)
				return 2
			}
			names = append(names, a)
		}
	}
	_ = online  // Phase 2 uses this; offline path ignores it
	_ = offline // offline is the only mode in Phase 1

	cat, err := catalog.Bundled()
	if err != nil {
		fmt.Fprintln(stderr, "log-listener init:", err)
		return 1
	}

	if list {
		printList(stdout, cat)
		return 0
	}
	if len(names) == 0 {
		fmt.Fprintln(stderr, "log-listener init: name at least one app or bundle (try --list)")
		return 2
	}

	env := catalog.DefaultEnv()
	gen, err := cat.Resolve(names, env)
	if err != nil {
		fmt.Fprintln(stderr, "log-listener init:", err)
		return 2
	}

	if outPath == "-" {
		data, err := gen.Marshal()
		if err != nil {
			fmt.Fprintln(stderr, "log-listener init:", err)
			return 1
		}
		_, _ = stdout.Write(data)
		return 0
	}

	final := gen
	if existingData, err := os.ReadFile(outPath); err == nil {
		// File exists — decide overwrite / merge / cancel.
		// Flags win; otherwise prompt on a TTY; otherwise refuse (non-interactive).
		action := ""
		switch {
		case force && merge:
			action = "merge"
		case force:
			action = "overwrite"
		case isTTY(os.Stdin):
			action = promptOverwrite(stdout, os.Stdin, outPath)
		default:
			fmt.Fprintf(stderr, "log-listener init: %s exists; pass --force (optionally --merge), or run in a terminal\n", outPath)
			return 1
		}
		switch action {
		case "cancel":
			fmt.Fprintln(stdout, "cancelled")
			return 0
		case "merge":
			existing, err := config.ParseFile(existingData)
			if err != nil {
				fmt.Fprintln(stderr, "log-listener init: cannot parse existing file:", err)
				return 1
			}
			final = config.MergeFiles(existing, gen)
		case "overwrite":
			final = gen
		}
	}

	data, err := final.Marshal()
	if err != nil {
		fmt.Fprintln(stderr, "log-listener init:", err)
		return 1
	}
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		fmt.Fprintln(stderr, "log-listener init:", err)
		return 1
	}
	fmt.Fprintf(stdout, "wrote %s (%d groups, %d renderers)\n",
		outPath, len(final.Directories)+len(final.Files), len(final.Renderers))
	return 0
}

func printList(w io.Writer, cat *catalog.Catalog) {
	fmt.Fprintln(w, "apps:")
	for name := range cat.Apps {
		fmt.Fprintf(w, "  %s\n", name)
	}
	fmt.Fprintln(w, "bundles:")
	for name, apps := range cat.Bundles {
		fmt.Fprintf(w, "  %s: %s\n", name, strings.Join(apps, ", "))
	}
}

// isTTY reports whether f is an interactive terminal (used to decide prompting).
// Uses IoctlGetTermios under the hood (via go-isatty) so /dev/null and pipes
// correctly return false even though they have ModeCharDevice set.
func isTTY(f *os.File) bool {
	return isatty.IsTerminal(f.Fd())
}

// promptOverwrite asks the overwrite/merge/cancel question and maps the reply
// to an action. Reader-injectable (not tied to a real TTY) so it is unit-testable.
// Returns "overwrite", "merge", or "cancel" (default/empty/unknown -> cancel).
func promptOverwrite(w io.Writer, r io.Reader, path string) string {
	fmt.Fprintf(w, "%s exists. [o]verwrite / [m]erge / [c]ancel? ", path)
	line, _ := bufio.NewReader(r).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "o", "overwrite":
		return "overwrite"
	case "m", "merge":
		return "merge"
	default:
		return "cancel"
	}
}
