package discover

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, content string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if !mtime.IsZero() {
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFileFilterAllow(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	f := &FileFilter{
		NameRegex:    regexp.MustCompile(`\.log$`),
		ExcludeRegex: regexp.MustCompile(`archive`),
		Older:        now,                    // mtime must be before now
		Younger:      now.Add(-24 * time.Hour), // mtime must be after yesterday
	}
	cases := []struct {
		name  string
		fname string
		mtime time.Time
		want  bool
	}{
		{"ok", "app.log", now.Add(-1 * time.Hour), true},
		{"wrong ext", "app.txt", now.Add(-1 * time.Hour), false},
		{"excluded", "archive.log", now.Add(-1 * time.Hour), false},
		{"too new", "app.log", now.Add(1 * time.Hour), false},
		{"too old", "app.log", now.Add(-48 * time.Hour), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := f.Allow(tc.fname, tc.mtime); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
	if !(*FileFilter)(nil).Allow("any", time.Time{}) {
		t.Fatal("nil filter must allow everything")
	}
}

func TestListCandidatesDirRecursive(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.log"), "a", time.Time{})
	writeFile(t, filepath.Join(dir, "sub", "b.log"), "b", time.Time{})
	writeFile(t, filepath.Join(dir, "sub", "c.txt"), "c", time.Time{})

	g := Group{ID: "default", Kind: GroupDir, Paths: []string{dir}, Recursive: true}
	got, err := ListCandidates(g)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 candidates, got %d: %v", len(got), got)
	}
}

func TestListCandidatesDirNonRecursive(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.log"), "a", time.Time{})
	writeFile(t, filepath.Join(dir, "sub", "b.log"), "b", time.Time{})

	g := Group{ID: "default", Kind: GroupDir, Paths: []string{dir}, Recursive: false}
	got, err := ListCandidates(g)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 candidate, got %d", len(got))
	}
}

func TestListCandidatesFileGlob(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "x.log"), "x", time.Time{})
	writeFile(t, filepath.Join(dir, "y.log"), "y", time.Time{})
	writeFile(t, filepath.Join(dir, "z.txt"), "z", time.Time{})

	g := Group{ID: "default", Kind: GroupFile, Paths: []string{filepath.Join(dir, "*.log")}}
	got, err := ListCandidates(g)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 from glob, got %d", len(got))
	}
}

func TestAssignFirstMatchWins(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.log"), "a", time.Time{})
	writeFile(t, filepath.Join(dir, "b.log"), "b", time.Time{})

	// Two overlapping groups: "1" gets ".*a\.log$"; "2" catches everything else.
	groups := []*Group{
		{ID: "1", Kind: GroupDir, Paths: []string{dir}, Recursive: true,
			Filter: &FileFilter{NameRegex: regexp.MustCompile(`a\.log$`)}},
		{ID: "2", Kind: GroupDir, Paths: []string{dir}, Recursive: true},
	}
	got, err := Assign(groups, nil)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]string{}
	for _, a := range got {
		byPath[filepath.Base(a.Path)] = a.GroupID
	}
	if byPath["a.log"] != "1" || byPath["b.log"] != "2" {
		t.Fatalf("unexpected assignment: %+v", byPath)
	}
}

func TestAssignAppliesGlobalFilter(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-48 * time.Hour)
	writeFile(t, filepath.Join(dir, "fresh.log"), "x", time.Now())
	writeFile(t, filepath.Join(dir, "stale.log"), "x", old)

	groups := []*Group{{ID: "default", Kind: GroupDir, Paths: []string{dir}, Recursive: true}}
	global := &FileFilter{Younger: time.Now().Add(-1 * time.Hour)}

	got, err := Assign(groups, global)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || filepath.Base(got[0].Path) != "fresh.log" {
		t.Fatalf("global filter not applied: %+v", got)
	}
}

func TestAssignFileGroupBypassesFilters(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-48 * time.Hour)
	writeFile(t, filepath.Join(dir, "ancient.log"), "x", old)

	groups := []*Group{
		{ID: "default", Kind: GroupFile, Paths: []string{filepath.Join(dir, "*.log")}},
	}
	global := &FileFilter{Younger: time.Now().Add(-1 * time.Hour)}

	got, err := Assign(groups, global)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("file group must ignore global filter: %+v", got)
	}
}
