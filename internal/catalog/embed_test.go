package catalog

import "testing"

func TestBundledParses(t *testing.T) {
	c, err := Bundled()
	if err != nil {
		t.Fatalf("Bundled: %v", err)
	}
	if c.Version < 1 {
		t.Errorf("Version = %d, want >= 1", c.Version)
	}
}
