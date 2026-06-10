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
	ActionQuit                 Action = "quit"
	ActionToggleFiles          Action = "toggle_files"
	ActionToggleGroups         Action = "toggle_groups"
	ActionToggleRenderers      Action = "toggle_renderers"
	ActionCloseOverlay         Action = "close_overlay"
	ActionSearch               Action = "search"
	ActionNextMatch            Action = "next_match"
	ActionPrevMatch            Action = "prev_match"
	ActionFilter               Action = "filter"
	ActionToggleGroupCol       Action = "toggle_group_col"
	ActionToggleFileCol        Action = "toggle_file_col"
	ActionClear                Action = "clear"
	ActionCatchUp              Action = "catch_up"
	ActionCollapseAll          Action = "collapse_all"
	ActionScrollUp             Action = "scroll_up"
	ActionScrollDown           Action = "scroll_down"
	ActionPageUp               Action = "page_up"
	ActionPageDown             Action = "page_down"
	ActionFastUp               Action = "fast_up"
	ActionFastDown             Action = "fast_down"
	ActionTop                  Action = "top"
	ActionBottom               Action = "bottom"
	ActionScrollLeft           Action = "scroll_left"
	ActionScrollRight          Action = "scroll_right"
	ActionFastLeft             Action = "fast_left"
	ActionFastRight            Action = "fast_right"
	ActionResetHoriz           Action = "reset_horiz"
	ActionSaveViewport         Action = "save_viewport"
	ActionSaveScrollback       Action = "save_scrollback"
	ActionNextBlock            Action = "next_block"
	ActionPrevBlock            Action = "prev_block"
	ActionNextMarkedBlock      Action = "next_marked_block"
	ActionPrevMarkedBlock      Action = "prev_marked_block"
	ActionToggleExceptionMarks Action = "toggle_exception_marks"
	ActionCopyReference        Action = "copy_reference"
	ActionCopyText             Action = "copy_text"
	ActionVisualSelect         Action = "visual_select"
	ActionToggleFilenameTrunc  Action = "toggle_filename_trunc"
	ActionToggleWordWrap       Action = "toggle_word_wrap"
	ActionDumpDebug            Action = "dump_debug"
	ActionHelp                 Action = "help"
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
	{ActionCatchUp, "Catch up to live", "Skip every tailer forward to the end of its file, dropping the unread backlog (use when the lag indicator shows the view is far behind). A marker line records how much was skipped.", "main"},
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
	{ActionSaveViewport, "Save viewport", "Write the visible rows to a text file.", "main"},
	{ActionSaveScrollback, "Save scrollback", "Write the full scrollback buffer to a text file.", "main"},
	{ActionNextBlock, "Next block", "Jump to the next multi-line block.", "main"},
	{ActionPrevBlock, "Previous block", "Jump to the previous multi-line block.", "main"},
	{ActionNextMarkedBlock, "Next marked block", "Jump to the next processor-matched block (e.g. exception).", "main"},
	{ActionPrevMarkedBlock, "Previous marked block", "Jump to the previous processor-matched block.", "main"},
	{ActionToggleExceptionMarks, "Toggle exception marks", "Show/hide the exception left-bar.", "main"},
	{ActionCopyReference, "Copy reference", "Copy a paste-ready id reference (search line, block range, or viewport range) for an agent.", "main"},
	{ActionCopyText, "Copy text", "Copy the selected text (search line, block, viewport, or visual selection) as displayed.", "main"},
	{ActionVisualSelect, "Visual select", "Enter visual line-selection mode (space sets the start; y copies the range, Y the text, s saves it to a file, all exit; esc cancels).", "main"},
	{ActionToggleFilenameTrunc, "Toggle filename truncation", "Shorten long filenames in the file column with a middle ellipsis.", "main"},
	{ActionToggleWordWrap, "Toggle word wrap", "Wrap long lines to multiple rows instead of horizontal scrolling.", "main"},
	{ActionDumpDebug, "Dump debug snapshot", "Write a diagnostic snapshot (buffer duplicate scan, view state, recent watch/reload events) to a debug-log-listener-*.txt file.", "main"},
	{ActionHelp, "Help", "Show the searchable keybindings panel.", "main"},
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
