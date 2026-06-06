# OS-aware Keybindings Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Introduce a central `internal/keymap` translation layer so every TUI keybinding has one named action → per-OS key list mapping, with macOS glyph display, YAML overrides, and an app-generated `docs/KEYBINDINGS.md`.

**Architecture:** A new `internal/keymap` package owns actions, per-OS default key lists, glyph display, override resolution (current-OS → `default` → app-default, per-action replace), collision/normalization validation, and markdown-doc rendering. `config` carries the raw YAML override layers; `cmd/log-listener` reads `runtime.GOOS` once, calls `keymap.Resolve`, and injects the resolved `*keymap.Keymap` into the TUI. `internal/tui` dispatches keys via `Keymap.Lookup` instead of a hard-coded `switch`, and builds all help text from the keymap.

**Tech Stack:** Go 1.26, bubbletea (key strings), gopkg.in/yaml.v3. Spec: `docs/superpowers/specs/2026-06-06-os-aware-keybindings-design.md`.

---

## File Structure

| File | Responsibility |
|------|----------------|
| `internal/keymap/actions.go` (new) | `Action` type; ordered `AllActions []ActionDef` |
| `internal/keymap/defaults.go` (new) | per-OS default key lists (`defaultFor`) |
| `internal/keymap/glyphs.go` (new) | key normalization + per-OS glyph display |
| `internal/keymap/keymap.go` (new) | `Keymap` struct, `Resolve`, `Default`, `Lookup`, `Display`, `Keys` |
| `internal/keymap/doc.go` (new) | `RenderMarkdownDoc` |
| `internal/keymap/*_test.go` (new) | unit tests per file |
| `internal/config/yaml.go` (modify) | `Keybindings` YAML struct + carry-through |
| `internal/config/cli.go` (modify) | `Config.Keybindings` field |
| `cmd/log-listener/main.go` (modify) | `--keybindings-doc` branch; `keymap.Resolve` wiring |
| `internal/tui/app.go` (modify) | `model.km`; action dispatch; generated help text |
| `docs/KEYBINDINGS.md` (new, generated) | committed generated reference |
| `Makefile` (modify) | `keybindings-docs` target |

**Canonical key vocabulary** (the only tokens the system understands; everything normalizes to these): modifiers `ctrl`, `alt`, `shift`; bases `up down left right home end pgup pgdown tab esc enter`, the space key as the single string `" "`, and any single character (`q`, `/`, `0`, `G`, …).

---

## Task 1: keymap actions

**Files:**
- Create: `internal/keymap/actions.go`
- Test: `internal/keymap/actions_test.go`

- [ ] **Step 1: Write the failing test**

```go
package keymap

import "testing"

func TestAllActionsUniqueAndNonEmpty(t *testing.T) {
	seen := map[Action]bool{}
	for _, d := range AllActions {
		if d.Action == "" {
			t.Fatalf("empty action in AllActions")
		}
		if d.Title == "" {
			t.Errorf("action %q has empty Title", d.Action)
		}
		if seen[d.Action] {
			t.Errorf("duplicate action %q", d.Action)
		}
		seen[d.Action] = true
	}
	if len(AllActions) != 26 {
		t.Errorf("expected 26 named actions, got %d", len(AllActions))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/keymap/ -run TestAllActionsUniqueAndNonEmpty`
Expected: FAIL — package/`AllActions` undefined (build error).

- [ ] **Step 3: Write the implementation**

```go
// Package keymap is the single source of truth for TUI keybindings:
// named actions, per-OS default keys, glyph display, override resolution,
// and reference-doc generation. A terminal TUI cannot capture the macOS
// Cmd key, so "Mac-native" here means glyph display plus a small per-OS
// default remap (see defaults.go), never Cmd handling.
package keymap

// Action is the stable name of a single TUI function ("system command").
// Keys are bound to actions; behavior and display both derive from this.
type Action string

const (
	ActionQuit           Action = "quit"
	ActionToggleFiles    Action = "toggle_files"
	ActionToggleGroups   Action = "toggle_groups"
	ActionToggleRenderers Action = "toggle_renderers"
	ActionCloseOverlay   Action = "close_overlay"
	ActionSearch         Action = "search"
	ActionNextMatch      Action = "next_match"
	ActionPrevMatch      Action = "prev_match"
	ActionFilter         Action = "filter"
	ActionToggleGroupCol Action = "toggle_group_col"
	ActionToggleFileCol  Action = "toggle_file_col"
	ActionClear          Action = "clear"
	ActionCollapseAll    Action = "collapse_all"
	ActionScrollUp       Action = "scroll_up"
	ActionScrollDown     Action = "scroll_down"
	ActionPageUp         Action = "page_up"
	ActionPageDown       Action = "page_down"
	ActionFastUp         Action = "fast_up"
	ActionFastDown       Action = "fast_down"
	ActionTop            Action = "top"
	ActionBottom         Action = "bottom"
	ActionScrollLeft     Action = "scroll_left"
	ActionScrollRight    Action = "scroll_right"
	ActionFastLeft       Action = "fast_left"
	ActionFastRight      Action = "fast_right"
	ActionResetHoriz     Action = "reset_horiz"
)

// ActionDef is the documentation/metadata for one action. Context groups
// actions in the generated doc ("main", "groups", "renderers", "files").
type ActionDef struct {
	Action  Action
	Title   string
	Desc    string
	Context string
}

// AllActions is the ordered list driving help text and the generated doc.
var AllActions = []ActionDef{
	{ActionQuit, "Quit", "Exit log-listener.", "main"},
	{ActionToggleFiles, "Toggle files overlay", "Show/hide the watched-files panel.", "main"},
	{ActionToggleGroups, "Toggle groups overlay", "Show/hide the groups panel.", "main"},
	{ActionToggleRenderers, "Toggle renderers overlay", "Show/hide the renderers panel.", "main"},
	{ActionCloseOverlay, "Close overlay / clear search", "Close the open overlay, or clear active search highlights.", "main"},
	{ActionSearch, "Search", "Start a substring search.", "main"},
	{ActionNextMatch, "Next match", "Jump to the next search hit.", "main"},
	{ActionPrevMatch, "Previous match", "Jump to the previous search hit.", "main"},
	{ActionFilter, "Toggle filter", "Show only entries matching the search term.", "main"},
	{ActionToggleGroupCol, "Toggle group column", "Show/hide the group column.", "main"},
	{ActionToggleFileCol, "Toggle file column", "Show/hide the file column.", "main"},
	{ActionClear, "Clear scrollback", "Empty the in-memory view (sources keep running).", "main"},
	{ActionCollapseAll, "Collapse multiline", "Collapse/expand multiline entries.", "main"},
	{ActionScrollUp, "Scroll up", "Move up one row.", "main"},
	{ActionScrollDown, "Scroll down", "Move down one row.", "main"},
	{ActionPageUp, "Page up", "Move up one page.", "main"},
	{ActionPageDown, "Page down", "Move down one page.", "main"},
	{ActionFastUp, "Fast scroll up", "Move up several rows.", "main"},
	{ActionFastDown, "Fast scroll down", "Move down several rows.", "main"},
	{ActionTop, "Jump to top", "Go to the oldest line.", "main"},
	{ActionBottom, "Jump to bottom", "Re-stick to the latest line.", "main"},
	{ActionScrollLeft, "Pan left", "Scroll left.", "main"},
	{ActionScrollRight, "Pan right", "Scroll right.", "main"},
	{ActionFastLeft, "Fast pan left", "Scroll left several columns.", "main"},
	{ActionFastRight, "Fast pan right", "Scroll right several columns.", "main"},
	{ActionResetHoriz, "Reset horizontal scroll", "Return to column 0.", "main"},
}

// IsAction reports whether name is a known action.
func IsAction(name string) bool {
	for _, d := range AllActions {
		if string(d.Action) == name {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/keymap/ -run TestAllActionsUniqueAndNonEmpty`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/keymap/actions.go internal/keymap/actions_test.go
git commit -m "feat(keymap): action vocabulary (AllActions)"
```

---

## Task 2: per-OS default key lists

**Files:**
- Create: `internal/keymap/defaults.go`
- Test: `internal/keymap/defaults_test.go`

- [ ] **Step 1: Write the failing test**

```go
package keymap

import "testing"

func TestDefaultForCoversEveryAction(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		dm := defaultFor(goos)
		for _, d := range AllActions {
			keys := dm[d.Action]
			if len(keys) == 0 {
				t.Errorf("%s: action %q has no default keys", goos, d.Action)
			}
		}
		if len(dm) != len(AllActions) {
			t.Errorf("%s: defaultFor has %d entries, want %d", goos, len(dm), len(AllActions))
		}
	}
}

func TestDarwinFastScrollAdvertisesShiftFirst(t *testing.T) {
	dm := defaultFor("darwin")
	if got := dm[ActionFastDown][0]; got != "shift+down" {
		t.Errorf("darwin fast_down primary = %q, want shift+down", got)
	}
	lin := defaultFor("linux")
	if got := lin[ActionFastDown][0]; got != "ctrl+down" {
		t.Errorf("linux fast_down primary = %q, want ctrl+down", got)
	}
	// Both forms remain bound on every platform.
	if !contains(dm[ActionFastDown], "ctrl+down") {
		t.Errorf("darwin fast_down must still bind ctrl+down")
	}
}

func TestWindowsEqualsLinux(t *testing.T) {
	win, lin := defaultFor("windows"), defaultFor("linux")
	for _, d := range AllActions {
		if !equalSlice(win[d.Action], lin[d.Action]) {
			t.Errorf("windows/linux differ for %q: %v vs %v", d.Action, win[d.Action], lin[d.Action])
		}
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/keymap/ -run 'TestDefaultFor|TestDarwin|TestWindows'`
Expected: FAIL — `defaultFor` undefined.

- [ ] **Step 3: Write the implementation**

```go
package keymap

// defaultFor returns the built-in default key list per action for the given
// runtime.GOOS. Key order = display priority; the handler matches ANY key in
// the list. The ONLY macOS difference is that fast-scroll actions advertise
// the shift+arrow form first, because ctrl+arrow is captured by macOS
// Mission Control / Spaces before a terminal sees it (ctrl+arrow stays bound
// so it still works if the user disabled those system shortcuts).
func defaultFor(goos string) map[Action][]string {
	m := map[Action][]string{
		ActionQuit:            {"ctrl+c", "q"},
		ActionToggleFiles:     {"ctrl+i", "tab"},
		ActionToggleGroups:    {"ctrl+g"},
		ActionToggleRenderers: {"ctrl+e"},
		ActionCloseOverlay:    {"esc"},
		ActionSearch:          {"/"},
		ActionNextMatch:       {"n"},
		ActionPrevMatch:       {"p"},
		ActionFilter:          {"t"},
		ActionToggleGroupCol:  {"ctrl+p"},
		ActionToggleFileCol:   {"ctrl+l"},
		ActionClear:           {"ctrl+r"},
		ActionCollapseAll:     {"m"},
		ActionScrollUp:        {"up", "k"},
		ActionScrollDown:      {"down", "j"},
		ActionPageUp:          {"pgup", "ctrl+b"},
		ActionPageDown:        {"pgdown", "ctrl+f", " "},
		ActionTop:             {"home", "g"},
		ActionBottom:          {"end", "G"},
		ActionScrollLeft:      {"left", "h"},
		ActionScrollRight:     {"right", "l"},
		ActionResetHoriz:      {"0"},
		// Fast scroll defaults differ per-OS; set below.
	}
	if goos == "darwin" {
		m[ActionFastUp] = []string{"shift+up", "ctrl+up"}
		m[ActionFastDown] = []string{"shift+down", "ctrl+down"}
		m[ActionFastLeft] = []string{"shift+left", "ctrl+left"}
		m[ActionFastRight] = []string{"shift+right", "ctrl+right"}
	} else {
		m[ActionFastUp] = []string{"ctrl+up", "shift+up"}
		m[ActionFastDown] = []string{"ctrl+down", "shift+down"}
		m[ActionFastLeft] = []string{"ctrl+left", "shift+left"}
		m[ActionFastRight] = []string{"ctrl+right", "shift+right"}
	}
	return m
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/keymap/ -run 'TestDefaultFor|TestDarwin|TestWindows'`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/keymap/defaults.go internal/keymap/defaults_test.go
git commit -m "feat(keymap): per-OS default key lists"
```

---

## Task 3: key normalization + glyph display

**Files:**
- Create: `internal/keymap/glyphs.go`
- Test: `internal/keymap/glyphs_test.go`

- [ ] **Step 1: Write the failing test**

```go
package keymap

import "testing"

func TestNormalizeKey(t *testing.T) {
	cases := map[string]string{
		"Ctrl+I": "ctrl+i",
		"ctrl+i": "ctrl+i",
		"Esc":    "esc",
		"Tab":    "tab",
		"Space":  " ",
		" ":      " ",
		"Shift+Up": "shift+up",
		"PgUp":   "pgup",
		"G":      "G",
		"/":      "/",
		"CTRL+ALT+DELETE": "", // delete not a known base -> error
	}
	for in, want := range cases {
		got, err := normalizeKey(in)
		if want == "" {
			if err == nil {
				t.Errorf("normalizeKey(%q): want error, got %q", in, got)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("normalizeKey(%q) = %q,%v; want %q", in, got, err, want)
		}
	}
}

func TestDisplayPerOS(t *testing.T) {
	mac := Display([]string{"ctrl+i", "tab"}, "darwin")
	if mac != "⌃I / ⇥" {
		t.Errorf("darwin display = %q, want ⌃I / ⇥", mac)
	}
	lin := Display([]string{"ctrl+i", "tab"}, "linux")
	if lin != "Ctrl+I / Tab" {
		t.Errorf("linux display = %q, want Ctrl+I / Tab", lin)
	}
	if got := Display([]string{"shift+down"}, "darwin"); got != "⇧↓" {
		t.Errorf("darwin shift+down = %q, want ⇧↓", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/keymap/ -run 'TestNormalizeKey|TestDisplayPerOS'`
Expected: FAIL — `normalizeKey`/`Display` undefined.

- [ ] **Step 3: Write the implementation**

```go
package keymap

import (
	"fmt"
	"strings"
)

// knownBase is the set of multi-char base tokens. Single runes are also
// valid bases (letters, digits, punctuation) and pass through verbatim.
var knownBase = map[string]bool{
	"up": true, "down": true, "left": true, "right": true,
	"home": true, "end": true, "pgup": true, "pgdown": true,
	"tab": true, "esc": true, "enter": true,
}

// normalizeKey canonicalizes a user-supplied key string to the vocabulary the
// dispatcher and bubbletea use. Modifiers (ctrl/alt/shift) are lowercased and
// ordered as written; the base is lowercased if it is a known named key, or
// kept verbatim if it is a single rune (so "G" stays "G"). The space key is
// the single string " " (also accepts "space"/"Space"). Unmappable tokens are
// an error — never a silent no-fire.
func normalizeKey(s string) (string, error) {
	if s == " " {
		return " ", nil
	}
	if strings.EqualFold(s, "space") {
		return " ", nil
	}
	parts := strings.Split(s, "+")
	for i, p := range parts {
		if p == "" {
			return "", fmt.Errorf("invalid key %q (empty token)", s)
		}
		isLast := i == len(parts)-1
		if !isLast {
			lp := strings.ToLower(p)
			if lp != "ctrl" && lp != "alt" && lp != "shift" {
				return "", fmt.Errorf("invalid modifier %q in key %q", p, s)
			}
			parts[i] = lp
			continue
		}
		// base token
		lp := strings.ToLower(p)
		if knownBase[lp] {
			parts[i] = lp
		} else if len([]rune(p)) == 1 {
			parts[i] = p // single rune: keep case ("G" vs "g")
		} else {
			return "", fmt.Errorf("unknown key token %q in key %q", p, s)
		}
	}
	return strings.Join(parts, "+"), nil
}

var macGlyph = map[string]string{
	"ctrl": "⌃", "alt": "⌥", "shift": "⇧",
	"esc": "⎋", "tab": "⇥", "enter": "↩",
	"up": "↑", "down": "↓", "left": "←", "right": "→",
	"pgup": "PgUp", "pgdown": "PgDn", " ": "Space",
}

var textLabel = map[string]string{
	"esc": "Esc", "tab": "Tab", "enter": "Enter",
	"up": "↑", "down": "↓", "left": "←", "right": "→",
	"pgup": "PgUp", "pgdown": "PgDn", " ": "Space",
}

// Display renders one action's key list to a per-OS label, e.g.
// ["ctrl+i","tab"] -> "⌃I / ⇥" on darwin, "Ctrl+I / Tab" elsewhere.
func Display(keys []string, goos string) string {
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, displayKey(k, goos))
	}
	return strings.Join(parts, " / ")
}

func displayKey(key string, goos string) string {
	mac := goos == "darwin"
	// Whole-key specials (space).
	if key == " " {
		return "Space"
	}
	toks := strings.Split(key, "+")
	var b strings.Builder
	for i, tok := range toks {
		last := i == len(toks)-1
		if mac {
			if g, ok := macGlyph[tok]; ok {
				b.WriteString(g)
			} else {
				b.WriteString(strings.ToUpper(tok))
			}
			// Mac glyphs are written tight (⌃I), no "+".
			continue
		}
		// linux/windows
		if !last {
			b.WriteString(strings.Title(tok)) // Ctrl/Alt/Shift
			b.WriteString("+")
			continue
		}
		if lbl, ok := textLabel[tok]; ok {
			b.WriteString(lbl)
		} else if len([]rune(tok)) == 1 {
			b.WriteString(strings.ToUpper(tok))
		} else {
			b.WriteString(strings.Title(tok))
		}
	}
	return b.String()
}
```

Note: `strings.Title` is deprecated but acceptable here (ASCII modifier names only). If `go vet` objects, replace with a small `capitalize(s)` helper.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/keymap/ -run 'TestNormalizeKey|TestDisplayPerOS'`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/keymap/glyphs.go internal/keymap/glyphs_test.go
git commit -m "feat(keymap): key normalization + per-OS glyph display"
```

---

## Task 4: Keymap struct, Resolve, Lookup, validation

**Files:**
- Create: `internal/keymap/keymap.go`
- Test: `internal/keymap/keymap_test.go`

- [ ] **Step 1: Write the failing test**

```go
package keymap

import (
	"strings"
	"testing"
)

func TestResolvePrecedence(t *testing.T) {
	// current-OS layer beats default layer beats app-default.
	userDefault := map[string][]string{"search": {"?"}}
	userOS := map[string][]string{"search": {":"}}
	km, err := Resolve("linux", userDefault, userOS)
	if err != nil {
		t.Fatal(err)
	}
	if got := km.Keys(ActionSearch); !equalSlice(got, []string{":"}) {
		t.Errorf("search keys = %v, want [:] (current-OS wins)", got)
	}
	// default layer applies when OS layer is silent.
	km2, _ := Resolve("linux", map[string][]string{"filter": {"F"}}, nil)
	if got := km2.Keys(ActionFilter); !equalSlice(got, []string{"F"}) {
		t.Errorf("filter keys = %v, want [F]", got)
	}
	// untouched action keeps app default.
	if got := km2.Keys(ActionQuit); !equalSlice(got, []string{"ctrl+c", "q"}) {
		t.Errorf("quit keys = %v, want app default", got)
	}
}

func TestResolveListReplaceAllowsClearingDefault(t *testing.T) {
	km, err := Resolve("linux", map[string][]string{"quit": {"ctrl+c"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := km.Keys(ActionQuit); !equalSlice(got, []string{"ctrl+c"}) {
		t.Errorf("quit keys = %v, want [ctrl+c] (q dropped)", got)
	}
}

func TestResolveCollisionIsError(t *testing.T) {
	// Rebind clear to n; next_match still owns n -> collision.
	_, err := Resolve("linux", map[string][]string{"clear": {"n"}}, nil)
	if err == nil {
		t.Fatal("expected collision error")
	}
	if !strings.Contains(err.Error(), "clear") || !strings.Contains(err.Error(), "next_match") || !strings.Contains(err.Error(), "n") {
		t.Errorf("collision error should name both actions and key: %v", err)
	}
}

func TestResolveUnknownActionIsError(t *testing.T) {
	_, err := Resolve("linux", map[string][]string{"frobnicate": {"x"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "frobnicate") {
		t.Fatalf("expected unknown-action error, got %v", err)
	}
}

func TestResolveBadKeyTokenIsError(t *testing.T) {
	_, err := Resolve("linux", map[string][]string{"search": {"ctrl+notakey"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "notakey") {
		t.Fatalf("expected key-token error, got %v", err)
	}
}

func TestResolveNormalizesUserKeys(t *testing.T) {
	km, err := Resolve("linux", map[string][]string{"search": {"Ctrl+I"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := km.Lookup("ctrl+i"); !ok {
		t.Errorf("normalized key ctrl+i should be looked up")
	}
}

func TestLookupAllDefaultKeys(t *testing.T) {
	km := Default("linux")
	for action, keys := range defaultFor("linux") {
		for _, k := range keys {
			got, ok := km.Lookup(k)
			if !ok || got != action {
				t.Errorf("Lookup(%q) = %q,%v; want %q", k, got, ok, action)
			}
		}
	}
}

func TestDefaultKeymapsHaveNoCollisions(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		if _, err := Resolve(goos, nil, nil); err != nil {
			t.Errorf("%s default keymap has a collision: %v", goos, err)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/keymap/ -run 'TestResolve|TestLookup|TestDefaultKeymaps'`
Expected: FAIL — `Resolve`/`Default`/`Keymap` undefined.

- [ ] **Step 3: Write the implementation**

```go
package keymap

import (
	"fmt"
	"sort"
)

// Keymap is a resolved, validated action↔keys mapping plus a reverse index
// for dispatch. Construct with Resolve or Default; do not build literally.
type Keymap struct {
	goos     string
	bindings map[Action][]string
	lookup   map[string]Action
}

// Default returns the built-in keymap for goos with no user overrides.
func Default(goos string) *Keymap {
	km, err := Resolve(goos, nil, nil)
	if err != nil {
		// Built-in defaults are collision-free by construction and covered by
		// TestDefaultKeymapsHaveNoCollisions; a failure here is a programmer bug.
		panic(fmt.Sprintf("keymap: built-in default for %q invalid: %v", goos, err))
	}
	return km
}

// Resolve merges user override layers over the built-in OS default, per action
// (first defining layer wins; the list REPLACES the lower layer — no key-by-key
// merge, so a user can clear a default by giving a shorter explicit list).
// Precedence per action: userOS > userDefault > app default. User keys are
// normalized; unknown action names, unmappable key tokens, and post-merge key
// collisions are all errors.
func Resolve(goos string, userDefault, userOS map[string][]string) (*Keymap, error) {
	bindings := defaultFor(goos)

	apply := func(layer map[string][]string) error {
		// Deterministic order for stable error messages.
		names := make([]string, 0, len(layer))
		for k := range layer {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, name := range names {
			if !IsAction(name) {
				return fmt.Errorf("keybindings: unknown action %q", name)
			}
			raw := layer[name]
			norm := make([]string, 0, len(raw))
			for _, k := range raw {
				nk, err := normalizeKey(k)
				if err != nil {
					return fmt.Errorf("keybindings.%s: %w", name, err)
				}
				norm = append(norm, nk)
			}
			bindings[Action(name)] = norm
		}
		return nil
	}

	// Lower precedence first; userOS applied last so it wins.
	if err := apply(userDefault); err != nil {
		return nil, err
	}
	if err := apply(userOS); err != nil {
		return nil, err
	}

	lookup, err := buildLookup(bindings)
	if err != nil {
		return nil, err
	}
	return &Keymap{goos: goos, bindings: bindings, lookup: lookup}, nil
}

func buildLookup(bindings map[Action][]string) (map[string]Action, error) {
	// Deterministic iteration so collision errors are stable.
	actions := make([]Action, 0, len(bindings))
	for a := range bindings {
		actions = append(actions, a)
	}
	sort.Slice(actions, func(i, j int) bool { return actions[i] < actions[j] })

	lookup := map[string]Action{}
	for _, a := range actions {
		for _, k := range bindings[a] {
			if other, dup := lookup[k]; dup && other != a {
				return nil, fmt.Errorf("keybindings: key %q is bound to both %q and %q", k, other, a)
			}
			lookup[k] = a
		}
	}
	return lookup, nil
}

// Lookup maps a bubbletea key string to its action.
func (k *Keymap) Lookup(key string) (Action, bool) {
	a, ok := k.lookup[key]
	return a, ok
}

// Keys returns the bound keys for an action (display order).
func (k *Keymap) Keys(a Action) []string { return k.bindings[a] }

// Display returns the per-OS label for an action's keys, e.g. "⌃G".
func (k *Keymap) Display(a Action) string { return Display(k.bindings[a], k.goos) }

// GOOS returns the platform this keymap was resolved for.
func (k *Keymap) GOOS() string { return k.goos }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/keymap/`
Expected: PASS (all keymap tests).

- [ ] **Step 5: Commit**

```bash
git add internal/keymap/keymap.go internal/keymap/keymap_test.go
git commit -m "feat(keymap): Resolve/Lookup with collision + normalization validation"
```

---

## Task 5: markdown doc rendering

**Files:**
- Create: `internal/keymap/doc.go`
- Test: `internal/keymap/doc_test.go`

- [ ] **Step 1: Write the failing test**

```go
package keymap

import (
	"strings"
	"testing"
)

func TestRenderMarkdownDocContents(t *testing.T) {
	doc := RenderMarkdownDoc()
	for _, want := range []string{
		"# Keybindings",
		"| Action |",
		"Quit",
		"⌃C", // mac glyph column
		"Ctrl+C", // linux column
		"shift+down", // verification caveat mentions the mac fast-scroll key
		"keybindings:", // override section
		"do not edit", // generated banner
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("RenderMarkdownDoc missing %q", want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/keymap/ -run TestRenderMarkdownDoc`
Expected: FAIL — `RenderMarkdownDoc` undefined.

- [ ] **Step 3: Write the implementation**

```go
package keymap

import (
	"fmt"
	"strings"
)

// RenderMarkdownDoc generates the full docs/KEYBINDINGS.md content from the
// built-in default keymaps — the same source of truth the TUI uses, so the
// doc cannot drift. Columns: Action / Linux·Windows / macOS.
func RenderMarkdownDoc() string {
	lin := Default("linux")
	mac := Default("darwin")

	var b strings.Builder
	b.WriteString("# Keybindings\n\n")
	b.WriteString("> Generated by `log-listener --keybindings-doc` — do not edit by hand.\n")
	b.WriteString("> Run `make keybindings-docs` to regenerate.\n\n")
	b.WriteString("A terminal TUI cannot capture the macOS Cmd (⌘) key, so macOS bindings ")
	b.WriteString("use the same Ctrl/Shift/Option keys, shown with Mac glyphs.\n\n")

	b.WriteString("| Action | Linux / Windows | macOS | Description |\n")
	b.WriteString("|--------|-----------------|-------|-------------|\n")
	for _, d := range AllActions {
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n",
			d.Title, lin.Display(d.Action), mac.Display(d.Action), d.Desc)
	}

	b.WriteString("\n## Positional toggles (not individually overridable)\n\n")
	b.WriteString("- `1`–`9` — toggle the corresponding group on/off.\n")
	b.WriteString("- `!@#$%^&*(` (Shift+1..9) — toggle the corresponding renderer on/off.\n")

	b.WriteString("\n## macOS note\n\n")
	b.WriteString("`Ctrl`+Arrow is captured by macOS Mission Control / Spaces, so the macOS ")
	b.WriteString("default advertises `shift+down` / `shift+up` / `shift+left` / `shift+right` ")
	b.WriteString("for fast scrolling (Ctrl+Arrow stays bound as a fallback). ")
	b.WriteString("**Shift+Arrow forwarding to a TUI has not been verified on every macOS ")
	b.WriteString("terminal — if it does nothing, PgUp/PgDn still page.**\n")

	b.WriteString("\n## Overriding keys in your config\n\n")
	b.WriteString("Add a `keybindings:` block to your `log-listener.yml`. Precedence per ")
	b.WriteString("action: current-OS section → `default` section → built-in default. ")
	b.WriteString("A listed action's key list fully replaces the lower layer.\n\n")
	b.WriteString("```yaml\n")
	b.WriteString("keybindings:\n")
	b.WriteString("  default:            # applies on every OS\n")
	b.WriteString("    search: [\"/\"]\n")
	b.WriteString("  darwin:\n")
	b.WriteString("    fast_down: [\"shift+down\"]\n")
	b.WriteString("  linux:\n")
	b.WriteString("    fast_down: [\"ctrl+down\"]\n")
	b.WriteString("  windows:\n")
	b.WriteString("    fast_down: [\"ctrl+down\"]\n")
	b.WriteString("```\n\n")
	b.WriteString("Valid action names: ")
	names := make([]string, len(AllActions))
	for i, d := range AllActions {
		names[i] = "`" + string(d.Action) + "`"
	}
	b.WriteString(strings.Join(names, ", "))
	b.WriteString(".\n")
	return b.String()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/keymap/ -run TestRenderMarkdownDoc`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/keymap/doc.go internal/keymap/doc_test.go
git commit -m "feat(keymap): markdown doc rendering"
```

---

## Task 6: config YAML carry-through

**Files:**
- Modify: `internal/config/yaml.go` (add `Keybindings` to `File`; carry into Config)
- Modify: `internal/config/cli.go:19-44` (add `Keybindings` field to `Config`)
- Test: `internal/config/yaml_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/config/yaml_test.go`:

```go
func TestYAMLKeybindingsCarriedThrough(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log-listener.yml")
	yml := `
files:
  - id: app
    paths: ["/tmp/app.log"]
keybindings:
  default:
    search: ["?"]
  darwin:
    fast_down: ["shift+down"]
`
	if err := os.WriteFile(path, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadWithFS([]string{"--config", path}, time.Now(), func() (string, error) { return dir, nil })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Keybindings == nil {
		t.Fatal("Keybindings not carried through")
	}
	if got := cfg.Keybindings.Default["search"]; len(got) != 1 || got[0] != "?" {
		t.Errorf("default.search = %v, want [?]", got)
	}
	if got := cfg.Keybindings.Darwin["fast_down"]; len(got) != 1 || got[0] != "shift+down" {
		t.Errorf("darwin.fast_down = %v, want [shift+down]", got)
	}
}
```

(Confirm `yaml_test.go` already imports `os`, `path/filepath`, `time`; they are used by existing tests in that file. If not, add them.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestYAMLKeybindingsCarriedThrough`
Expected: FAIL — `File` has no `keybindings` field (strict decode error) and `cfg.Keybindings` undefined.

- [ ] **Step 3a: Add the YAML struct + field in `internal/config/yaml.go`**

In the `File` struct (after the `TUI` line, `yaml.go:52`), add:

```go
	TUI              *TUI                   `yaml:"tui,omitempty"`
	Keybindings      *Keybindings           `yaml:"keybindings,omitempty"`
}
```

Add the type near the other small structs (e.g. after `TUI`, around `yaml.go:134`):

```go
// Keybindings is the raw YAML override layers for TUI keys. Action names and
// key strings are validated later by keymap.Resolve (cmd wiring), not here, so
// config stays decoupled from the keymap package.
type Keybindings struct {
	Default map[string][]string `yaml:"default,omitempty"`
	Darwin  map[string][]string `yaml:"darwin,omitempty"`
	Linux   map[string][]string `yaml:"linux,omitempty"`
	Windows map[string][]string `yaml:"windows,omitempty"`
}
```

- [ ] **Step 3b: Carry it into Config in `mergeYAMLInto` (`yaml.go`, before `return nil` at line 371)**

```go
	// keybindings — carried through verbatim; resolved+validated in cmd
	// (needs runtime.GOOS). YAML-only, no CLI flags.
	if yc.Keybindings != nil {
		cfg.Keybindings = yc.Keybindings
	}

	return nil
```

- [ ] **Step 3c: Add the field to `Config` in `internal/config/cli.go` (after `MuteSpecs`, line 39)**

```go
	MuteSpecs     []MuteSpec
	Keybindings   *Keybindings // raw YAML key override layers; resolved in cmd
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/`
Expected: PASS (new test + existing config tests).

- [ ] **Step 5: Commit**

```bash
git add internal/config/yaml.go internal/config/cli.go internal/config/yaml_test.go
git commit -m "feat(config): carry keybindings override layers from YAML"
```

---

## Task 7: cmd wiring — `--keybindings-doc` + Resolve

**Files:**
- Modify: `cmd/log-listener/main.go` (early `--keybindings-doc` branch; Resolve in `run`; pass into `runWatchTUI` → `tui.New`)
- Test: `cmd/log-listener/main_test.go` (append; if absent, create)

- [ ] **Step 1: Write the failing test**

Append to `cmd/log-listener/main_test.go` (create the file with `package main` + imports `bytes`, `strings`, `testing` if it does not exist):

```go
func TestKeybindingsDocFlagPrintsAndExitsZero(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"--keybindings-doc"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "# Keybindings") {
		t.Errorf("doc not printed; got: %q", out.String())
	}
}

func TestBadKeybindingExitsNonZero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log-listener.yml")
	yml := "files:\n  - id: a\n    paths: [\"/tmp/a.log\"]\nkeybindings:\n  default:\n    clear: [\"n\"]\n"
	if err := os.WriteFile(path, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := run([]string{"--no-tui", "--once", "--config", path}, &out, &errb)
	if code == 0 {
		t.Fatalf("expected non-zero exit for colliding keybinding")
	}
	if !strings.Contains(errb.String(), "clear") {
		t.Errorf("stderr should explain the collision; got %q", errb.String())
	}
}
```

(Ensure imports include `bytes`, `os`, `path/filepath`, `strings`, `testing`.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/log-listener/ -run 'TestKeybindingsDocFlag|TestBadKeybinding'`
Expected: FAIL — flag unhandled (prints config error / tries to run) and resolve not wired.

- [ ] **Step 3a: Add the early doc branch + Resolve in `run` (`main.go`)**

Add `"runtime"` and `"log-listener/internal/keymap"` to the import block. Then, at the top of `run` (right after the `init` branch, `main.go:40`):

```go
	if len(args) > 0 && args[0] == "--keybindings-doc" {
		fmt.Fprint(stdout, keymap.RenderMarkdownDoc())
		return 0
	}
```

After `cfg, err := config.Load(...)` succeeds (`main.go:46`), resolve the keymap so a bad binding fails fast in every mode:

```go
	var km *keymap.Keymap
	{
		var userDefault, userOS map[string][]string
		if cfg.Keybindings != nil {
			userDefault = cfg.Keybindings.Default
			switch runtime.GOOS {
			case "darwin":
				userOS = cfg.Keybindings.Darwin
			case "windows":
				userOS = cfg.Keybindings.Windows
			default:
				userOS = cfg.Keybindings.Linux
			}
		}
		km, err = keymap.Resolve(runtime.GOOS, userDefault, userOS)
		if err != nil {
			fmt.Fprintln(stderr, "log-listener:", err)
			return 2
		}
	}
```

- [ ] **Step 3b: Thread `km` into the TUI**

Change the `runWatchTUI` signature (`main.go:319`) to accept `km`:

```go
func runWatchTUI(cfg *config.Config, args []string, dropUnmatched bool, assignments []discover.Assignment, pipePtr *atomic.Pointer[render.Pipeline], sseHub *sink.SSEHub, km *keymap.Keymap, stderr io.Writer) error {
```

Update the call site (`main.go:103`):

```go
		if err := runWatchTUI(cfg, args, cfg.DropUnmatched, assignments, &pipePtr, sseHub, km, stderr); err != nil {
```

In `runWatchTUI`, pass `km` into `tui.New` (the `tui.Options` literal at `main.go:341`), adding one field:

```go
	app := tui.New(tui.Options{
		Scrollback:   cfg.TUIScrollback,
		InitialFiles: initial,
		Groups:       groups,
		Renderers:    renderers,
		Keymap:       km,
		SetRendererOn: func(i int, on bool) { pipePtr.Load().SetRendererEnabled(i, on) },
		RenderFn: func(group, file, raw string) (render.Event, bool) {
			return pipePtr.Load().Render(time.Now(), group, file, raw)
		},
	})
```

(`tui.Options.Keymap` is added in Task 8; this step will not compile until Task 8's Options field exists. Implement Task 8 Step 3a before re-running `go build`. If you prefer strict per-task green, swap the order: do Task 8 first, then Task 7. The tests in this task already require Task 8's field — run `go build ./...` only after both.)

- [ ] **Step 4: Run test to verify it passes** (after Task 8 Options field exists)

Run: `go test ./cmd/log-listener/ -run 'TestKeybindingsDocFlag|TestBadKeybinding'`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/log-listener/main.go cmd/log-listener/main_test.go
git commit -m "feat(cmd): --keybindings-doc flag and keymap.Resolve wiring"
```

---

## Task 8: TUI action dispatch

**Files:**
- Modify: `internal/tui/app.go` — add `Keymap` to `Options`, `km` to `model`, default in `New`, and convert the `switch msg.String()` (`app.go:447-669`) to action dispatch.
- Test: `internal/tui/app_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/tui/app_test.go`:

```go
func TestCustomQuitBinding(t *testing.T) {
	km, err := keymap.Resolve("linux", map[string][]string{"quit": {"x"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(100)
	m.km = km
	// 'x' should now quit (returns tea.Quit), and 'q' should NOT.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if cmd == nil {
		t.Errorf("custom binding 'x' did not trigger quit")
	}
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd != nil {
		t.Errorf("'q' should no longer quit after rebind")
	}
}

func TestPositionalGroupToggleStillWorks(t *testing.T) {
	m := newModel(100)
	m.km = keymap.Default("linux")
	m.groupOrder = []string{"g0", "g1"}
	m.groupEnabled = map[string]bool{"g0": true, "g1": true}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	if m.groupEnabled["g0"] {
		t.Errorf("digit '1' should have toggled group g0 off")
	}
}
```

Add `"log-listener/internal/keymap"` to the test imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run 'TestCustomQuitBinding|TestPositionalGroupToggle'`
Expected: FAIL — `model.km` undefined.

- [ ] **Step 3a: Add `Keymap` to `Options` and `km` to `model`; default it in `New`**

Import `"runtime"` and `"log-listener/internal/keymap"` in `app.go`.

In `Options` (`app.go:142`), add a field:

```go
	Renderers         []RendererInfo
	Keymap            *keymap.Keymap // resolved key bindings; nil → built-in for runtime.GOOS
	SetRendererOn     func(idx int, on bool)
```

In `model` (`app.go:265`), add a field (near the top of the struct):

```go
type model struct {
	km *keymap.Keymap
	// entries is the source of truth: ...
	entries []scrollbackEvent
```

In `New` (`app.go:155`), after `m := newModel(scrollback)`:

```go
	m.km = opts.Keymap
	if m.km == nil {
		m.km = keymap.Default(runtime.GOOS)
	}
```

Also make `newModel` set a non-nil default so direct-construction tests never panic. In `newModel` (find its body; it returns a `model`), ensure `km` is set — simplest is a lazy guard in the dispatcher (Step 3b handles nil by falling back). Choose ONE: either set `m.km = keymap.Default(runtime.GOOS)` in `newModel`, or guard in Update. This plan guards in Update (Step 3b) AND every test sets `m.km` explicitly, so `newModel` needs no change.

- [ ] **Step 3b: Convert the key `switch` to action dispatch**

Replace the entire block `app.go:447-669` (from `switch msg.String() {` through its closing `}` just before `case EventMsg:`) with the following. Behavior bodies are unchanged — only the case labels change from raw key strings to actions, with the positional toggles handled before the action switch.

```go
		key := msg.String()

		// Positional toggles are not part of the action keymap (they are
		// inherently 1-9 / shifted-1-9 by position). Handle them first.
		if key >= "1" && key <= "9" {
			idx := int(key[0] - '1')
			if idx < len(m.groupOrder) {
				gid := m.groupOrder[idx]
				m.groupEnabled[gid] = !m.groupEnabled[gid]
			}
			return m, nil
		}
		if ri := strings.IndexByte("!@#$%^&*(", key[0]); ri >= 0 && len(key) == 1 {
			m.toggleRenderer(ri)
			return m, nil
		}

		km := m.km
		if km == nil {
			km = keymap.Default(runtime.GOOS)
		}
		action, ok := km.Lookup(key)
		if !ok {
			return m, nil
		}
		switch action {
		case keymap.ActionQuit:
			return m, tea.Quit
		case keymap.ActionToggleFiles:
			m.showFiles = !m.showFiles
			if m.showFiles {
				m.showGroupsPanel = false
				m.showRenderersPanel = false
			}
			m.filesScroll = 0
		case keymap.ActionToggleGroups:
			m.showGroupsPanel = !m.showGroupsPanel
			if m.showGroupsPanel {
				m.showFiles = false
				m.showRenderersPanel = false
			}
			m.groupsScroll = 0
		case keymap.ActionCloseOverlay:
			if m.showFiles {
				m.showFiles = false
			}
			if m.showGroupsPanel {
				m.showGroupsPanel = false
			}
			if m.showRenderersPanel {
				m.showRenderersPanel = false
			}
			if !m.showFiles && !m.showGroupsPanel && !m.showRenderersPanel && m.searchTerm != "" {
				m.clearSearch()
			}
		case keymap.ActionSearch:
			m.searchInput = true
			m.searchQuery = ""
		case keymap.ActionNextMatch:
			m.searchNext()
		case keymap.ActionPrevMatch:
			m.searchPrev()
		case keymap.ActionFilter:
			if m.searchTerm != "" {
				m.filterMode = !m.filterMode
				if m.filterMode {
					m.tailMode = false
				}
			}
		case keymap.ActionToggleGroupCol:
			m.showGroup = !m.showGroup
		case keymap.ActionToggleFileCol:
			m.showFile = !m.showFile
		case keymap.ActionClear:
			m.entries = nil
			m.lines = nil
			m.streamTop = 0
			m.tailMode = true
			m.horizScroll = 0
			m.searchHit = -1
			m.filterMode = false
		case keymap.ActionScrollUp:
			if m.showFiles {
				if m.filesScroll > 0 {
					m.filesScroll--
				}
			} else if m.searchTerm != "" {
				m.searchPrev()
			} else {
				m.unstickFromTail()
				m.streamTop--
				if m.streamTop < 0 {
					m.streamTop = 0
				}
			}
		case keymap.ActionScrollDown:
			if m.showFiles {
				if m.filesScroll < len(m.files)-1 {
					m.filesScroll++
				}
			} else if m.searchTerm != "" {
				m.searchNext()
			} else if !m.tailMode {
				m.streamTop++
				m.maybeReStick()
			}
		case keymap.ActionPageUp:
			page := m.contentHeight()
			if m.showFiles {
				m.filesScroll -= page
				if m.filesScroll < 0 {
					m.filesScroll = 0
				}
			} else {
				m.unstickFromTail()
				m.streamTop -= page
				if m.streamTop < 0 {
					m.streamTop = 0
				}
			}
		case keymap.ActionPageDown:
			page := m.contentHeight()
			if m.showFiles {
				m.filesScroll += page
				if m.filesScroll > len(m.files)-1 {
					m.filesScroll = len(m.files) - 1
				}
				if m.filesScroll < 0 {
					m.filesScroll = 0
				}
			} else if !m.tailMode {
				m.streamTop += page
				m.maybeReStick()
			}
		case keymap.ActionFastUp:
			if m.showFiles {
				m.filesScroll -= vertFastStep
				if m.filesScroll < 0 {
					m.filesScroll = 0
				}
			} else {
				m.unstickFromTail()
				m.streamTop -= vertFastStep
				if m.streamTop < 0 {
					m.streamTop = 0
				}
			}
		case keymap.ActionFastDown:
			if m.showFiles {
				m.filesScroll += vertFastStep
				if m.filesScroll > len(m.files)-1 {
					m.filesScroll = len(m.files) - 1
				}
				if m.filesScroll < 0 {
					m.filesScroll = 0
				}
			} else if !m.tailMode {
				m.streamTop += vertFastStep
				m.maybeReStick()
			}
		case keymap.ActionTop:
			if m.showFiles {
				m.filesScroll = 0
			} else {
				m.tailMode = false
				m.streamTop = 0
			}
		case keymap.ActionBottom:
			if m.showFiles {
				m.filesScroll = len(m.files) - 1
				if m.filesScroll < 0 {
					m.filesScroll = 0
				}
			} else {
				m.tailMode = true
			}
		case keymap.ActionScrollLeft:
			m.horizScroll -= horizStep
			if m.horizScroll < 0 {
				m.horizScroll = 0
			}
		case keymap.ActionScrollRight:
			m.horizScroll += horizStep
		case keymap.ActionFastLeft:
			m.horizScroll -= horizFastStep
			if m.horizScroll < 0 {
				m.horizScroll = 0
			}
		case keymap.ActionFastRight:
			m.horizScroll += horizFastStep
		case keymap.ActionResetHoriz:
			m.horizScroll = 0
		case keymap.ActionCollapseAll:
			m.collapseMultiline = !m.collapseMultiline
		case keymap.ActionToggleRenderers:
			m.showRenderersPanel = !m.showRenderersPanel
			if m.showRenderersPanel {
				m.showFiles = false
				m.showGroupsPanel = false
			}
			m.renderersScroll = 0
		}
```

Confirm `strings` is imported in `app.go` (it is used elsewhere; if not, add it).

- [ ] **Step 4: Run tests**

Run: `go test ./internal/tui/`
Expected: PASS — new dispatch tests plus the full existing TUI suite (`app_test`, `search_test`, `multiline_test`, `renderers_test`) stay green, proving the refactor preserved behavior.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/app.go internal/tui/app_test.go
git commit -m "feat(tui): dispatch keys via keymap actions"
```

---

## Task 9: generated help text in the TUI

**Files:**
- Modify: `internal/tui/app.go` — header (`app.go:1038`), `renderFooter`, overlay headers (`app.go:1148/1205/1444`) built from the keymap.
- Test: `internal/tui/app_test.go` (append)

- [ ] **Step 1: Write the failing test**

```go
func TestHeaderUsesKeymapDisplay(t *testing.T) {
	m := newModel(100)
	m.km = keymap.Default("darwin")
	m.width = 200
	m.height = 10
	view := m.View()
	if !strings.Contains(view, "⌃G") { // mac glyph for toggle_groups
		t.Errorf("darwin header should show ⌃G; view header missing it")
	}
	if strings.Contains(view, "Ctrl+G") {
		t.Errorf("darwin header should not show linux-style Ctrl+G")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run TestHeaderUsesKeymapDisplay`
Expected: FAIL — header is the hard-coded `" … Ctrl+G groups …"` string.

- [ ] **Step 3: Build the header/footer from the keymap**

Add a helper to `app.go`:

```go
// hint renders "<keys> <label>" for an action using the model's keymap, e.g.
// "⌃G groups" on macOS or "Ctrl+G groups" elsewhere.
func (m *model) hint(a keymap.Action, label string) string {
	km := m.km
	if km == nil {
		km = keymap.Default(runtime.GOOS)
	}
	return km.Display(a) + " " + label
}
```

Replace the hard-coded header (`app.go:1038`) with a generated one:

```go
	hints := []string{
		m.hint(keymap.ActionQuit, "quit"),
		m.hint(keymap.ActionToggleFiles, "files"),
		m.hint(keymap.ActionToggleGroups, "groups"),
		m.hint(keymap.ActionToggleRenderers, "rend"),
		"1-9 grp",
		m.hint(keymap.ActionCollapseAll, "collapse"),
		m.hint(keymap.ActionToggleGroupCol, "grpcol"),
		m.hint(keymap.ActionToggleFileCol, "filecol"),
		m.hint(keymap.ActionClear, "clear"),
		m.hint(keymap.ActionSearch, "search"),
		m.hint(keymap.ActionNextMatch, "next") + "/" + m.hint(keymap.ActionPrevMatch, "prev"),
		m.hint(keymap.ActionFilter, "filter"),
	}
	header := headerBg.Width(m.width).MaxHeight(1).Render(" log-listener — " + strings.Join(hints, " · ") + " ")
```

For the three overlay panel headers, replace the literal key text with the keymap display. Examples:

- Groups panel (`app.go:1148`):
```go
	out = append(out, headerBg.Width(m.width).MaxHeight(1).Render(" Groups ("+m.km.Display(keymap.ActionToggleGroups)+" or "+m.km.Display(keymap.ActionCloseOverlay)+" to close · 1-9 to toggle) "))
```
- Renderers panel (`app.go:1205`):
```go
	out = append(out, headerBg.Width(m.width).MaxHeight(1).Render(" Renderers ("+m.km.Display(keymap.ActionToggleRenderers)+" or "+m.km.Display(keymap.ActionCloseOverlay)+" to close · !-( to toggle) "))
```
- Files panel (`app.go:1444`):
```go
	out = append(out, headerBg.Width(m.width).MaxHeight(1).Render(" Watched files ("+m.km.Display(keymap.ActionToggleFiles)+" or "+m.km.Display(keymap.ActionCloseOverlay)+" to close) "))
```

If `renderFooter` contains hard-coded key text, apply the same `m.hint`/`m.km.Display` treatment there. (Inspect `renderFooter`; if it only shows counts/search state and no key text, leave it.)

- [ ] **Step 4: Run tests**

Run: `go test ./internal/tui/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tui/app.go internal/tui/app_test.go
git commit -m "feat(tui): generate header/overlay help text from keymap"
```

---

## Task 10: committed generated doc + staleness guard + Makefile

**Files:**
- Create: `docs/KEYBINDINGS.md` (generated)
- Create: `internal/keymap/docfile_test.go` (staleness guard)
- Modify: `Makefile` (add `keybindings-docs` target)

- [ ] **Step 1: Write the failing staleness test**

Create `internal/keymap/docfile_test.go`:

```go
package keymap_test

import (
	"os"
	"testing"

	"log-listener/internal/keymap"
)

// TestDocsUpToDate fails if docs/KEYBINDINGS.md drifts from RenderMarkdownDoc.
// Regenerate with: make keybindings-docs
func TestDocsUpToDate(t *testing.T) {
	const path = "../../docs/KEYBINDINGS.md"
	got := keymap.RenderMarkdownDoc()
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v (run `make keybindings-docs`)", path, err)
	}
	if string(want) != got {
		t.Errorf("%s is stale — run `make keybindings-docs`", path)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/keymap/ -run TestDocsUpToDate`
Expected: FAIL — `docs/KEYBINDINGS.md` does not exist yet.

- [ ] **Step 3a: Add the Makefile target**

Append to `Makefile`:

```make
.PHONY: keybindings-docs
keybindings-docs: ## regenerate docs/KEYBINDINGS.md from the keymap
	go run ./cmd/log-listener --keybindings-doc > docs/KEYBINDINGS.md
```

- [ ] **Step 3b: Generate the doc**

Run:
```bash
make keybindings-docs
```
Expected: writes `docs/KEYBINDINGS.md`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/keymap/ -run TestDocsUpToDate`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add docs/KEYBINDINGS.md internal/keymap/docfile_test.go Makefile
git commit -m "docs: app-generated KEYBINDINGS.md + staleness guard"
```

---

## Task 11: full-suite verification + CLAUDE.md note

**Files:**
- Modify: `CLAUDE.md` (module map row + one bullet)

- [ ] **Step 1: Run the whole suite**

Run:
```bash
make test && make vet && make race
```
Expected: all green.

- [ ] **Step 2: Add the keymap row to the CLAUDE.md module map**

Add to the module-map table:

```
| `internal/keymap`          | Actions ↔ per-OS keys, glyph display, override resolve, doc gen. |
```

And one bullet under "Locked design rules":

```
- **Keybindings flow through `internal/keymap`**: one named action per TUI
  function; per-OS default keys; YAML overrides resolve current-OS → `default`
  → app-default (per-action replace); `docs/KEYBINDINGS.md` is generated via
  `--keybindings-doc` and guarded by `TestDocsUpToDate`.
```

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: document internal/keymap in CLAUDE.md"
```

---

## Self-Review notes

- **Spec coverage:** translation layer (Tasks 1–4), glyph display (Task 3), per-OS defaults + macOS remap (Task 2), YAML overrides with precedence (Tasks 4,6,7), collision/normalization/unknown-action errors (Task 4), positional-toggle scope boundary (Task 8), app-generated doc + staleness guard (Tasks 5,7,10), CLAUDE.md (Task 11). All spec sections map to a task.
- **Cross-task type consistency:** `Action`, `*Keymap`, `Resolve(goos, userDefault, userOS)`, `Default(goos)`, `(*Keymap).Lookup/Keys/Display`, `Display(keys, goos)`, `RenderMarkdownDoc()`, `config.Keybindings{Default,Darwin,Linux,Windows map[string][]string}`, `tui.Options.Keymap`, `model.km` — names used identically across Tasks 1–10.
- **Task 7/8 ordering caveat:** Task 7's `tui.Options.Keymap` field is introduced in Task 8 Step 3a. When executing, implement Task 8 Step 3a before running `go build ./...` for Task 7, or do Task 8 first. Each task's *own* unit tests still pass in isolation once both edits land.
```
