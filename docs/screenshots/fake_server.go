package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// fakeAiService is an in-process AiService that persists nothing and answers
// each StreamGenerateMessage with the next scripted response.
type fakeAiService struct {
	aiservicepb.UnimplementedAiServiceServer

	mu        sync.Mutex
	responses [][]*aipb.Block
	title     string
	chats     []*aipb.Chat
	counter   int
}

func (f *fakeAiService) next() string {
	f.counter++
	return fmt.Sprintf("%06d", f.counter)
}

func (f *fakeAiService) CreateChat(_ context.Context, request *aiservicepb.CreateChatRequest) (*aipb.Chat, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	chat := proto.CloneOf(request.GetChat())
	chat.Name = request.GetParent() + "/chats/" + f.next()
	chat.Title = f.title
	chat.CreateTime = timestamppb.Now()
	chat.UpdateTime = chat.CreateTime
	f.chats = append(f.chats, chat)
	return chat, nil
}

func (f *fakeAiService) GetChat(_ context.Context, request *aiservicepb.GetChatRequest) (*aipb.Chat, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, chat := range f.chats {
		if chat.GetName() == request.GetName() {
			return chat, nil
		}
	}
	return nil, fmt.Errorf("not found")
}

func (f *fakeAiService) UpdateChat(_ context.Context, request *aiservicepb.UpdateChatRequest) (*aipb.Chat, error) {
	return request.GetChat(), nil
}

func (f *fakeAiService) ListChats(_ context.Context, _ *aiservicepb.ListChatsRequest) (*aiservicepb.ListChatsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &aiservicepb.ListChatsResponse{Chats: f.chats}, nil
}

func (f *fakeAiService) ListMessages(_ context.Context, _ *aiservicepb.ListMessagesRequest) (*aiservicepb.ListMessagesResponse, error) {
	return &aiservicepb.ListMessagesResponse{}, nil
}

func (f *fakeAiService) CreateMessage(_ context.Context, request *aiservicepb.CreateMessageRequest) (*aipb.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	message := proto.CloneOf(request.GetMessage())
	message.Name = request.GetParent() + "/messages/" + f.next()
	return message, nil
}

func (f *fakeAiService) UpdateMessage(_ context.Context, request *aiservicepb.UpdateMessageRequest) (*aipb.Message, error) {
	return request.GetMessage(), nil
}

// StreamGenerateMessage streams the next scripted response block by block,
// inheriting tool annotations onto tool calls exactly like the real server.
func (f *fakeAiService) StreamGenerateMessage(request *aiservicepb.GenerateMessageRequest, stream aiservicepb.AiService_StreamGenerateMessageServer) error {
	f.mu.Lock()
	if len(f.responses) == 0 {
		f.mu.Unlock()
		return fmt.Errorf("no scripted response left")
	}
	blocks := f.responses[0]
	f.responses = f.responses[1:]
	f.mu.Unlock()

	toolAnnotations := map[string]map[string]string{}
	for _, tool := range request.GetTools() {
		toolAnnotations[tool.GetName()] = tool.GetAnnotations()
	}

	message := &aipb.Message{Role: aipb.Role_ROLE_ASSISTANT}
	for i, block := range blocks {
		block = proto.CloneOf(block)
		block.Index = int64(i)
		if toolCall := block.GetToolCall(); toolCall != nil {
			toolCall.Annotations = map[string]string{}
			for key, value := range toolAnnotations[toolCall.GetName()] {
				toolCall.Annotations[key] = value
			}
		}
		message.Blocks = append(message.Blocks, block)
		if err := stream.Send(&aiservicepb.StreamGenerateMessageResponse{
			Content: &aiservicepb.StreamGenerateMessageResponse_Block{Block: block},
		}); err != nil {
			return err
		}
		time.Sleep(20 * time.Millisecond)
	}
	usage := &aipb.ModelUsage{
		Model:       request.GetModel(),
		InputToken:  &aipb.ResourceConsumption{Quantity: 4312, Price: 0.0216},
		OutputToken: &aipb.ResourceConsumption{Quantity: 388, Price: 0.0291},
	}
	if err := stream.Send(&aiservicepb.StreamGenerateMessageResponse{
		Content: &aiservicepb.StreamGenerateMessageResponse_ModelUsage{ModelUsage: usage},
	}); err != nil {
		return err
	}
	message.Name = request.GetParent() + "/messages/" + f.next()
	message.ModelUsage = usage
	message.Price = usage.GetInputToken().GetPrice() + usage.GetOutputToken().GetPrice()
	return stream.Send(&aiservicepb.StreamGenerateMessageResponse{
		Content: &aiservicepb.StreamGenerateMessageResponse_GeneratedMessage{GeneratedMessage: message},
	})
}
