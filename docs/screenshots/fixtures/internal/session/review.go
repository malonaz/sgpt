package session

import "context"

// pendingReview is a tool call awaiting a verdict: the tool's name (so the
// user can whitelist it) and the channel its turn goroutine is blocked on.
type pendingReview struct {
	toolName string
	verdicts chan verdict
}

// awaitVerdict blocks the turn until the UI answers for toolCallID.
func (s *Session) awaitVerdict(ctx context.Context, toolCallID string) (verdict, bool) {
	s.mu.Lock()
	review, ok := s.pendingReviews[toolCallID]
	s.mu.Unlock()
	if !ok {
		return verdict{}, false
	}
	select {
	case v := <-review.verdicts:
		return v, true
	case <-ctx.Done():
		return verdict{}, false
	}
}
