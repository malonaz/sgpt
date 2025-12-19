package session

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"

	"github.com/malonaz/sgpt/cli/chat/types"
	"github.com/malonaz/sgpt/internal/tools"
	"github.com/malonaz/sgpt/store"
)

func (m *Model) sendMessage() tea.Cmd {
	userInput := strings.TrimSpace(m.textarea.Value())
	if userInput == "" {
		return nil
	}

	m.history.Add(userInput)
	m.historyNavigating = false

	userMessage := &aipb.Message{
		Role:    aipb.Role_ROLE_USER,
		Content: userInput,
	}
	m.runtimeMessages = append(m.runtimeMessages, types.NewUserMessage(userInput))
	m.pendingUserMessage = userMessage

	m.textarea.Reset()

	m.currentResponse.Reset()
	m.currentReasoning.Reset()
	m.currentToolCalls = nil

	m.streaming = true
	m.recalculateLayout()
	m.viewport.GotoBottom()

	return m.startStreaming()
}

func (m *Model) startStreaming() tea.Cmd {
	streamCtx, cancel := context.WithCancel(m.ctx)
	m.cancelStream = cancel

	aiClient := m.aiClient
	opts := m.opts
	additionalMessages := m.additionalMessages
	chatMessages := m.getMessagesForAPI()

	p := m.getProgram()
	if p == nil {
		return func() tea.Msg {
			return types.StreamErrorMsg{Err: fmt.Errorf("program not set")}
		}
	}

	go func() {
		messages := append([]*aipb.Message{}, additionalMessages...)
		messages = append(messages, chatMessages...)

		var toolsList []*aipb.Tool
		if opts.EnableTools {
			toolsList = append(toolsList, tools.ShellCommandTool)
		}

		request := &aiservicepb.TextToTextStreamRequest{
			Model:    opts.Model,
			Messages: messages,
			Tools:    toolsList,
			Configuration: &aiservicepb.TextToTextConfiguration{
				MaxTokens:       opts.MaxTokens,
				Temperature:     opts.Temperature,
				ReasoningEffort: opts.ReasoningEffort,
			},
		}

		stream, err := aiClient.TextToTextStream(streamCtx, request)
		if err != nil {
			p.Send(types.StreamErrorMsg{Err: err})
			return
		}

		for {
			select {
			case <-streamCtx.Done():
				p.Send(types.StreamErrorMsg{Err: streamCtx.Err()})
				return
			default:
			}

			response, err := stream.Recv()
			if err != nil {
				p.Send(types.StreamErrorMsg{Err: err})
				return
			}

			p.Send(response)
		}
	}()

	return m.spinner.Tick
}

func (m *Model) finalizeResponse(err error) {
	hasContent := m.currentResponse.Len() > 0 || m.currentReasoning.Len() > 0 || len(m.currentToolCalls) > 0

	if hasContent {
		// Build the proto message for persistence
		assistantMessage := &aipb.Message{
			Role:      aipb.Role_ROLE_ASSISTANT,
			Content:   m.currentResponse.String(),
			Reasoning: m.currentReasoning.String(),
			ToolCalls: m.currentToolCalls,
		}

		// Add runtime messages for display
		if m.currentReasoning.Len() > 0 {
			m.runtimeMessages = append(m.runtimeMessages, types.NewThinkingMessage(m.currentReasoning.String()))
		}
		if m.currentResponse.Len() > 0 {
			if err != nil {
				m.runtimeMessages = append(m.runtimeMessages, types.NewAssistantMessageWithError(m.currentResponse.String(), err))
			} else {
				m.runtimeMessages = append(m.runtimeMessages, types.NewAssistantMessage(m.currentResponse.String()))
			}
		}
		for _, tc := range m.currentToolCalls {
			m.runtimeMessages = append(m.runtimeMessages, types.NewToolCallMessage(tc))
		}

		if err != nil {
			m.pendingUserMessage = nil
		} else {
			if m.pendingUserMessage != nil {
				m.chat.Messages = append(m.chat.Messages, m.pendingUserMessage)
				m.pendingUserMessage = nil
			}
			m.chat.Messages = append(m.chat.Messages, assistantMessage)
		}
	} else if err != nil {
		m.pendingUserMessage = nil
	}

	m.currentResponse.Reset()
	m.currentReasoning.Reset()
	wasAtBottom := m.viewport.AtBottom()
	m.recalculateLayout()
	if wasAtBottom {
		m.viewport.GotoBottom()
	}
}

func (m *Model) saveChat() tea.Cmd {
	return func() tea.Msg {
		now := time.Now().UnixMicro()
		if m.chat.CreationTimestamp == 0 {
			m.chat.CreationTimestamp = now
			m.chat.UpdateTimestamp = now
			createReq := &store.CreateChatRequest{Chat: m.chat}
			if _, err := m.store.CreateChat(createReq); err != nil {
				return types.StreamErrorMsg{Err: err}
			}

			go func() {
				_ = GenerateChatSummary(m.ctx, m.config, m.store, m.aiClient, m.chat)
			}()
		} else {
			m.chat.UpdateTimestamp = now
			updateReq := &store.UpdateChatRequest{
				Chat:       m.chat,
				UpdateMask: []string{"messages", "files", "tags"},
			}
			if err := m.store.UpdateChat(updateReq); err != nil {
				return types.StreamErrorMsg{Err: err}
			}
		}
		return types.ChatSavedMsg{}
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
			args, err := tools.ParseShellCommandArgs(toolCall.Arguments)
			if err != nil {
				return types.StreamErrorMsg{Err: err}
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
			return types.ToolCancelledMsg{}
		}

		result, err := tools.ExecuteShellCommand(args)
		if err != nil {
			return types.ToolResultMsg{Result: fmt.Sprintf("Error: %v", err)}
		}
		return types.ToolResultMsg{Result: result}
	}
}

func (m *Model) continueWithToolResult() tea.Cmd {
	m.currentResponse.Reset()
	m.currentReasoning.Reset()
	m.currentToolCalls = nil
	m.streaming = true
	return m.startStreaming()
}
