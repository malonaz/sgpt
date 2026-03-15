package search

import (
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/malonaz/sgpt/cli/tui/screen"
)

var (
	keyUp   = key.NewBinding(key.WithKeys("ctrl+p"))
	keyDown = key.NewBinding(key.WithKeys("ctrl+n"))
	keyOpen = key.NewBinding(key.WithKeys("enter"))
)

func (m *Model) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.recalculateLayout()
		return nil

	case searchResultsMsg:
		m.loading = false
		if msg.Query != m.lastQuery {
			return nil
		}
		if msg.Err != nil {
			m.err = msg.Err
			m.viewport.SetContent(m.renderResults())
			return nil
		}
		m.results = msg.Chats
		m.err = nil
		m.cursor = 0
		m.viewport.SetContent(m.renderResults())
		return nil

	case debounceTickMsg:
		currentQuery := m.queryInput.Value()
		if msg.Query == currentQuery && currentQuery != "" && currentQuery != m.lastQuery {
			m.lastQuery = currentQuery
			return m.executeSearch(currentQuery)
		}
		return nil

	case tea.KeyPressMsg:
		return m.handleKeyPress(msg)
	}

	var cmd tea.Cmd
	m.queryInput, cmd = m.queryInput.Update(msg)
	return cmd
}

func (m *Model) handleKeyPress(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case key.Matches(msg, keyUp):
		if m.cursor > 0 {
			m.cursor--
			m.viewport.SetContent(m.renderResults())
		}
		return nil

	case key.Matches(msg, keyDown):
		if m.cursor < len(m.results)-1 {
			m.cursor++
			m.viewport.SetContent(m.renderResults())
		}
		return nil

	case key.Matches(msg, keyOpen):
		if m.cursor < len(m.results) {
			return m.wrapCmd(screen.OpenChatMsg{Chat: m.results[m.cursor]})
		}
		return nil
	}

	switch msg.String() {
	case "ctrl+c":
		return m.wrapCmd(screen.CloseTabMsg{})
	case "escape":
		if m.queryInput.Value() != "" {
			m.queryInput.SetValue("")
			m.lastQuery = ""
			m.results = nil
			m.viewport.SetContent(m.renderResults())
			return nil
		}
		return m.wrapCmd(screen.CloseTabMsg{})
	}

	var cmd tea.Cmd
	m.queryInput, cmd = m.queryInput.Update(msg)

	currentQuery := m.queryInput.Value()
	if currentQuery != m.lastQuery {
		query := currentQuery
		cmd = tea.Batch(cmd, tea.Tick(debounceInterval, func(time.Time) tea.Msg {
			return debounceTickMsg{Query: query}
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
