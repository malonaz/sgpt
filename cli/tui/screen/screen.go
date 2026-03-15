package screen

import (
	tea "charm.land/bubbletea/v2"
	chatpb "github.com/malonaz/sgpt/genproto/chat/v1"
)

// WrapFunc wraps a tea.Msg with the owning tab's ID so the app routes it correctly.
// Used inside tea.Cmd functions to return properly routed messages.
type WrapFunc func(tea.Msg) tea.Msg

// SendFunc sends a tea.Msg from a background goroutine via program.Send,
// pre-tagged with the owning tab's ID. Used only for long-lived goroutines
// (e.g. streaming) that cannot return messages via tea.Cmd.
type SendFunc func(tea.Msg)

// Screen is implemented by every top-level view (chat, menu, search).
type Screen interface {
	Init() tea.Cmd
	Update(msg tea.Msg) tea.Cmd
	View() string
	Title() string
	ShortTitle() string
	SetSize(width, height int)
	OnFocus() tea.Cmd
	OnBlur()
}

// TabMsg wraps a message destined for a specific tab.
type TabMsg struct {
	TabID string
	Msg   tea.Msg
}

// OpenChatMsg requests opening a chat in a new tab.
type OpenChatMsg struct {
	Chat *chatpb.Chat
	Fork bool
}

// OpenMenuMsg requests opening or focusing the menu tab.
type OpenMenuMsg struct{}

// OpenSearchMsg requests opening or focusing the search tab.
type OpenSearchMsg struct{}

// CloseTabMsg requests closing the specified tab (or the active tab if empty).
type CloseTabMsg struct {
	TabID string
}

// AlertMsg displays a temporary notification overlay.
type AlertMsg struct {
	Text string
}
