package chat_service

import (
	"context"

	pb "github.com/malonaz/sgpt/genproto/chat/chat_service/v1"
	chatpb "github.com/malonaz/sgpt/genproto/chat/v1"
)

func (s *Service) CreateChat(ctx context.Context, request *pb.CreateChatRequest) (*chatpb.Chat, error) {
	chat, err := s.ChatServer.CreateChat(ctx, request)
	if err != nil {
		return nil, err
	}
	return chat, s.updateChatSearchableContent(ctx, chat)
}
