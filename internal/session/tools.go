package session

import (
	"fmt"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/debug"
	"github.com/malonaz/sgpt/internal/tools"
)

// processToolCallsAfterStream handles tool calls after the stream completes.
// Auto-execute tool calls are run immediately. Non-auto ones are left pending
// for user accept/reject. Returns true if all tool calls were auto-executed.
func (s *Session) processToolCallsAfterStream(toolCalls []*aipb.ToolCall) (bool, error) {
	var autoToolCalls []*aipb.ToolCall
	var prePopulatedToolCalls []*aipb.ToolCall
	hasManual := false
	for _, toolCall := range toolCalls {
		debug.LogProto(toolCall.GetName(), toolCall)
		if toolCall.GetResult() != nil {
			prePopulatedToolCalls = append(prePopulatedToolCalls, toolCall)
			continue
		}
		metadata, err := tools.ParseToolCallMetadata(toolCall)
		if err != nil {
			return false, fmt.Errorf("parsing tool call metadata: %w", err)
		}
		if metadata.GetAutoExecute() {
			autoToolCalls = append(autoToolCalls, toolCall)
		} else {
			hasManual = true
		}
	}

	s.mu.Lock()
	for _, toolCall := range autoToolCalls {
		tools.SetToolCallStatus(toolCall, tools.ToolCallStatusAccepted)
	}
	s.mu.Unlock()

	if hasManual {
		// Manual calls stay pending; pre-accepted auto calls execute alongside
		// them once the user resolves the pending ones.
		return false, nil
	}

	s.executeToolCalls(append(prePopulatedToolCalls, autoToolCalls...))
	return true, nil
}

// ResolveToolCalls produces a single tool message with results for ALL tool
// calls of the last assistant message: accepted ones are executed, rejected
// ones get error results. Then starts a new turn.
func (s *Session) ResolveToolCalls() {
	s.mu.Lock()
	messages := s.chat.GetMetadata().GetMessages()
	var toolCalls []*aipb.ToolCall
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i].GetMessage()
		if message.GetRole() != aipb.Role_ROLE_ASSISTANT {
			continue
		}
		for _, block := range ai.FilterBlocks(message.GetBlocks(), ai.BlockTypeToolCall) {
			toolCalls = append(toolCalls, block.GetToolCall())
		}
		break
	}
	s.mu.Unlock()

	resultBlocks := make([]*aipb.Block, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		resultBlocks = append(resultBlocks, ai.NewToolResultBlock(s.resolveToolCall(toolCall)))
	}
	s.appendToolMessage(resultBlocks)
	s.refresh()

	s.mu.Lock()
	s.streaming = true
	s.mu.Unlock()

	s.runTurn()
}

// resolveToolCall produces a result for a reviewed tool call based on its status.
func (s *Session) resolveToolCall(toolCall *aipb.ToolCall) *aipb.ToolResult {
	if toolCall.GetResult() != nil {
		return toolCall.GetResult()
	}
	switch tools.GetToolCallStatus(toolCall) {
	case tools.ToolCallStatusRejected:
		reason := tools.GetToolCallRejectionReason(toolCall)
		return ai.NewErrorToolResult(toolCall.Name, toolCall.Id, fmt.Errorf("rejected by user: %s", reason))
	case tools.ToolCallStatusAccepted:
		toolResult, err := s.registry.Execute(s.ctx, toolCall)
		if err != nil {
			return ai.NewErrorToolResult(toolCall.Name, toolCall.Id, err)
		}
		return toolResult
	default:
		return ai.NewErrorToolResult(toolCall.Name, toolCall.Id, fmt.Errorf("unresolved tool call"))
	}
}

// executeToolCalls executes tool calls and appends a single tool message with
// all results. Used for fully-auto turns only.
func (s *Session) executeToolCalls(toolCalls []*aipb.ToolCall) {
	resultBlocks := make([]*aipb.Block, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		if toolCall.GetResult() != nil {
			resultBlocks = append(resultBlocks, ai.NewToolResultBlock(toolCall.GetResult()))
			continue
		}
		toolResult, err := s.registry.Execute(s.ctx, toolCall)
		if err != nil {
			toolResult = ai.NewErrorToolResult(toolCall.Name, toolCall.Id, err)
		}
		resultBlocks = append(resultBlocks, ai.NewToolResultBlock(toolResult))
	}
	s.appendToolMessage(resultBlocks)
	s.refresh()
}

func (s *Session) appendToolMessage(resultBlocks []*aipb.Block) {
	s.mu.Lock()
	defer s.mu.Unlock()
	toolMessage := ai.NewToolMessage(resultBlocks...)
	s.chat.Metadata.Messages = append(s.chat.Metadata.Messages, &sgptpb.Message{Message: toolMessage})
}
