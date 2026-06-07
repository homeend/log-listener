package blocks

import "testing"

func exFromText(ss ...string) *ExceptionInfo {
	bs := Segment(lines(ss...))
	if len(bs) == 0 {
		return nil
	}
	return bs[0].Exception
}

func TestExceptionDetectionPerLanguage(t *testing.T) {
	cases := []struct {
		name string
		lang string
		text []string
	}{
		{"python", "python", []string{"Traceback (most recent call last):", "  File \"a.py\", line 1, in <module>", "ValueError: x"}},
		{"go", "go", []string{"panic: boom", "goroutine 1 [running]:", "\tmain.go:9 +0x1d"}},
		{"rust", "rust", []string{"thread 'main' panicked at src/main.rs:3:5:", "  boom"}},
		{"csanitizer", "c/c++", []string{"==123==ERROR: AddressSanitizer: heap-use-after-free", "    #0 0x1 in f a.c:1"}},
		{"php", "php", []string{"PHP Fatal error:  Uncaught Exception: x in /a.php:1", "Stack trace:", "#0 /a.php(9): f()"}},
		{"java", "java", []string{"java.lang.NullPointerException: x", "\tat com.foo.Bar.baz(Bar.java:42)"}},
		{"kotlin", "kotlin", []string{"java.lang.IllegalStateException", "\tat com.foo.Main.run(Main.kt:7)"}},
		{"node", "javascript", []string{"TypeError: x is not a function", "    at Object.<anonymous> (/app/a.js:10:5)"}},
		{"ts", "typescript", []string{"TypeError: x", "    at Foo (/app/a.ts:10:5)"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ex := exFromText(c.text...)
			if ex == nil {
				t.Fatalf("%s: expected an exception annotation, got nil", c.name)
			}
			if ex.Language != c.lang {
				t.Errorf("%s: language = %q, want %q", c.name, ex.Language, c.lang)
			}
		})
	}
}

func TestNonExceptionBlockUnflagged(t *testing.T) {
	if ex := exFromText("just a normal log line"); ex != nil {
		t.Errorf("plain line flagged as exception: %+v", ex)
	}
}

func TestProcessorDoesNotChangeBoundaries(t *testing.T) {
	bs := Segment(lines("panic: x", "goroutine 1 [running]:"))
	if bs[0].Start != 0 || bs[0].End != 1 {
		t.Errorf("processor altered block range: %+v", bs[0])
	}
}
