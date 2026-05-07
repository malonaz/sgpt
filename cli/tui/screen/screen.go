package screen

import (
	tea "charm.land/bubbletea/v2"
	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
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
	Chat *sgptpb.Chat
	Fork bool
}

type OpenMenuMsg struct{}
type OpenSearchMsg struct{}

type CloseTabMsg struct {
	TabID string
}

type AlertMsg struct {
	Text string
}
