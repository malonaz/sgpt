package tui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"go.dalton.dog/bubbleup"

	"github.com/malonaz/sgpt/cli/tui/styles"
	chatservicepb "github.com/malonaz/sgpt/genproto/chat/chat_service/v1"
	chatpb "github.com/malonaz/sgpt/genproto/chat/v1"
	"github.com/malonaz/sgpt/internal/configuration"
	"github.com/malonaz/sgpt/internal/debug"
	"github.com/malonaz/sgpt/internal/history"
	"github.com/malonaz/sgpt/internal/markdown"
	"github.com/malonaz/sgpt/internal/tools"
	"github.com/malonaz/sgpt/internal/types"
)

const (
	renderThrottleInterval = 66 * time.Millisecond

	FocusTextarea FocusedComponent = iota
	FocusViewport
)

var (
	log              *slog.Logger
	errUserInterrupt = errors.New("user interrupt")
)

type FocusedComponent int

// Model represents the Bubble Tea model for the chat session.
type Model struct {
	// Core dependencies
	ctx        context.Context
	config     *configuration.Config
	aiClient   aiservicepb.AiServiceClient
	chatClient chatservicepb.ChatServiceClient

	// Chat state
	chat               *chatpb.Chat
	opts               types.ChatOptions
	additionalMessages []*aipb.Message
	injectedFiles      []string
	totalModelUsage    *aipb.ModelUsage
	lastModelUsage     *aipb.ModelUsage

	// Runtime messages for display (decoupled from proto messages)
	runtimeMessages        []*types.RuntimeMessage
	messageViewportOffsets []int   // Tracks the line offset of each message in the viewport.
	blockViewportOffsets   [][]int // Tracks the line offset of each block within each message (for block mode).

	// UI components
	textarea textarea.Model
	viewport viewport.Model
	spinner  spinner.Model
	renderer *markdown.Renderer

	// UI state
	title       string
	titleHeight int

	width            int
	height           int
	ready            bool
	streaming        bool
	err              error
	quitting         bool
	windowFocused    bool
	focusedComponent FocusedComponent

	// Alert notifications.
	alertClipboardWrite bubbleup.AlertModel

	// Tool confirmation state
	pendingToolCall *aipb.ToolCall
	pendingToolArgs *tools.ShellCommandArgs
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

	// Tracks the index of the message we're currently navigating. (-1 if none is selected).
	navigationMessageIndex int
	navigationBlockIndex   int // Index within the current message's block. (-1 if we're not in block mode).
}

// New creates a new chat session model.
func New(
	ctx context.Context,
	config *configuration.Config,
	aiClient aiservicepb.AiServiceClient,
	chatClient chatservicepb.ChatServiceClient,
	chat *chatpb.Chat,
	opts types.ChatOptions,
	additionalMessages []*aipb.Message,
	injectedFiles []string,
) (*Model, error) {
	log = debug.GetLogger()

	// Create textarea for input
	ta := textarea.New()
	ta.Placeholder = "Type your message... (Ctrl+J to send, Alt+V to view, Alt+P/N for history, Ctrl+C to quit)"
	ta.Focus()
	ta.CharLimit = 0
	ta.SetWidth(styles.DefaultTextareaWidth)
	ta.SetHeight(styles.MinTextareaHeight)
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetEnabled(true)
	ta.Prompt = ""

	// Create spinner
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = styles.SpinnerStyle

	alertClipboardWrite := bubbleup.NewAlertModel(25, true, 1)

	// Initialize runtime messages from existing chat messages
	runtimeMsgs := types.RuntimeMessagesFromProto(chat.Metadata.Messages)

	renderer, err := markdown.NewRenderer(styles.DefaultTextareaWidth)
	if err != nil {
		return nil, err
	}

	m := &Model{
		ctx:                    ctx,
		config:                 config,
		aiClient:               aiClient,
		chatClient:             chatClient,
		chat:                   chat,
		opts:                   opts,
		windowFocused:          true,
		focusedComponent:       FocusTextarea,
		additionalMessages:     additionalMessages,
		injectedFiles:          injectedFiles,
		textarea:               ta,
		spinner:                sp,
		history:                history.NewHistory(),
		runtimeMessages:        runtimeMsgs,
		renderer:               renderer,
		alertClipboardWrite:    *alertClipboardWrite,
		navigationMessageIndex: -1,
		navigationBlockIndex:   -1,
		totalModelUsage: &aipb.ModelUsage{
			InputToken:           &aipb.ResourceConsumption{},
			OutputToken:          &aipb.ResourceConsumption{},
			OutputReasoningToken: &aipb.ResourceConsumption{},
			InputCacheReadToken:  &aipb.ResourceConsumption{},
			InputCacheWriteToken: &aipb.ResourceConsumption{},
		},
		lastModelUsage: &aipb.ModelUsage{},
	}

	m.setTitle()
	return m, nil
}

// SetProgram sets the tea.Program reference for async message sending.
func (m *Model) SetProgram(p *tea.Program) {
	m.programMu.Lock()
	defer m.programMu.Unlock()
	m.program = p
}

// getProgram safely gets the program reference.
func (m *Model) getProgram() *tea.Program {
	m.programMu.Lock()
	defer m.programMu.Unlock()
	return m.program
}

// Init initializes the model.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		m.spinner.Tick,
		m.alertClipboardWrite.Init(),
	)
}

// getMessagesForAPI returns messages suitable for sending to the API.
func (m *Model) getMessagesForAPI() []*aipb.Message {
	messages := make([]*aipb.Message, 0, len(m.chat.Metadata.Messages))
	for _, msg := range m.chat.Metadata.Messages {
		if msg.Error == nil {
			messages = append(messages, msg.Message)
		}
	}
	if m.pendingUserMessage != nil {
		messages = append(messages, m.pendingUserMessage)
	}
	return messages
}

// getSelectedContent returns the content of the currently selected message or block.
func (m *Model) getSelectedContent() (string, string) {
	if m.navigationMessageIndex < 0 || m.navigationMessageIndex >= len(m.runtimeMessages) {
		return "", ""
	}
	msg := m.runtimeMessages[m.navigationMessageIndex]
	if m.navigationBlockIndex != -1 {
		blocks := msg.Blocks
		if m.navigationBlockIndex >= 0 && m.navigationBlockIndex < len(blocks) {
			return blocks[m.navigationBlockIndex].Content(), blocks[m.navigationBlockIndex].Extension()
		}
		return "", ""
	}
	return msg.Content(), ""
}

func (m *Model) setTitle() {
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

	totalInputTokens := m.totalModelUsage.GetInputToken().GetQuantity() + m.totalModelUsage.GetInputCacheReadToken().GetQuantity()
	totalOutputTokens := m.totalModelUsage.GetOutputToken().GetQuantity() + m.totalModelUsage.GetOutputReasoningToken().GetQuantity()
	totalPrice := m.totalModelUsage.GetInputToken().GetPrice() + m.totalModelUsage.GetOutputToken().GetPrice() + m.totalModelUsage.GetOutputReasoningToken().GetPrice() + m.totalModelUsage.GetInputCacheReadToken().GetPrice() + m.totalModelUsage.GetInputCacheWriteToken().GetPrice()

	tokenStr := fmt.Sprintf("↑%s ↓%s $%.4f", formatTokenCount(totalInputTokens), formatTokenCount(totalOutputTokens), totalPrice)

	// Calculate context usage percentage
	contextStr := ""
	if contextLimit := m.opts.Model.GetTtt().GetContextTokenLimit(); contextLimit > 0 {
		lastInputTokens := m.lastModelUsage.GetInputToken().GetQuantity() + m.lastModelUsage.GetInputCacheReadToken().GetQuantity()
		usagePercent := float64(lastInputTokens) / float64(contextLimit) * 100
		contextStr = fmt.Sprintf(" │ 📦 %.0f%% (%s/%s)", usagePercent, formatTokenCount(lastInputTokens), formatTokenCount(contextLimit))
	}

	modelRn := &aipb.ModelResourceName{}
	modelRn.UnmarshalString(m.opts.Model.Name)
	modelStr := fmt.Sprintf("%s/%s", modelRn.Provider, modelRn.Model)

	m.title = fmt.Sprintf(
		" 🤖 %s │ 👤 %s │ 💬 %s │ 🧠 %s │ 📊 %s%s%s ",
		modelStr, roleName, m.opts.ChatID, reasoningStr, tokenStr, contextStr, toolsStr,
	)
}

// formatTokenCount formats a token count with appropriate suffix (k for thousands, m for millions).
func formatTokenCount(count int32) string {
	if count < 1000 {
		return fmt.Sprintf("%d", count)
	}
	if count < 1000000 {
		return fmt.Sprintf("%.1fk", float64(count)/1000)
	}
	return fmt.Sprintf("%.1fm", float64(count)/1000000)
}
