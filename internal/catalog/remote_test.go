package catalog

import (
	"errors"
	"testing"
)

type fakeFetcher struct {
	data []byte
	err  error
}

func (f fakeFetcher) Fetch() ([]byte, error) { return f.data, f.err }

func newerCatalogYAML(v int) []byte {
	return []byte("version: " + itoa(v) + "\ndefaults:\n  output: {color: true, drop_unmatched: false}\n  tui: {enabled: true, scrollback: 1}\nfragments: {}\napps: {}\nrenderers: {}\nbundles: {}\n")
}

func TestSelectPrefersNewerRemote(t *testing.T) {
	bundled, _ := Parse(newerCatalogYAML(2))
	got := Select(bundled, fakeFetcher{data: newerCatalogYAML(5)})
	if got.Version != 5 {
		t.Errorf("version = %d, want 5 (remote newer)", got.Version)
	}
}

func TestSelectKeepsBundledWhenRemoteOlder(t *testing.T) {
	bundled, _ := Parse(newerCatalogYAML(9))
	got := Select(bundled, fakeFetcher{data: newerCatalogYAML(3)})
	if got.Version != 9 {
		t.Errorf("version = %d, want 9 (bundled newer)", got.Version)
	}
}

func TestSelectFallsBackOnFetchError(t *testing.T) {
	bundled, _ := Parse(newerCatalogYAML(4))
	got := Select(bundled, fakeFetcher{err: errors.New("offline")})
	if got.Version != 4 {
		t.Errorf("version = %d, want 4 (fallback)", got.Version)
	}
}

func TestSelectFallsBackOnMalformedRemote(t *testing.T) {
	bundled, _ := Parse(newerCatalogYAML(4))
	got := Select(bundled, fakeFetcher{data: []byte("{{ not yaml")})
	if got.Version != 4 {
		t.Errorf("version = %d, want 4 (malformed remote ignored)", got.Version)
	}
}
