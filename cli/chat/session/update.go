package session

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"go.dalton.dog/bubbleup"
	"golang.design/x/clipboard"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/malonaz/sgpt/cli/chat/types"
	"github.com/malonaz/sgpt/cli/chat/viewer"
)

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
			log.Info("update completed", "msg_type", fmt.Sprintf("%T", msg), "navigation_index", m.navigationMessageIndex)
		}
	}()

	switch msg := msg.(type) {
	case viewer.ExitMsg:
		m.viewerMode = false
		m.viewerModel = nil
		m.textarea.Focus()
		m.viewport.GotoBottom()
		return m, tea.Batch(textarea.Blink, tea.EnableMouseCellMotion)

	case tea.FocusMsg:
		m.windowFocused = true
		m.textarea.Focus()
		cmds = append(cmds, textarea.Blink)
		return m, tea.Batch(cmds...)

	case tea.BlurMsg:
		m.windowFocused = false
		m.textarea.Blur()
		return m, nil

	case tea.KeyMsg:
		if m.viewerMode {
			var cmd tea.Cmd
			m.viewerModel, cmd = m.viewerModel.Update(msg)
			return m, cmd
		}

		// Handle navigation commands.
		if msg.String() == "alt+{" {
			if m.navigationMessageIndex == -1 {
				m.navigationMessageIndex = len(m.runtimeMessages)
			}
			if m.navigationMessageIndex > 0 {
				m.navigationMessageIndex-- // Go up one message.
				m.viewport.SetContent(m.renderMessages())
				m.scrollToNavigatedMessage()
			}
			return m, nil
		}
		if msg.String() == "alt+}" {
			if m.navigationMessageIndex != -1 {
				m.navigationMessageIndex++ // Go to next message.
				if m.navigationMessageIndex == len(m.runtimeMessages) {
					m.navigationMessageIndex = -1
					m.viewport.GotoBottom()
				}
				m.viewport.SetContent(m.renderMessages())
				if m.navigationMessageIndex != -1 {
					m.scrollToNavigatedMessage()
				}
			}
			return m, nil
		}

		// Copy navigated message content to clipboard
		if msg.String() == "alt+w" && m.navigationMessageIndex != -1 {
			content := m.runtimeMessages[m.navigationMessageIndex].Message.Content
			clipboard.Write(clipboard.FmtText, []byte(content))
			cmds = append(cmds, m.alertClipboardWrite.NewAlertCmd(bubbleup.InfoKey, "Copied to clipboard!"))
			return m, tea.Batch(cmds...)
		}

		if msg.String() == "alt+v" && !m.streaming && !m.awaitingConfirm && len(m.runtimeMessages) > 0 {
			m.viewerMode = true
			m.viewerModel = viewer.New(m.runtimeMessages, m.renderer, m.width, m.height)
			return m, m.viewerModel.Init()
		}

		if msg.Alt && !m.streaming && !m.awaitingConfirm {
			switch msg.String() {
			case "alt+p":
				if entry, ok := m.history.Previous(m.textarea.Value()); ok {
					m.textarea.SetValue(entry)
					m.historyNavigating = true
					m.adjustTextareaHeight()
					return m, nil
				}
			case "alt+n":
				if entry, ok := m.history.Next(); ok {
					m.textarea.SetValue(entry)
					m.historyNavigating = true
					m.adjustTextareaHeight()
					return m, nil
				}
			}
		}

		switch msg.Type {
		case tea.KeyCtrlC:
			if m.streaming {
				if m.cancelStream != nil {
					m.cancelStream()
				}
				m.streaming = false
				m.finalizeResponse(errUserInterrupt)
				return m, m.saveChat()
			}
			m.quitting = true
			return m, tea.Quit

		case tea.KeyCtrlJ:
			if !m.streaming && !m.awaitingConfirm && strings.TrimSpace(m.textarea.Value()) != "" {
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
		if m.viewerMode && m.viewerModel != nil {
			m.viewerModel, _ = m.viewerModel.Update(msg)
		}
		m.recalculateLayout()

	case *aiservicepb.TextToTextStreamResponse:
		switch content := msg.Content.(type) {
		case *aiservicepb.TextToTextStreamResponse_ContentChunk:
			m.currentResponse.WriteString(content.ContentChunk)
		case *aiservicepb.TextToTextStreamResponse_ReasoningChunk:
			m.currentReasoning.WriteString(content.ReasoningChunk)
		case *aiservicepb.TextToTextStreamResponse_ToolCall:
			m.currentToolCalls = append(m.currentToolCalls, content.ToolCall)
		}
		wasAtBottom := m.viewport.AtBottom()
		m.viewport.SetContent(m.renderMessages())
		if wasAtBottom {
			m.viewport.GotoBottom()
		}
		return m, nil

	case types.StreamErrorMsg:
		if errors.Is(msg.Err, io.EOF) {
			m.streaming = false
			m.cancelStream = nil
			m.finalizeResponse(nil)

			if len(m.currentToolCalls) > 0 {
				cmds = append(cmds, m.saveChat())
				cmds = append(cmds, m.promptToolCall())
				return m, tea.Batch(cmds...)
			}
			return m, m.saveChat()
		}

		m.streaming = false
		m.cancelStream = nil
		if msg.Err != nil && status.Code(msg.Err) != codes.Canceled {
			m.err = msg.Err
		}
		m.finalizeResponse(msg.Err)
		return m, nil

	case types.ChatSavedMsg:
		return m, nil

	case types.ToolResultMsg:
		if msg.Result != "" && m.pendingToolCall != nil {
			toolMessage := &aipb.Message{
				Role:       aipb.Role_ROLE_TOOL,
				Content:    msg.Result,
				ToolCallId: m.pendingToolCall.Id,
			}
			m.addRuntimeMessage(toolMessage)
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
		m.runtimeMessages = append(m.runtimeMessages, &types.RuntimeMessage{
			Message: &aipb.Message{
				Role:    aipb.Role_ROLE_TOOL,
				Content: "[Tool execution cancelled by user]",
			},
			Err: errUserInterrupt,
		})
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
}

// Filter returns the filter function for the tea.Program.
func (m *Model) Filter() func(tea.Model, tea.Msg) tea.Msg {
	return m.filter
}
