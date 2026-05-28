// Package sink defines the output destinations for rendered events:
// terminal stdout (optionally colorized) and an HTTP/SSE broadcast hub.
package sink

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"log-listener/internal/render"
)

// ANSI SGR codes used for terminal colors. Empty strings are substituted when
// color is disabled, so callers don't need separate code paths.
const (
	ansiReset  = "\033[0m"
	ansiDim    = "\033[2m"
	ansiCyan   = "\033[36m"
	ansiBlue   = "\033[34m"
	ansiYellow = "\033[33m"
)

// Stdout is a terminal output sink. Color is enabled per-instance; callers
// determine whether stdout is a TTY (see IsTTY) and whether the user
// disabled color with --no-color or NO_COLOR.
type Stdout struct {
	w     io.Writer
	color bool
}

// NewStdout creates a Stdout sink.
func NewStdout(w io.Writer, color bool) *Stdout {
	return &Stdout{w: w, color: color}
}

// Emit writes the rendered event in the human-readable format:
//
//	[<group>] <basename>: <text...>
//	<indented JSON block>
//	<pretty XML block>
//
// JSON parts are marshaled with 2-space indent; XML parts are pre-formatted
// by the renderer.
func (s *Stdout) Emit(ev render.Event) {
	var text strings.Builder
	var blocks []string
	for _, p := range ev.Rendered {
		switch p.Type {
		case "text":
			text.WriteString(p.Value.(string))
		case "json":
			b, err := json.MarshalIndent(p.Value, "", "  ")
			if err != nil {
				continue
			}
			blocks = append(blocks, string(b))
		case "xml":
			blocks = append(blocks, p.Value.(string))
		}
	}
	group, base := ev.Group, filepath.Base(ev.File)
	if s.color {
		fmt.Fprintf(s.w, "%s[%s]%s %s%s%s: %s",
			ansiCyan, group, ansiReset,
			ansiBlue, base, ansiReset,
			text.String())
	} else {
		fmt.Fprintf(s.w, "[%s] %s: %s", group, base, text.String())
	}
	if !strings.HasSuffix(text.String(), "\n") {
		fmt.Fprintln(s.w)
	}
	for _, b := range blocks {
		if s.color {
			fmt.Fprintf(s.w, "%s%s%s\n", ansiDim, b, ansiReset)
		} else {
			fmt.Fprintln(s.w, b)
		}
	}
}

// Close is a no-op for Stdout; it doesn't own the writer.
func (s *Stdout) Close() error { return nil }

// IsTTY reports whether the given file is a character device (terminal).
// Used to auto-disable color when output is piped or redirected.
func IsTTY(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
