package viewer

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	aipb "github.com/malonaz/core/genproto/ai/v1"

	"github.com/malonaz/sgpt/cli/chat/types"
	"github.com/malonaz/sgpt/internal/markdown"
)

// Viewer-specific styles
var (
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("39"))

	footerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	dividerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("238"))
)

// ExitMsg is sent when exiting the viewer.
type ExitMsg struct{}

// Model represents the full-screen message viewer.
type Model struct {
	runtimeMessages []*types.RuntimeMessage
	currentIndex    int
	viewport        viewport.Model
	renderer        *markdown.Renderer
	width           int
	height          int
	ready           bool
}

// New creates a new viewer model starting at the last message.
func New(messages []*types.RuntimeMessage, renderer *markdown.Renderer, width, height int) *Model {
	startIndex := len(messages) - 1
	if startIndex < 0 {
		startIndex = 0
	}

	m := &Model{
		runtimeMessages: messages,
		currentIndex:    startIndex,
		renderer:        renderer,
		width:           width,
		height:          height,
	}

	// Initialize viewport - reserve 2 lines for footer
	viewportHeight := height - 2
	if viewportHeight < 1 {
		viewportHeight = 1
	}
	m.viewport = viewport.New(width, viewportHeight)
	m.viewport.MouseWheelEnabled = false // Disable mouse for copy/paste
	m.ready = true
	m.updateContent()

	return m
}

// Init initializes the viewer model.
func (m *Model) Init() tea.Cmd {
	return tea.DisableMouse
}

// Update handles messages.
func (m *Model) Update(msg tea.Msg) (*Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			return m, func() tea.Msg { return ExitMsg{} }

		case "j", "down":
			if m.currentIndex > 0 {
				m.currentIndex--
				m.updateContent()
				m.viewport.GotoTop()
			}
			return m, nil

		case "k", "up":
			if m.currentIndex < len(m.runtimeMessages)-1 {
				m.currentIndex++
				m.updateContent()
				m.viewport.GotoTop()
			}
			return m, nil

		case "alt+{":
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd

		case "alt+}":
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd

		case "g":
			m.viewport.GotoTop()
			return m, nil

		case "G":
			m.viewport.GotoBottom()
			return m, nil
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		viewportHeight := msg.Height - 2
		if viewportHeight < 1 {
			viewportHeight = 1
		}
		m.viewport.Width = msg.Width
		m.viewport.Height = viewportHeight
		m.renderer.SetWidth(msg.Width)
		m.updateContent()
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

// View renders the viewer.
func (m *Model) View() string {
	if !m.ready || len(m.runtimeMessages) == 0 {
		return "No messages to display. Press q to exit."
	}

	var b strings.Builder

	b.WriteString(m.viewport.View())
	b.WriteString("\n")

	divider := strings.Repeat("─", m.width)
	b.WriteString(dividerStyle.Render(divider))
	b.WriteString("\n")

	footer := fmt.Sprintf(" %d/%d │ Alt+{ prev │ Alt+} scroll │ j/k next │ q exit",
		m.currentIndex+1, len(m.runtimeMessages))
	b.WriteString(footerStyle.Render(footer))

	return b.String()
}

// SetMessages updates the messages list.
func (m *Model) SetMessages(messages []*types.RuntimeMessage) {
	m.runtimeMessages = messages
	if m.currentIndex >= len(messages) {
		m.currentIndex = len(messages) - 1
	}
	if m.currentIndex < 0 {
		m.currentIndex = 0
	}
	m.updateContent()
}

// updateContent updates the viewport content with the current message.
func (m *Model) updateContent() {
	if len(m.runtimeMessages) == 0 {
		m.viewport.SetContent("No messages")
		return
	}

	rm := m.runtimeMessages[m.currentIndex]
	msg := rm.Message

	var b strings.Builder

	switch msg.Role {
	case aipb.Role_ROLE_USER:
		header := headerStyle.Render("👤 User")
		b.WriteString(header)
		b.WriteString("\n\n")
		rendered := m.renderer.ToMarkdown(msg.Content, m.currentIndex, true)
		b.WriteString(rendered)

	case aipb.Role_ROLE_ASSISTANT:
		header := headerStyle.Render("🤖 Assistant")
		b.WriteString(header)
		b.WriteString("\n\n")

		if msg.Reasoning != "" {
			b.WriteString(headerStyle.Render("💭 Thinking:"))
			b.WriteString("\n")
			b.WriteString(msg.Reasoning)
			b.WriteString("\n\n")
		}

		rendered := m.renderer.ToMarkdown(msg.Content, m.currentIndex, true)
		b.WriteString(rendered)

		if len(msg.ToolCalls) > 0 {
			b.WriteString("\n\n")
			b.WriteString(headerStyle.Render("🔧 Tool Calls:"))
			b.WriteString("\n")
			for _, tc := range msg.ToolCalls {
				b.WriteString(fmt.Sprintf("\n%s:\n", tc.Name))
				b.WriteString(tc.Arguments)
			}
		}

		if rm.Err != nil {
			b.WriteString("\n\n")
			b.WriteString(fmt.Sprintf("⚠️ Error: %v", rm.Err))
		}

	case aipb.Role_ROLE_TOOL:
		header := headerStyle.Render("⚡ Tool Result")
		b.WriteString(header)
		b.WriteString("\n\n")
		rendered := m.renderer.ToMarkdown(msg.Content, m.currentIndex, true)
		b.WriteString(rendered)

	case aipb.Role_ROLE_SYSTEM:
		header := headerStyle.Render("⚙️ System")
		b.WriteString(header)
		b.WriteString("\n\n")
		b.WriteString(msg.Content)
	}

	m.viewport.SetContent(b.String())
}
