package chat

import (
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"
	"golang.design/x/clipboard"

	"github.com/malonaz/sgpt/cli/tui/screen"
)

var (
	keySessionCycleFocus     = key.NewBinding(key.WithKeys("tab"))
	keySessionCycleReasoning = key.NewBinding(key.WithKeys("alt+t"))
	keySessionForkChat       = key.NewBinding(key.WithKeys("alt+="))

	keyOpenEditor = key.NewBinding(key.WithKeys("ctrl+o"))

	keyViewportToTop       = key.NewBinding(key.WithKeys("alt+<"))
	keyViewportToBottom    = key.NewBinding(key.WithKeys("alt+>"))
	keyViewportPrevMessage = key.NewBinding(key.WithKeys("alt+{"))
	keyViewportNextMessage = key.NewBinding(key.WithKeys("alt+}"))
	keyViewportPrevBlock   = key.NewBinding(key.WithKeys("alt+["))
	keyViewportNextBlock   = key.NewBinding(key.WithKeys("alt+]"))
	keyViewportSelectAll   = key.NewBinding(key.WithKeys("alt+a"))
	keyViewportScrollUp    = key.NewBinding(key.WithKeys("ctrl+p"))
	keyViewportScrollDown  = key.NewBinding(key.WithKeys("ctrl+n"))
	keyViewportCopy        = key.NewBinding(key.WithKeys("alt+w"))

	keyInputPrevHistory = key.NewBinding(key.WithKeys("alt+p"))
	keyInputNextHistory = key.NewBinding(key.WithKeys("alt+n"))
)

func (m *Model) Update(msg tea.Msg) tea.Cmd {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.recalculateLayout()
		return nil

	case streamRenderMsg:
		wasAtBottom := m.viewport.AtBottom()
		m.viewport.SetContent(m.renderMessages())
		if wasAtBottom {
			m.viewport.GotoBottom()
		}
		return nil

	case streamDoneMsg:
		m.finalizeStream(msg)

		var toolCalls []*aipb.ToolCall
		for _, block := range ai.FilterBlocks(msg.Blocks, ai.BlockTypeToolCall) {
			toolCalls = append(toolCalls, block.GetToolCall())
		}

		if len(toolCalls) > 0 && msg.Err == nil {
			cmds = append(cmds, m.saveChat())
			m.promptToolCalls(toolCalls)
			return tea.Batch(cmds...)
		}

		if msg.Err == nil {
			cmds = append(cmds, m.saveChat())
		}
		return tea.Batch(cmds...)

	case chatSavedMsg:
		return nil

	case toolResultMsg:
		return m.handleToolResult(msg)

	case toolCancelledMsg:
		m.cancelToolCall()
		return nil

	case editorClosedMsg:
		switch m.focusedComponent {
		case FocusTextarea:
			if msg.Modified {
				m.textarea.SetValue(msg.Content)
				m.adjustTextareaHeight()
			}
		case FocusViewport:
			// Do nothing.
		}
		return nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return cmd

	case tea.KeyPressMsg:
		return m.handleKeyPress(msg)
	}

	if !m.streaming && !m.awaitingConfirm {
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		cmds = append(cmds, cmd)
		m.adjustTextareaHeight()
	}

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if m.streaming || m.awaitingConfirm {
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			cmds = append(cmds, cmd)
		}
	default:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		cmds = append(cmds, cmd)
	}

	return tea.Batch(cmds...)
}

func (m *Model) handleKeyPress(msg tea.KeyPressMsg) tea.Cmd {
	var cmds []tea.Cmd

	switch {
	case key.Matches(msg, keySessionCycleFocus):
		return m.cycleFocus()

	case key.Matches(msg, keySessionCycleReasoning):
		m.cycleReasoningEffort()
		return nil

	case key.Matches(msg, keySessionForkChat):
		return m.forkChat()
	}

	switch m.focusedComponent {
	case FocusTextarea:
		if cmd := m.handleTextareaKey(msg); cmd != nil {
			return cmd
		}
	case FocusViewport:
		return m.handleViewportKey(msg)
	}

	switch msg.String() {
	case "ctrl+c":
		if m.streaming {
			if m.cancelStream != nil {
				m.cancelStream()
			}
			return nil
		}
		return func() tea.Msg { return screen.CloseTabMsg{} }

	case "ctrl+j":
		if !m.streaming && !m.awaitingConfirm && m.textarea.Value() != "" {
			m.navigationMessageIndex = -1
			return m.sendUserMessage()
		}

	case "enter":
		if m.awaitingConfirm {
			return m.executeShellCommand()
		}
		if m.historyNavigating {
			m.inputHistory.Reset()
			m.historyNavigating = false
		}

	case "esc":
		if m.awaitingConfirm {
			m.cancelToolCall()
			return nil
		}
	}

	if m.awaitingConfirm {
		switch msg.String() {
		case "y", "Y":
			return m.executeShellCommand()
		case "n", "N":
			m.cancelToolCall()
			return nil
		}
		return nil
	}

	if !m.streaming && !m.awaitingConfirm && m.historyNavigating {
		m.inputHistory.Reset()
		m.historyNavigating = false
	}

	if !m.streaming && !m.awaitingConfirm {
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		cmds = append(cmds, cmd)
		m.adjustTextareaHeight()
	}

	return tea.Batch(cmds...)
}

func (m *Model) handleTextareaKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.streaming || m.awaitingConfirm {
		return nil
	}

	switch {
	case key.Matches(msg, keyInputPrevHistory):
		if entry, ok := m.inputHistory.Previous(m.textarea.Value()); ok {
			m.textarea.SetValue(entry)
			m.historyNavigating = true
			m.adjustTextareaHeight()
		}
		return nil

	case key.Matches(msg, keyInputNextHistory):
		if entry, ok := m.inputHistory.Next(); ok {
			m.textarea.SetValue(entry)
			m.historyNavigating = true
			m.adjustTextareaHeight()
		}
		return nil

	case key.Matches(msg, keyOpenEditor):
		return m.openInEditor(m.textarea.Value(), "md")
	}

	return nil
}

func (m *Model) handleViewportKey(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case key.Matches(msg, keyViewportToTop):
		if m.toTop() {
			m.viewport.SetContent(m.renderMessages())
			m.scrollToNavigatedBlock()
		}
		return nil

	case key.Matches(msg, keyViewportToBottom):
		if m.toBottom() {
			m.viewport.SetContent(m.renderMessages())
			m.scrollToNavigatedBlock()
		}
		return nil

	case key.Matches(msg, keyViewportPrevMessage):
		if m.toPreviousMessage() {
			m.viewport.SetContent(m.renderMessages())
			m.scrollToNavigatedMessage()
		}
		return nil

	case key.Matches(msg, keyViewportNextMessage):
		if m.toNextMessage() {
			m.viewport.SetContent(m.renderMessages())
			m.scrollToNavigatedMessage()
		}
		return nil

	case key.Matches(msg, keyViewportPrevBlock):
		if m.toPreviousBlock() {
			m.viewport.SetContent(m.renderMessages())
			m.scrollToNavigatedBlock()
		}
		return nil

	case key.Matches(msg, keyViewportNextBlock):
		if m.toNextBlock() {
			m.viewport.SetContent(m.renderMessages())
			m.scrollToNavigatedBlock()
		}
		return nil

	case key.Matches(msg, keyViewportSelectAll):
		if m.navigationMessageIndex != -1 {
			m.navigationBlockIndex = -1
			m.viewport.SetContent(m.renderMessages())
			m.scrollToNavigatedMessage()
		}
		return nil

	case key.Matches(msg, keyViewportScrollUp):
		m.viewport.ScrollUp(3)
		return nil

	case key.Matches(msg, keyViewportScrollDown):
		m.viewport.ScrollDown(3)
		return nil

	case key.Matches(msg, keyViewportCopy):
		if m.navigationMessageIndex != -1 {
			content, _ := m.getSelectedContent()
			send := m.send
			return func() tea.Msg {
				clipboard.Write(clipboard.FmtText, []byte(content))
				send(screen.AlertMsg{Text: "Copied to clipboard!"})
				return nil
			}
		}
		return nil

	case key.Matches(msg, keyOpenEditor):
		if m.navigationMessageIndex != -1 {
			content, ext := m.getSelectedContent()
			return m.openInEditor(content, ext)
		}
		return nil
	}

	switch msg.String() {
	case "ctrl+c":
		if m.streaming {
			if m.cancelStream != nil {
				m.cancelStream()
			}
			return nil
		}
		return func() tea.Msg { return screen.CloseTabMsg{} }
	case "esc":
		if m.awaitingConfirm {
			m.cancelToolCall()
			return nil
		}
	}

	return nil
}

func (m *Model) cycleFocus() tea.Cmd {
	switch m.focusedComponent {
	case FocusTextarea:
		m.focusedComponent = FocusViewport
		m.textarea.Blur()
		if m.navigationMessageIndex == -1 {
			m.toBottom()
		}
		m.viewport.SetContent(m.renderMessages())
		m.scrollToNavigatedMessage()
	case FocusViewport:
		m.focusedComponent = FocusTextarea
		m.viewport.SetContent(m.renderMessages())
		m.textarea.Focus()
		return textarea.Blink
	}
	return nil
}

func (m *Model) cycleReasoningEffort() {
	switch m.opts.ReasoningEffort {
	case aipb.ReasoningEffort_REASONING_EFFORT_UNSPECIFIED:
		m.opts.ReasoningEffort = aipb.ReasoningEffort_REASONING_EFFORT_LOW
	case aipb.ReasoningEffort_REASONING_EFFORT_LOW:
		m.opts.ReasoningEffort = aipb.ReasoningEffort_REASONING_EFFORT_MEDIUM
	case aipb.ReasoningEffort_REASONING_EFFORT_MEDIUM:
		m.opts.ReasoningEffort = aipb.ReasoningEffort_REASONING_EFFORT_HIGH
	case aipb.ReasoningEffort_REASONING_EFFORT_HIGH:
		m.opts.ReasoningEffort = aipb.ReasoningEffort_REASONING_EFFORT_UNSPECIFIED
	}
	m.setTitle()
}

// Compile-time interface check.
var _ screen.Screen = (*Model)(nil)
