package session

import (
	"fmt"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"

	"github.com/malonaz/sgpt/internal/debug"
	"github.com/malonaz/sgpt/internal/tool"
)

// processToolCallsAfterStream handles tool calls after the stream completes.
// Auto-execute tool calls run immediately (read-only tools may already carry
// a result from eager execution during streaming). Manual ones are left
// pending for user review. Returns true if everything was auto-executed.
func (s *Session) processToolCallsAfterStream(assistantMessage *aipb.Message, toolCalls []*aipb.ToolCall) (bool, error) {
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
		if metadata.GetAutoExecute() || s.IsToolAutoAccepted(toolCall.GetName()) {
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

	s.executeToolCalls(assistantMessage, executable)
	return true, nil
}

// ResolveToolCalls executes ALL tool calls of the last assistant message —
// accepted ones run, rejected ones get error results — then starts a new turn.
func (s *Session) ResolveToolCalls() {
	s.mu.Lock()
	var assistantMessage *aipb.Message
	var toolCalls []*aipb.ToolCall
	for i := len(s.messages) - 1; i >= 0; i-- {
		message := s.messages[i]
		if message.GetRole() != aipb.Role_ROLE_ASSISTANT {
			continue
		}
		assistantMessage = message
		for _, block := range ai.FilterBlocks(message.GetBlocks(), ai.BlockTypeToolCall) {
			toolCalls = append(toolCalls, block.GetToolCall())
		}
		break
	}
	s.mu.Unlock()

	s.executeToolCalls(assistantMessage, toolCalls)

	s.setState(StateStreaming)
	s.refresh()
	s.runTurn()
}

// executeToolCalls resolves tool calls strictly sequentially, emitting a
// refresh before and after each call so the UI shows the in-flight call and
// renders each result the moment it lands — not all at once at the end.
// Verdicts and results are persisted back onto the assistant message, and the
// tool results are queued as input for the next generation.
func (s *Session) executeToolCalls(assistantMessage *aipb.Message, toolCalls []*aipb.ToolCall) {
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

	// Persist the review state (statuses, results, metadata) living inside
	// the assistant message's tool call blocks.
	if assistantMessage.GetName() != "" {
		if _, err := s.store.UpdateMessage(s.ctx, assistantMessage, "blocks"); err != nil {
			s.emitError(fmt.Errorf("persisting tool call results: %w", err))
		}
	}

	toolMessage := ai.NewToolMessage(resultBlocks...)
	s.mu.Lock()
	s.messages = append(s.messages, toolMessage)
	// The tool message is persisted server-side as input to the next turn.
	s.pendingInputMessages = append(s.pendingInputMessages, toolMessage)
	s.invalidatePrice()
	s.mu.Unlock()
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

// ---- Review verdicts ----
//
// Tool call blocks are shared with the streaming/turn goroutines, so every
// verdict mutation goes through the session lock. The TUI never writes to a
// tool call proto itself.

// AcceptToolCall marks a single call accepted.
func (s *Session) AcceptToolCall(toolCall *aipb.ToolCall) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tool.SetToolCallStatus(toolCall, tool.ToolCallStatusAccepted)
}

// RejectToolCall marks a single call rejected, recording the user's reason.
func (s *Session) RejectToolCall(toolCall *aipb.ToolCall, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tool.SetToolCallStatus(toolCall, tool.ToolCallStatusRejected)
	tool.SetToolCallRejectionReason(toolCall, reason)
}

// AcceptAllPendingToolCalls accepts every call still awaiting a verdict.
func (s *Session) AcceptAllPendingToolCalls() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, toolCall := range s.pendingToolCallsLocked() {
		tool.SetToolCallStatus(toolCall, tool.ToolCallStatusAccepted)
	}
}

// AlwaysAcceptTool whitelists the call's tool for the rest of the session and
// accepts its pending siblings now.
func (s *Session) AlwaysAcceptTool(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.autoAcceptedToolNameSet[name] = true
	for _, toolCall := range s.pendingToolCallsLocked() {
		if toolCall.GetName() == name {
			tool.SetToolCallStatus(toolCall, tool.ToolCallStatusAccepted)
		}
	}
}

// ReopenToolCall returns an unexecuted call to pending so the user can change
// a verdict before the turn resolves. Reports whether it was reopened.
func (s *Session) ReopenToolCall(toolCall *aipb.ToolCall) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if toolCall.GetResult() != nil {
		return false
	}
	switch tool.GetToolCallStatus(toolCall) {
	case tool.ToolCallStatusAccepted, tool.ToolCallStatusRejected:
		tool.SetToolCallStatus(toolCall, tool.ToolCallStatusPending)
		return true
	}
	return false
}

// ToolCallStatus reads a call's review status under the lock.
func (s *Session) ToolCallStatus(toolCall *aipb.ToolCall) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return tool.GetToolCallStatus(toolCall)
}
