package render

import "testing"

func TestDecomposeLinesTextHeadAndContinuations(t *testing.T) {
	ev := Event{Rendered: []Part{{Type: "text", Value: "head line\n  cont one\n  cont two"}}}
	got := DecomposeLines(ev)
	if len(got) != 3 {
		t.Fatalf("want 3 lines, got %d: %+v", len(got), got)
	}
	if got[0].Text != "head line" || got[0].IsCont {
		t.Errorf("head wrong: %+v", got[0])
	}
	if got[1].Text != "  cont one" || !got[1].IsCont {
		t.Errorf("cont1 wrong: %+v", got[1])
	}
}

func TestDecomposeLinesExpandsTabs(t *testing.T) {
	ev := Event{Rendered: []Part{{Type: "text", Value: "a\tb"}}}
	got := DecomposeLines(ev)
	if got[0].Text != "a       b" { // tab → spaces to 8-col stop
		t.Errorf("tabs not expanded: %q", got[0].Text)
	}
}

func TestDecomposeLinesJSONBlock(t *testing.T) {
	ev := Event{Rendered: []Part{
		{Type: "text", Value: "got json"},
		{Type: "json", Value: map[string]any{"k": "v"}},
	}}
	got := DecomposeLines(ev)
	if got[0].Text != "got json" || got[0].IsCont {
		t.Fatalf("head wrong: %+v", got[0])
	}
	if len(got) < 2 || !got[1].IsCont {
		t.Fatalf("json rows should be continuations: %+v", got)
	}
}
