package match

import "testing"

func TestCompileRequiresAtLeastOneCriterion(t *testing.T) {
	if _, err := Compile(Spec{}); err == nil {
		t.Fatal("expected error for empty matcher")
	}
}

func TestCompileRejectsLiteralAndRegexSameDimension(t *testing.T) {
	cases := []Spec{
		{Line: "a", LineRegex: "a"},
		{Name: "a", NameRegex: "a"},
		{Path: "a", PathRegex: "a"},
	}
	for _, s := range cases {
		if _, err := Compile(s); err == nil {
			t.Fatalf("expected error for both literal+regex set: %+v", s)
		}
	}
}

func TestCompileRejectsInvalidRegex(t *testing.T) {
	if _, err := Compile(Spec{LineRegex: "a[b"}); err == nil {
		t.Fatal("expected error for invalid regex")
	}
}

func TestMatchNameLiteralIsExactBasename(t *testing.T) {
	m, err := Compile(Spec{Name: "idea.log"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Match("/var/log/idea.log", "anything"); !ok {
		t.Fatal("exact basename should match")
	}
	if _, ok := m.Match("/var/log/idea.log.1", "anything"); ok {
		t.Fatal("rotated name must NOT match an exact literal")
	}
}

func TestMatchPathLiteralIsExactFullPath(t *testing.T) {
	m, _ := Compile(Spec{Path: "/var/log/app.log"})
	if _, ok := m.Match("/var/log/app.log", "x"); !ok {
		t.Fatal("exact path should match")
	}
	if _, ok := m.Match("/var/log/app.log.1", "x"); ok {
		t.Fatal("non-equal path must not match")
	}
}

func TestMatchLineLiteralIsExactWholeLine(t *testing.T) {
	m, _ := Compile(Spec{Line: "DEBUG"})
	if _, ok := m.Match("/f", "DEBUG"); !ok {
		t.Fatal("exact line should match")
	}
	if _, ok := m.Match("/f", "DEBUG: details"); ok {
		t.Fatal("substring must NOT match an exact line literal")
	}
}

func TestMatchRegexAndCaptures(t *testing.T) {
	m, _ := Compile(Spec{LineRegex: `^(\d+) (.*)$`})
	caps, ok := m.Match("/f", "42 hello")
	if !ok || len(caps) != 3 || caps[1] != "42" || caps[2] != "hello" {
		t.Fatalf("captures = %v ok=%v", caps, ok)
	}
}

func TestMatchAndAcrossDimensions(t *testing.T) {
	m, _ := Compile(Spec{Name: "idea.log", LineRegex: "ERROR"})
	if _, ok := m.Match("/x/idea.log", "ERROR here"); !ok {
		t.Fatal("both criteria satisfied should match")
	}
	if _, ok := m.Match("/x/other.log", "ERROR here"); ok {
		t.Fatal("name mismatch must fail AND")
	}
	if _, ok := m.Match("/x/idea.log", "info"); ok {
		t.Fatal("line mismatch must fail AND")
	}
}

func TestHasLineRegex(t *testing.T) {
	with, _ := Compile(Spec{LineRegex: "x"})
	if !with.HasLineRegex() {
		t.Fatal("expected HasLineRegex true")
	}
	without, _ := Compile(Spec{Name: "idea.log"})
	if without.HasLineRegex() {
		t.Fatal("expected HasLineRegex false")
	}
}
