// Package review holds the tool calls awaiting the user's verdict. One Queue
// serves a whole chat tree — the chat the user opened and every sub-agent it
// launched — so a sub-agent's calls surface in the root chat and are answered
// there like its own.
package review

import (
	"context"
	"sync"

	aipb "github.com/malonaz/core/genproto/ai/v1"
)

// Verdict is the user's answer to a review.
type Verdict struct {
	Approved bool
	// Reason explains a rejection.
	Reason string
}

// Request is a tool call parked on the user's verdict.
type Request struct {
	ToolCall *aipb.ToolCall
	// Owner identifies the session that made the call.
	Owner uint64
	// Origin is the owning chat's title, shown where the review surfaces.
	Origin string
	// OnChange tells the owner its review began or ended; the queue's own
	// callback tells the root.
	OnChange func()

	verdictCh chan Verdict
}

// Queue is the set of pending reviews, in arrival order. Safe for concurrent
// use: turn goroutines await while the UI answers.
type Queue struct {
	// onChange fires (outside the lock) whenever a review enters or leaves.
	onChange func()

	mu      sync.Mutex
	pending []*Request
}

// NewQueue returns an empty queue notifying onChange on every change.
func NewQueue(onChange func()) *Queue {
	return &Queue{onChange: onChange}
}

// Await parks a request until it is answered or ctx ends; cancellation reads
// as a rejection so the call still resolves.
func (q *Queue) Await(ctx context.Context, request *Request) Verdict {
	request.verdictCh = make(chan Verdict, 1)
	q.mu.Lock()
	q.pending = append(q.pending, request)
	q.mu.Unlock()
	q.notify(request)
	defer q.remove(request)

	select {
	case verdict := <-request.verdictCh:
		return verdict
	case <-ctx.Done():
		return Verdict{Reason: "turn cancelled"}
	}
}

// Pending returns the requests awaiting a verdict, oldest first.
func (q *Queue) Pending() []*Request {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]*Request(nil), q.pending...)
}

// Answer delivers a verdict to the call awaiting it. Reports whether one was.
func (q *Queue) Answer(toolCallID string, verdict Verdict) bool {
	request := q.take(func(candidate *Request) bool { return candidate.ToolCall.GetId() == toolCallID })
	if request == nil {
		return false
	}
	// Buffered and taken exactly once: never blocks the UI goroutine.
	request.verdictCh <- verdict
	return true
}

// remove drops a request that ended without an answer (cancelled turn).
func (q *Queue) remove(request *Request) {
	q.take(func(candidate *Request) bool { return candidate == request })
}

// take removes and returns the first matching request, notifying on change.
func (q *Queue) take(match func(*Request) bool) *Request {
	q.mu.Lock()
	var taken *Request
	for i, candidate := range q.pending {
		if match(candidate) {
			taken = candidate
			q.pending = append(q.pending[:i:i], q.pending[i+1:]...)
			break
		}
	}
	q.mu.Unlock()
	if taken != nil {
		q.notify(taken)
	}
	return taken
}

func (q *Queue) notify(request *Request) {
	if q.onChange != nil {
		q.onChange()
	}
	if request.OnChange != nil {
		request.OnChange()
	}
}
