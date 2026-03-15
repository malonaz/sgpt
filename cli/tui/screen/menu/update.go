package menu

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/malonaz/sgpt/cli/tui/screen"
)

var (
	keyUp       = key.NewBinding(key.WithKeys("ctrl+p"))
	keyDown     = key.NewBinding(key.WithKeys("ctrl+n"))
	keyTop      = key.NewBinding(key.WithKeys("g"))
	keyBottom   = key.NewBinding(key.WithKeys("G"))
	keyOpen     = key.NewBinding(key.WithKeys("enter"))
	keyDelete   = key.NewBinding(key.WithKeys("d"))
	keyFilter   = key.NewBinding(key.WithKeys("/"))
	keyRefresh  = key.NewBinding(key.WithKeys("r"))
	keyNextPage = key.NewBinding(key.WithKeys("alt+]"))
	keyPrevPage = key.NewBinding(key.WithKeys("alt+["))
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
		m.chats = msg.Chats
		m.currentPageToken = msg.PageToken
		m.nextPageToken = msg.NextPageToken
		m.cursor = 0
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
		if m.cursor >= len(m.filteredChats()) && m.cursor > 0 {
			m.cursor--
		}
		m.updateSelection()
		m.listViewport.SetContent(m.renderList())
		return m.wrapCmd(screen.AlertMsg{Text: "Chat deleted"})

	case tea.KeyPressMsg:
		if m.filtering {
			return m.handleFilterKey(msg)
		}
		return m.handleListKey(msg)
	}

	return nil
}

func (m *Model) handleListKey(msg tea.KeyPressMsg) tea.Cmd {
	filtered := m.filteredChats()

	switch {
	case key.Matches(msg, keyNextPage):
		return m.nextPage()

	case key.Matches(msg, keyPrevPage):
		return m.previousPage()

	case key.Matches(msg, keyUp):
		if m.cursor > 0 {
			m.cursor--
			m.updateSelection()
			m.listViewport.SetContent(m.renderList())
		}

	case key.Matches(msg, keyDown):
		if m.cursor < len(filtered)-1 {
			m.cursor++
			m.updateSelection()
			m.listViewport.SetContent(m.renderList())
		}

	case key.Matches(msg, keyTop):
		if m.cursor != 0 {
			m.cursor = 0
			m.updateSelection()
			m.listViewport.SetContent(m.renderList())
		}

	case key.Matches(msg, keyBottom):
		if len(filtered) > 0 && m.cursor != len(filtered)-1 {
			m.cursor = len(filtered) - 1
			m.updateSelection()
			m.listViewport.SetContent(m.renderList())
		}

	case key.Matches(msg, keyOpen):
		if m.cursor < len(filtered) {
			return m.wrapCmd(screen.OpenChatMsg{Chat: filtered[m.cursor]})
		}

	case key.Matches(msg, keyDelete):
		if m.cursor < len(filtered) {
			return m.deleteChat(filtered[m.cursor].Name)
		}

	case key.Matches(msg, keyFilter):
		m.filtering = true
		m.filterInput.Focus()
		m.recalculateLayout()
		return nil

	case key.Matches(msg, keyRefresh):
		m.pageTokenStack = nil
		return m.loadChats("")
	}

	switch msg.String() {
	case "ctrl+c":
		return m.wrapCmd(screen.CloseTabMsg{})
	case "escape":
		if m.filterText != "" {
			m.filterText = ""
			m.filterInput.SetValue("")
			m.cursor = 0
			m.updateSelection()
			m.listViewport.SetContent(m.renderList())
		}
	}

	return nil
}

func (m *Model) handleFilterKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "enter", "escape":
		m.filtering = false
		m.filterText = m.filterInput.Value()
		m.filterInput.Blur()
		m.cursor = 0
		m.recalculateLayout()
		m.updateSelection()
		m.listViewport.SetContent(m.renderList())
		return nil
	}

	var cmd tea.Cmd
	m.filterInput, cmd = m.filterInput.Update(msg)
	m.filterText = m.filterInput.Value()
	m.cursor = 0
	m.updateSelection()
	m.listViewport.SetContent(m.renderList())
	return cmd
}

func (m *Model) wrapCmd(msg tea.Msg) tea.Cmd {
	wrap := m.wrap
	return func() tea.Msg {
		return wrap(msg)
	}
}
