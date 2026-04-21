package sgpt_service

import (
	"context"
	"strings"

	"github.com/malonaz/core/go/aip"
	"github.com/malonaz/core/go/grpc/status"
	"google.golang.org/grpc/codes"

	pb "github.com/malonaz/sgpt/genproto/sgpt/sgpt_service/v1"
	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
)

func (s *Service) updateChatSearchableContent(ctx context.Context, chat *sgptpb.Chat) error {
	chatRn := &sgptpb.ChatResourceName{}
	if err := chatRn.UnmarshalString(chat.Name); err != nil {
		return status.Errorf(codes.Internal, "unmarshaling chat resource name: %v", err).Err()
	}

	var sb strings.Builder
	if chat.GetMetadata().GetTitle() != "" {
		sb.WriteString(chat.GetMetadata().GetTitle())
		sb.WriteString("\n")
	}
	for _, message := range chat.GetMetadata().GetMessages() {
		for _, block := range message.GetMessage().GetBlocks() {
			if block.GetText() != "" {
				sb.WriteString(block.GetText())
				sb.WriteString("\n")
			}
		}
	}

	searchableContent := sb.String()
	if len(searchableContent) == 0 {
		return nil
	}

	if _, err := s.sgptPostgresStore.UpdateChatSearchableContent(
		ctx, chatRn.Chat, searchableContent,
	); err != nil {
		return status.Errorf(codes.Internal, "updating searchable content: %v", err).Err()
	}

	return nil
}

var searchChatsRequestParser = aip.MustNewSearchRequestParser[*pb.SearchChatsRequest, *sgptpb.Chat]()

func (s *Service) SearchChats(ctx context.Context, request *pb.SearchChatsRequest) (*pb.SearchChatsResponse, error) {
	parsed, err := searchChatsRequestParser.Parse(request)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "parsing request: %v", err).Err()
	}
	whereClause, whereParams := parsed.GetSQLWhereClause()
	var dbColumns []string

	dbChats, err := s.sgptPostgresStore.SearchChats(
		ctx, request.Query, whereClause, parsed.GetSQLPaginationClause(), dbColumns, whereParams...,
	)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "searching chats: %v", err).Err()
	}
	nextPageToken := parsed.GetNextPageToken(len(dbChats))
	if nextPageToken != "" {
		dbChats = dbChats[:len(dbChats)-1]
	}

	// Convert back to proto.
	chats := make([]*sgptpb.Chat, 0, len(dbChats))
	for _, dbChat := range dbChats {
		chat, err := dbChat.ToPb()
		if err != nil {
			return nil, status.Errorf(codes.Internal, "converting model.Chat to pb.Chat: %v", err).Err()
		}
		chats = append(chats, chat)
	}

	// Create and return response.
	return &pb.SearchChatsResponse{
		Chats:         chats,
		NextPageToken: nextPageToken,
	}, nil
}
