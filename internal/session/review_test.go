package session

import (
	"context"
	"sync"
	"testing"
	"time"

	aipb "github.com/malonaz/core/genproto/ai/v1"
)

// newReviewSession builds the minimal session needed to exercise the review
// handshake: no store, no registry, no turn loop.
func newReviewSession(ctx context.Context) *Session {
	return &Session{
		ctx:                     ctx,
		pendingReviews:          map[string]pendingReview{},
		autoAcceptedToolNameSet: map[string]bool{},
		eventCh:                 make(chan Event, 1024),
	}
}

func toolCall(id, name string) *aipb.ToolCall {
	return &aipb.ToolCall{Id: id, Name: name}
}

// awaitPending spins until the review for id is registered, so tests never
// answer before the turn goroutine has parked.
func awaitPending(t *testing.T, s *Session, id string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s.PendingToolCallIDs()[id] {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("tool call %q never became pending", id)
}

func TestAwaitVerdictApprove(t *testing.T) {
	s := newReviewSession(context.Background())

	type result struct {
		approved bool
		reason   string
	}
	resultCh := make(chan result, 1)
	go func() {
		approved, reason := s.awaitVerdict(s.ctx, toolCall("call-1", "shell"))
		resultCh <- result{approved, reason}
	}()

	awaitPending(t, s, "call-1")
	if got := s.State(); got != StateAwaitingReview {
		t.Fatalf("state = %v, want StateAwaitingReview", got)
	}
	s.ApproveToolCall("call-1")

	got := <-resultCh
	if !got.approved {
		t.Fatalf("approved = false, want true")
	}
	if pending := s.PendingToolCallIDs(); len(pending) != 0 {
		t.Fatalf("pending = %v, want empty after verdict", pending)
	}
}

func TestAwaitVerdictReject(t *testing.T) {
	s := newReviewSession(context.Background())

	reasonCh := make(chan string, 1)
	go func() {
		approved, reason := s.awaitVerdict(s.ctx, toolCall("call-1", "shell"))
		if approved {
			t.Error("approved = true, want false")
		}
		reasonCh <- reason
	}()

	awaitPending(t, s, "call-1")
	s.RejectToolCall("call-1", "too risky")

	if got := <-reasonCh; got != "too risky" {
		t.Fatalf("reason = %q, want %q", got, "too risky")
	}
}

// A cancelled session must unblock the turn goroutine, not deadlock it.
func TestAwaitVerdictCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := newReviewSession(ctx)

	doneCh := make(chan bool, 1)
	go func() {
		approved, _ := s.awaitVerdict(s.ctx, toolCall("call-1", "shell"))
		doneCh <- approved
	}()

	awaitPending(t, s, "call-1")
	cancel()

	select {
	case approved := <-doneCh:
		if approved {
			t.Fatal("approved = true on cancellation, want false")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("awaitVerdict did not unblock on cancellation")
	}
}

// Answering an unknown ID must be a no-op, not a panic or a blocked send.
func TestAnswerVerdictUnknownID(t *testing.T) {
	s := newReviewSession(context.Background())
	if s.answerVerdict("nope", verdict{approved: true}) {
		t.Fatal("answerVerdict reported a waiting review for an unknown id")
	}
}

// AlwaysApproveTool must approve only the named tool's pending call and
// whitelist it for subsequent calls.
func TestAlwaysApproveToolScopedToName(t *testing.T) {
	s := newReviewSession(context.Background())

	shellCh := make(chan bool, 1)
	go func() {
		approved, _ := s.awaitVerdict(s.ctx, toolCall("call-shell", "shell"))
		shellCh <- approved
	}()
	diffCh := make(chan bool, 1)
	go func() {
		approved, _ := s.awaitVerdict(s.ctx, toolCall("call-diff", "diff"))
		diffCh <- approved
	}()

	awaitPending(t, s, "call-shell")
	awaitPending(t, s, "call-diff")

	s.AlwaysApproveTool("shell")

	if approved := <-shellCh; !approved {
		t.Fatal("shell call was not approved")
	}
	if !s.IsToolAutoAccepted("shell") {
		t.Fatal("shell was not whitelisted")
	}
	// The diff call must still be waiting.
	if pending := s.PendingToolCallIDs(); !pending["call-diff"] {
		t.Fatalf("pending = %v, want call-diff still awaiting review", pending)
	}
	s.RejectToolCall("call-diff", "no")
	if approved := <-diffCh; approved {
		t.Fatal("diff call was approved, want rejected")
	}
}

// The UI polls PendingToolCallIDs on every render while the turn goroutine
// registers and clears reviews — this is the interaction that used to race.
func TestConcurrentReviewAndRender(t *testing.T) {
	s := newReviewSession(context.Background())

	var wg sync.WaitGroup
	stopCh := make(chan struct{})

	wg.Add(1)
	go func() { // renderer
		defer wg.Done()
		for {
			select {
			case <-stopCh:
				return
			default:
				_ = s.PendingToolCallIDs()
				_ = s.State()
			}
		}
	}()

	for i := 0; i < 200; i++ {
		id := "call"
		doneCh := make(chan struct{})
		go func() {
			s.awaitVerdict(s.ctx, toolCall(id, "shell"))
			close(doneCh)
		}()
		awaitPending(t, s, id)
		s.ApproveToolCall(id)
		<-doneCh
	}

	close(stopCh)
	wg.Wait()
}
