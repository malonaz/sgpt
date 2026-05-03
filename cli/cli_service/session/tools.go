package session

import (
	"context"
	"fmt"
	"strings"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/tools"
)

// handleToolCalls inspects tool calls and determines if all can be auto-executed.
// Returns true if all tool calls should be auto-executed.
func (s *Session) handleToolCalls(toolCalls []*aipb.ToolCall) (bool, error) {
	allToolDefs := s.allTools()
	toolNameToTool := map[string]*aipb.Tool{}
	for _, tool := range allToolDefs {
		toolNameToTool[tool.Name] = tool
	}

	autoExecuteAll := true
	for _, toolCall := range toolCalls {
		tool, ok := toolNameToTool[toolCall.Name]
		if !ok {
			autoExecuteAll = false
			continue
		}
		handlerID := tool.GetAnnotations()[tools.ToolHandlerIDAnnotation]
		handler, ok := s.toolHandlerIDToHandler[handlerID]
		if !ok {
			autoExecuteAll = false
			continue
		}
		handleResult, err := handler.HandleToolCall(s.ctx, toolCall)
		if err != nil {
			return false, fmt.Errorf("handling tool call %q: %w", toolCall.Name, err)
		}
		if !handleResult.AutoExecute {
			autoExecuteAll = false
		}
	}

	return autoExecuteAll, nil
}

// executeToolCalls marks pending tool calls as accepted and executes them.
// Blocks until all tool calls complete, then starts a new turn.
func (s *Session) executeToolCalls(pending []*aipb.ToolCall) {
	if err := s.executeToolCallsBlocking(pending); err != nil {
		s.emitError(fmt.Errorf("executing tool calls: %w", err))
		s.refresh()
		return
	}

	s.mu.Lock()
	s.streaming = true
	s.mu.Unlock()

	s.runTurn()
}

// executeToolCallsBlocking processes each tool call sequentially. Blocks until complete.
func (s *Session) executeToolCallsBlocking(pending []*aipb.ToolCall) error {
	allToolDefs := s.allTools()
	toolNameToTool := map[string]*aipb.Tool{}
	for _, tool := range allToolDefs {
		toolNameToTool[tool.Name] = tool
	}

	for _, toolCall := range pending {
		toolResult, err := s.executeSingleToolCall(s.ctx, toolCall, toolNameToTool)
		if err != nil {
			toolResult = ai.NewErrorToolResult(toolCall.Name, toolCall.Id, err)
		}

		s.mu.Lock()
		toolMessage := ai.NewToolMessage(ai.NewToolResultBlock(toolResult))
		s.chat.Metadata.Messages = append(s.chat.Metadata.Messages, &sgptpb.Message{Message: toolMessage})
		s.mu.Unlock()

		s.refresh()

		if toolResult.GetError() != nil {
			return fmt.Errorf("tool %q returned error: %s", toolCall.Name, toolResult.GetError().GetMessage())
		}
	}
	return nil
}

// executeSingleToolCall dispatches a single tool call to its handler.
func (s *Session) executeSingleToolCall(ctx context.Context, toolCall *aipb.ToolCall, toolNameToTool map[string]*aipb.Tool) (*aipb.ToolResult, error) {
	tool, ok := toolNameToTool[toolCall.Name]
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", toolCall.Name)
	}
	handlerID := tool.GetAnnotations()[tools.ToolHandlerIDAnnotation]
	handler, ok := s.toolHandlerIDToHandler[handlerID]
	if !ok {
		return nil, fmt.Errorf("no handler for tool %s (handler_id=%s)", toolCall.Name, handlerID)
	}
	toolResult, err := handler.ProcessToolCall(ctx, toolCall)
	if err != nil {
		return nil, fmt.Errorf("processing tool call %q: %w", toolCall.Name, err)
	}
	return toolResult, nil
}

// RejectToolCalls exported version for use when user explicitly accepts
// after being prompted (non-auto-execute path).
func (s *Session) rejectToolCallsLocked(reason string) {
	pending := s.pendingToolCallsLocked()
	for _, toolCall := range pending {
		tools.SetToolCallStatus(toolCall, tools.ToolCallStatusRejected)
		errorMessage := fmt.Sprintf("rejected by user: %s", strings.TrimSpace(reason))
		toolMessage := ai.NewToolMessage(ai.NewToolResultBlock(ai.NewErrorToolResult(toolCall.Name, toolCall.Id, fmt.Errorf(errorMessage))))
		s.chat.Metadata.Messages = append(s.chat.Metadata.Messages, &sgptpb.Message{Message: toolMessage})
	}
}
