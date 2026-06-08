package searchmatch

import "testing"

func TestSmartCaseFoldsWhenLowercase(t *testing.T) {
	m, err := Compile("error", false)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Match("an ERROR here") || !m.Match("Error") || !m.Match("error") {
		t.Fatal("lowercase query should fold case")
	}
}

func TestSmartCaseSensitiveWhenUppercase(t *testing.T) {
	m, _ := Compile("Error", false)
	if !m.Match("Error here") {
		t.Fatal("should match exact case")
	}
	if m.Match("an error here") {
		t.Fatal("uppercase query must be case-sensitive (should not match 'error')")
	}
}

func TestFindOffsetsOriginalText(t *testing.T) {
	m, _ := Compile("err", false) // folds
	s, e, ok := m.Find("an ERR x")
	if !ok || s != 3 || e != 6 {
		t.Fatalf("Find = (%d,%d,%v), want (3,6,true) into original text", s, e, ok)
	}
}

func TestFindMultibyteOffsets(t *testing.T) {
	m, _ := Compile("café", false)
	s, e, ok := m.Find("a café x") // 'é' is 2 bytes; offsets are byte offsets
	if !ok || "a café x"[s:e] != "café" {
		t.Fatalf("Find slice = %q, want café (s=%d e=%d ok=%v)", "a café x"[s:e], s, e, ok)
	}
}

func TestFoldMatchesNonASCIIUppercase(t *testing.T) {
	// Exercises the (?i) fold on a non-ASCII letter: lowercase query, uppercase
	// text. A plain strings.Contains fold would miss this.
	m, _ := Compile("café", false)
	text := "a CAFÉ x"
	if !m.Match(text) {
		t.Fatal("smart-case fold should match CAFÉ for query café")
	}
	if s, e, ok := m.Find(text); !ok || text[s:e] != "CAFÉ" {
		t.Fatalf("Find = %q (s=%d e=%d ok=%v), want CAFÉ", text[s:e], s, e, ok)
	}
}

func TestZeroValueAndNilMatchNothing(t *testing.T) {
	var byVal Matcher
	if byVal.Match("anything") || len(byVal.FindAll("anything")) != 0 {
		t.Fatal("zero-value Matcher must match nothing (no Contains(text,\"\")==true trap)")
	}
	if _, _, ok := byVal.Find("anything"); ok {
		t.Fatal("zero-value Find must report no match")
	}
	var nilM *Matcher
	if nilM.Match("x") || nilM.FindAll("x") != nil {
		t.Fatal("nil *Matcher must match nothing, not panic")
	}
}

func TestRegexMatchAndFindAll(t *testing.T) {
	m, err := Compile("a.c", true)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Match("xabcx") || m.Match("xyz") {
		t.Fatal("regex match wrong")
	}
	all := m.FindAll("abc-aXc")
	if len(all) != 2 {
		t.Fatalf("FindAll = %v, want 2 matches", all)
	}
}

func TestInvalidRegexErrors(t *testing.T) {
	if _, err := Compile("a(", true); err == nil {
		t.Fatal("invalid regex should error")
	}
}

func TestEmptyQueryMatchesNothing(t *testing.T) {
	m, _ := Compile("", false)
	if m.Match("anything") {
		t.Fatal("empty query must match nothing")
	}
	if all := m.FindAll("anything"); len(all) != 0 {
		t.Fatalf("FindAll on empty query = %v, want none", all)
	}
}

func TestFindAllZeroWidthRegexTerminates(t *testing.T) {
	m, _ := Compile("x*", true)   // can match empty
	all := m.FindAll("axbx")      // must terminate (stdlib advances past zero-width)
	if len(all) != 3 {            // [0,0], [1,2], [3,4]
		t.Fatalf("FindAll(x* over axbx) = %v, want 3 matches", all)
	}
}
