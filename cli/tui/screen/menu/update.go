package menu

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/malonaz/sgpt/cli/tui/screen"
)

var (
	keyUp       = key.NewBinding(key.WithKeys("ctrl+p"))
	keyDown     = key.NewBinding(key.WithKeys("ctrl+n"))
	keyOpen     = key.NewBinding(key.WithKeys("enter"))
	keyDelete   = key.NewBinding(key.WithKeys("alt+d"))
	keyRefresh  = key.NewBinding(key.WithKeys("alt+r"))
	keyNextPage = key.NewBinding(key.WithKeys("alt+]"))
	keyPrevPage = key.NewBinding(key.WithKeys("alt+["))
	keyToTop    = key.NewBinding(key.WithKeys("alt+<"))
	keyToBottom = key.NewBinding(key.WithKeys("alt+>"))
)

func (m *Model) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.recalculateLayout()
		return nil

	case chatsLoadedMsg:
		m.loading = false
		if msg.Err != nil {
			m.err = msg.Err
			return nil
		}
		if msg.SearchQuery != m.searchQuery {
			return nil
		}
		m.chats = msg.Chats
		m.currentPageToken = msg.PageToken
		m.nextPageToken = msg.NextPageToken
		m.err = nil
		m.chatCursor = 0
		m.updateSelection()
		m.listViewport.SetContent(m.renderList())
		return nil

	case chatDeletedMsg:
		if msg.Err != nil {
			return m.wrapCmd(screen.AlertMsg{Text: "Delete failed: " + msg.Err.Error()})
		}
		for i, chat := range m.chats {
			if chat.Name == msg.Name {
				m.chats = append(m.chats[:i], m.chats[i+1:]...)
				break
			}
		}
		m.updateSelection()
		m.listViewport.SetContent(m.renderList())
		return m.wrapCmd(screen.AlertMsg{Text: "Chat deleted"})

	case searchDebounceTickMsg:
		currentQuery := strings.TrimSpace(m.searchInput.Value())
		if msg.Query != currentQuery {
			return nil
		}
		if currentQuery == m.lastSearchQuery {
			return nil
		}
		m.lastSearchQuery = currentQuery
		m.searchQuery = currentQuery
		m.resetPagination()
		return m.fetchChats("")

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	return nil
}

func (m *Model) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case key.Matches(msg, keyToTop):
		m.focusTarget = FocusFilter
		m.listViewport.SetContent(m.renderList())
		return m.applyFocus()

	case key.Matches(msg, keyToBottom):
		displayed := m.displayedChats()
		if len(displayed) > 0 {
			m.focusTarget = FocusChatList
			m.chatCursor = len(displayed) - 1
			m.updateSelection()
			m.listViewport.SetContent(m.renderList())
		}
		return m.applyFocus()

	case key.Matches(msg, keyUp):
		return m.navigateUp()

	case key.Matches(msg, keyDown):
		return m.navigateDown()

	case key.Matches(msg, keyOpen):
		if m.focusTarget == FocusChatList {
			if chat := m.selectedChat(); chat != nil {
				return m.wrapCmd(screen.OpenChatMsg{Chat: chat})
			}
		}
		return nil

	case key.Matches(msg, keyDelete):
		if m.focusTarget == FocusChatList {
			if chat := m.selectedChat(); chat != nil {
				return m.deleteChat(chat.Name)
			}
		}
		return nil

	case key.Matches(msg, keyRefresh):
		m.resetPagination()
		return m.fetchChats("")

	case key.Matches(msg, keyNextPage):
		return m.nextPage()

	case key.Matches(msg, keyPrevPage):
		return m.previousPage()
	}

	switch m.focusTarget {
	case FocusFilter:
		return m.handleFilterInput(msg)
	case FocusSearch:
		return m.handleSearchInput(msg)
	}

	return nil
}

func (m *Model) navigateUp() tea.Cmd {
	switch m.focusTarget {
	case FocusFilter:
		return nil
	case FocusSearch:
		m.focusTarget = FocusFilter
		m.listViewport.SetContent(m.renderList())
		return m.applyFocus()
	case FocusChatList:
		if m.chatCursor > 0 {
			m.chatCursor--
			m.updateSelection()
			m.listViewport.SetContent(m.renderList())
			return nil
		}
		m.focusTarget = FocusSearch
		m.listViewport.SetContent(m.renderList())
		return m.applyFocus()
	}
	return nil
}

func (m *Model) navigateDown() tea.Cmd {
	switch m.focusTarget {
	case FocusFilter:
		m.focusTarget = FocusSearch
		m.listViewport.SetContent(m.renderList())
		return m.applyFocus()
	case FocusSearch:
		displayed := m.displayedChats()
		if len(displayed) > 0 {
			m.focusTarget = FocusChatList
			m.chatCursor = 0
			m.updateSelection()
			m.listViewport.SetContent(m.renderList())
			return m.applyFocus()
		}
		return nil
	case FocusChatList:
		displayed := m.displayedChats()
		if m.chatCursor < len(displayed)-1 {
			m.chatCursor++
			m.updateSelection()
			m.listViewport.SetContent(m.renderList())
		}
		return nil
	}
	return nil
}

func (m *Model) handleFilterInput(msg tea.KeyPressMsg) tea.Cmd {
	var cmd tea.Cmd
	m.filterInput, cmd = m.filterInput.Update(msg)

	newFilter := strings.TrimSpace(m.filterInput.Value())
	if newFilter != m.filterText {
		m.filterText = newFilter
		m.chatCursor = 0
		m.updateSelection()
		m.listViewport.SetContent(m.renderList())
	}
	return cmd
}

func (m *Model) handleSearchInput(msg tea.KeyPressMsg) tea.Cmd {
	var cmd tea.Cmd
	m.searchInput, cmd = m.searchInput.Update(msg)

	currentQuery := strings.TrimSpace(m.searchInput.Value())

	if currentQuery == "" && m.searchQuery != "" {
		m.searchQuery = ""
		m.lastSearchQuery = ""
		m.resetPagination()
		return tea.Batch(cmd, m.fetchChats(""))
	}

	if currentQuery != "" && currentQuery != m.lastSearchQuery {
		query := currentQuery
		cmd = tea.Batch(cmd, tea.Tick(searchDebounceInterval, func(time.Time) tea.Msg {
			return searchDebounceTickMsg{Query: query}
		}))
	}

	return cmd
}

func (m *Model) wrapCmd(msg tea.Msg) tea.Cmd {
	wrap := m.wrap
	return func() tea.Msg {
		return wrap(msg)
	}
}
