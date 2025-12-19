package session

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"go.dalton.dog/bubbleup"

	"github.com/malonaz/sgpt/cli/chat/styles"
	"github.com/malonaz/sgpt/cli/chat/types"
	"github.com/malonaz/sgpt/cli/chat/viewer"
	"github.com/malonaz/sgpt/internal/configuration"
	"github.com/malonaz/sgpt/internal/debug"
	"github.com/malonaz/sgpt/internal/history"
	"github.com/malonaz/sgpt/internal/markdown"
	"github.com/malonaz/sgpt/internal/tools"
	"github.com/malonaz/sgpt/store"
)

const (
	renderThrottleInterval = 66 * time.Millisecond
)

var (
	log              = debug.GetLogger()
	errUserInterrupt = errors.New("user interrupt")
)

// Model represents the Bubble Tea model for the chat session.
type Model struct {
	// Core dependencies
	ctx      context.Context
	config   *configuration.Config
	store    *store.Store
	aiClient aiservicepb.AiClient

	// Chat state
	chat               *store.Chat
	opts               types.ChatOptions
	additionalMessages []*aipb.Message
	injectedFiles      []string

	// Runtime messages (includes errored messages for display)
	runtimeMessages        []*types.RuntimeMessage
	messageViewportOffsets []int // Tracks the line offset of each message in the viewport.

	// UI components
	textarea textarea.Model
	viewport viewport.Model
	spinner  spinner.Model
	renderer *markdown.Renderer

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

	// Streaming render throttle
	renderThrottleTicker *time.Ticker
	pendingRender        bool
	lastRenderTime       time.Time

	// Sub-views
	viewerMode  bool
	viewerModel *viewer.Model
}

// New creates a new chat session model.
func New(
	ctx context.Context,
	config *configuration.Config,
	s *store.Store,
	aiClient aiservicepb.AiClient,
	chat *store.Chat,
	opts types.ChatOptions,
	additionalMessages []*aipb.Message,
	injectedFiles []string,
) (*Model, error) {
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
	runtimeMsgs := make([]*types.RuntimeMessage, len(chat.Messages))
	for i, msg := range chat.Messages {
		runtimeMsgs[i] = &types.RuntimeMessage{Message: msg}
	}

	renderer, err := markdown.NewRenderer(styles.DefaultTextareaWidth)
	if err != nil {
		return nil, err
	}

	return &Model{
		ctx:                    ctx,
		config:                 config,
		store:                  s,
		aiClient:               aiClient,
		chat:                   chat,
		opts:                   opts,
		windowFocused:          true,
		additionalMessages:     additionalMessages,
		injectedFiles:          injectedFiles,
		textarea:               ta,
		spinner:                sp,
		history:                history.NewHistory(),
		runtimeMessages:        runtimeMsgs,
		renderer:               renderer,
		alertClipboardWrite:    *alertClipboardWrite,
		navigationMessageIndex: -1,
	}, nil
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

// addRuntimeMessage adds a message to runtime messages for display.
func (m *Model) addRuntimeMessage(msg *aipb.Message) {
	m.runtimeMessages = append(m.runtimeMessages, &types.RuntimeMessage{Message: msg})
}

// addRuntimeMessageWithError adds a message with an error to runtime messages (not persisted).
func (m *Model) addRuntimeMessageWithError(msg *aipb.Message, err error) {
	m.runtimeMessages = append(m.runtimeMessages, &types.RuntimeMessage{Message: msg, Err: err})
}

// getMessagesForAPI returns messages suitable for sending to the API.
func (m *Model) getMessagesForAPI() []*aipb.Message {
	messages := make([]*aipb.Message, len(m.chat.Messages))
	copy(messages, m.chat.Messages)
	if m.pendingUserMessage != nil {
		messages = append(messages, m.pendingUserMessage)
	}
	return messages
}
