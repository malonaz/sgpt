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
	keyUp           = keymap.New("ctrl+p", "Move up").WithShort("up")
	keyDown         = keymap.New("ctrl+n", "Move down").WithShort("down")
	keyPageUp       = keymap.New("pgup", "Page up").WithShort("page up")
	keyPageDown     = keymap.New("pgdown", "Page down").WithShort("page down")
	keyOpen         = keymap.New("enter", "Open chat").WithShort("open")
	keyBack         = keymap.New("esc", "Back to search").WithShort("search")
	keyDelete       = keymap.New("alt+d", "Delete chat (asks first)").WithShort("delete")
	keyRefresh      = keymap.New("alt+r", "Refresh").WithShort("refresh")
	keyToTop        = keymap.New("alt+<", "Jump to search").WithShort("search")
	keyToBottom     = keymap.New("alt+>", "Jump to last chat").WithShort("last")
	keyMenuFavorite = keymap.New("alt+shift+f", "Toggle favorite").WithShort("favorite")
	keyToggleAgents = keymap.New("alt+s", "Show/hide sub-agent chats").WithShort("sub-agents")
)

func (m *Model) Keymaps() []keymap.Map {
	return []keymap.Map{{
		Name: "Chats",
		Bindings: []keymap.Binding{
			keyUp, keyDown, keyPageUp, keyPageDown, keyOpen, keyBack, keyDelete, keyMenuFavorite,
			keyToggleAgents, keyRefresh, keyToTop, keyToBottom,
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
		if msg.generation != m.listGeneration {
			return nil
		}
		if msg.Err != nil {
			m.loading = false
			m.loadingMore = false
			m.err = msg.Err
			return nil
		}
		m.applyChats(&msg)
		return m.maybeLoadMessages()

	case filterSettledMsg:
		if msg.generation != m.filterGeneration {
			return nil
		}
		m.chatCursor = 0
		m.listYOffset = 0
		return m.fetchChats("", false)

	case chatDeletedMsg:
		if msg.Err != nil {
			return m.wrapCmd(screen.AlertMsg{Text: "Delete failed: " + msg.Err.Error(), Level: screen.AlertError})
		}
		delete(m.detailCache, msg.Name)
		m.removeChatByName(msg.Name)
		m.refreshList()
		return tea.Batch(m.maybeLoadMessages(), m.wrapCmd(screen.AlertMsg{Text: "Chat deleted", Level: screen.AlertSuccess}))

	case chatFavoriteToggledMsg:
		if msg.Err != nil {
			return m.wrapCmd(screen.AlertMsg{Text: "Favorite toggle failed: " + msg.Err.Error(), Level: screen.AlertError})
		}
		// Re-fetch to get correct server-side ordering.
		delete(m.detailCache, msg.Name)
		label := "Added to"
		if !msg.Favorited {
			label = "Removed from"
		}
		return tea.Batch(m.fetchChats("", false), m.wrapCmd(screen.AlertMsg{Text: label + " favorites", Level: screen.AlertSuccess}))

	case chatMessagesLoadedMsg:
		delete(m.loadingMessagesSet, msg.name)
		if msg.err != nil {
			return m.wrapCmd(screen.AlertMsg{Text: "Loading preview failed: " + msg.err.Error(), Level: screen.AlertError})
		}
		m.messagesCache[msg.name] = msg.messages
		delete(m.detailCache, msg.name)
		m.touchPreview(msg.name)
		m.updateSelection()
		return nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	return nil
}

// removeChatByName drops a chat from both sections: a favorite also appears
// among the recent chats.
func (m *Model) removeChatByName(name string) {
	remove := func(chats []*aipb.Chat) []*aipb.Chat {
		kept := chats[:0]
		for _, chat := range chats {
			if chat.GetName() != name {
				kept = append(kept, chat)
			}
		}
		return kept
	}
	m.favorites = remove(m.favorites)
	m.others = remove(m.others)
}

func (m *Model) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case key.Matches(msg, keyToTop.Key):
		return m.focusFilter()

	case key.Matches(msg, keyBack.Key):
		if m.focusTarget == FocusChatList {
			return m.focusFilter()
		}
		return nil

	case key.Matches(msg, keyToBottom.Key):
		return m.moveCursorTo(len(m.displayed) - 1)

	case key.Matches(msg, keyUp.Key):
		if m.focusTarget == FocusChatList && m.chatCursor == 0 {
			return m.focusFilter()
		}
		if m.focusTarget == FocusChatList {
			return m.moveCursorTo(m.chatCursor - 1)
		}
		return nil

	case key.Matches(msg, keyDown.Key):
		if m.focusTarget == FocusFilter {
			return m.moveCursorTo(0)
		}
		return m.moveCursorTo(m.chatCursor + 1)

	case key.Matches(msg, keyPageUp.Key):
		if m.focusTarget == FocusChatList {
			return m.moveCursorTo(max(0, m.chatCursor-m.listHeight))
		}
		return nil

	case key.Matches(msg, keyPageDown.Key):
		start := m.chatCursor
		if m.focusTarget == FocusFilter {
			start = 0
		}
		return m.moveCursorTo(min(len(m.displayed)-1, start+m.listHeight))

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
				return m.confirmDelete(chat)
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

	case key.Matches(msg, keyToggleAgents.Key):
		return m.toggleAgents()

	case key.Matches(msg, keyRefresh.Key):
		m.detailCache = map[string]string{}
		return m.fetchChats("", false)
	}

	if m.focusTarget == FocusFilter {
		return m.handleFilterInput(msg)
	}
	return nil
}

func (m *Model) focusFilter() tea.Cmd {
	m.focusTarget = FocusFilter
	m.listYOffset = 0
	return m.applyFocus()
}

// moveCursorTo selects the chat at index (clamped), moving focus into the
// list, and tops the list up before the cursor reaches its end.
func (m *Model) moveCursorTo(index int) tea.Cmd {
	if len(m.displayed) == 0 {
		return nil
	}
	var focusCmd tea.Cmd
	if m.focusTarget != FocusChatList {
		m.focusTarget = FocusChatList
		focusCmd = m.applyFocus()
	}
	m.chatCursor = index
	m.updateSelection()
	m.ensureCursorVisible()
	return tea.Batch(focusCmd, m.maybeLoadMore(), m.maybeLoadMessages())
}

// handleFilterInput edits the query and schedules a debounced server-side
// search; the list keeps its current rows until the results land.
func (m *Model) handleFilterInput(msg tea.KeyPressMsg) tea.Cmd {
	var cmd tea.Cmd
	m.filterInput, cmd = m.filterInput.Update(msg)

	query := strings.TrimSpace(m.filterInput.Value())
	if query == m.filterText {
		return cmd
	}
	m.filterText = query
	m.filterGeneration++
	generation := m.filterGeneration
	wrap := m.wrap
	return tea.Batch(cmd, tea.Tick(filterDebounce, func(time.Time) tea.Msg {
		return wrap(filterSettledMsg{generation: generation})
	}))
}

func (m *Model) wrapCmd(msg tea.Msg) tea.Cmd {
	wrap := m.wrap
	return func() tea.Msg {
		return wrap(msg)
	}
}
