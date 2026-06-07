// Package blocks groups a line stream into multi-line blocks (stack traces,
// pretty-printed JSON/XML, indented continuations) and runs annotate-only
// processors over them. Neutral and dependency-free so both the TUI and the
// MCP server can consume it.
package blocks

import "strings"

// Line is the neutral input: the plain (ANSI-stripped) text of one row, plus
// whether it is a render-block row (pretty-printed JSON/XML), which is always a
// continuation regardless of leading whitespace.
type Line struct {
	Text          string
	IsRenderBlock bool
}

// ExceptionInfo is the exception processor's annotation.
type ExceptionInfo struct {
	Language string // "java", "python", … or "" if unsure
}

// Block is a contiguous run of lines [Start, End] (inclusive indices into the
// Line slice it was segmented from), plus processor annotations.
type Block struct {
	Start, End int
	Exception  *ExceptionInfo
}

// Processed reports whether any processor matched this block. v1: exception only.
func (b Block) Processed() bool { return b.Exception != nil }

// IsWhitespaceCont is the whitespace-only continuation test shared with the
// TUI's isContinuation: a render-block row, or a non-empty line whose first
// byte is a space or tab. The segmenter layers signatures on top via
// IsContinuation; collapse uses only this primitive.
func IsWhitespaceCont(ln Line) bool {
	if ln.IsRenderBlock {
		return true
	}
	if ln.Text == "" {
		return false
	}
	c := ln.Text[0]
	return c == ' ' || c == '\t'
}

// hasContSignature matches the small set of non-indented prefixes that
// nonetheless continue a block. Tab/space-indented frames are already caught by
// IsWhitespaceCont and are intentionally absent here.
func hasContSignature(text string) bool {
	if strings.HasPrefix(text, "Caused by:") || strings.HasPrefix(text, "goroutine ") {
		return true
	}
	// PHP frames: '#' + digits + ' ' at line start (e.g. "#0 /path(9): f()").
	if len(text) >= 3 && text[0] == '#' && text[1] >= '0' && text[1] <= '9' {
		i := 1
		for i < len(text) && text[i] >= '0' && text[i] <= '9' {
			i++
		}
		if i < len(text) && text[i] == ' ' {
			return true
		}
	}
	return false
}

// IsContinuation is the segmenter's predicate: whitespace OR a signature.
func IsContinuation(ln Line) bool {
	if IsWhitespaceCont(ln) {
		return true
	}
	return hasContSignature(ln.Text)
}

// Processor annotates a block in place. Processors MUST NOT change Start/End.
type Processor interface {
	Process(b *Block, lines []Line)
}

// processors is the fixed processor set, populated by each processor file's
// init (v1: exception.go). No config-driven registry.
var processors []Processor

// Annotate runs every processor over a single (possibly still-growing) block.
func Annotate(b *Block, lines []Line) {
	for _, p := range processors {
		p.Process(b, lines)
	}
}

// Segment groups lines into blocks (head + following continuations) and
// annotates each with the processors. Pure / full recompute.
func Segment(lines []Line) []Block {
	var blocks []Block
	i := 0
	for i < len(lines) {
		start := i
		i++
		for i < len(lines) && IsContinuation(lines[i]) {
			i++
		}
		b := Block{Start: start, End: i - 1}
		Annotate(&b, lines)
		blocks = append(blocks, b)
	}
	return blocks
}
