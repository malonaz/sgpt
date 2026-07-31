package session

import (
	"fmt"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/debug"
	"github.com/malonaz/sgpt/internal/tool"
)

// processToolCallsAfterStream handles tool calls after the stream completes.
// Auto-execute tool calls run immediately (read-only tools may already carry
// a result from eager execution during streaming). Manual ones are left
// pending for user review. Returns true if everything was auto-executed.
func (s *Session) processToolCallsAfterStream(toolCalls []*aipb.ToolCall) (bool, error) {
	var executable []*aipb.ToolCall
	hasManual := false
	for _, toolCall := range toolCalls {
		debug.LogProto(toolCall.GetName(), toolCall)
		if toolCall.GetResult() != nil {
			// Already executed — eagerly during streaming, or server-side.
			executable = append(executable, toolCall)
			continue
		}
		metadata, err := tool.ParseToolCallMetadata(toolCall)
		if err != nil {
			// Review failed or never ran; resolve with an error result so the
			// model can react rather than killing the turn.
			toolCall.Result = ai.NewErrorToolResult(toolCall.Name, toolCall.Id, err)
			tool.SetToolCallStatus(toolCall, tool.ToolCallStatusFailed)
			executable = append(executable, toolCall)
			continue
		}
		if metadata.GetAutoExecute() {
			tool.SetToolCallStatus(toolCall, tool.ToolCallStatusAccepted)
			executable = append(executable, toolCall)
		} else {
			hasManual = true
		}
	}

	if hasManual {
		// Pre-accepted auto calls execute alongside the manual ones once the
		// user resolves every pending review.
		s.setState(StateAwaitingReview)
		return false, nil
	}

	s.executeToolCalls(executable)
	return true, nil
}

// ResolveToolCalls executes ALL tool calls of the last assistant message —
// accepted ones run, rejected ones get error results — then starts a new turn.
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

	s.executeToolCalls(toolCalls)

	s.setState(StateStreaming)
	s.refresh()
	s.runTurn()
}

// executeToolCalls resolves tool calls strictly sequentially, emitting a
// refresh before and after each call so the UI shows the in-flight call and
// renders each result the moment it lands — not all at once at the end.
func (s *Session) executeToolCalls(toolCalls []*aipb.ToolCall) {
	if len(toolCalls) == 0 {
		return
	}
	s.setState(StateExecutingTools)
	s.refresh()

	resultBlocks := make([]*aipb.Block, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		toolResult := toolCall.GetResult()
		if toolResult == nil {
			s.setExecutingToolCall(toolCall.GetId())
			s.refresh()
			toolResult = s.resolveToolCall(toolCall)
			// Attach the result to its call so the UI renders the
			// request/response pair adjacently and the pairing persists.
			toolCall.Result = toolResult
			s.setExecutingToolCall("")
			s.refresh()
		}
		resultBlocks = append(resultBlocks, ai.NewToolResultBlock(toolResult))
	}
	s.appendToolMessage(resultBlocks)
	s.refresh()
}

// resolveToolCall produces a result for a reviewed tool call based on its status.
func (s *Session) resolveToolCall(toolCall *aipb.ToolCall) *aipb.ToolResult {
	switch tool.GetToolCallStatus(toolCall) {
	case tool.ToolCallStatusRejected:
		reason := tool.GetToolCallRejectionReason(toolCall)
		return ai.NewErrorToolResult(toolCall.Name, toolCall.Id, fmt.Errorf("rejected by user: %s", reason))
	case tool.ToolCallStatusAccepted:
		toolResult, err := s.registry.Execute(s.ctx, toolCall)
		if err != nil {
			return ai.NewErrorToolResult(toolCall.Name, toolCall.Id, err)
		}
		return toolResult
	default:
		return ai.NewErrorToolResult(toolCall.Name, toolCall.Id, fmt.Errorf("unresolved tool call"))
	}
}

func (s *Session) appendToolMessage(resultBlocks []*aipb.Block) {
	s.mu.Lock()
	defer s.mu.Unlock()
	toolMessage := ai.NewToolMessage(resultBlocks...)
	s.chat.Metadata.Messages = append(s.chat.Metadata.Messages, &sgptpb.Message{Message: toolMessage})
}
