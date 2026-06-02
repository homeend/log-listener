package catalog

import "testing"

func TestExpandPath(t *testing.T) {
	env := func(k string) string {
		return map[string]string{"LOCALAPPDATA": `C:/Users/me/AppData/Local`, "XDG_CACHE": "/x"}[k]
	}
	cases := []struct{ in, want string }{
		{"~/.cache/JetBrains/{product}*/log", "/home/me/.cache/JetBrains/GoLand*/log"},
		{"%LOCALAPPDATA%/JetBrains/{product}*/log", "C:/Users/me/AppData/Local/JetBrains/GoLand*/log"},
		{"$XDG_CACHE/{product}", "/x/GoLand"},
		{"%MISSING%/x", "%MISSING%/x"}, // unknown var left intact
	}
	for _, c := range cases {
		got := expandPath(substituteProduct(c.in, "GoLand"), "/home/me", env)
		if got != c.want {
			t.Errorf("expand(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSubstituteProductNoToken(t *testing.T) {
	if got := substituteProduct("/var/log/app", "GoLand"); got != "/var/log/app" {
		t.Errorf("got %q", got)
	}
}
