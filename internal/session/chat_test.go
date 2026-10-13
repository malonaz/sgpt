package session

import (
	"context"
	"sync"
	"testing"

	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/store"
	"github.com/malonaz/sgpt/internal/tool"
)

func newPersistedChatSession(fake *fakeAI) *Session {
	chatStore := store.New(&sgptpb.Configuration{}, fake)
	return New(context.Background(), chatStore, tool.NewRegistry(), &aipb.Chat{Name: "chats/1"}, nil, Params{})
}

// A favorite toggled while a chat write is in flight must survive that
// write's response and reach the server.
func TestChatEditDuringFlushIsPersisted(t *testing.T) {
	fake := newFakeAI()
	inFlight, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	var lastWrite *aipb.Chat
	fake.updateChat = func(request *aiservicepb.UpdateChatRequest) {
		once.Do(func() {
			close(inFlight)
			<-release
		})
		mu.Lock()
		lastWrite = request.GetChat()
		mu.Unlock()
	}
	s := newPersistedChatSession(fake)
	// Mid-turn, so the toggle relies on the turn's flush to persist it.
	s.mu.Lock()
	s.currentTurn = newTurn(s, context.Background())
	s.mu.Unlock()

	flushed := make(chan struct{})
	go func() {
		s.flushChat()
		close(flushed)
	}()
	<-inFlight
	s.ToggleFavorite()
	close(release)
	<-flushed

	if !store.IsFavorite(s.Chat()) {
		t.Fatal("favorite reverted by the in-flight write's response")
	}
	mu.Lock()
	defer mu.Unlock()
	if !store.IsFavorite(lastWrite) {
		t.Fatal("favorite never reached the server")
	}
}

// Chat edits, writes and renders run on different goroutines; none may
// observe another's mutation in progress.
func TestConcurrentChatEditsAndWrites(t *testing.T) {
	s := newPersistedChatSession(newFakeAI())
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				s.ToggleFavorite()
				s.flushChat()
				_ = store.IsFavorite(s.Chat())
			}
		}()
	}
	wg.Wait()
	if errs := s.Errors(); len(errs) > 0 {
		t.Fatalf("errors = %v", errs)
	}
}
