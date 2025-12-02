package chat_service

import (
	"context"

	pb "github.com/malonaz/sgpt/chat/chat_service/v1"
	chatpb "github.com/malonaz/sgpt/chat/v1"
)

func (s *Service) UpdateChat(ctx context.Context, request *pb.UpdateChatRequest) (*chatpb.Chat, error) {
	// Execute the codegen.
	chat, err := s.codegen.UpdateChat(ctx, request)
	if err != nil {
		return nil, err
	}
	return chat, s.updateChatSearchableContent(ctx, chat)
}
