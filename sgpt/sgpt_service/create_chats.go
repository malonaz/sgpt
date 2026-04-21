package sgpt_service

import (
	"context"

	pb "github.com/malonaz/sgpt/genproto/sgpt/sgpt_service/v1"
	chatpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
)

func (s *Service) CreateChat(ctx context.Context, request *pb.CreateChatRequest) (*chatpb.Chat, error) {
	chat, err := s.SgptServiceServer.CreateChat(ctx, request)
	if err != nil {
		return nil, err
	}
	return chat, s.updateChatSearchableContent(ctx, chat)
}
