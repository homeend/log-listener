package blocks

import (
	"regexp"
	"strings"
)

type exceptionProcessor struct{}

func init() { processors = append(processors, exceptionProcessor{}) }

// jvmFrameRE matches a JVM stack frame ending in (File.java:NN) or (File.kt:NN).
var jvmFrameRE = regexp.MustCompile(`\.(java|kt):\d+\)`)

// jsFrameRE matches a V8/Node frame tail ":line:col" (two colons), optionally
// closed by ")".
var jsFrameRE = regexp.MustCompile(`:\d+:\d+\)?$`)

// Process flags the block as a likely exception and guesses the language by
// scanning its lines for per-language markers. Heuristic, signature-based —
// precision over recall. Single-line headers win first; otherwise frame shape
// distinguishes JVM (Java/Kotlin) from JS/TS.
func (exceptionProcessor) Process(b *Block, lines []Line) {
	for i := b.Start; i <= b.End && i < len(lines); i++ {
		t := lines[i].Text
		switch {
		case strings.HasPrefix(t, "Traceback (most recent call last):"):
			b.Exception = &ExceptionInfo{Language: "python"}
			return
		case strings.HasPrefix(t, "panic:") || strings.HasPrefix(t, "goroutine ") || strings.Contains(t, "runtime error:"):
			b.Exception = &ExceptionInfo{Language: "go"}
			return
		case strings.HasPrefix(t, "thread '") && strings.Contains(t, "panicked at"):
			b.Exception = &ExceptionInfo{Language: "rust"}
			return
		case strings.Contains(t, "AddressSanitizer:") || strings.Contains(t, "terminate called after throwing an instance of"):
			b.Exception = &ExceptionInfo{Language: "c/c++"}
			return
		case (strings.Contains(t, "PHP ") && (strings.Contains(t, "Fatal error") || strings.Contains(t, "Uncaught"))) || strings.HasPrefix(t, "Stack trace:"):
			b.Exception = &ExceptionInfo{Language: "php"}
			return
		}
	}
	if lang, ok := detectFrameLanguage(*b, lines); ok {
		b.Exception = &ExceptionInfo{Language: lang}
	}
}

// detectFrameLanguage classifies a block by the shape of its `at …` frames.
func detectFrameLanguage(b Block, lines []Line) (string, bool) {
	var java, kotlin, ts, js bool
	for i := b.Start; i <= b.End && i < len(lines); i++ {
		t := strings.TrimLeft(lines[i].Text, " \t")
		if !strings.HasPrefix(t, "at ") {
			continue
		}
		switch {
		case jvmFrameRE.MatchString(t):
			if strings.Contains(t, ".kt:") {
				kotlin = true
			} else {
				java = true
			}
		case strings.Contains(t, ".ts:") && jsFrameRE.MatchString(t):
			ts = true
		case jsFrameRE.MatchString(t):
			js = true
		}
	}
	switch {
	case kotlin:
		return "kotlin", true
	case java:
		return "java", true
	case ts:
		return "typescript", true
	case js:
		return "javascript", true
	}
	return "", false
}
