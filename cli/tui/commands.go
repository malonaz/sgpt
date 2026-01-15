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
	"github.com/malonaz/core/go/ai"
	"github.com/malonaz/core/go/pbutil"
	"github.com/malonaz/core/go/pbutil/pbfieldmask"
	chatservicepb "github.com/malonaz/sgpt/genproto/chat/chat_service/v1"
	chatpb "github.com/malonaz/sgpt/genproto/chat/v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/malonaz/sgpt/internal/tools"
	"github.com/malonaz/sgpt/internal/types"
)

func (m *Model) sendMessage() tea.Cmd {
	userInput := strings.TrimSpace(m.textarea.Value())
	if userInput == "" {
		return nil
	}

	m.history.Add(userInput)
	m.historyNavigating = false

	userMessage := ai.NewUserMessage(&aipb.UserMessage{Content: userInput})
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
			cancel()
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
				toolMsg, err := types.NewToolCallMessage(content.ToolCall)
				if err != nil {
					finalize(err)
				}
				*runtimeMessages = append(*runtimeMessages, toolMsg)
			}

			checkRender()
		}
	}()

	return m.spinner.Tick
}

func (m *Model) finalizeResponse(done types.StreamDoneMsg) {
	// Add user message.
	userMessage := &chatpb.Message{Message: m.pendingUserMessage}
	if done.Err != nil {
		userMessage.Error = status.Convert(done.Err).Proto()
	}
	m.chat.Metadata.Messages = append(m.chat.Metadata.Messages, userMessage)

	// Add assistant message.
	hasContent := done.Response != "" || done.Reasoning != "" || len(done.ToolCalls) > 0
	if hasContent {
		assistantMessage := &chatpb.Message{
			Message: ai.NewAssistantMessage(&aipb.AssistantMessage{
				Reasoning: done.Reasoning,
				Content:   done.Response,
				ToolCalls: done.ToolCalls,
			}),
		}
		if done.Err != nil {
			assistantMessage.Error = status.Convert(done.Err).Proto()
		}
		m.chat.Metadata.Messages = append(m.chat.Metadata.Messages, assistantMessage)
	}

	m.pendingUserMessage = nil

	wasAtBottom := m.viewport.AtBottom()
	m.recalculateLayout()
	if wasAtBottom {
		m.viewport.GotoBottom()
	}
}

func (m *Model) saveChat() tea.Cmd {
	return func() tea.Msg {
		updateChatRequest := &chatservicepb.UpdateChatRequest{
			Chat:       m.chat,
			UpdateMask: pbfieldmask.FromPaths("tags", "files", "metadata").MustValidate(&chatpb.Chat{}).Proto(),
		}
		chat, err := m.chatClient.UpdateChat(m.ctx, updateChatRequest)
		if err != nil {
			return types.StreamErrorMsg{Err: err}
		}
		m.chat = chat
		return types.ChatSavedMsg{}
	}
}

func (m *Model) promptToolCall(toolCalls []*aipb.ToolCall) []tea.Cmd {
	var cmds []tea.Cmd
	for _, toolCall := range toolCalls {
		cmd := func() tea.Msg {
			bytes, err := pbutil.JSONMarshalPretty(toolCall.Arguments)
			if err != nil {
				return types.StreamErrorMsg{Err: err}
			}

			switch toolCall.Name {
			case tools.ShellCommand.Name:
				args, err := tools.ParseShellCommandArgs(bytes)
				if err != nil {
					return types.StreamErrorMsg{Err: err}
				}
				m.pendingToolCall = toolCall
				m.pendingToolArgs = args
				m.awaitingConfirm = true

			case tools.ReadFiles.Name:
				args, err := tools.ParseReadFilesArgs(bytes)
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

		var toolResult *aipb.ToolResult
		result, err := tools.ExecuteShellCommand(args)
		if err != nil {
			toolResult = ai.NewErrorToolResult(err)
		} else {
			toolResult = ai.NewToolResult(result)
		}
		return types.ToolResultMsg{
			ToolCallID: m.pendingToolCall.Id,
			ToolResult: toolResult,
		}
	}
}

func (m *Model) continueWithToolResult() tea.Cmd {
	m.streaming = true
	return m.startStreaming()
}

func (m *Model) openInEditor(content, ext string) tea.Cmd {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		editor = "vim" // fallback
	}

	// Create temp file
	tmpFile, err := os.CreateTemp("", "sgpt-message-*."+ext)
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
