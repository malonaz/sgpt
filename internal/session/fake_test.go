package session

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/store"
	"github.com/malonaz/sgpt/internal/tool"
)

// fakeAI is an in-memory AI service: it persists what it is sent and plays
// each generation from a script the test supplies.
type fakeAI struct {
	aiservicepb.AiServiceClient

	mu       sync.Mutex
	requests []*aiservicepb.GenerateMessageRequest
	ids      int
	// generate scripts the nth (0-based) generation.
	generate func(n int, stream *fakeStream)
	// createChatErr fails the next CreateChat once.
	createChatErr error
	// updateChat, when set, observes every chat write before it is answered.
	updateChat func(*aiservicepb.UpdateChatRequest)
	// opened receives the index of every generation as its stream opens.
	opened chan int
}

func newFakeAI() *fakeAI {
	return &fakeAI{opened: make(chan int, 16)}
}

func (f *fakeAI) nextName(prefix string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ids++
	return fmt.Sprintf("%s/%d", prefix, f.ids)
}

func (f *fakeAI) CreateChat(_ context.Context, request *aiservicepb.CreateChatRequest, _ ...grpc.CallOption) (*aipb.Chat, error) {
	f.mu.Lock()
	err := f.createChatErr
	f.createChatErr = nil
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	chat := proto.CloneOf(request.GetChat())
	chat.Name = f.nextName("chats")
	return chat, nil
}

func (f *fakeAI) UpdateChat(_ context.Context, request *aiservicepb.UpdateChatRequest, _ ...grpc.CallOption) (*aipb.Chat, error) {
	// Serializing is what the real transport does with the request.
	if _, err := proto.Marshal(request); err != nil {
		return nil, err
	}
	if f.updateChat != nil {
		f.updateChat(request)
	}
	return proto.CloneOf(request.GetChat()), nil
}

func (f *fakeAI) CreateMessage(_ context.Context, request *aiservicepb.CreateMessageRequest, _ ...grpc.CallOption) (*aipb.Message, error) {
	message := proto.CloneOf(request.GetMessage())
	message.Name = f.nextName(request.GetParent() + "/messages")
	return message, nil
}

func (f *fakeAI) UpdateMessage(_ context.Context, request *aiservicepb.UpdateMessageRequest, _ ...grpc.CallOption) (*aipb.Message, error) {
	return proto.CloneOf(request.GetMessage()), nil
}

func (f *fakeAI) StreamGenerateMessage(ctx context.Context, request *aiservicepb.GenerateMessageRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[aiservicepb.StreamGenerateMessageResponse], error) {
	f.mu.Lock()
	n := len(f.requests)
	f.requests = append(f.requests, proto.CloneOf(request))
	f.mu.Unlock()
	// Unbuffered: a send returns once the turn has received it.
	stream := &fakeStream{ctx: ctx, responses: make(chan *aiservicepb.StreamGenerateMessageResponse)}
	go f.generate(n, stream)
	f.opened <- n
	return stream, nil
}

// requestTexts returns the text of every input message of each generation.
func (f *fakeAI) requestTexts() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var texts [][]string
	for _, request := range f.requests {
		texts = append(texts, messageTexts(request.GetMessages()))
	}
	return texts
}

// fakeStream delivers scripted responses; closing it ends the stream (EOF),
// and the caller's context ends it as gRPC would.
type fakeStream struct {
	grpc.ClientStream
	ctx       context.Context
	responses chan *aiservicepb.StreamGenerateMessageResponse
	// err, when set before responses closes, ends the stream instead of EOF.
	err error
}

func (s *fakeStream) Recv() (*aiservicepb.StreamGenerateMessageResponse, error) {
	select {
	case response, ok := <-s.responses:
		if !ok {
			if s.err != nil {
				return nil, s.err
			}
			return nil, io.EOF
		}
		return response, nil
	case <-s.ctx.Done():
		return nil, grpcstatus.Error(codes.Canceled, s.ctx.Err().Error())
	}
}

func (s *fakeStream) text(text string) {
	s.responses <- &aiservicepb.StreamGenerateMessageResponse{Content: &aiservicepb.StreamGenerateMessageResponse_Block{
		Block: &aipb.Block{Content: &aipb.Block_Text{Text: text}},
	}}
}

func (s *fakeStream) persisted(name string) {
	s.responses <- &aiservicepb.StreamGenerateMessageResponse{Content: &aiservicepb.StreamGenerateMessageResponse_GeneratedMessage{
		GeneratedMessage: &aipb.Message{Name: name, Role: aipb.Role_ROLE_ASSISTANT},
	}}
}

func (s *fakeStream) usage() {
	s.responses <- &aiservicepb.StreamGenerateMessageResponse{Content: &aiservicepb.StreamGenerateMessageResponse_ModelUsage{
		ModelUsage: &aipb.ModelUsage{},
	}}
}

func (s *fakeStream) fail(err error) {
	s.err = err
	close(s.responses)
}

// reply answers with a complete text message.
func reply(text string) func(*fakeStream) {
	return func(stream *fakeStream) {
		stream.text(text)
		close(stream.responses)
	}
}

func newTestSession(t *testing.T, fake *fakeAI) *Session {
	t.Helper()
	chatStore := store.New(&sgptpb.Configuration{}, fake)
	return New(context.Background(), chatStore, tool.NewRegistry(), &aipb.Chat{}, nil, Params{Model: &aipb.Model{Name: "models/test"}})
}

// userTexts returns the text of the user messages of the local history.
func userTexts(s *Session) []string {
	var userMessages []*aipb.Message
	for _, message := range s.Messages() {
		if message.GetRole() == aipb.Role_ROLE_USER {
			userMessages = append(userMessages, message)
		}
	}
	return messageTexts(userMessages)
}

func messageTexts(messages []*aipb.Message) []string {
	texts := make([]string, 0, len(messages))
	for _, message := range messages {
		var parts []string
		for _, block := range ai.FilterBlocks(message.GetBlocks(), ai.BlockTypeText) {
			parts = append(parts, block.GetText())
		}
		texts = append(texts, strings.Join(parts, " "))
	}
	return texts
}

func awaitOpened(t *testing.T, fake *fakeAI, want int) {
	t.Helper()
	select {
	case got := <-fake.opened:
		if got != want {
			t.Fatalf("generation %d opened, want %d", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("generation %d never opened", want)
	}
}

func awaitIdle(t *testing.T, s *Session) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for s.TurnInFlight() {
		if time.Now().After(deadline) {
			t.Fatal("turn never ended")
		}
		time.Sleep(time.Millisecond)
	}
}
