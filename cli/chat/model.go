package chat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/malonaz/sgpt/internal/configuration"
	"github.com/malonaz/sgpt/internal/history"
	"github.com/malonaz/sgpt/store"
)

// Layout constants
const (
	minTextareaHeight    = 3
	maxTextareaHeight    = 20
	defaultTextareaWidth = 80
	minViewportHeight    = 1
	truncateLength       = 100
	truncateSuffix       = "..."
	truncateSuffixLength = 3
)

var (
	errUserInterrupt = errors.New("user interrupt")
)

type MessageState int

// ChatOptions holds the options for the chat
type ChatOptions struct {
	Model           string
	Role            *configuration.Role
	MaxTokens       int32
	Temperature     float64
	ReasoningEffort aipb.ReasoningEffort
	EnableTools     bool
	ChatID          string
}

// runtimeMessage wraps a message with optional error state
type runtimeMessage struct {
	message *aipb.Message
	err     error // non-nil if this message had an error during generation
	blocks  []any
}

// Model represents the Bubble Tea model for the chat
type Model struct {
	// Core dependencies
	ctx      context.Context
	config   *configuration.Config
	store    *store.Store
	aiClient aiservicepb.AiClient

	// Chat state
	chat               *store.Chat
	opts               ChatOptions
	additionalMessages []*aipb.Message
	injectedFiles      []string

	// Runtime messages (includes errored messages for display)
	runtimeMessages []*runtimeMessage

	// UI components
	textarea textarea.Model
	viewport viewport.Model
	spinner  spinner.Model

	// Renderer.
	renderer *renderer

	// UI state
	width            int
	height           int
	ready            bool
	streaming        bool
	currentResponse  strings.Builder
	currentReasoning strings.Builder
	currentToolCalls []*aipb.ToolCall
	err              error
	quitting         bool
	windowFocused    bool

	// Tool confirmation state
	pendingToolCall *aipb.ToolCall
	pendingToolArgs *ShellCommandArgs
	awaitingConfirm bool

	// Stream control
	cancelStream context.CancelFunc

	// Program reference for sending messages from goroutines
	program   *tea.Program
	programMu sync.Mutex

	// Input history
	history           *history.History
	historyNavigating bool

	// Pending user message (not yet persisted, waiting for successful response)
	pendingUserMessage *aipb.Message
}

// Message types for Bubble Tea
type (
	streamErrorMsg   struct{ err error }
	chatSavedMsg     struct{}
	toolResultMsg    struct{ result string }
	toolCancelledMsg struct{}
)

// NewModel creates a new chat model
func NewModel(
	ctx context.Context,
	config *configuration.Config,
	s *store.Store,
	aiClient aiservicepb.AiClient,
	chat *store.Chat,
	opts ChatOptions,
	additionalMessages []*aipb.Message,
	injectedFiles []string,
) (*Model, error) {
	// Create textarea for input
	ta := textarea.New()
	ta.Placeholder = "Type your message... (Ctrl+J to send, Alt+P/N for history, Ctrl+C to quit)"
	ta.Focus()
	ta.CharLimit = 0 // No limit
	ta.SetWidth(defaultTextareaWidth)
	ta.SetHeight(minTextareaHeight)
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetEnabled(true)
	ta.Prompt = ""

	// Create spinner
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = spinnerStyle

	// Initialize runtime messages from existing chat messages
	runtimeMsgs := make([]*runtimeMessage, len(chat.Messages))
	for i, msg := range chat.Messages {
		runtimeMsgs[i] = &runtimeMessage{message: msg}
	}

	renderer, err := newRenderer(defaultTextareaWidth)
	if err != nil {
		return nil, err
	}

	return &Model{
		ctx:                ctx,
		config:             config,
		store:              s,
		aiClient:           aiClient,
		chat:               chat,
		opts:               opts,
		windowFocused:      true,
		additionalMessages: additionalMessages,
		injectedFiles:      injectedFiles,
		textarea:           ta,
		spinner:            sp,
		history:            history.NewHistory(),
		runtimeMessages:    runtimeMsgs,
		renderer:           renderer,
	}, nil
}

func (m *Model) filter(model tea.Model, msg tea.Msg) tea.Msg {
	return msg
	/*
		keyMsg, ok := msg.(tea.KeyMsg)
		if ok {
			log.Printf("t=%v runes=%v alt=%v paste=%v\n", keyMsg.Type, keyMsg.Runes, keyMsg.Alt, keyMsg.Paste)
		}*/
}

// SetProgram sets the tea.Program reference for async message sending
func (m *Model) SetProgram(p *tea.Program) {
	m.programMu.Lock()
	defer m.programMu.Unlock()
	m.program = p
}

// getProgram safely gets the program reference
func (m *Model) getProgram() *tea.Program {
	m.programMu.Lock()
	defer m.programMu.Unlock()
	return m.program
}

// Init initializes the model
func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		m.spinner.Tick,
	)
}

// Update handles messages
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
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
		// Handle history navigation (Alt)
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

		// Handle key presses
		switch msg.Type {
		case tea.KeyCtrlC:
			if m.streaming {
				// Cancel streaming
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
			// Send message
			if !m.streaming && !m.awaitingConfirm && strings.TrimSpace(m.textarea.Value()) != "" {
				return m, m.sendMessage()
			}

		case tea.KeyEnter:
			if m.awaitingConfirm {
				// Confirm tool execution
				return m, m.executeToolCall()
			}
			// Reset history navigation on Enter (new line in textarea)
			if m.historyNavigating {
				m.history.Reset()
				m.historyNavigating = false
			}

		case tea.KeyEsc:
			if m.awaitingConfirm {
				// Cancel tool execution
				m.awaitingConfirm = false
				m.pendingToolCall = nil
				m.pendingToolArgs = nil
				return m, func() tea.Msg { return toolCancelledMsg{} }
			}
		}

		// Handle 'y' or 'n' for confirmation
		if m.awaitingConfirm {
			switch msg.String() {
			case "y", "Y":
				return m, m.executeToolCall()
			case "n", "N":
				m.awaitingConfirm = false
				m.pendingToolCall = nil
				m.pendingToolArgs = nil
				return m, func() tea.Msg { return toolCancelledMsg{} }
			}
			return m, nil
		}

		// Reset history navigation on any other key press that modifies input
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

	case streamErrorMsg:
		if errors.Is(msg.err, io.EOF) {
			m.streaming = false
			m.cancelStream = nil
			m.finalizeResponse(nil)

			// Check for tool calls first
			if len(m.currentToolCalls) > 0 {
				cmds = append(cmds, m.saveChat())
				cmds = append(cmds, m.promptToolCall())
				return m, tea.Batch(cmds...)
			}
			return m, m.saveChat()
		}

		m.streaming = false
		m.cancelStream = nil
		if msg.err != nil && status.Code(msg.err) != codes.Canceled {
			m.err = msg.err
		}
		m.finalizeResponse(msg.err)
		return m, nil

	case chatSavedMsg:
		// Chat saved successfully
		return m, nil

	case toolResultMsg:
		// Add tool result to messages
		if msg.result != "" && m.pendingToolCall != nil {
			toolMessage := &aipb.Message{
				Role:       aipb.Role_ROLE_TOOL,
				Content:    msg.result,
				ToolCallId: m.pendingToolCall.Id,
			}
			m.addRuntimeMessage(toolMessage)
			m.chat.Messages = append(m.chat.Messages, toolMessage)
		}

		m.pendingToolCall = nil
		m.pendingToolArgs = nil
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()

		// Continue conversation with tool result
		if msg.result != "" {
			return m, m.continueWithToolResult()
		}
		return m, nil

	case toolCancelledMsg:
		// Add cancellation message to runtime messages only (not persisted)
		m.runtimeMessages = append(m.runtimeMessages, &runtimeMessage{
			message: &aipb.Message{
				Role:    aipb.Role_ROLE_TOOL,
				Content: "[Tool execution cancelled by user]",
			},
			err: errUserInterrupt,
		})
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)
	}

	// Update textarea if not streaming and not awaiting confirmation
	if !m.streaming && !m.awaitingConfirm {
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		cmds = append(cmds, cmd)
		m.adjustTextareaHeight()
	}

	// Update viewport - but don't pass key messages when textarea is focused
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.streaming || m.awaitingConfirm {
			// When not inputting, viewport handles all keys
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			cmds = append(cmds, cmd)
		} else {
			// When inputting, block only keys that conflict with typing
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

// View renders the model
func (m *Model) View() string {
	if m.quitting {
		return ""
	}

	if !m.ready {
		return "Initializing..."
	}

	var b strings.Builder

	// Title bar
	b.WriteString(m.renderTitle())
	b.WriteString("\n")

	// Viewport with messages
	b.WriteString(viewportStyle.Render(m.viewport.View()))
	b.WriteString("\n")

	// Tool confirmation dialog
	if m.awaitingConfirm && m.pendingToolArgs != nil {
		b.WriteString(m.renderConfirmDialog())
		b.WriteString("\n")
	} else {
		// Input area
		if m.streaming {
			b.WriteString(fmt.Sprintf("%s Generating...\n", m.spinner.View()))
		} else {
			b.WriteString(textAreaStyle.Render(m.textarea.View()))
			b.WriteString("\n")
		}
	}

	// Help text
	if m.awaitingConfirm {
		b.WriteString(helpStyle.Render("Press Y to confirm, N or Esc to cancel"))
	}
	// Error display
	if m.err != nil {
		b.WriteString("\n")
		b.WriteString(errorStyle.Render(fmt.Sprintf("Error: %v", m.err)))
	}

	return b.String()
}

func (m *Model) renderTitle() string {
	roleName := "anon"
	if m.opts.Role != nil {
		roleName = m.opts.Role.Name
	}

	reasoningStr := "none"
	switch m.opts.ReasoningEffort {
	case aipb.ReasoningEffort_REASONING_EFFORT_LOW:
		reasoningStr = "low"
	case aipb.ReasoningEffort_REASONING_EFFORT_MEDIUM:
		reasoningStr = "medium"
	case aipb.ReasoningEffort_REASONING_EFFORT_HIGH:
		reasoningStr = "high"
	}

	toolsStr := ""
	if m.opts.EnableTools {
		toolsStr = " 🔧"
	}

	title := fmt.Sprintf(" 🤖 %s │ 👤 %s │ 💬 %s │ 🧠 %s%s ",
		m.opts.Model, roleName, m.opts.ChatID, reasoningStr, toolsStr)

	return titleStyle.Width(m.width).Render(title)
}

func (m *Model) renderMessages() string {
	var b strings.Builder

	// Calculate content width using style's actual padding values
	contentWidth := m.viewport.Width

	// Show injected files at the top of scrollable content
	if len(m.injectedFiles) > 0 {
		for i, f := range m.injectedFiles {
			b.WriteString(fileStyle.Width(contentWidth).Render(fmt.Sprintf("📎 File #%d: %s", i+1, f)))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// Calculate widths accounting for each style's padding

	for i, rm := range m.runtimeMessages {
		if i > 0 {
			b.WriteString("\n\n")
		}
		msg := rm.message
		switch msg.Role {
		case aipb.Role_ROLE_USER:
			// Render user message with markdown
			rendered := m.renderer.toMarkdown(msg.Content, i, true)
			b.WriteString(userMessageStyle.Render(rendered))

		case aipb.Role_ROLE_ASSISTANT:
			if msg.Reasoning != "" {
				b.WriteString(thoughtLabelStyle.Render("💭 Thinking:"))
				b.WriteString("\n")
				b.WriteString(thoughtStyle.Render(msg.Reasoning))
			}
			// Render assistant message with markdown/syntax highlighting
			rendered := m.renderer.toMarkdown(msg.Content, i, true)
			b.WriteString(aiMessageStyle.Render(rendered))

			if len(msg.ToolCalls) > 0 {
				for _, tc := range msg.ToolCalls {
					b.WriteString("\n")
					b.WriteString(toolLabelStyle.Render(fmt.Sprintf("🔧 Tool: %s", tc.Name)))
					b.WriteString("\n")
					b.WriteString(toolCallStyle.Render(tc.Arguments))
				}
			}
			// Display error if this message has one
			if rm.err != nil {
				b.WriteString("\n")
				if errors.Is(rm.err, errUserInterrupt) {
					b.WriteString(messageInterruptStyle.Render("⚡ Interrupted by user"))
				} else {
					b.WriteString(messageErrorStyle.Render(fmt.Sprintf("⚠️ %v", rm.err)))
				}
			}

		case aipb.Role_ROLE_TOOL:
			b.WriteString(toolLabelStyle.Render("⚡ Tool Result:"))
			b.WriteString("\n")
			// Render tool result - might contain code output
			rendered := m.renderer.toMarkdown(msg.Content, i, true)
			b.WriteString(toolResultStyle.Render(rendered))

		case aipb.Role_ROLE_SYSTEM:
			b.WriteString(systemStyle.Render(fmt.Sprintf("System: %s", truncate(msg.Content, truncateLength))))
		}
	}

	// Show current streaming response with markdown rendering
	if m.streaming || m.currentResponse.Len() > 0 || m.currentReasoning.Len() > 0 {
		b.WriteString("\n\n")
		if m.currentReasoning.Len() > 0 {
			b.WriteString(thoughtLabelStyle.Render("💭 Thinking:"))
			b.WriteString("\n")
			b.WriteString(thoughtStyle.Render(m.currentReasoning.String()))
			b.WriteString("\n")
		}
		if m.currentResponse.Len() > 0 {
			// Render streaming content with markdown
			rendered := m.renderer.toMarkdown(m.currentResponse.String(), -1, false)
			b.WriteString(aiMessageStyle.Render(rendered))
		}
		if m.streaming {
			b.WriteString(spinnerStyle.Render("▋"))
		}
	}

	return b.String()
}

func (m *Model) renderConfirmDialog() string {
	var b strings.Builder
	b.WriteString(confirmTitleStyle.Render("🔧 Execute Shell Command?"))
	b.WriteString("\n\n")
	b.WriteString(confirmCommandStyle.Render(fmt.Sprintf("$ %s", m.pendingToolArgs.Command)))
	if m.pendingToolArgs.WorkingDirectory != "" {
		b.WriteString("\n")
		b.WriteString(dimTextStyle.Render(
			fmt.Sprintf("Working directory: %s", m.pendingToolArgs.WorkingDirectory)))
	}
	return confirmBoxStyle.Render(b.String())
}

// addRuntimeMessage adds a message to runtime messages for display
func (m *Model) addRuntimeMessage(msg *aipb.Message) {
	m.runtimeMessages = append(m.runtimeMessages, &runtimeMessage{message: msg})
}

// addRuntimeMessageWithError adds a message with an error to runtime messages (not persisted)
func (m *Model) addRuntimeMessageWithError(msg *aipb.Message, err error) {
	m.runtimeMessages = append(m.runtimeMessages, &runtimeMessage{message: msg, err: err})
}

// getMessagesForAPI returns messages suitable for sending to the API
// This includes persisted messages plus the pending user message
func (m *Model) getMessagesForAPI() []*aipb.Message {
	messages := make([]*aipb.Message, len(m.chat.Messages))
	copy(messages, m.chat.Messages)
	if m.pendingUserMessage != nil {
		messages = append(messages, m.pendingUserMessage)
	}
	return messages
}

func (m *Model) sendMessage() tea.Cmd {
	userInput := strings.TrimSpace(m.textarea.Value())
	if userInput == "" {
		return nil
	}

	// Add to history
	m.history.Add(userInput)
	m.historyNavigating = false

	// Create user message - add to runtime for display but NOT to chat.Messages yet
	userMessage := &aipb.Message{
		Role:    aipb.Role_ROLE_USER,
		Content: userInput,
	}
	m.addRuntimeMessage(userMessage)
	m.pendingUserMessage = userMessage // Track for API calls

	// Clear input
	m.textarea.Reset()

	// Reset response builders
	m.currentResponse.Reset()
	m.currentReasoning.Reset()
	m.currentToolCalls = nil

	// Start streaming
	m.streaming = true
	m.recalculateLayout()
	m.viewport.GotoBottom()

	return m.startStreaming()
}

func (m *Model) startStreaming() tea.Cmd {
	// Create a cancellable context for this stream
	streamCtx, cancel := context.WithCancel(m.ctx)
	m.cancelStream = cancel

	// Capture necessary values for the goroutine
	aiClient := m.aiClient
	opts := m.opts
	additionalMessages := m.additionalMessages
	chatMessages := m.getMessagesForAPI()

	// Get program reference
	p := m.getProgram()
	if p == nil {
		return func() tea.Msg {
			return streamErrorMsg{err: fmt.Errorf("program not set")}
		}
	}

	// Start streaming in a goroutine
	go func() {
		// Build messages
		messages := append([]*aipb.Message{}, additionalMessages...)
		messages = append(messages, chatMessages...)

		// Build tools list
		var tools []*aipb.Tool
		if opts.EnableTools {
			tools = append(tools, shellCommandTool)
		}

		// Create request
		request := &aiservicepb.TextToTextStreamRequest{
			Model:    opts.Model,
			Messages: messages,
			Tools:    tools,
			Configuration: &aiservicepb.TextToTextConfiguration{
				MaxTokens:       opts.MaxTokens,
				Temperature:     opts.Temperature,
				ReasoningEffort: opts.ReasoningEffort,
			},
		}

		// Start stream
		stream, err := aiClient.TextToTextStream(streamCtx, request)
		if err != nil {
			p.Send(streamErrorMsg{err: err})
			return
		}

		// Process stream and send chunks to the program
		for {
			select {
			case <-streamCtx.Done():
				p.Send(streamErrorMsg{err: streamCtx.Err()})
				return
			default:
			}

			response, err := stream.Recv()
			if err != nil {
				p.Send(streamErrorMsg{err: err})
				return
			}

			// Send chunk to program for real-time UI updates
			p.Send(response)
		}
	}()

	// Return spinner tick to keep UI responsive while streaming
	return m.spinner.Tick
}

func (m *Model) finalizeResponse(err error) {
	if m.currentResponse.Len() > 0 || m.currentReasoning.Len() > 0 || len(m.currentToolCalls) > 0 {
		assistantMessage := &aipb.Message{
			Role:      aipb.Role_ROLE_ASSISTANT,
			Content:   m.currentResponse.String(),
			Reasoning: m.currentReasoning.String(),
			ToolCalls: m.currentToolCalls,
		}

		if err != nil {
			// Error case: add to runtime with error (not persisted)
			m.addRuntimeMessageWithError(assistantMessage, err)
			// Clear the pending user message - it won't be persisted
			m.pendingUserMessage = nil
		} else {
			// Success case: persist both user message and assistant response
			if m.pendingUserMessage != nil {
				m.chat.Messages = append(m.chat.Messages, m.pendingUserMessage)
				m.pendingUserMessage = nil
			}
			m.chat.Messages = append(m.chat.Messages, assistantMessage)
			m.addRuntimeMessage(assistantMessage)
		}
	} else if err != nil {
		// Error with no response content - still need to clear pending user message
		m.pendingUserMessage = nil
	}

	m.currentResponse.Reset()
	m.currentReasoning.Reset()
	m.recalculateLayout()
	m.viewport.GotoBottom()
}

func (m *Model) saveChat() tea.Cmd {
	return func() tea.Msg {
		now := time.Now().UnixMicro()
		if m.chat.CreationTimestamp == 0 {
			m.chat.CreationTimestamp = now
			m.chat.UpdateTimestamp = now
			createReq := &store.CreateChatRequest{Chat: m.chat}
			if _, err := m.store.CreateChat(createReq); err != nil {
				return streamErrorMsg{err: err}
			}

			// Generate summary asynchronously
			go func() {
				_ = generateChatSummary(m.ctx, m.config, m.store, m.aiClient, m.chat)
			}()
		} else {
			m.chat.UpdateTimestamp = now
			updateReq := &store.UpdateChatRequest{
				Chat:       m.chat,
				UpdateMask: []string{"messages", "files", "tags"},
			}
			if err := m.store.UpdateChat(updateReq); err != nil {
				return streamErrorMsg{err: err}
			}
		}
		return chatSavedMsg{}
	}
}

func (m *Model) promptToolCall() tea.Cmd {
	return func() tea.Msg {
		if len(m.currentToolCalls) == 0 {
			return nil
		}

		toolCall := m.currentToolCalls[0]
		m.currentToolCalls = m.currentToolCalls[1:]

		if toolCall.Name == "execute_shell_command" {
			args, err := ParseShellCommandArgs(toolCall.Arguments)
			if err != nil {
				return streamErrorMsg{err: err}
			}
			m.pendingToolCall = toolCall
			m.pendingToolArgs = args
			m.awaitingConfirm = true
		}
		return nil
	}
}

func (m *Model) executeToolCall() tea.Cmd {
	m.awaitingConfirm = false
	args := m.pendingToolArgs

	return func() tea.Msg {
		if args == nil {
			return toolCancelledMsg{}
		}

		result, err := ExecuteShellCommand(args)
		if err != nil {
			return toolResultMsg{result: fmt.Sprintf("Error: %v", err)}
		}
		return toolResultMsg{result: result}
	}
}

func (m *Model) continueWithToolResult() tea.Cmd {
	m.currentResponse.Reset()
	m.currentReasoning.Reset()
	m.currentToolCalls = nil
	m.streaming = true
	return m.startStreaming()
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-truncateSuffixLength] + truncateSuffix
}

// adjustTextareaHeight resizes the textarea based on content line count
func (m *Model) adjustTextareaHeight() {
	content := m.textarea.Value()
	lineCount := strings.Count(content, "\n") + 1

	newHeight := lineCount
	if newHeight < minTextareaHeight {
		newHeight = minTextareaHeight
	}
	if newHeight > maxTextareaHeight {
		newHeight = maxTextareaHeight
	}

	oldHeight := m.textarea.Height()
	if oldHeight != newHeight {
		m.textarea.SetHeight(newHeight)

		// Calculate height difference: positive means textarea grew (viewport shrinks)
		heightDiff := newHeight - oldHeight

		m.recalculateLayout()

		// Adjust viewport scroll to compensate for the height change
		// When textarea grows, viewport shrinks, so we need to scroll down to keep content in view
		// When textarea shrinks, viewport grows, so we scroll up
		if heightDiff != 0 && m.ready {
			m.viewport.LineDown(heightDiff)
		}
	}
}

// recalculateLayout adjusts viewport and textarea dimensions based on current state
func (m *Model) recalculateLayout() {
	if m.width == 0 || m.height == 0 {
		return
	}

	viewportHeight := m.height - headerHeight
	viewportWidth := m.width

	viewportHeight -= m.textarea.Height() + inputBorderHeight

	if m.err != nil {
		viewportHeight -= 1
	}

	if viewportHeight < minViewportHeight {
		viewportHeight = minViewportHeight
	}
	contentWidth := viewportWidth
	m.renderer.SetWidth(contentWidth - messageHorizontalFrameSize)

	if !m.ready {
		m.viewport = viewport.New(viewportWidth, viewportHeight)
		m.ready = true
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom() // Only on initial render
	} else {
		m.viewport.Width = viewportWidth
		m.viewport.Height = viewportHeight
		m.viewport.SetContent(m.renderMessages())
	}

	m.textarea.SetWidth(viewportWidth - textAreaStyle.GetHorizontalPadding() - textAreaStyle.GetHorizontalBorderSize())
}
