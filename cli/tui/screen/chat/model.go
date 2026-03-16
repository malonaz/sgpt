package chat

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"

	"github.com/malonaz/sgpt/cli/tui/screen"
	"github.com/malonaz/sgpt/cli/tui/styles"
	chatservicepb "github.com/malonaz/sgpt/genproto/chat/chat_service/v1"
	chatpb "github.com/malonaz/sgpt/genproto/chat/v1"
	"github.com/malonaz/sgpt/internal/configuration"
	"github.com/malonaz/sgpt/internal/debug"
	"github.com/malonaz/sgpt/internal/history"
	"github.com/malonaz/sgpt/internal/markdown"
	"github.com/malonaz/sgpt/internal/tools"
)

type FocusedComponent int

const (
	FocusTextarea FocusedComponent = iota
	FocusViewport
)

type Options struct {
	Model           *aipb.Model
	Role            *configuration.Role
	MaxTokens       int32
	Temperature     float64
	ReasoningEffort aipb.ReasoningEffort
	EnableTools     bool
	ChatID          string
}

type Model struct {
	ctx        context.Context
	config     *configuration.Config
	aiClient   aiservicepb.AiServiceClient
	chatClient chatservicepb.ChatServiceClient
	wrap       screen.WrapFunc
	send       screen.SendFunc
	log        *slog.Logger

	chat               *chatpb.Chat
	opts               Options
	additionalMessages []*aipb.Message
	injectedFiles      []string
	totalModelUsage    *aipb.ModelUsage
	lastModelUsage     *aipb.ModelUsage

	streamingMessage   *aipb.Message
	pendingUserMessage *aipb.Message
	streamError        error

	messageViewportOffsets []int
	blockViewportOffsets   [][]int
	markdownBlockCounts    []int

	textarea textarea.Model
	viewport viewport.Model
	spinner  spinner.Model
	renderer *markdown.Renderer

	title       string
	titleHeight int

	width            int
	height           int
	ready            bool
	streaming        bool
	focused          bool
	focusedComponent FocusedComponent

	pendingToolCall *aipb.ToolCall
	pendingToolArgs *tools.ShellCommandArgs
	awaitingConfirm bool

	cancelStream context.CancelFunc

	inputHistory      *history.History
	historyNavigating bool

	navigationMessageIndex int
	navigationBlockIndex   int
}

func New(
	ctx context.Context,
	config *configuration.Config,
	aiClient aiservicepb.AiServiceClient,
	chatClient chatservicepb.ChatServiceClient,
	wrap screen.WrapFunc,
	send screen.SendFunc,
	chat *chatpb.Chat,
	opts Options,
	additionalMessages []*aipb.Message,
	injectedFiles []string,
) *Model {
	ta := textarea.New()
	ta.Placeholder = "Type your message... (Ctrl+J to send, Tab to navigate)"
	ta.CharLimit = 0
	ta.SetWidth(styles.DefaultTextareaWidth)
	ta.SetHeight(styles.MinTextareaHeight)
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetEnabled(true)
	ta.Prompt = ""

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = styles.SpinnerStyle

	renderer, _ := markdown.NewRenderer(styles.DefaultTextareaWidth)

	model := &Model{
		ctx:                    ctx,
		config:                 config,
		aiClient:               aiClient,
		chatClient:             chatClient,
		wrap:                   wrap,
		send:                   send,
		log:                    debug.GetLogger(),
		chat:                   chat,
		opts:                   opts,
		additionalMessages:     additionalMessages,
		injectedFiles:          injectedFiles,
		textarea:               ta,
		spinner:                sp,
		renderer:               renderer,
		inputHistory:           history.NewHistory(),
		focusedComponent:       FocusTextarea,
		navigationMessageIndex: -1,
		navigationBlockIndex:   -1,
		totalModelUsage:        &aipb.ModelUsage{},
		lastModelUsage:         &aipb.ModelUsage{},
	}
	model.setTitle()
	return model
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, m.spinner.Tick)
}

func (m *Model) Title() string {
	if t := m.chat.GetMetadata().GetTitle(); t != "" {
		return t
	}
	name := m.chat.GetName()
	if name == "" {
		return "New Chat"
	}
	return strings.TrimPrefix(name, "chats/")
}

func (m *Model) ShortTitle() string {
	return styles.Truncate(m.Title(), 20)
}

func (m *Model) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.recalculateLayout()
}

func (m *Model) OnFocus() tea.Cmd {
	m.focused = true
	if m.focusedComponent == FocusTextarea && !m.streaming {
		m.textarea.Focus()
		return textarea.Blink
	}
	return nil
}

func (m *Model) OnBlur() {
	m.focused = false
	m.textarea.Blur()
}

func (m *Model) IsStreaming() bool {
	return m.streaming
}

func (m *Model) Chat() *chatpb.Chat {
	return m.chat
}

func (m *Model) Opts() Options {
	return m.opts
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

	totalInputTokens := m.totalModelUsage.GetInputToken().GetQuantity() + m.totalModelUsage.GetInputTokenCacheRead().GetQuantity()
	totalOutputTokens := m.totalModelUsage.GetOutputToken().GetQuantity() + m.totalModelUsage.GetOutputReasoningToken().GetQuantity()
	totalPrice := m.totalModelUsage.GetInputToken().GetPrice() +
		m.totalModelUsage.GetOutputToken().GetPrice() +
		m.totalModelUsage.GetOutputReasoningToken().GetPrice() +
		m.totalModelUsage.GetInputTokenCacheRead().GetPrice() +
		m.totalModelUsage.GetInputTokenCacheWrite().GetPrice()

	tokenStr := fmt.Sprintf("↑%s ↓%s $%.4f", formatTokenCount(totalInputTokens), formatTokenCount(totalOutputTokens), totalPrice)

	contextStr := ""
	if contextLimit := m.opts.Model.GetTtt().GetContextTokenLimit(); contextLimit > 0 {
		lastInputTokens := m.lastModelUsage.GetInputToken().GetQuantity() + m.lastModelUsage.GetInputTokenCacheRead().GetQuantity()
		usagePercent := float64(lastInputTokens) / float64(contextLimit) * 100
		contextStr = fmt.Sprintf(" │ 📦 %.0f%% (%s/%s)", usagePercent, formatTokenCount(lastInputTokens), formatTokenCount(contextLimit))
	}

	modelResourceName := &aipb.ModelResourceName{}
	modelResourceName.UnmarshalString(m.opts.Model.Name)
	modelStr := fmt.Sprintf("%s/%s", modelResourceName.Provider, modelResourceName.Model)

	m.title = fmt.Sprintf(
		" 🤖 %s │ 👤 %s │ 🧠 %s │ 📊 %s%s%s ",
		modelStr, roleName, reasoningStr, tokenStr, contextStr, toolsStr,
	)
}

func formatTokenCount(count int32) string {
	if count < 1000 {
		return fmt.Sprintf("%d", count)
	}
	if count < 1000000 {
		return fmt.Sprintf("%.1fk", float64(count)/1000)
	}
	return fmt.Sprintf("%.1fm", float64(count)/1000000)
}

func (m *Model) messagesForAPI() []*aipb.Message {
	messages := make([]*aipb.Message, 0, len(m.additionalMessages)+len(m.chat.Metadata.Messages)+1)
	messages = append(messages, m.additionalMessages...)
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

var _ screen.Screen = (*Model)(nil)
