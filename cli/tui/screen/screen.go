package screen

import (
	tea "charm.land/bubbletea/v2"
	aipb "github.com/malonaz/core/genproto/ai/v1"
)

type WrapFunc func(tea.Msg) tea.Msg
type SendFunc func(tea.Msg)

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

type TabMsg struct {
	TabID string
	Msg   tea.Msg
}

type OpenChatMsg struct {
	Chat *aipb.Chat
	Fork bool
}

type OpenMenuMsg struct{}

type CloseTabMsg struct {
	TabID string
}

type AlertMsg struct {
	Text string
}
