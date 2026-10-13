package session

import (
	"context"
	"errors"
	"slices"
	"testing"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/store"
	"github.com/malonaz/sgpt/internal/tool"
)

func agentMessage(text string) *aipb.Message {
	return ai.NewUserMessage(ai.NewTextBlock(text))
}

// A message delivered mid-turn joins that turn, so its reply is the turn's
// final answer — the same one the message that started the turn gets.
func TestDeliveredMessagesShareTheTurnThatAnswersThem(t *testing.T) {
	fake := newFakeAI()
	release := make(chan struct{})
	fake.generate = func(n int, stream *fakeStream) {
		if n == 0 {
			<-release
			reply("interim")(stream)
			return
		}
		reply("final")(stream)
	}
	s := newTestSession(t, fake)

	first, err := s.Deliver(agentMessage("task"))
	if err != nil {
		t.Fatal(err)
	}
	awaitOpened(t, fake, 0)
	second, err := s.Deliver(agentMessage("follow-up"))
	if err != nil {
		t.Fatal(err)
	}
	close(release)

	for _, outcomeCh := range []<-chan tool.TurnOutcome{first, second} {
		if outcome := awaitOutcome(t, outcomeCh); outcome.Err != nil || outcome.Text != "final" {
			t.Fatalf("outcome = %+v, want the turn's final answer", outcome)
		}
	}
	want := [][]string{{"task"}, {"follow-up"}}
	if got := fake.requestTexts(); !slices.EqualFunc(got, want, slices.Equal) {
		t.Fatalf("requests = %q, want %q", got, want)
	}
}

// Agents talking to an idle chat may start only so many turns before the user
// speaks again; beyond, the message waits in the queue for the user's next one.
func TestAgentTurnLimitQueuesUntilTheUserSpeaks(t *testing.T) {
	fake := newFakeAI()
	fake.generate = func(n int, stream *fakeStream) { reply("answer")(stream) }
	chatStore := store.New(&sgptpb.Configuration{}, fake)
	s := New(context.Background(), chatStore, tool.NewRegistry(), &aipb.Chat{}, nil,
		Params{Model: &aipb.Model{Name: "models/test"}, MaxAgentTurns: 1})

	outcomeCh, err := s.Deliver(agentMessage("report"))
	if err != nil {
		t.Fatal(err)
	}
	awaitOutcome(t, outcomeCh)
	awaitIdle(t, s)

	if _, err := s.Deliver(agentMessage("another report")); !errors.Is(err, tool.ErrAgentTurnLimit) {
		t.Fatalf("err = %v, want the turn limit", err)
	}
	if s.TurnInFlight() {
		t.Fatal("a turn started beyond the limit")
	}

	s.SendMessage("go on")

	if got, want := fake.requestTexts()[1], []string{"another report", "go on"}; !slices.Equal(got, want) {
		t.Fatalf("request = %q, want %q", got, want)
	}
	// The user spoke: agents may start turns again.
	if _, err := s.Deliver(agentMessage("third report")); err != nil {
		t.Fatalf("err = %v after the user spoke", err)
	}
}

// Closing a chat answers everyone waiting on it and refuses further messages:
// nothing may run in a chat the user can no longer see.
func TestCloseFailsPendingRepliesAndRefusesMessages(t *testing.T) {
	fake := newFakeAI()
	fake.generate = func(int, *fakeStream) {} // never answers
	s := newTestSession(t, fake)

	running, err := s.Deliver(agentMessage("task"))
	if err != nil {
		t.Fatal(err)
	}
	awaitOpened(t, fake, 0)
	queued, err := s.Deliver(agentMessage("follow-up"))
	if err != nil {
		t.Fatal(err)
	}

	s.Close()

	if outcome := awaitOutcome(t, running); !IsCancelError(outcome.Err) {
		t.Fatalf("running outcome = %+v, want a cancellation", outcome)
	}
	if outcome := awaitOutcome(t, queued); outcome.Err == nil {
		t.Fatalf("queued outcome = %+v, want an error", outcome)
	}
	if _, err := s.Deliver(agentMessage("late")); !errors.Is(err, tool.ErrAgentClosed) {
		t.Fatalf("err = %v, want the chat closed", err)
	}
}
