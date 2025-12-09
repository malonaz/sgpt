package chat_service

import (
	"context"
	"strings"

	"github.com/malonaz/core/go/aip"
	"github.com/malonaz/core/go/grpc"
	"google.golang.org/grpc/codes"

	pb "github.com/malonaz/sgpt/genproto/chat/chat_service/v1"
	chatpb "github.com/malonaz/sgpt/genproto/chat/v1"
)

func (s *Service) updateChatSearchableContent(ctx context.Context, chat *chatpb.Chat) error {
	chatRn := &chatpb.ChatResourceName{}
	if err := chatRn.UnmarshalString(chat.Name); err != nil {
		return grpc.Errorf(codes.Internal, "unmarshaling chat resource name: %v", err).Err()
	}

	// Compute the searchable content.
	var sb strings.Builder
	searchableContent := sb.String()
	if len(searchableContent) == 0 {
		return nil
	}

	if _, err := s.chatPostgresStore.UpdateChatSearchableContent(
		ctx, chatRn.Chat, searchableContent,
	); err != nil {
		return grpc.Errorf(codes.Internal, "updating searchable content: %v", err).Err()
	}

	return nil
}

var searchChatsRequestParser = aip.MustNewSearchRequestParser[*pb.SearchChatsRequest, *chatpb.Chat]()

func (s *Service) SearchChats(ctx context.Context, request *pb.SearchChatsRequest) (*pb.SearchChatsResponse, error) {
	parsed, err := searchChatsRequestParser.Parse(request)
	if err != nil {
		return nil, grpc.Errorf(codes.InvalidArgument, "parsing request: %v", err).Err()
	}
	whereClause, whereParams := parsed.GetSQLWhereClause()
	var dbColumns []string

	dbChats, err := s.chatPostgresStore.SearchChats(
		ctx, request.Query, whereClause, parsed.GetSQLPaginationClause(), dbColumns, whereParams...,
	)
	if err != nil {
		return nil, grpc.Errorf(codes.Internal, "searching chats: %v", err).Err()
	}
	nextPageToken := parsed.GetNextPageToken(len(dbChats))
	if nextPageToken != "" {
		dbChats = dbChats[:len(dbChats)-1]
	}

	// Convert back to proto.
	chats := make([]*chatpb.Chat, 0, len(dbChats))
	for _, dbChat := range dbChats {
		chat, err := dbChat.ToPb()
		if err != nil {
			return nil, grpc.Errorf(codes.Internal, "converting model.Chat to pb.Chat: %v", err).Err()
		}
		chats = append(chats, chat)
	}

	// Create and return response.
	return &pb.SearchChatsResponse{
		Chats:         chats,
		NextPageToken: nextPageToken,
	}, nil
}
