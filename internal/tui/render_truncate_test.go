package tui

import (
	"strings"
	"testing"
)

func TestRenderTruncatesFileColumnAndWidth(t *testing.T) {
	m := newModel(100)
	m.showGroup = false // isolate the file column
	m.showFile = true
	m.truncateFiles = true
	m.filenameWidth = 16

	dl := displayLine{
		file:      "application-server.log", // 22 cols -> "applica...er.log" (16)
		body:      "x",
		bodyWidth: 1,
	}
	out, w := m.renderDisplayLineCore(dl, false)

	if !strings.Contains(stripANSI(out), "applica...er.log") {
		t.Fatalf("file not truncated in output: %q", stripANSI(out))
	}
	if strings.Contains(stripANSI(out), "application-server.log") {
		t.Fatalf("full name leaked into output: %q", stripANSI(out))
	}
	// visW = bodyWidth(1) + dispWidth("applica...er.log")(16) + 2 (": ") = 19.
	if w != 19 {
		t.Fatalf("reported width want 19, got %d", w)
	}
}

func TestRenderNoTruncateWhenOff(t *testing.T) {
	m := newModel(100)
	m.showGroup = false
	m.showFile = true
	m.truncateFiles = false // off

	dl := displayLine{file: "application-server.log", body: "x", bodyWidth: 1}
	out, _ := m.renderDisplayLineCore(dl, false)
	if !strings.Contains(stripANSI(out), "application-server.log") {
		t.Fatalf("name should be full when truncation off: %q", stripANSI(out))
	}
}
