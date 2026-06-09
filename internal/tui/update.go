package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/keymap"
)

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		// Any keypress dismisses a transient flash message.
		m.flash = ""
		// Modal key paths take priority — search input swallows almost
		// everything, and a pending wrap prompt swallows y/n/Esc before
		// the normal dispatcher sees them.
		if m.visualMode {
			return m.handleVisualKey(msg)
		}
		if m.searchInput {
			return m.handleSearchInputKey(msg), nil
		}
		if m.wrapPrompt != 0 {
			return m.handleWrapPromptKey(msg), nil
		}
		if m.showHelp {
			return m.handleHelpKey(msg), nil
		}
		key := msg.String()

		// Positional toggles are not part of the action keymap (they are
		// inherently 1-9 / shifted-1-9 by position). Handle them first.
		if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
			idx := int(key[0] - '1')
			if idx < len(m.groupOrder) {
				gid := m.groupOrder[idx]
				m.groupEnabled[gid] = !m.groupEnabled[gid]
			}
			return m, nil
		}
		if len(key) == 1 {
			if ri := strings.IndexByte("!@#$%^&*(", key[0]); ri >= 0 {
				m.toggleRenderer(ri)
				return m, nil
			}
		}

		km := m.resolvedKM()
		action, ok := km.Lookup(key)
		if !ok {
			return m, nil
		}
		switch action {
		case keymap.ActionQuit:
			return m, tea.Quit
		case keymap.ActionToggleFiles:
			// Ctrl+I and Tab share byte 0x09 in terminals; bubbletea
			// usually surfaces it as "tab". Both bind to this action so the
			// binding works regardless of terminal handling.
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
			m.blockFocused = false
			if m.showFiles {
				m.showFiles = false
			}
			if m.showGroupsPanel {
				m.showGroupsPanel = false
			}
			if m.showRenderersPanel {
				m.showRenderersPanel = false
			}
			// Esc with no overlay open clears any active search results
			// — term goes away, highlights vanish, hit pointer resets.
			if !m.showFiles && !m.showGroupsPanel && !m.showRenderersPanel && m.matcher != nil {
				m.clearSearch()
			}
		case keymap.ActionSearch:
			m.searchInput = true
			m.searchQuery = ""
			m.searchRegex = false // every fresh search starts in substring mode
		case keymap.ActionNextMatch:
			m.searchNext()
		case keymap.ActionPrevMatch:
			m.searchPrev()
		case keymap.ActionFilter:
			if m.matcher != nil {
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
			// Clear the TUI's scrollback. The shared buffer / watcher / sinks
			// keep running (MCP still sees everything); only the in-memory view
			// is reset by raising the Clear floor to the latest entry's Seq, so
			// reconcile hides everything at or before it. Re-enter tail mode so
			// the next event appears immediately at the top.
			if snap, _ := m.buf.Snapshot(0); len(snap) > 0 {
				m.clearedSeq = snap[len(snap)-1].Seq
			}
			m.displayCache = map[string][]displayLine{}
			m.prevIDLines = map[string]int{}
			m.lines = nil
			m.lastGen = 0 // force the next reconcile
			m.setStreamTopRow(0)
			m.tailMode = true
			m.horizScroll = 0
			m.setSearchHitRow(-1)
			// Filtering an emptied buffer would render blank; drop it.
			m.filterMode = false
			m.reconcile()
		case keymap.ActionScrollUp:
			m.blockFocused = false
			if m.showFiles {
				m.scrollFiles(-1)
			} else if m.matcher != nil {
				m.searchPrev()
			} else {
				m.scrollBy(-1)
			}
		case keymap.ActionScrollDown:
			m.blockFocused = false
			if m.showFiles {
				m.scrollFiles(1)
			} else if m.matcher != nil {
				m.searchNext()
			} else {
				m.scrollBy(1)
			}
		case keymap.ActionPageUp:
			m.blockFocused = false
			page := m.contentHeight()
			if m.showFiles {
				m.scrollFiles(-page)
			} else {
				m.scrollBy(-m.vstep(page))
			}
		case keymap.ActionPageDown:
			m.blockFocused = false
			page := m.contentHeight()
			if m.showFiles {
				m.scrollFiles(page)
			} else {
				m.scrollBy(m.vstep(page))
			}
		case keymap.ActionFastUp:
			m.blockFocused = false
			if m.showFiles {
				m.scrollFiles(-vertFastStep)
			} else {
				m.scrollBy(-m.vstep(vertFastStep))
			}
		case keymap.ActionFastDown:
			m.blockFocused = false
			if m.showFiles {
				m.scrollFiles(vertFastStep)
			} else {
				m.scrollBy(m.vstep(vertFastStep))
			}
		case keymap.ActionTop:
			m.blockFocused = false
			if m.showFiles {
				m.filesScroll = 0
			} else {
				m.tailMode = false
				m.setStreamTopRow(0)
			}
		case keymap.ActionBottom:
			m.blockFocused = false
			if m.showFiles {
				m.filesScroll = len(m.files) - 1
				if m.filesScroll < 0 {
					m.filesScroll = 0
				}
			} else {
				m.tailMode = true // re-stick to the latest, even when new events arrive
			}
		case keymap.ActionScrollLeft:
			m.panBy(-horizStep)
		case keymap.ActionScrollRight:
			m.panBy(horizStep)
		case keymap.ActionFastLeft:
			m.panBy(-horizFastStep)
		case keymap.ActionFastRight:
			m.panBy(horizFastStep)
		case keymap.ActionResetHoriz:
			m.horizScroll = 0
		case keymap.ActionToggleWordWrap:
			m.wordWrap = !m.wordWrap
			if m.wordWrap {
				m.horizScroll = 0
			}
		case keymap.ActionCopyReference:
			if ref := copyReference(m); ref != "" {
				m.flash = "copied " + ref
			}
		case keymap.ActionCopyText:
			if _, n := m.copySelectionText(); n > 0 {
				m.flash = fmt.Sprintf("copied %d lines", n)
			} else {
				m.flash = "nothing to copy"
			}
		case keymap.ActionVisualSelect:
			m.enterVisual()
		case keymap.ActionSaveViewport:
			return m, m.saveCmd(m.snapshotViewport())
		case keymap.ActionSaveScrollback:
			return m, m.saveCmd(m.snapshotScrollback())
		case keymap.ActionCollapseAll:
			// Collapse multiline entries (continuation rows hidden behind
			// a "[...]" marker on the head). Toggles repeatedly.
			m.collapseMultiline = !m.collapseMultiline
		case keymap.ActionHelp:
			m.showHelp = true
			m.helpQuery = ""
			m.helpScroll = 0
			m.showFiles = false
			m.showGroupsPanel = false
			m.showRenderersPanel = false
		case keymap.ActionToggleFilenameTrunc:
			m.truncateFiles = !m.truncateFiles
		case keymap.ActionToggleExceptionMarks:
			m.showExceptionMarks = !m.showExceptionMarks
		case keymap.ActionNextBlock:
			m.gotoNextBlock(false)
		case keymap.ActionPrevBlock:
			m.gotoPrevBlock(false)
		case keymap.ActionNextMarkedBlock:
			m.gotoNextBlock(true)
		case keymap.ActionPrevMarkedBlock:
			m.gotoPrevBlock(true)
		case keymap.ActionToggleRenderers:
			m.showRenderersPanel = !m.showRenderersPanel
			if m.showRenderersPanel {
				m.showFiles = false
				m.showGroupsPanel = false
			}
			m.renderersScroll = 0
		}
	case EventMsg:
		// The pump already appended to the shared buffer before Push; just
		// reconcile from it (the event payload is redundant).
		m.reconcile()
	case FileListMsg:
		m.files = msg.Files
		if m.filesScroll >= len(m.files) {
			m.filesScroll = 0
		}
	case ReloadMsg:
		m.applyReload(msg)
	case QuitMsg:
		return m, tea.Quit
	case saveResultMsg:
		if msg.err != nil {
			m.flash = "save failed: " + msg.err.Error()
		} else {
			m.flash = fmt.Sprintf("saved %d lines to %s", msg.n, msg.path)
		}
	}
	return m, nil
}

// applyReload swaps in the new config's panels and toggle state, then
// re-renders existing scrollback through renderFn (which now reads the
// reloaded pipeline). Scrollback source entries are preserved; only their
// rendered lines are rebuilt. Toggle state is reset to the new config's
// StartOff defaults — the renderer set may have changed, so preserving old
// indices would be ambiguous.
func (m *model) applyReload(msg ReloadMsg) {
	// Reset to nil (the newModel idiom) rather than slice[:0] — the renderer
	// set may shrink, and nil avoids retaining/aliasing the old backing array.
	m.groupOrder = nil
	m.groupEnabled = map[string]bool{}
	for _, g := range msg.Groups {
		m.groupOrder = append(m.groupOrder, g.ID)
		m.groupEnabled[g.ID] = !g.StartOff
	}
	m.rendererOrder = nil
	m.rendererEnabled = nil
	for _, r := range msg.Renderers {
		m.rendererOrder = append(m.rendererOrder, r.Name)
		m.rendererEnabled = append(m.rendererEnabled, !r.StartOff)
	}
	m.files = msg.Files
	if m.filesScroll >= len(m.files) {
		m.filesScroll = 0
	}
	if m.groupsScroll >= len(m.groupOrder) {
		m.groupsScroll = 0
	}
	if m.renderersScroll >= len(m.rendererOrder) {
		m.renderersScroll = 0
	}
	m.reRenderAll()
	m.blocksDirty = true
}
