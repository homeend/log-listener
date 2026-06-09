package linebuf

import (
	"strings"
	"testing"

	"github.com/homeend/log-listener/internal/render"
)

func TestDuplicateReportFlagsRepeatedRaw(t *testing.T) {
	b := New(100, func(render.Event) []Line { return []Line{{Text: "x"}} })
	b.Append(render.Event{Group: "g", File: "/a.log", Raw: "alpha"})
	b.Append(render.Event{Group: "g", File: "/a.log", Raw: "beta"})
	b.Append(render.Event{Group: "g", File: "/a.log", Raw: "alpha"}) // dup of #1
	rep := b.DuplicateReport()
	if !strings.Contains(rep, "DUP x2") || !strings.Contains(rep, "alpha") {
		t.Fatalf("should flag the repeated line:\n%s", rep)
	}
	if !strings.Contains(rep, "distinct duplicated lines: 1") {
		t.Fatalf("should total 1 distinct dup:\n%s", rep)
	}
}

func TestDuplicateReportCleanBuffer(t *testing.T) {
	b := New(100, func(render.Event) []Line { return []Line{{Text: "x"}} })
	b.Append(render.Event{Raw: "one"})
	b.Append(render.Event{Raw: "two"})
	if rep := b.DuplicateReport(); !strings.Contains(rep, "no duplicate Raw content") {
		t.Fatalf("clean buffer should report none:\n%s", rep)
	}
}
