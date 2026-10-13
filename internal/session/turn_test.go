package session

import (
	"slices"
	"testing"
)

// An interjection that lands after the turn's last queue check — here from
// the completion callback, just before the turn is released — must still be
// answered rather than left queued with no turn to consume it.
func TestInterjectionAtTurnEndIsAnswered(t *testing.T) {
	fake := newFakeAI()
	fake.generate = func(n int, stream *fakeStream) { reply("answer")(stream) }
	s := newTestSession(t, fake)
	interjected := false
	s.SetOnTurnComplete(func(string, error) {
		if !interjected {
			interjected = true
			s.SendMessage("by the way")
		}
	})

	s.SendMessage("hello")

	if queued := s.QueuedMessages(); len(queued) != 0 {
		t.Fatalf("queued = %v, want the interjection sent", messageTexts(queued))
	}
	want := [][]string{{"hello"}, {"by the way"}}
	if got := fake.requestTexts(); !slices.EqualFunc(got, want, slices.Equal) {
		t.Fatalf("requests = %q, want %q", got, want)
	}
}
