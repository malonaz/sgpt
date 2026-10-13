package review

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	aipb "github.com/malonaz/core/genproto/ai/v1"
)

// awaitPending spins until n requests are parked, so tests never answer
// before the awaiting goroutine has registered.
func awaitPending(t *testing.T, q *Queue, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(q.Pending()) == n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("pending = %d, want %d", len(q.Pending()), n)
}

func await(q *Queue, ctx context.Context, request *Request) <-chan Verdict {
	verdictCh := make(chan Verdict, 1)
	go func() { verdictCh <- q.Await(ctx, request) }()
	return verdictCh
}

func TestAnswerDeliversOnceInArrivalOrder(t *testing.T) {
	var rootChanges, ownerChanges atomic.Int32
	q := NewQueue(func() { rootChanges.Add(1) })
	first := await(q, context.Background(), &Request{ToolCall: &aipb.ToolCall{Id: "a"}, Owner: 1, OnChange: func() { ownerChanges.Add(1) }})
	awaitPending(t, q, 1)
	second := await(q, context.Background(), &Request{ToolCall: &aipb.ToolCall{Id: "b"}, Owner: 2})
	awaitPending(t, q, 2)

	if got := q.Pending()[0].ToolCall.GetId(); got != "a" {
		t.Fatalf("oldest = %q, want a", got)
	}
	if !q.Answer("a", Verdict{Approved: true}) {
		t.Fatal("Answer(a) found nothing")
	}
	if q.Answer("a", Verdict{}) {
		t.Fatal("Answer(a) delivered twice")
	}
	if verdict := <-first; !verdict.Approved {
		t.Fatal("a was not approved")
	}
	q.Answer("b", Verdict{Reason: "no"})
	if verdict := <-second; verdict.Approved || verdict.Reason != "no" {
		t.Fatalf("b verdict = %+v, want rejected with reason", verdict)
	}
	// Enter and leave for each request on the root; only a's on its owner.
	if got := rootChanges.Load(); got != 4 {
		t.Errorf("root changes = %d, want 4", got)
	}
	if got := ownerChanges.Load(); got != 2 {
		t.Errorf("owner changes = %d, want 2", got)
	}
}

func TestCancellationRejectsAndRemoves(t *testing.T) {
	q := NewQueue(nil)
	ctx, cancel := context.WithCancel(context.Background())
	verdictCh := await(q, ctx, &Request{ToolCall: &aipb.ToolCall{Id: "a"}})
	awaitPending(t, q, 1)
	cancel()
	if verdict := <-verdictCh; verdict.Approved {
		t.Fatal("cancelled request was approved")
	}
	if len(q.Pending()) != 0 {
		t.Fatal("cancelled request still pending")
	}
}
