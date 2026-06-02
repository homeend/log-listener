package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"log-listener/internal/catalog"
	"log-listener/internal/config"
)

// initFetcher is a seam so tests can inject a fake remote catalog.
var initFetcher = func() catalog.Fetcher { return catalog.NewHTTPFetcher() }

// runInit implements `log-listener init <app|bundle>... [flags]`.
// Flags: -o <path|-> (default ./log-listener.yml), --offline/--online,
// --force (overwrite/merge non-interactively), --merge (merge vs overwrite),
// --list (print available apps/bundles).
//
// interactive reports whether prompts are allowed (a real terminal); the caller
// decides it (production: sink.IsTTY(os.Stdin)) so this stays testable without a
// TTY. When an existing output file is found and the run is non-interactive,
// init refuses unless --force is given. stdin is where prompt replies are read.
func runInit(args []string, stdin io.Reader, interactive bool, stdout, stderr io.Writer) int {
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
	bundled, err := catalog.Bundled()
	if err != nil {
		fmt.Fprintln(stderr, "log-listener init:", err)
		return 1
	}

	if list {
		printList(stdout, bundled)
		return 0
	}
	if len(names) == 0 {
		fmt.Fprintln(stderr, "log-listener init: name at least one app or bundle (try --list)")
		return 2
	}

	// Choose the catalog source. --online/--offline force it; otherwise, on an
	// interactive run, ask. Non-interactive defaults to offline. Select() falls
	// back to bundled on any network/parse failure, so this never hard-fails.
	cat := bundled
	useOnline := online
	if !online && !offline && interactive {
		useOnline = promptYesNo(stdout, stdin, "Check GitHub for newer templates?")
	}
	if useOnline {
		cat = catalog.Select(bundled, initFetcher())
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
		case interactive:
			action = promptOverwrite(stdout, stdin, outPath)
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
	apps := make([]string, 0, len(cat.Apps))
	for name := range cat.Apps {
		apps = append(apps, name)
	}
	sort.Strings(apps)
	fmt.Fprintln(w, "apps:")
	for _, name := range apps {
		fmt.Fprintf(w, "  %s\n", name)
	}

	bundles := make([]string, 0, len(cat.Bundles))
	for name := range cat.Bundles {
		bundles = append(bundles, name)
	}
	sort.Strings(bundles)
	fmt.Fprintln(w, "bundles:")
	for _, name := range bundles {
		fmt.Fprintf(w, "  %s: %s\n", name, strings.Join(cat.Bundles[name], ", "))
	}
}

// promptYesNo writes a [Y/n] prompt to w and reads a reply from r.
// Returns true for anything other than "n" or "no" (case-insensitive).
func promptYesNo(w io.Writer, r io.Reader, q string) bool {
	fmt.Fprintf(w, "%s [Y/n] ", q)
	line, _ := bufio.NewReader(r).ReadString('\n')
	s := strings.ToLower(strings.TrimSpace(line))
	return s != "n" && s != "no"
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
