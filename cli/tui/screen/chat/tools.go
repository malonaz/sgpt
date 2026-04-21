package chat

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/tools"
)

type toolResultMsg struct {
	ToolCallID string
	ToolResult *aipb.ToolResult
}

// setPendingToolCalls stores tool calls for user accept/reject.
func (m *Model) setPendingToolCalls(toolCalls []*aipb.ToolCall) {
	m.pendingToolCalls = toolCalls
	m.recalculateLayout()
}

// acceptToolCalls executes all pending tool calls via their registered handlers.
func (m *Model) acceptToolCalls() tea.Cmd {
	pendingToolCalls := m.pendingToolCalls
	m.pendingToolCalls = nil

	handlerIDToHandler := m.toolHandlerIDToHandler
	ctx := m.ctx
	send := m.send
	allTools := m.allTools()

	// Build a tool name -> tool map for annotation lookup.
	toolNameToTool := map[string]*aipb.Tool{}
	for _, tool := range allTools {
		toolNameToTool[tool.Name] = tool
	}

	go func() {
		for _, toolCall := range pendingToolCalls {
			tool, ok := toolNameToTool[toolCall.Name]
			if !ok {
				send(toolResultMsg{
					ToolCallID: toolCall.Id,
					ToolResult: ai.NewErrorToolResult(toolCall.Name, toolCall.Id, fmt.Errorf("unknown tool: %s", toolCall.Name)),
				})
				continue
			}

			handlerID := tool.GetAnnotations()[tools.ToolHandlerIDAnnotation]
			handler, ok := handlerIDToHandler[handlerID]
			if !ok {
				send(toolResultMsg{
					ToolCallID: toolCall.Id,
					ToolResult: ai.NewErrorToolResult(toolCall.Name, toolCall.Id, fmt.Errorf("no handler for tool %s (handler_id=%s)", toolCall.Name, handlerID)),
				})
				continue
			}

			toolResult, err := handler.HandleToolCall(ctx, toolCall)
			if err != nil {
				send(toolResultMsg{
					ToolCallID: toolCall.Id,
					ToolResult: ai.NewErrorToolResult(toolCall.Name, toolCall.Id, err),
				})
				continue
			}
			send(toolResultMsg{
				ToolCallID: toolCall.Id,
				ToolResult: toolResult,
			})
		}
	}()

	return nil
}

// rejectToolCalls rejects all pending tool calls with the user's message.
func (m *Model) rejectToolCalls(reason string) {
	for _, toolCall := range m.pendingToolCalls {
		errorMessage := fmt.Sprintf("rejected by user: %s", strings.TrimSpace(reason))
		toolMessage := ai.NewToolMessage(ai.NewToolResultBlock(ai.NewErrorToolResult(toolCall.Name, toolCall.Id, fmt.Errorf(errorMessage))))
		m.chat.Metadata.Messages = append(m.chat.Metadata.Messages, &sgptpb.Message{Message: toolMessage})
	}
	m.pendingToolCalls = nil
	m.viewport.SetContent(m.renderMessages())
	m.viewport.GotoBottom()
	m.recalculateLayout()
}

// handleToolResult processes a single tool result and continues streaming if no error.
func (m *Model) handleToolResult(msg toolResultMsg) tea.Cmd {
	toolMessage := ai.NewToolMessage(ai.NewToolResultBlock(msg.ToolResult))
	m.chat.Metadata.Messages = append(m.chat.Metadata.Messages, &sgptpb.Message{Message: toolMessage})

	m.viewport.SetContent(m.renderMessages())
	m.viewport.GotoBottom()

	if msg.ToolResult.GetError() != nil {
		return nil
	}

	m.streaming = true
	m.recalculateLayout()
	return m.startStreaming()
}
