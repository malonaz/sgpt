package tui

import (
	"context"
	"fmt"

	"charm.land/bubbles/v2/cursor"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"
	"golang.design/x/clipboard"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	chatpb "github.com/malonaz/sgpt/genproto/chat/v1"
	"github.com/malonaz/sgpt/internal/types"
)

// KeyMapSession defines key bindings available at the session level regardless
// of which component is focused (e.g., cycling focus between textarea and viewport).
type KeyMapSession struct {
	CycleFocus           key.Binding
	CycleReasoningEffort key.Binding
}

// KeyMapViewport defines key bindings available when the viewport is focused,
// covering message/block navigation, scrolling, copying, and editor launch.
type KeyMapViewport struct {
	ToTop             key.Binding
	ToBottom          key.Binding
	ToPreviousMessage key.Binding
	ToNextMessage     key.Binding
	ToPreviousBlock   key.Binding
	ToNextBlock       key.Binding
	SelectAllBlocks   key.Binding
	ScrollUp          key.Binding
	ScrollDown        key.Binding
	OpenInEditor      key.Binding
	Copy              key.Binding
}

// InputKeyMap defines key bindings for navigating input history when the textarea
// is focused.
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
	ToTop: key.NewBinding(
		key.WithKeys("alt+<"),
	),
	ToBottom: key.NewBinding(
		key.WithKeys("alt+>"),
	),

	ToPreviousMessage: key.NewBinding(
		key.WithKeys("alt+{"),
	),
	ToNextMessage: key.NewBinding(
		key.WithKeys("alt+}"),
	),

	ToPreviousBlock: key.NewBinding(
		key.WithKeys("alt+["),
	),
	ToNextBlock: key.NewBinding(
		key.WithKeys("alt+]"),
	),
	SelectAllBlocks: key.NewBinding(
		key.WithKeys("alt+a"),
	),

	ScrollUp: key.NewBinding(
		key.WithKeys("ctrl+p"),
	),
	ScrollDown: key.NewBinding(
		key.WithKeys("ctrl+n"),
	),

	Copy: key.NewBinding(
		key.WithKeys("alt+w"),
	),

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

// Update is the main Bubble Tea update function. It routes incoming messages to the
// appropriate handler based on message type (window events, key presses, streaming
// events, tool results, etc.) and the currently focused component.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	// Log non-tick messages for debugging (guarded by a false flag to avoid noise).
	defer func() {
		switch msg.(type) {
		case spinner.TickMsg, cursor.BlinkMsg, tea.MouseMsg:
		default:
			if false {
				log.Info("update completed", "msg_type", fmt.Sprintf("%T", msg), "navigation_index", m.navigationMessageIndex)
			}
		}
	}()

	switch msg := msg.(type) {
	case alertDismissMsg:
		// Timer expired — hide the alert overlay.
		m.alert.visible = false
		return m, nil

	case tea.FocusMsg:
		// Terminal window gained focus — re-enable textarea blinking if it's the
		// active component.
		m.windowFocused = true
		if m.focusedComponent == FocusTextarea {
			m.textarea.Focus()
		}
		cmds = append(cmds, textarea.Blink)
		return m, tea.Batch(cmds...)

	case tea.BlurMsg:
		// Terminal window lost focus — blur the textarea to stop cursor blinking.
		m.windowFocused = false
		m.textarea.Blur()
		return m, nil

	case tea.KeyPressMsg:
		// Handle session-level key bindings first (available regardless of focus).
		switch {
		case key.Matches(msg, keyMapSession.CycleFocus):
			// Toggle focus between textarea and viewport. When entering viewport
			// mode, initialize navigation at the bottom if not already navigating.
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
			// Cycle through reasoning effort levels: unspecified → low → medium → high → unspecified.
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
			// Delegate to the focused component's key bindings.
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

				case key.Matches(msg, km.SelectAllBlocks):
					// Enter "select all" mode: blockIndex = -1 means the entire
					// message is selected rather than a single block.
					if m.navigationMessageIndex != -1 {
						m.navigationBlockIndex = -1
						m.viewport.SetContent(m.renderMessages())
						m.scrollToNavigatedMessage()
					}
					return m, nil

				case key.Matches(msg, km.ScrollUp):
					m.viewport.ScrollUp(3)
					return m, nil

				case key.Matches(msg, km.ScrollDown):
					m.viewport.ScrollDown(3)
					return m, nil

				case key.Matches(msg, km.OpenInEditor):
					if m.navigationMessageIndex != -1 {
						content, ext := m.getSelectedContent()
						return m, m.openInEditor(content, ext)
					}

				case key.Matches(msg, km.Copy):
					// Copy the selected message or block content to the system clipboard.
					if m.navigationMessageIndex != -1 {
						content, _ := m.getSelectedContent()
						clipboard.Write(clipboard.FmtText, []byte(content))
						cmds = append(cmds, m.showAlert("Copied to clipboard!"))
						return m, tea.Batch(cmds...)
					}
				}
			}
		}

		// Handle key presses by string representation for keys that don't use
		// key.Binding (ctrl+c, ctrl+j, enter, escape).
		switch msg.String() {
		case "ctrl+c":
			// During streaming: cancel the in-flight request and wait for StreamDoneMsg.
			// Otherwise: quit the application.
			if m.streaming {
				if m.cancelStream != nil {
					m.cancelStream()
				}
				return m, nil
			}
			m.quitting = true
			return m, tea.Quit

		case "ctrl+j":
			// Submit the textarea content as a new user message.
			if !m.streaming && !m.awaitingConfirm && m.textarea.Value() != "" {
				m.navigationMessageIndex = -1
				return m, m.sendMessage()
			}

		case "enter":
			// Confirm a pending tool call, or reset history navigation state.
			if m.awaitingConfirm {
				return m, m.executeToolCall()
			}
			if m.historyNavigating {
				m.history.Reset()
				m.historyNavigating = false
			}

		case "escape":
			// Cancel a pending tool confirmation dialog.
			if m.awaitingConfirm {
				m.awaitingConfirm = false
				m.pendingToolCall = nil
				m.pendingToolArgs = nil
				return m, func() tea.Msg { return types.ToolCancelledMsg{} }
			}
		}

		// Handle tool confirmation y/n shortcuts.
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

		// When navigating history and the user starts typing, exit history mode
		// so the new keystrokes modify the textarea normally.
		if !m.streaming && !m.awaitingConfirm && m.historyNavigating {
			m.history.Reset()
			m.historyNavigating = false
		}

	case tea.WindowSizeMsg:
		// Terminal was resized — recompute all layout dimensions.
		m.width = msg.Width
		m.height = msg.Height
		m.recalculateLayout()

	case types.StreamRenderMsg:
		// Throttled render tick from the streaming goroutine — update viewport
		// content and auto-scroll if the user was already at the bottom.
		wasAtBottom := m.viewport.AtBottom()
		m.viewport.SetContent(m.renderMessages())
		if wasAtBottom {
			m.viewport.GotoBottom()
		}
		return m, tea.Batch(cmds...)

	case types.StreamDoneMsg:
		// Streaming finished (success or error). Transition out of streaming state,
		// finalize the response into chat history, and handle tool calls if present.
		m.streaming = false
		m.cancelStream = nil
		if msg.Err != nil && msg.Err != context.Canceled {
			if status.Code(msg.Err) != codes.Canceled {
				m.err = msg.Err
			}
		}

		m.finalizeResponse(msg)

		var toolCalls []*aipb.ToolCall
		for _, block := range ai.FilterBlocks(msg.Blocks, ai.BlockTypeToolCall) {
			toolCalls = append(toolCalls, block.GetToolCall())
		}
		if len(toolCalls) > 0 && msg.Err == nil {
			cmds = append(cmds, m.saveChat())
			cmds = append(cmds, m.promptToolCall(toolCalls)...)
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
		// A tool has produced a result. Add it to both the runtime display messages
		// and the persisted chat metadata, then continue streaming if successful.
		output, err := ai.ParseToolResult(msg.ToolResult)
		if err != nil {
			panic(err)
		}
		m.runtimeMessages = append(m.runtimeMessages, types.NewToolResultMessage(m.pendingToolCall.Id, output))

		toolMessage := ai.NewToolMessage(ai.NewToolResultBlock(msg.ToolResult))
		m.chat.Metadata.Messages = append(m.chat.Metadata.Messages, &chatpb.Message{Message: toolMessage})

		m.pendingToolCall = nil
		m.pendingToolArgs = nil
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()

		if msg.ToolResult.GetError() == nil {
			return m, m.continueWithToolResult()
		}
		return m, nil

	case types.ToolCancelledMsg:
		// User declined the tool call — record the cancellation in the message stream.
		m.runtimeMessages = append(m.runtimeMessages, types.NewToolResultMessage("", "[Tool execution cancelled by user]").WithError(errUserInterrupt))
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)
	}

	// Forward messages to the textarea when not blocked by streaming/confirmation.
	if !m.streaming && !m.awaitingConfirm {
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		cmds = append(cmds, cmd)
		m.adjustTextareaHeight()
	}

	// Forward messages to the viewport. During streaming/confirmation, pass all key
	// presses. Otherwise, filter out vim-style navigation keys that would conflict
	// with textarea input.
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if m.streaming || m.awaitingConfirm {
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			cmds = append(cmds, cmd)
		} else {
			switch msg.String() {
			case "j", "k", "g", "G", "u", "d", "b", "ctrl+u", "ctrl+d", "f", "space":
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

// filter is a no-op message filter attached to the tea.Program. It can be used
// to intercept or transform messages before they reach Update.
func (m *Model) filter(model tea.Model, msg tea.Msg) tea.Msg {
	return msg
}

// Filter returns the filter function for the tea.Program.
func (m *Model) Filter() func(tea.Model, tea.Msg) tea.Msg {
	return m.filter
}
