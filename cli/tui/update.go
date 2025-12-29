package tui

import (
	"context"
	"fmt"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"
	"go.dalton.dog/bubbleup"
	"golang.design/x/clipboard"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/malonaz/sgpt/internal/types"
)

type KeyMapSession struct {
	CycleFocus           key.Binding
	CycleReasoningEffort key.Binding
}

type KeyMapViewport struct {
	ToTop             key.Binding
	ToBottom          key.Binding
	ToPreviousMessage key.Binding
	ToNextMessage     key.Binding
	ToPreviousBlock   key.Binding
	ToNextBlock       key.Binding
	ScrollUp          key.Binding
	ScrollDown        key.Binding
	OpenInEditor      key.Binding
	Copy              key.Binding
}

type InputKeyMap struct {
	PreviousHistoryEntry key.Binding
	NextHistoryEntry     key.Binding
}

var keyMapSession = KeyMapSession{
	CycleFocus: key.NewBinding(
		key.WithKeys("tab"),
	),

	CycleReasoningEffort: key.NewBinding(
		key.WithKeys("alt+t"),
	),
}

var keyMapViewport = KeyMapViewport{
	// Message navigation.
	ToTop: key.NewBinding(
		key.WithKeys("alt+<"),
	),
	ToBottom: key.NewBinding(
		key.WithKeys("alt+>"),
	),

	// Message navigation.
	ToPreviousMessage: key.NewBinding(
		key.WithKeys("alt+{"),
	),
	ToNextMessage: key.NewBinding(
		key.WithKeys("alt+}"),
	),

	// Block navigation.
	ToPreviousBlock: key.NewBinding(
		key.WithKeys("alt+["),
	),
	ToNextBlock: key.NewBinding(
		key.WithKeys("alt+]"),
	),

	// Scrolling.
	ScrollUp: key.NewBinding(
		key.WithKeys("ctrl+p"),
	),
	ScrollDown: key.NewBinding(
		key.WithKeys("ctrl+n"),
	),

	// Copy.
	Copy: key.NewBinding(
		key.WithKeys("alt+w"),
	),

	// Open in editor.
	OpenInEditor: key.NewBinding(
		key.WithKeys("ctrl+o"),
	),
}

var inputKeyMap = InputKeyMap{
	PreviousHistoryEntry: key.NewBinding(
		key.WithKeys("alt+p"),
	),

	NextHistoryEntry: key.NewBinding(
		key.WithKeys("alt+n"),
	),
}

// Update handles messages.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	// Always update the alert model with every message
	outAlert, alertCmd := m.alertClipboardWrite.Update(msg)
	m.alertClipboardWrite = outAlert.(bubbleup.AlertModel)
	if alertCmd != nil {
		cmds = append(cmds, alertCmd)
	}

	// Log for non-tick messages only
	defer func() {
		switch msg.(type) {
		case spinner.TickMsg, cursor.BlinkMsg, tea.MouseMsg:
		// Skip logging for spinner ticks
		default:
			if false {
				log.Info("update completed", "msg_type", fmt.Sprintf("%T", msg), "navigation_index", m.navigationMessageIndex)
			}
		}
	}()

	switch msg := msg.(type) {
	case tea.FocusMsg:
		m.windowFocused = true
		if m.focusedComponent == FocusTextarea {
			m.textarea.Focus()
		}
		cmds = append(cmds, textarea.Blink)
		return m, tea.Batch(cmds...)

	case tea.BlurMsg:
		m.windowFocused = false
		m.textarea.Blur()
		return m, nil

	case tea.KeyMsg:
		switch {
		case key.Matches(msg, keyMapSession.CycleFocus):
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
				cmds = append(cmds, textarea.Blink)
			}
			return m, tea.Batch(cmds...)

		case key.Matches(msg, keyMapSession.CycleReasoningEffort):
			// Cycle reasoning efforts.
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
			return m, nil

		default:
			switch m.focusedComponent {
			case FocusTextarea:
				km := inputKeyMap
				awaitingInput := m.streaming || m.awaitingConfirm

				switch {
				case key.Matches(msg, km.PreviousHistoryEntry):
					if awaitingInput {
						break
					}
					if entry, ok := m.history.Previous(m.textarea.Value()); ok {
						m.textarea.SetValue(entry)
						m.historyNavigating = true
						m.adjustTextareaHeight()
					}
					return m, nil
				case key.Matches(msg, km.NextHistoryEntry):
					if awaitingInput {
						break
					}
					if entry, ok := m.history.Next(); ok {
						m.textarea.SetValue(entry)
						m.historyNavigating = true
						m.adjustTextareaHeight()
					}
					return m, nil
				}

			case FocusViewport:
				km := keyMapViewport
				switch {
				case key.Matches(msg, km.ToTop):
					if m.toTop() {
						m.viewport.SetContent(m.renderMessages())
						m.scrollToNavigatedBlock()
					}
					return m, nil

				case key.Matches(msg, km.ToBottom):
					if m.toBottom() {
						m.viewport.SetContent(m.renderMessages())
						m.scrollToNavigatedBlock()
					}
					return m, nil

				case key.Matches(msg, km.ToPreviousMessage):
					if m.toPreviousMessage() {
						m.viewport.SetContent(m.renderMessages())
						m.scrollToNavigatedMessage()
					}
					return m, nil

				case key.Matches(msg, km.ToNextMessage):
					if m.toNextMessage() {
						m.viewport.SetContent(m.renderMessages())
						m.scrollToNavigatedMessage()
					}
					return m, nil

				case key.Matches(msg, km.ToPreviousBlock):
					if m.toPreviousBlock() {
						m.viewport.SetContent(m.renderMessages())
						m.scrollToNavigatedBlock()
					}
					return m, nil

				case key.Matches(msg, km.ToNextBlock):
					if m.toNextBlock() {
						m.viewport.SetContent(m.renderMessages())
						m.scrollToNavigatedBlock()
					}
					return m, nil

				case key.Matches(msg, km.ScrollUp):
					m.viewport.LineUp(3)
					return m, nil

				case key.Matches(msg, km.ScrollDown):
					m.viewport.LineDown(3)
					return m, nil

				case key.Matches(msg, km.OpenInEditor):
					if m.navigationMessageIndex != -1 {
						content, ext := m.getSelectedContent()
						return m, m.openInEditor(content, ext)
					}

				case key.Matches(msg, km.Copy):
					if m.navigationMessageIndex != -1 {
						content, _ := m.getSelectedContent()
						clipboard.Write(clipboard.FmtText, []byte(content))
						cmds = append(cmds, m.alertClipboardWrite.NewAlertCmd(bubbleup.InfoKey, "Copied to clipboard!"))
						return m, tea.Batch(cmds...)
					}
				}
			}
		}

		switch msg.Type {
		case tea.KeyCtrlC:
			if m.streaming {
				if m.cancelStream != nil {
					m.cancelStream()
				}
				return m, nil // Wait for StreamDoneMsg
			}
			m.quitting = true
			return m, tea.Quit

		case tea.KeyCtrlJ:
			if !m.streaming && !m.awaitingConfirm && m.textarea.Value() != "" {
				m.navigationMessageIndex = -1 // Reset the navigation on send.
				return m, m.sendMessage()
			}

		case tea.KeyEnter:
			if m.awaitingConfirm {
				return m, m.executeToolCall()
			}
			if m.historyNavigating {
				m.history.Reset()
				m.historyNavigating = false
			}

		case tea.KeyEsc:
			if m.awaitingConfirm {
				m.awaitingConfirm = false
				m.pendingToolCall = nil
				m.pendingToolArgs = nil
				return m, func() tea.Msg { return types.ToolCancelledMsg{} }
			}
		}

		if m.awaitingConfirm {
			switch msg.String() {
			case "y", "Y":
				return m, m.executeToolCall()
			case "n", "N":
				m.awaitingConfirm = false
				m.pendingToolCall = nil
				m.pendingToolArgs = nil
				return m, func() tea.Msg { return types.ToolCancelledMsg{} }
			}
			return m, nil
		}

		if !m.streaming && !m.awaitingConfirm && m.historyNavigating {
			switch msg.Type {
			case tea.KeyRunes, tea.KeyBackspace, tea.KeyDelete:
				m.history.Reset()
				m.historyNavigating = false
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.recalculateLayout()

	case types.StreamRenderMsg:
		wasAtBottom := m.viewport.AtBottom()
		m.viewport.SetContent(m.renderMessages())
		if wasAtBottom {
			m.viewport.GotoBottom()
		}
		return m, tea.Batch(cmds...)

	case types.StreamDoneMsg:
		m.streaming = false
		m.cancelStream = nil

		if msg.Err != nil && msg.Err != context.Canceled {
			if status.Code(msg.Err) != codes.Canceled {
				m.err = msg.Err
			}
		}

		m.finalizeResponse(msg)

		if len(msg.ToolCalls) > 0 && msg.Err == nil {
			cmds = append(cmds, m.saveChat())
			cmds = append(cmds, m.promptToolCall(msg.ToolCalls)...)
			return m, tea.Batch(cmds...)
		}

		if msg.Err == nil {
			return m, m.saveChat()
		}
		return m, tea.Batch(cmds...)

	case types.StreamErrorMsg:
		m.err = msg.Err
		return m, nil

	case types.ChatSavedMsg:
		return m, nil

	case types.ToolResultMsg:
		if msg.Result != "" && m.pendingToolCall != nil {
			toolMessage := ai.NewToolResultMessage(&aipb.ToolResultMessage{
				ToolCallId: m.pendingToolCall.Id,
				Result:     ai.NewToolResult(msg.Result),
			})
			m.runtimeMessages = append(m.runtimeMessages, types.NewToolResultMessage(m.pendingToolCall.Id, msg.Result))
			m.chat.Messages = append(m.chat.Messages, toolMessage)
		}

		m.pendingToolCall = nil
		m.pendingToolArgs = nil
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()

		if msg.Result != "" {
			return m, m.continueWithToolResult()
		}
		return m, nil

	case types.ToolCancelledMsg:
		m.runtimeMessages = append(m.runtimeMessages, types.NewToolResultMessage("", "[Tool execution cancelled by user]").WithError(errUserInterrupt))
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)
	}

	if !m.streaming && !m.awaitingConfirm {
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		cmds = append(cmds, cmd)
		m.adjustTextareaHeight()
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.streaming || m.awaitingConfirm {
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			cmds = append(cmds, cmd)
		} else {
			switch msg.String() {
			case "j", "k", "g", "G", "u", "d", "b", "ctrl+u", "ctrl+d", "f", " ":
				// Don't pass vim navigation keys to viewport while typing
			default:
				var cmd tea.Cmd
				m.viewport, cmd = m.viewport.Update(msg)
				cmds = append(cmds, cmd)
			}
		}
	default:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m *Model) filter(model tea.Model, msg tea.Msg) tea.Msg {
	return msg
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		log.Info("keymsg", "val", keyMsg)
	}
	return msg
}

// Filter returns the filter function for the tea.Program.
func (m *Model) Filter() func(tea.Model, tea.Msg) tea.Msg {
	return m.filter
}
