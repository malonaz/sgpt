package session

import (
	"context"
	"sync"
	"testing"
	"time"

	aipb "github.com/malonaz/core/genproto/ai/v1"

	"github.com/malonaz/sgpt/internal/permission"
)

// newReviewSession builds the minimal session needed to exercise the review
// handshake: no store, no registry, no turn loop.
func newReviewSession(ctx context.Context) *Session {
	s := &Session{
		ctx:     ctx,
		policy:  &permission.Policy{},
		eventCh: make(chan Event, 1024),
	}
	s.initReviews(nil)
	return s
}

// newSubAgentSession builds a sub-agent of parent, wired as the launcher does.
func newSubAgentSession(parent *Session) *Session {
	s := &Session{
		ctx:     parent.ctx,
		chat:    &aipb.Chat{Title: "explore"},
		policy:  parent.policy.Child(),
		eventCh: make(chan Event, 1024),
	}
	s.initReviews(parent.reviews)
	return s
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
	if s.policy.Decide(toolCall("call-next", "shell"), false).Mode != permission.ModeAllow {
		t.Fatal("shell was not granted")
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

// A sub-agent's call surfaces in the root chat and is answered there; the
// sub-agent still sees only its own.
func TestSubAgentReviewSurfacesInRoot(t *testing.T) {
	root := newReviewSession(context.Background())
	child := newSubAgentSession(root)

	approvedCh := make(chan bool, 1)
	go func() {
		approved, _ := child.awaitVerdict(child.ctx, toolCall("call-child", "shell"))
		approvedCh <- approved
	}()

	awaitPending(t, root, "call-child")
	if got := root.State(); got != StateAwaitingReview {
		t.Fatalf("root state = %v, want StateAwaitingReview", got)
	}
	escalated := root.EscalatedReviews()
	if len(escalated) != 1 || escalated[0].Origin != "explore" {
		t.Fatalf("escalated = %v, want the sub-agent's call from %q", escalated, "explore")
	}
	if !child.PendingToolCallIDs()["call-child"] || len(child.EscalatedReviews()) != 0 {
		t.Fatal("sub-agent must see its own call, and nothing escalated")
	}

	root.ApproveToolCall("call-child")
	if approved := <-approvedCh; !approved {
		t.Fatal("sub-agent call was not approved from the root")
	}
}

// A sub-agent sees none of the root's reviews, so it cannot answer them.
func TestSubAgentCannotAnswerRootReview(t *testing.T) {
	root := newReviewSession(context.Background())
	child := newSubAgentSession(root)

	approvedCh := make(chan bool, 1)
	go func() {
		approved, _ := root.awaitVerdict(root.ctx, toolCall("call-root", "shell"))
		approvedCh <- approved
	}()
	awaitPending(t, root, "call-root")

	if len(child.PendingToolCallIDs()) != 0 {
		t.Fatalf("sub-agent pending = %v, want empty", child.PendingToolCallIDs())
	}
	child.ApproveToolCall("call-root")
	root.RejectToolCall("call-root", "no")
	if approved := <-approvedCh; approved {
		t.Fatal("root call was approved by the sub-agent")
	}
}

// "Always accept" in the root approves the sub-agent's pending call and
// covers its future ones.
func TestRootGrantCascadesToSubAgent(t *testing.T) {
	root := newReviewSession(context.Background())
	child := newSubAgentSession(root)

	approvedCh := make(chan bool, 1)
	go func() {
		approved, _ := child.awaitVerdict(child.ctx, toolCall("call-child", "shell"))
		approvedCh <- approved
	}()
	awaitPending(t, root, "call-child")

	root.AlwaysApproveTool("shell")
	if approved := <-approvedCh; !approved {
		t.Fatal("sub-agent call was not approved by the root grant")
	}
	if child.policy.Decide(toolCall("call-next", "shell"), false).Mode != permission.ModeAllow {
		t.Fatal("root grant did not cascade to the sub-agent")
	}
}
