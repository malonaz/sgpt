package session

import (
	"context"
	"fmt"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"

	"github.com/malonaz/sgpt/internal/permission"
	"github.com/malonaz/sgpt/internal/review"
	"github.com/malonaz/sgpt/internal/tool"
)

// resolveToolCall is the only place a tool call acquires a result; it is
// idempotent, so eager (mid-stream) and deferred (turn loop) resolution can
// safely overlap.
//
// Eager mode resolves only what needs no human — review-attached results,
// calls the policy allows or denies — and leaves everything else untouched.
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

	switch decision := s.policy.Decide(toolCall, metadata.GetAutoExecute()); decision.Mode {
	case permission.ModeDeny:
		// Refused without a human: the model is told which rule, so it can
		// route around it instead of retrying.
		toolCall.Result = ai.NewErrorToolResult(toolCall.Name, toolCall.Id,
			fmt.Errorf("denied by permission policy (%s): this tool call never ran", decision.Reason()))
		return
	case permission.ModeReview:
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

// initReviews joins the launcher's review queue, or starts a new tree whose
// reviews — sub-agents' included — surface in this chat.
func (s *Session) initReviews(reviews *review.Queue) {
	s.id = sessionIDs.Add(1)
	s.reviews = reviews
	if s.reviews == nil {
		s.reviews = review.NewQueue(s.refresh)
		s.reviewsRoot = true
	}
}

// awaitVerdict blocks the turn goroutine until the user answers for this tool
// call — here or, for a sub-agent, in the root chat — or the turn is
// cancelled (treated as a rejection so the call still resolves and the
// history stays valid).
func (s *Session) awaitVerdict(ctx context.Context, toolCall *aipb.ToolCall) (approved bool, reason string) {
	verdict := s.reviews.Await(ctx, &review.Request{
		ToolCall: toolCall,
		Owner:    s.id,
		Origin:   s.Chat().GetTitle(),
		OnChange: s.refresh,
	})
	return verdict.Approved, verdict.Reason
}

// visibleReviews are the reviews this chat answers: all of the tree's in the
// root, only its own in a sub-agent.
func (s *Session) visibleReviews() []*review.Request {
	pending := s.reviews.Pending()
	if s.reviewsRoot {
		return pending
	}
	own := pending[:0]
	for _, request := range pending {
		if request.Owner == s.id {
			own = append(own, request)
		}
	}
	return own
}

// answerVerdict delivers a verdict to a review this chat answers. Reports
// whether one was waiting on this ID.
func (s *Session) answerVerdict(toolCallID string, verdict review.Verdict) bool {
	if !s.PendingToolCallIDs()[toolCallID] {
		return false
	}
	return s.reviews.Answer(toolCallID, verdict)
}

// ---- Review API (called from the UI goroutine) ----

// PendingReviews returns the reviews this chat answers, oldest first.
func (s *Session) PendingReviews() []*review.Request {
	return s.visibleReviews()
}

// PendingToolCallIDs returns the tool calls currently awaiting a verdict.
func (s *Session) PendingToolCallIDs() map[string]bool {
	requests := s.visibleReviews()
	pending := make(map[string]bool, len(requests))
	for _, request := range requests {
		pending[request.ToolCall.GetId()] = true
	}
	return pending
}

// EscalatedReviews returns the sub-agents' calls awaiting a verdict in this
// chat; they live in other chats' histories, so the UI renders them apart.
func (s *Session) EscalatedReviews() []*review.Request {
	var escalated []*review.Request
	for _, request := range s.visibleReviews() {
		if request.Owner != s.id {
			escalated = append(escalated, request)
		}
	}
	return escalated
}

// ReviewsEscalate reports whether this chat's reviews also surface in a
// root chat — a sub-agent's do.
func (s *Session) ReviewsEscalate() bool {
	return !s.reviewsRoot
}

// ApproveToolCall approves a call awaiting review; it executes immediately.
func (s *Session) ApproveToolCall(toolCallID string) {
	s.answerVerdict(toolCallID, review.Verdict{Approved: true})
}

// RejectToolCall rejects a call awaiting review, recording the user's reason.
func (s *Session) RejectToolCall(toolCallID, reason string) {
	s.answerVerdict(toolCallID, review.Verdict{Reason: reason})
}

// ApproveAllToolCalls approves every call currently awaiting review. Calls are
// executed one at a time by their turn goroutines, in call order.
func (s *Session) ApproveAllToolCalls() {
	for toolCallID := range s.PendingToolCallIDs() {
		s.ApproveToolCall(toolCallID)
	}
}

// AlwaysApproveTool grants a tool for the rest of the session (and the
// sub-agents it launches), so its future calls skip review, and approves any
// call of it awaiting review here right now.
func (s *Session) AlwaysApproveTool(name string) {
	s.policy.Grant(name)
	for _, request := range s.visibleReviews() {
		if request.ToolCall.GetName() == name {
			s.ApproveToolCall(request.ToolCall.GetId())
		}
	}
}
