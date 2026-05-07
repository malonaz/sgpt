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

	// Separate auto-execute from manual tool calls based on metadata set during streaming.
	var autoToolCalls []*aipb.ToolCall
	var manualToolCalls []*aipb.ToolCall
	for _, toolCall := range toolCalls {
		metadata, err := tools.ParseToolCallMetadata(toolCall)
		if err != nil {
			manualToolCalls = append(manualToolCalls, toolCall)
			continue
		}
		if metadata.GetAutoExecute() {
			autoToolCalls = append(autoToolCalls, toolCall)
		} else {
			manualToolCalls = append(manualToolCalls, toolCall)
		}
	}

	if len(manualToolCalls) > 0 {
		// Execute auto ones, leave manual ones pending for user.
		if len(autoToolCalls) > 0 {
			s.mu.Lock()
			for _, toolCall := range autoToolCalls {
				tools.SetToolCallStatus(toolCall, tools.ToolCallStatusAccepted)
			}
			s.mu.Unlock()

			if err := s.executeToolCallsIntoResults(autoToolCalls, toolNameToTool); err != nil {
				return false, err
			}
		}
		return false, nil
	}

	// All auto-execute: run them all.
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

// executeToolCallsIntoResults executes tool calls and appends a single tool message
// with all results. Blocks until complete.
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

// AcceptToolCalls marks pending tool calls as accepted and executes them.
// Produces a single tool message with all results. Blocks until complete.
func (s *Session) AcceptToolCalls() {
	s.mu.Lock()
	pending := s.pendingToolCallsLocked()
	if len(pending) == 0 {
		s.mu.Unlock()
		return
	}
	for _, toolCall := range pending {
		tools.SetToolCallStatus(toolCall, tools.ToolCallStatusAccepted)
	}
	s.mu.Unlock()

	allToolDefs := s.allTools()
	toolNameToTool := map[string]*aipb.Tool{}
	for _, tool := range allToolDefs {
		toolNameToTool[tool.Name] = tool
	}

	if err := s.executeToolCallsIntoResults(pending, toolNameToTool); err != nil {
		s.emitError(fmt.Errorf("executing tool calls: %w", err))
		s.refresh()
		return
	}

	s.mu.Lock()
	s.streaming = true
	s.mu.Unlock()

	s.runTurn()
}

// RejectToolCalls marks all pending tool calls as rejected and appends a single
// tool message with error results for each.
func (s *Session) RejectToolCalls(reason string) {
	s.mu.Lock()
	pending := s.pendingToolCallsLocked()
	var resultBlocks []*aipb.Block
	for _, toolCall := range pending {
		tools.SetToolCallStatus(toolCall, tools.ToolCallStatusRejected)
		errorMessage := fmt.Sprintf("rejected by user: %s", reason)
		toolResult := ai.NewErrorToolResult(toolCall.Name, toolCall.Id, fmt.Errorf(errorMessage))
		resultBlocks = append(resultBlocks, ai.NewToolResultBlock(toolResult))
	}
	if len(resultBlocks) > 0 {
		toolMessage := ai.NewToolMessage(resultBlocks...)
		s.chat.Metadata.Messages = append(s.chat.Metadata.Messages, &sgptpb.Message{Message: toolMessage})
	}
	s.mu.Unlock()
	s.refresh()
}

// RejectAndResend rejects pending tool calls then starts a new turn.
// Blocks until the turn completes.
func (s *Session) RejectAndResend(reason string) {
	s.RejectToolCalls(reason)

	s.mu.Lock()
	s.streaming = true
	s.mu.Unlock()

	s.runTurn()
}

// executeToolCalls marks pending tool calls as accepted and executes them.
// Blocks until all tool calls complete, then starts a new turn.
func (s *Session) executeToolCalls(pending []*aipb.ToolCall) {
	allToolDefs := s.allTools()
	toolNameToTool := map[string]*aipb.Tool{}
	for _, tool := range allToolDefs {
		toolNameToTool[tool.Name] = tool
	}

	if err := s.executeToolCallsIntoResults(pending, toolNameToTool); err != nil {
		s.emitError(fmt.Errorf("executing tool calls: %w", err))
		s.refresh()
		return
	}

	s.mu.Lock()
	s.streaming = true
	s.mu.Unlock()

	s.runTurn()
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
