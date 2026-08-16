package session

import (
	"context"
	"fmt"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"

	"github.com/malonaz/sgpt/internal/tool"
)

// resolveToolCall is the only place a tool call acquires a result; it is
// idempotent, so eager (mid-stream) and deferred (turn loop) resolution can
// safely overlap.
//
// Eager mode resolves only what needs no human — review-attached results,
// auto-execute and whitelisted tools — and leaves everything else untouched.
// Deferred mode always attaches a terminal result: it awaits the user's
// verdict where required, and a cancelled turn resolves to an error result so
// the history stays valid (providers reject unanswered tool calls outright).
func (s *Session) resolveToolCall(ctx context.Context, toolCall *aipb.ToolCall, eager bool) {
	if toolCall.GetResult() != nil {
		return
	}
	if ctx.Err() != nil {
		if eager {
			return
		}
		toolCall.Result = ai.NewErrorToolResult(toolCall.Name, toolCall.Id,
			fmt.Errorf("turn cancelled by user: this tool call never ran"))
		return
	}

	if eager {
		if !s.registry.Handles(toolCall) {
			return
		}
		// Review attaches display/auto-execute metadata — and, for discovery
		// tools, the result itself — exactly once, when the call first
		// appears in the stream.
		if _, err := s.registry.Review(ctx, toolCall); err != nil {
			// Feed the failure back to the model as a tool result instead of
			// aborting the turn; an unresolved call poisons the history.
			toolCall.Result = ai.NewErrorToolResult(toolCall.Name, toolCall.Id, err)
			return
		}
		if toolCall.GetResult() != nil {
			return
		}
	}

	metadata, err := tool.ParseToolCallMetadata(toolCall)
	if err != nil {
		if eager {
			return
		}
		toolCall.Result = ai.NewErrorToolResult(toolCall.Name, toolCall.Id, err)
		return
	}

	if !metadata.GetAutoExecute() && !s.IsToolAutoAccepted(toolCall.GetName()) {
		if eager {
			// Needs a human: the turn loop awaits the verdict.
			return
		}
		approved, reason := s.awaitVerdict(ctx, toolCall)
		if !approved {
			toolCall.Result = ai.NewErrorToolResult(toolCall.Name, toolCall.Id,
				fmt.Errorf("rejected by user: %s", reason))
			return
		}
	}

	s.executeToolCall(ctx, toolCall)
}

// executeToolCall runs the call through the registry and attaches its
// terminal result; execution failures resolve as error results so the model
// always gets an answer.
func (s *Session) executeToolCall(ctx context.Context, toolCall *aipb.ToolCall) {
	s.setExecutingToolCall(toolCall.GetId())
	s.refresh()
	defer func() {
		s.setExecutingToolCall("")
		s.refresh()
	}()

	toolResult, err := s.registry.Execute(ctx, toolCall)
	if err != nil {
		toolResult = ai.NewErrorToolResult(toolCall.Name, toolCall.Id, err)
	}
	toolCall.Result = toolResult
}

// awaitVerdict blocks the turn goroutine until the user answers for this tool
// call, or the turn is cancelled (treated as a rejection so the call still
// resolves and the history stays valid).
func (s *Session) awaitVerdict(ctx context.Context, toolCall *aipb.ToolCall) (approved bool, reason string) {
	toolCallID := toolCall.GetId()
	verdictCh := make(chan verdict, 1)

	s.mu.Lock()
	s.pendingReviews[toolCallID] = pendingReview{
		toolName:  toolCall.GetName(),
		verdictCh: verdictCh,
	}
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
	case <-ctx.Done():
		return false, "turn cancelled"
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
