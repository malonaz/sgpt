package session

import (
	"fmt"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"

	"github.com/malonaz/sgpt/internal/debug"
	"github.com/malonaz/sgpt/internal/tool"
)

// executeToolCalls resolves every tool call of the assistant message strictly
// sequentially, then queues the results as input for the next generation.
//
// Review is a blocking await inside this loop: a call needing approval parks
// the turn goroutine until the user answers (see awaitVerdict). There is no
// review state to track — a call is "pending" precisely while the loop is
// waiting on it, and a verdict produces a terminal result immediately.
func (s *Session) executeToolCalls(assistantMessage *aipb.Message, toolCalls []*aipb.ToolCall) {
	if len(toolCalls) == 0 {
		return
	}

	resultBlocks := make([]*aipb.Block, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		debug.LogProto(toolCall.GetName(), toolCall)
		toolResult := toolCall.GetResult()
		if toolResult == nil {
			toolResult = s.resolveToolCall(toolCall)
			// Attach the result to its call so the UI renders the
			// request/response pair adjacently and the pairing persists.
			toolCall.Result = toolResult
			s.refresh()
		}
		resultBlocks = append(resultBlocks, ai.NewToolResultBlock(toolResult))
	}

	// Persist the results living inside the assistant message's tool call blocks.
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

// resolveToolCall produces the terminal result for one tool call: it awaits
// user approval when required, then executes. Never returns nil.
func (s *Session) resolveToolCall(toolCall *aipb.ToolCall) *aipb.ToolResult {
	metadata, err := tool.ParseToolCallMetadata(toolCall)
	if err != nil {
		// Review never ran (e.g. unparseable arguments); feed the failure back
		// to the model rather than killing the turn.
		return ai.NewErrorToolResult(toolCall.Name, toolCall.Id, err)
	}

	if !metadata.GetAutoExecute() && !s.IsToolAutoAccepted(toolCall.GetName()) {
		approved, reason := s.awaitVerdict(toolCall)
		if !approved {
			return ai.NewErrorToolResult(toolCall.Name, toolCall.Id,
				fmt.Errorf("rejected by user: %s", reason))
		}
	}

	s.setState(StateExecutingTools)
	s.setExecutingToolCall(toolCall.GetId())
	s.refresh()
	defer func() {
		s.setExecutingToolCall("")
		s.refresh()
	}()

	toolResult, err := s.registry.Execute(s.ctx, toolCall)
	if err != nil {
		return ai.NewErrorToolResult(toolCall.Name, toolCall.Id, err)
	}
	return toolResult
}

// awaitVerdict blocks the turn goroutine until the user answers for this tool
// call, or the session is cancelled (treated as a rejection so the call still
// resolves and the history stays valid).
func (s *Session) awaitVerdict(toolCall *aipb.ToolCall) (approved bool, reason string) {
	toolCallID := toolCall.GetId()
	verdictCh := make(chan verdict, 1)

	s.mu.Lock()
	s.pendingReviews[toolCallID] = pendingReview{
		toolName:  toolCall.GetName(),
		verdictCh: verdictCh,
	}
	s.state = StateAwaitingReview
	s.mu.Unlock()
	s.refresh()

	defer func() {
		s.mu.Lock()
		delete(s.pendingReviews, toolCallID)
		s.mu.Unlock()
	}()

	select {
	case answer := <-verdictCh:
		return answer.approved, answer.reason
	case <-s.ctx.Done():
		return false, "session cancelled"
	}
}

// answerVerdict delivers a verdict to the awaiting turn goroutine. Reports
// whether a review was actually waiting on this ID.
func (s *Session) answerVerdict(toolCallID string, answer verdict) bool {
	s.mu.Lock()
	review, ok := s.pendingReviews[toolCallID]
	if ok {
		// Buffered channel, and awaitVerdict is the only receiver, so this
		// never blocks the caller (the UI goroutine).
		review.verdictCh <- answer
		delete(s.pendingReviews, toolCallID)
	}
	s.mu.Unlock()
	return ok
}

// ---- Review API (called from the UI goroutine) ----

// PendingToolCallIDs returns the tool calls currently awaiting a verdict.
func (s *Session) PendingToolCallIDs() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	pending := make(map[string]bool, len(s.pendingReviews))
	for toolCallID := range s.pendingReviews {
		pending[toolCallID] = true
	}
	return pending
}

// ApproveToolCall approves a call awaiting review; it executes immediately.
func (s *Session) ApproveToolCall(toolCallID string) {
	s.answerVerdict(toolCallID, verdict{approved: true})
}

// RejectToolCall rejects a call awaiting review, recording the user's reason.
func (s *Session) RejectToolCall(toolCallID, reason string) {
	s.answerVerdict(toolCallID, verdict{reason: reason})
}

// ApproveAllToolCalls approves every call currently awaiting review. Calls are
// executed one at a time by the turn goroutine, in call order.
func (s *Session) ApproveAllToolCalls() {
	for toolCallID := range s.PendingToolCallIDs() {
		s.ApproveToolCall(toolCallID)
	}
}

// AlwaysApproveTool whitelists a tool for the rest of the session, so its
// future calls skip review, and approves any call awaiting review right now.
func (s *Session) AlwaysApproveTool(name string) {
	s.mu.Lock()
	s.autoAcceptedToolNameSet[name] = true
	var toolCallIDs []string
	for toolCallID, review := range s.pendingReviews {
		if review.toolName == name {
			toolCallIDs = append(toolCallIDs, toolCallID)
		}
	}
	s.mu.Unlock()
	for _, toolCallID := range toolCallIDs {
		s.ApproveToolCall(toolCallID)
	}
}
