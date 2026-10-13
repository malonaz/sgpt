package screen

import (
	tea "charm.land/bubbletea/v2"
	aipb "github.com/malonaz/core/genproto/ai/v1"

	"github.com/malonaz/sgpt/internal/tool"
	"github.com/malonaz/sgpt/internal/tool/agent"
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

// AgentDirectory is the sub-agent tree as a chat screen shows it.
type AgentDirectory interface {
	Children(agent tool.Agent) []agent.Status
	Parent(agent tool.Agent) tool.Agent
}

type TabMsg struct {
	TabID string
	Msg   tea.Msg
}

type OpenChatMsg struct {
	Chat *aipb.Chat
}

type OpenMenuMsg struct{}

type CloseTabMsg struct {
	TabID string
}

// FocusAgentMsg asks for the tab of an agent's chat to be focused.
type FocusAgentMsg struct {
	Agent tool.Agent
	// Title names the agent should its tab be gone.
	Title string
}

type AlertMsg struct {
	Text string
}
