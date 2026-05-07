package session

import (
	"context"
	"fmt"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/tools"
)

// processToolCallsAfterStream handles tool calls after the stream completes.
// Auto-execute tool calls are run immediately. Non-auto ones are left pending
// for user accept/reject. Returns true if all tool calls were auto-executed.
func (s *Session) processToolCallsAfterStream(toolCalls []*aipb.ToolCall) (bool, error) {
	allToolDefs := s.allTools()
	toolNameToTool := map[string]*aipb.Tool{}
	for _, tool := range allToolDefs {
		toolNameToTool[tool.Name] = tool
	}

	var autoToolCalls []*aipb.ToolCall
	hasManual := false
	for _, toolCall := range toolCalls {
		metadata, err := tools.ParseToolCallMetadata(toolCall)
		if err != nil {
			hasManual = true
			continue
		}
		if metadata.GetAutoExecute() {
			autoToolCalls = append(autoToolCalls, toolCall)
		} else {
			hasManual = true
		}
	}

	if hasManual {
		// Mark auto ones as accepted but don't execute yet — we need all results
		// in one message, so we wait for user to resolve manual ones.
		s.mu.Lock()
		for _, toolCall := range autoToolCalls {
			tools.SetToolCallStatus(toolCall, tools.ToolCallStatusAccepted)
		}
		s.mu.Unlock()
		return false, nil
	}

	// All auto-execute: run them all and produce a single tool message.
	s.mu.Lock()
	for _, toolCall := range autoToolCalls {
		tools.SetToolCallStatus(toolCall, tools.ToolCallStatusAccepted)
	}
	s.mu.Unlock()

	if err := s.executeToolCallsIntoResults(autoToolCalls, toolNameToTool); err != nil {
		return false, err
	}
	return true, nil
}

// ResolveToolCalls executes all tool calls from the last assistant message based
// on their annotation status. Accepted ones are executed, rejected ones get error
// results. Produces a single tool message with results for ALL tool calls.
func (s *Session) ResolveToolCalls() {
	allToolDefs := s.allTools()
	toolNameToTool := map[string]*aipb.Tool{}
	for _, tool := range allToolDefs {
		toolNameToTool[tool.Name] = tool
	}

	// Collect ALL tool calls from the last assistant message.
	s.mu.Lock()
	messages := s.chat.GetMetadata().GetMessages()
	var allToolCalls []*aipb.ToolCall
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i].GetMessage()
		if message.GetRole() != aipb.Role_ROLE_ASSISTANT {
			continue
		}
		for _, block := range ai.FilterBlocks(message.GetBlocks(), ai.BlockTypeToolCall) {
			allToolCalls = append(allToolCalls, block.GetToolCall())
		}
		break
	}
	s.mu.Unlock()

	var resultBlocks []*aipb.Block
	for _, toolCall := range allToolCalls {
		toolCallStatus := tools.GetToolCallStatus(toolCall)
		switch toolCallStatus {
		case tools.ToolCallStatusRejected:
			reason := tools.GetToolCallRejectionReason(toolCall)
			errorMessage := fmt.Sprintf("rejected by user: %s", reason)
			toolResult := ai.NewErrorToolResult(toolCall.Name, toolCall.Id, fmt.Errorf(errorMessage))
			resultBlocks = append(resultBlocks, ai.NewToolResultBlock(toolResult))

		case tools.ToolCallStatusAccepted:
			toolResult, err := s.executeSingleToolCall(s.ctx, toolCall, toolNameToTool)
			if err != nil {
				toolResult = ai.NewErrorToolResult(toolCall.Name, toolCall.Id, err)
			}
			resultBlocks = append(resultBlocks, ai.NewToolResultBlock(toolResult))

		default:
			toolResult := ai.NewErrorToolResult(toolCall.Name, toolCall.Id, fmt.Errorf("unresolved tool call"))
			resultBlocks = append(resultBlocks, ai.NewToolResultBlock(toolResult))
		}
	}

	s.mu.Lock()
	toolMessage := ai.NewToolMessage(resultBlocks...)
	s.chat.Metadata.Messages = append(s.chat.Metadata.Messages, &sgptpb.Message{Message: toolMessage})
	s.mu.Unlock()

	s.refresh()

	s.mu.Lock()
	s.streaming = true
	s.mu.Unlock()

	s.runTurn()
}

// executeToolCallsIntoResults executes tool calls and appends a single tool message
// with all results. Used for fully-auto turns only.
func (s *Session) executeToolCallsIntoResults(toolCalls []*aipb.ToolCall, toolNameToTool map[string]*aipb.Tool) error {
	var resultBlocks []*aipb.Block
	for _, toolCall := range toolCalls {
		toolResult, err := s.executeSingleToolCall(s.ctx, toolCall, toolNameToTool)
		if err != nil {
			toolResult = ai.NewErrorToolResult(toolCall.Name, toolCall.Id, err)
		}
		resultBlocks = append(resultBlocks, ai.NewToolResultBlock(toolResult))
	}

	s.mu.Lock()
	toolMessage := ai.NewToolMessage(resultBlocks...)
	s.chat.Metadata.Messages = append(s.chat.Metadata.Messages, &sgptpb.Message{Message: toolMessage})
	s.mu.Unlock()

	s.refresh()
	return nil
}

// AcceptToolCalls marks all pending tool calls as accepted via annotations, then resolves.
func (s *Session) AcceptToolCalls() {
	s.mu.Lock()
	pending := s.pendingToolCallsLocked()
	for _, toolCall := range pending {
		tools.SetToolCallStatus(toolCall, tools.ToolCallStatusAccepted)
	}
	s.mu.Unlock()

	if len(pending) == 0 {
		return
	}
	s.ResolveToolCalls()
}

// RejectToolCalls marks all pending tool calls as rejected via annotations, then resolves.
func (s *Session) RejectToolCalls(reason string) {
	s.mu.Lock()
	pending := s.pendingToolCallsLocked()
	for _, toolCall := range pending {
		tools.SetToolCallStatus(toolCall, tools.ToolCallStatusRejected)
		tools.SetToolCallRejectionReason(toolCall, reason)
	}
	s.mu.Unlock()

	if len(pending) == 0 {
		return
	}
	s.ResolveToolCalls()
}

// RejectAndResend rejects pending tool calls then resolves (which starts a new turn).
func (s *Session) RejectAndResend(reason string) {
	s.RejectToolCalls(reason)
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
