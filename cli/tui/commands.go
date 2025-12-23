package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"google.golang.org/protobuf/proto"

	"github.com/malonaz/sgpt/internal/tools"
	"github.com/malonaz/sgpt/internal/types"
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
	runtimeMessages := &m.runtimeMessages

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
			toolsList = append(toolsList, tools.ShellCommand, tools.ReadFiles)
		}

		request := &aiservicepb.TextToTextStreamRequest{
			Model:    opts.Model.Name,
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
			p.Send(types.StreamDoneMsg{Err: err})
			return
		}

		// Track streaming messages
		var thinkingMsg *types.RuntimeMessage
		var assistantMsg *types.RuntimeMessage
		var toolCalls []*aipb.ToolCall

		// Track content for persistence
		var responseContent strings.Builder
		var reasoningContent strings.Builder

		// Throttling
		lastRender := time.Now()
		pendingRender := false

		sendRender := func() {
			p.Send(types.StreamRenderMsg{})
			lastRender = time.Now()
			pendingRender = false
		}

		checkRender := func() {
			if time.Since(lastRender) >= renderThrottleInterval {
				sendRender()
			} else {
				pendingRender = true
			}
		}

		finalize := func(err error) {
			if thinkingMsg != nil {
				thinkingMsg.Finalize(err)
			}
			if assistantMsg != nil {
				assistantMsg.Finalize(err)
			}
			if pendingRender {
				sendRender()
			}
			p.Send(types.StreamDoneMsg{
				Err:       err,
				Response:  responseContent.String(),
				Reasoning: reasoningContent.String(),
				ToolCalls: toolCalls,
			})
		}

		defer func() {
			m.totalModelUsage.InputToken.Quantity += m.lastModelUsage.GetInputToken().GetQuantity()
			m.totalModelUsage.InputToken.Price += m.lastModelUsage.GetInputToken().GetPrice()
			m.totalModelUsage.OutputToken.Quantity += m.lastModelUsage.GetOutputToken().GetQuantity()
			m.totalModelUsage.OutputToken.Price += m.lastModelUsage.GetOutputToken().GetPrice()
			m.totalModelUsage.OutputReasoningToken.Quantity += m.lastModelUsage.GetOutputReasoningToken().GetQuantity()
			m.totalModelUsage.OutputReasoningToken.Price += m.lastModelUsage.GetOutputReasoningToken().GetPrice()
			m.totalModelUsage.InputCacheReadToken.Quantity += m.lastModelUsage.GetInputCacheReadToken().GetQuantity()
			m.totalModelUsage.InputCacheReadToken.Price += m.lastModelUsage.GetInputCacheReadToken().GetPrice()
			m.totalModelUsage.InputCacheWriteToken.Quantity += m.lastModelUsage.GetInputCacheWriteToken().GetQuantity()
			m.totalModelUsage.InputCacheWriteToken.Price += m.lastModelUsage.GetInputCacheWriteToken().GetPrice()

			// Reset the model usages.
			m.lastModelUsage = &aipb.ModelUsage{}

			m.setTitle()
			m.renderTitle()
		}()

		for {
			select {
			case <-streamCtx.Done():
				finalize(streamCtx.Err())
				return
			default:
			}

			response, err := stream.Recv()
			if err != nil {
				if err == io.EOF {
					finalize(nil)
				} else {
					finalize(err)
				}
				return
			}

			switch content := response.Content.(type) {
			case *aiservicepb.TextToTextStreamResponse_ModelUsage:
				modelUsage := content.ModelUsage
				proto.Merge(m.lastModelUsage, modelUsage)

			case *aiservicepb.TextToTextStreamResponse_GenerationMetrics:
			case *aiservicepb.TextToTextStreamResponse_ReasoningChunk:
				reasoningContent.WriteString(content.ReasoningChunk)
				if thinkingMsg == nil {
					thinkingMsg = types.NewThinkingMessage("").WithStreaming()
					*runtimeMessages = append(*runtimeMessages, thinkingMsg)
				}
				thinkingMsg.AppendContent(content.ReasoningChunk)

			case *aiservicepb.TextToTextStreamResponse_ContentChunk:
				responseContent.WriteString(content.ContentChunk)
				if assistantMsg == nil {
					assistantMsg = types.NewAssistantMessage("").WithStreaming()
					*runtimeMessages = append(*runtimeMessages, assistantMsg)
				}
				assistantMsg.AppendContent(content.ContentChunk)

			case *aiservicepb.TextToTextStreamResponse_ToolCall:
				toolCalls = append(toolCalls, content.ToolCall)
				toolMsg := types.NewToolCallMessage(content.ToolCall)
				*runtimeMessages = append(*runtimeMessages, toolMsg)
			}

			checkRender()
		}
	}()

	return m.spinner.Tick
}

func (m *Model) finalizeResponse(done types.StreamDoneMsg) {
	hasContent := done.Response != "" || done.Reasoning != "" || len(done.ToolCalls) > 0

	if hasContent {
		// Build the proto message for persistence
		assistantMessage := &aipb.Message{
			Role:      aipb.Role_ROLE_ASSISTANT,
			Content:   done.Response,
			Reasoning: done.Reasoning,
			ToolCalls: done.ToolCalls,
		}

		if done.Err != nil {
			m.pendingUserMessage = nil
		} else {
			if m.pendingUserMessage != nil {
				m.chat.Messages = append(m.chat.Messages, m.pendingUserMessage)
				m.pendingUserMessage = nil
			}
			m.chat.Messages = append(m.chat.Messages, assistantMessage)
		}
	} else if done.Err != nil {
		m.pendingUserMessage = nil
	}

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

func (m *Model) promptToolCall(toolCalls []*aipb.ToolCall) []tea.Cmd {
	var cmds []tea.Cmd
	for _, toolCall := range toolCalls {
		cmd := func() tea.Msg {
			switch toolCall.Name {
			case tools.ShellCommand.Name:
				args, err := tools.ParseShellCommandArgs(toolCall.Arguments)
				if err != nil {
					return types.StreamErrorMsg{Err: err}
				}
				m.pendingToolCall = toolCall
				m.pendingToolArgs = args
				m.awaitingConfirm = true

			case tools.ReadFiles.Name:
				args, err := tools.ParseReadFilesArgs(toolCall.Arguments)
				if err != nil {
					return types.StreamErrorMsg{Err: err}
				}
				_ = args
			}
			return nil
		}
		cmds = append(cmds, cmd)
	}
	return cmds
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
	m.streaming = true
	return m.startStreaming()
}

func (m *Model) openInEditor(content string) tea.Cmd {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		editor = "vim" // fallback
	}

	// Create temp file
	tmpFile, err := os.CreateTemp("", "sgpt-message-*.md")
	if err != nil {
		return func() tea.Msg {
			return types.StreamErrorMsg{Err: fmt.Errorf("failed to create temp file: %w", err)}
		}
	}
	tmpPath := tmpFile.Name()

	// Write content
	if _, err := tmpFile.WriteString(content); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return func() tea.Msg {
			return types.StreamErrorMsg{Err: fmt.Errorf("failed to write temp file: %w", err)}
		}
	}
	tmpFile.Close()

	// Split editor command into parts (handles "emacs -nw", "code --wait", etc.)
	parts := strings.Fields(editor)
	if len(parts) == 0 {
		os.Remove(tmpPath)
		return func() tea.Msg {
			return types.StreamErrorMsg{Err: fmt.Errorf("empty editor command")}
		}
	}

	// Build command: first part is the executable, rest are args, then add the file
	args := append(parts[1:], tmpPath)
	cmd := exec.Command(parts[0], args...)

	// Use tea.ExecProcess to properly suspend Bubble Tea and restore terminal
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		// Cleanup temp file after editor closes
		os.Remove(tmpPath)

		if err != nil {
			return types.StreamErrorMsg{Err: fmt.Errorf("editor failed: %w", err)}
		}
		return types.EditorClosedMsg{}
	})
}
