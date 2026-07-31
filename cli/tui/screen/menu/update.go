package menu

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	aipb "github.com/malonaz/core/genproto/ai/v1"

	"github.com/malonaz/sgpt/cli/tui/keymap"
	"github.com/malonaz/sgpt/cli/tui/screen"
)

var (
	keyUp           = keymap.New("ctrl+p", "Move up")
	keyDown         = keymap.New("ctrl+n", "Move down")
	keyOpen         = keymap.New("enter", "Open chat")
	keyDelete       = keymap.New("alt+d", "Delete chat")
	keyRefresh      = keymap.New("alt+r", "Refresh")
	keyToTop        = keymap.New("alt+<", "Jump to filter")
	keyToBottom     = keymap.New("alt+>", "Jump to last chat")
	keyMenuFavorite = keymap.New("alt+shift+f", "Toggle favorite")
)

func (m *Model) Keymaps() []keymap.Map {
	return []keymap.Map{{
		Name: "Menu",
		Bindings: []keymap.Binding{
			keyUp, keyDown, keyOpen, keyDelete, keyMenuFavorite,
			keyRefresh, keyToTop, keyToBottom,
		},
	}}
}

func (m *Model) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.recalculateLayout()
		return nil

	case chatsLoadedMsg:
		if msg.Err != nil {
			m.loading = false
			m.loadingMore = false
			m.err = msg.Err
			return nil
		}
		m.applyChats(&msg)
		return nil

	case chatDeletedMsg:
		if msg.Err != nil {
			return m.wrapCmd(screen.AlertMsg{Text: "Delete failed: " + msg.Err.Error()})
		}
		delete(m.detailCache, msg.Name)
		m.removeChatByName(msg.Name)
		m.refreshList()
		return m.wrapCmd(screen.AlertMsg{Text: "Chat deleted"})

	case chatFavoriteToggledMsg:
		if msg.Err != nil {
			return m.wrapCmd(screen.AlertMsg{Text: "Favorite toggle failed: " + msg.Err.Error()})
		}
		// Re-fetch to get correct server-side ordering.
		delete(m.detailCache, msg.Name)
		label := "added to"
		if !msg.Favorited {
			label = "removed from"
		}
		return tea.Batch(m.fetchChats("", false), m.wrapCmd(screen.AlertMsg{Text: "Chat " + label + " favorites"}))

	case searchDebounceMsg:
		if msg.seq != m.searchSeq {
			return nil
		}
		return m.runSearch(msg.seq)

	case searchResultsMsg:
		if msg.seq != m.searchSeq || m.filterText == "" {
			return nil
		}
		if msg.err != nil {
			// Substring filtering stays in effect; just surface the failure.
			return m.wrapCmd(screen.AlertMsg{Text: "Search failed: " + msg.err.Error()})
		}
		m.searchResults = msg.results
		m.searchFetchedChats = map[string]*aipb.Chat{}
		for _, chat := range msg.fetched {
			m.searchFetchedChats[chat.GetName()] = chat
		}
		m.refreshList()
		return nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	return nil
}

func (m *Model) removeChatByName(name string) {
	for i, chat := range m.favorites {
		if chat.Name == name {
			m.favorites = append(m.favorites[:i], m.favorites[i+1:]...)
			return
		}
	}
	for i, chat := range m.others {
		if chat.Name == name {
			m.others = append(m.others[:i], m.others[i+1:]...)
			return
		}
	}
}

func (m *Model) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case key.Matches(msg, keyToTop.Key):
		m.focusTarget = FocusFilter
		m.listYOffset = 0
		return m.applyFocus()

	case key.Matches(msg, keyToBottom.Key):
		displayed := m.displayedChats()
		if len(displayed) > 0 {
			m.focusTarget = FocusChatList
			m.chatCursor = len(displayed) - 1
			m.updateSelection()
			m.ensureCursorVisible()
		}
		return m.applyFocus()

	case key.Matches(msg, keyUp.Key):
		return m.navigateUp()

	case key.Matches(msg, keyDown.Key):
		return m.navigateDown()

	case key.Matches(msg, keyOpen.Key):
		if m.focusTarget == FocusChatList {
			if chat := m.selectedChat(); chat != nil {
				return m.wrapCmd(screen.OpenChatMsg{Chat: chat})
			}
		}
		return nil

	case key.Matches(msg, keyDelete.Key):
		if m.focusTarget == FocusChatList {
			if chat := m.selectedChat(); chat != nil {
				return m.deleteChat(chat.Name)
			}
		}
		return nil

	case key.Matches(msg, keyMenuFavorite.Key):
		if m.focusTarget == FocusChatList {
			if chat := m.selectedChat(); chat != nil {
				return m.toggleFavorite(chat)
			}
		}
		return nil

	case key.Matches(msg, keyRefresh.Key):
		m.detailCache = map[string]string{}
		return m.fetchChats("", false)
	}

	if m.focusTarget == FocusFilter {
		return m.handleFilterInput(msg)
	}
	return nil
}

func (m *Model) navigateUp() tea.Cmd {
	switch m.focusTarget {
	case FocusFilter:
		return nil
	case FocusChatList:
		if m.chatCursor > 0 {
			m.chatCursor--
			m.updateSelection()
			m.ensureCursorVisible()
			return nil
		}
		m.focusTarget = FocusFilter
		m.listYOffset = 0
		return m.applyFocus()
	}
	return nil
}

func (m *Model) navigateDown() tea.Cmd {
	switch m.focusTarget {
	case FocusFilter:
		displayed := m.displayedChats()
		if len(displayed) > 0 {
			m.focusTarget = FocusChatList
			m.chatCursor = 0
			m.updateSelection()
			m.ensureCursorVisible()
			return m.applyFocus()
		}
		return nil
	case FocusChatList:
		displayed := m.displayedChats()
		if m.chatCursor < len(displayed)-1 {
			m.chatCursor++
			m.updateSelection()
			m.ensureCursorVisible()
		}
		// Infinite scroll: top up the list before the cursor hits the end.
		return m.maybeLoadMore()
	}
	return nil
}

func (m *Model) handleFilterInput(msg tea.KeyPressMsg) tea.Cmd {
	var cmd tea.Cmd
	m.filterInput, cmd = m.filterInput.Update(msg)

	newFilter := strings.TrimSpace(m.filterInput.Value())
	if newFilter == m.filterText {
		return cmd
	}
	m.filterText = newFilter
	m.chatCursor = 0
	// Drop stale ranked results; substring filtering covers the gap until
	// the debounced index query lands.
	m.searchResults = nil
	m.refreshList()
	if newFilter == "" || m.store.SearchIndex() == nil {
		return cmd
	}
	m.searchSeq++
	seq := m.searchSeq
	wrap := m.wrap
	return tea.Batch(cmd, tea.Tick(searchDebounceInterval, func(time.Time) tea.Msg {
		return wrap(searchDebounceMsg{seq: seq})
	}))
}

func (m *Model) wrapCmd(msg tea.Msg) tea.Cmd {
	wrap := m.wrap
	return func() tea.Msg {
		return wrap(msg)
	}
}
