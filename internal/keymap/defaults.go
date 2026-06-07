package keymap

// defaultFor returns the built-in default key list per action for the given
// runtime.GOOS. Key order = display priority; the handler matches ANY key in
// the list. The ONLY macOS difference is that fast-scroll actions advertise
// the shift+arrow form first, because ctrl+arrow is captured by macOS
// Mission Control / Spaces before a terminal sees it (ctrl+arrow stays bound
// so it still works if the user disabled those system shortcuts).
func defaultFor(goos string) map[Action][]string {
	m := map[Action][]string{
		ActionQuit:                 {"ctrl+c", "q"},
		ActionToggleFiles:          {"ctrl+i", "tab"},
		ActionToggleGroups:         {"ctrl+g"},
		ActionToggleRenderers:      {"ctrl+e"},
		ActionCloseOverlay:         {"esc"},
		ActionSearch:               {"/"},
		ActionNextMatch:            {"n"},
		ActionPrevMatch:            {"p"},
		ActionFilter:               {"t"},
		ActionToggleGroupCol:       {"ctrl+p"},
		ActionToggleFileCol:        {"ctrl+l"},
		ActionClear:                {"ctrl+r"},
		ActionCollapseAll:          {"m"},
		ActionScrollUp:             {"up", "k"},
		ActionScrollDown:           {"down", "j"},
		ActionPageUp:               {"pgup", "ctrl+b"},
		ActionPageDown:             {"pgdown", "ctrl+f", " "},
		ActionTop:                  {"home", "g"},
		ActionBottom:               {"end", "G"},
		ActionScrollLeft:           {"left", "h"},
		ActionScrollRight:          {"right", "l"},
		ActionResetHoriz:           {"0"},
		ActionSaveViewport:         {"s"},
		ActionSaveScrollback:       {"S"},
		ActionNextBlock:            {"]"},
		ActionPrevBlock:            {"["},
		ActionNextMarkedBlock:      {"}"},
		ActionPrevMarkedBlock:      {"{"},
		ActionToggleExceptionMarks: {"e"},
		ActionCopyReference:        {"y"},
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
