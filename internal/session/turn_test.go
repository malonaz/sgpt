package session

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"

	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/tool"
)

// An interjection that lands after the turn's last queue check — here from
// the turn's completion, just before it is released — must still be answered
// rather than left queued with no turn to consume it.
func TestInterjectionAtTurnEndIsAnswered(t *testing.T) {
	fake := newFakeAI()
	fake.generate = func(n int, stream *fakeStream) { reply("answer")(stream) }
	s := newTestSession(t, fake)

	sendWithWaiter(s, "hello", func(tool.TurnOutcome) { s.SendMessage("by the way") })

	if queued := s.QueuedMessages(); len(queued) != 0 {
		t.Fatalf("queued = %v, want the interjection sent", messageTexts(queued))
	}
	want := [][]string{{"hello"}, {"by the way"}}
	if got := fake.requestTexts(); !slices.EqualFunc(got, want, slices.Equal) {
		t.Fatalf("requests = %q, want %q", got, want)
	}
}

// Interjections left queued by a cancelled turn were typed before the next
// message, so they must reach the model — and the timeline — ahead of it.
func TestInterjectionSurvivingCancelKeepsItsOrder(t *testing.T) {
	fake := newFakeAI()
	fake.generate = func(n int, stream *fakeStream) {
		if n > 0 {
			reply("answer")(stream)
		}
		// The first generation hangs until cancelled.
	}
	s := newTestSession(t, fake)

	go s.SendMessage("first")
	awaitOpened(t, fake, 0)
	s.SendMessage("interjection")
	s.CancelTurn()
	awaitIdle(t, s)

	s.SendMessage("second")

	want := []string{"first", "interjection", "second"}
	if got := fake.requestTexts()[1]; !slices.Equal(got, want) {
		t.Fatalf("retry request = %q, want %q", got, want)
	}
	if got := userTexts(s); !slices.Equal(got, want) {
		t.Fatalf("history = %q, want %q", got, want)
	}
}

// A failed stream requeues its inputs, interjections included: the retry
// sends each exactly once and the local history never duplicates them.
func TestRequeuedInputsAreSentOnce(t *testing.T) {
	fake := newFakeAI()
	interjected := make(chan struct{})
	fake.generate = func(n int, stream *fakeStream) {
		switch n {
		case 0:
			// Hold the answer until the interjection is queued.
			<-interjected
			reply("answer")(stream)
		case 1:
			// The follow-up generation for the interjection fails.
			stream.fail(grpcstatus.Error(codes.Unavailable, "boom"))
		default:
			reply("answer")(stream)
		}
	}
	s := newTestSession(t, fake)

	go s.SendMessage("first")
	awaitOpened(t, fake, 0)
	s.SendMessage("interjection")
	close(interjected)
	awaitOpened(t, fake, 1)
	awaitIdle(t, s)

	s.SendMessage("second")

	want := [][]string{{"first"}, {"interjection"}, {"interjection", "second"}}
	if got := fake.requestTexts(); !slices.EqualFunc(got, want, slices.Equal) {
		t.Fatalf("requests = %q, want %q", got, want)
	}
	if got, want := userTexts(s), []string{"first", "interjection", "second"}; !slices.Equal(got, want) {
		t.Fatalf("history = %q, want %q", got, want)
	}
}

// A turn that dies before reaching the model must not leave the message
// rendered as sent: it returns to the queue and goes out with the next send.
func TestContextFailureKeepsMessageUnsent(t *testing.T) {
	fake := newFakeAI()
	fake.createChatErr = errors.New("unavailable")
	fake.generate = func(n int, stream *fakeStream) { reply("answer")(stream) }
	s := newTestSession(t, fake)
	var turnErr error
	sendWithWaiter(s, "lost", func(outcome tool.TurnOutcome) { turnErr = outcome.Err })

	if turnErr == nil {
		t.Fatal("turn reported success despite the context failure")
	}
	if got := userTexts(s); len(got) != 0 {
		t.Fatalf("history = %q, want the unsent message out of it", got)
	}
	if got := messageTexts(s.QueuedMessages()); !slices.Equal(got, []string{"lost"}) {
		t.Fatalf("queued = %q, want [lost]", got)
	}

	s.SendMessage("again")

	want := []string{"lost", "again"}
	if got := fake.requestTexts()[0]; !slices.Equal(got, want) {
		t.Fatalf("request = %q, want %q", got, want)
	}
	if got := userTexts(s); !slices.Equal(got, want) {
		t.Fatalf("history = %q, want %q", got, want)
	}
}

// Once the server has sent the persisted assistant message the generation is
// committed; a cancel landing before the trailing EOF must not requeue (and so
// re-send) inputs the server already stored successfully.
func TestCancelAfterPersistedMessageKeepsGeneration(t *testing.T) {
	fake := newFakeAI()
	persisted := make(chan struct{})
	fake.generate = func(n int, stream *fakeStream) {
		if n == 0 {
			stream.text("answer")
			stream.persisted("chats/1/messages/9")
			// Received only once the persisted message has been handled.
			stream.usage()
			close(persisted)
			// The EOF never comes: the cancel wins.
			return
		}
		reply("answer")(stream)
	}
	s := newTestSession(t, fake)

	go s.SendMessage("first")
	<-persisted
	s.CancelTurn()
	awaitIdle(t, s)

	s.SendMessage("second")

	if got, want := fake.requestTexts()[1], []string{"second"}; !slices.Equal(got, want) {
		t.Fatalf("next request = %q, want %q", got, want)
	}
}

// reviewedTool needs the user's verdict for every call.
type reviewedTool struct{}

func (reviewedTool) Review(context.Context, *aipb.ToolCall) (*sgptpb.ToolCallMetadata, error) {
	return &sgptpb.ToolCallMetadata{}, nil
}

func (reviewedTool) Execute(_ context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error) {
	return ai.NewToolResult(toolCall.GetName(), toolCall.GetId(), "ran"), nil
}

// Cancelling a turn parked in review resolves the call as cancelled; the
// interjection typed during the review follows its result, then the next
// message.
func TestCancelInReviewKeepsInterjectionAfterToolResult(t *testing.T) {
	fake := newFakeAI()
	fake.generate = func(n int, stream *fakeStream) {
		if n > 0 {
			reply("answer")(stream)
			return
		}
		toolCall := &aipb.ToolCall{Id: "call-1", Name: "reviewed", Annotations: map[string]string{tool.ToolHandlerIDAnnotation: "reviewed"}}
		stream.responses <- &aiservicepb.StreamGenerateMessageResponse{Content: &aiservicepb.StreamGenerateMessageResponse_Block{
			Block: ai.NewToolCallBlock(toolCall),
		}}
		close(stream.responses)
	}
	s := newTestSession(t, fake)
	s.registry.Register("reviewed", reviewedTool{})

	go s.SendMessage("first")
	awaitPending(t, s, "call-1")
	s.SendMessage("by the way")
	s.CancelTurn()
	awaitIdle(t, s)
	s.SendMessage("next")

	request := fake.requests[1].GetMessages()
	if len(request) != 3 || request[0].GetRole() != aipb.Role_ROLE_TOOL {
		t.Fatalf("retry request = %v, want [tool result, by the way, next]", request)
	}
	if got, want := messageTexts(request[1:]), []string{"by the way", "next"}; !slices.Equal(got, want) {
		t.Fatalf("retry request texts = %q, want %q", got, want)
	}
	if result := request[0].GetBlocks()[0].GetToolResult(); !strings.Contains(result.String(), "cancelled") {
		t.Fatalf("tool result = %v, want a cancellation", result)
	}
}
