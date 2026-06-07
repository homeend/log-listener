package blocks

import (
	"reflect"
	"testing"
)

func lines(ss ...string) []Line {
	out := make([]Line, len(ss))
	for i, s := range ss {
		out[i] = Line{Text: s}
	}
	return out
}

func ranges(bs []Block) [][2]int {
	out := make([][2]int, len(bs))
	for i, b := range bs {
		out[i] = [2]int{b.Start, b.End}
	}
	return out
}

func TestSegmentWhitespaceContinuation(t *testing.T) {
	got := ranges(Segment(lines(
		"NullPointerException: boom",
		"\tat Foo.bar(Foo.java:1)",
		"    at Foo.baz(Foo.java:2)",
		"next normal line",
	)))
	want := [][2]int{{0, 2}, {3, 3}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ranges = %v, want %v", got, want)
	}
}

func TestSegmentSignatureContinuation(t *testing.T) {
	got := ranges(Segment(lines(
		"panic: boom",
		"goroutine 1 [running]:",
		"Caused by: other",
		"#0 /a.php(9): f()",
		"plain head",
	)))
	want := [][2]int{{0, 3}, {4, 4}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ranges = %v, want %v", got, want)
	}
}

func TestSegmentBareAtIsNotASignature(t *testing.T) {
	got := ranges(Segment(lines("head", "at 10:00 server started")))
	want := [][2]int{{0, 0}, {1, 1}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ranges = %v, want %v", got, want)
	}
}

func TestSegmentRenderBlockRowContinues(t *testing.T) {
	ls := []Line{{Text: "msg:"}, {Text: "{", IsRenderBlock: true}, {Text: "}", IsRenderBlock: true}}
	got := ranges(Segment(ls))
	want := [][2]int{{0, 2}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ranges = %v, want %v", got, want)
	}
}

func TestSegmentLeadingWhitespaceIsDegenerateHead(t *testing.T) {
	got := ranges(Segment(lines("  indented first", "plain")))
	want := [][2]int{{0, 0}, {1, 1}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ranges = %v, want %v", got, want)
	}
}
