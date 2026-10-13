package screen

import (
	tea "charm.land/bubbletea/v2"
	aipb "github.com/malonaz/core/genproto/ai/v1"

	"github.com/malonaz/sgpt/cli/tui/widget"
	"github.com/malonaz/sgpt/internal/tool"
	"github.com/malonaz/sgpt/internal/tool/agent"
)

type WrapFunc func(tea.Msg) tea.Msg
type SendFunc func(tea.Msg)

type Screen interface {
	Init() tea.Cmd
	Update(msg tea.Msg) tea.Cmd
	View() string
	// Title labels the screen's tab; the tab bar truncates it to fit.
	Title() string
	SetSize(width, height int)
	OnFocus() tea.Cmd
	OnBlur()
}

// Interrupter is a screen with something for ctrl+c to stop short of
// quitting; Interrupt reports whether it acted.
type Interrupter interface {
	Interrupt() bool
}

// StatusReporter is a screen that fills the status bar.
type StatusReporter interface {
	Status() widget.Status
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

// Restyler screens drop color-baked renders when the palette changes.
type Restyler interface {
	Restyle()
}

// AlertLevel grades an alert: it picks the toast's color and how long it
// stays up.
type AlertLevel int

const (
	AlertInfo AlertLevel = iota
	AlertSuccess
	AlertError
)

type AlertMsg struct {
	Text  string
	Level AlertLevel
}

// ConfirmMsg asks the app to guard Action behind a yes/no modal.
type ConfirmMsg struct {
	Title  string
	Body   string
	Action tea.Cmd
}
