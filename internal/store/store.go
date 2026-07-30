// Package store is the single RPC boundary for chat and model operations.
// All request construction, ID generation, field masks and caching live here
// so the TUI and session layers never touch raw gRPC clients.
package store

import (
	"context"
	"fmt"

	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	"github.com/malonaz/core/go/pbutil/pbfieldmask"
	"github.com/malonaz/core/go/uuid"
	"google.golang.org/protobuf/proto"

	sgptservicepb "github.com/malonaz/sgpt/genproto/sgpt/sgpt_service/v1"
	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
)

const (
	// FavoriteTag marks a chat as a favorite.
	FavoriteTag = "favorite"
	// FavoriteFilter is the server-side filter matching favorite chats.
	FavoriteFilter = `tags:"favorite"`
)

// Store wraps the sgpt and ai service clients.
type Store struct {
	configuration     *sgptpb.Configuration
	aiServiceClient   aiservicepb.AiServiceClient
	chatServiceClient sgptservicepb.SgptServiceClient
}

// New instantiates a store.
func New(
	configuration *sgptpb.Configuration,
	aiServiceClient aiservicepb.AiServiceClient,
	chatServiceClient sgptservicepb.SgptServiceClient,
) *Store {
	return &Store{
		configuration:     configuration,
		aiServiceClient:   aiServiceClient,
		chatServiceClient: chatServiceClient,
	}
}

// newChatID returns the last 8 characters of a v7 UUID. The first characters
// of a v7 UUID are a timestamp prefix that collides for chats created within
// the same ~65s window; the last ones are random.
func newChatID() string {
	chatID := uuid.MustNewV7().String()
	return chatID[len(chatID)-8:]
}

// CreateChat persists a new chat.
func (s *Store) CreateChat(ctx context.Context, chat *sgptpb.Chat) (*sgptpb.Chat, error) {
	createChatRequest := &sgptservicepb.CreateChatRequest{
		RequestId: uuid.MustNewV7().String(),
		ChatId:    newChatID(),
		Chat:      chat,
	}
	createdChat, err := s.chatServiceClient.CreateChat(ctx, createChatRequest)
	if err != nil {
		return nil, fmt.Errorf("creating chat: %w", err)
	}
	return createdChat, nil
}

// UpdateChat persists the given paths of a chat.
func (s *Store) UpdateChat(ctx context.Context, chat *sgptpb.Chat, paths ...string) (*sgptpb.Chat, error) {
	updateChatRequest := &sgptservicepb.UpdateChatRequest{
		Chat:       chat,
		UpdateMask: pbfieldmask.FromPaths(paths...).MustValidate(&sgptpb.Chat{}).Proto(),
	}
	updatedChat, err := s.chatServiceClient.UpdateChat(ctx, updateChatRequest)
	if err != nil {
		return nil, fmt.Errorf("updating chat: %w", err)
	}
	return updatedChat, nil
}

// GetChat fetches a chat by resource name.
func (s *Store) GetChat(ctx context.Context, name string) (*sgptpb.Chat, error) {
	getChatRequest := &sgptservicepb.GetChatRequest{Name: name}
	chat, err := s.chatServiceClient.GetChat(ctx, getChatRequest)
	if err != nil {
		return nil, fmt.Errorf("getting chat: %w", err)
	}
	return chat, nil
}

// DeleteChat deletes a chat by resource name.
func (s *Store) DeleteChat(ctx context.Context, name string) error {
	deleteChatRequest := &sgptservicepb.DeleteChatRequest{Name: name}
	if _, err := s.chatServiceClient.DeleteChat(ctx, deleteChatRequest); err != nil {
		return fmt.Errorf("deleting chat: %w", err)
	}
	return nil
}

// ForkChat clones a chat into a new resource.
func (s *Store) ForkChat(ctx context.Context, chat *sgptpb.Chat) (*sgptpb.Chat, error) {
	forkedChat := proto.Clone(chat).(*sgptpb.Chat)
	forkedChat.Name = ""
	return s.CreateChat(ctx, forkedChat)
}

// ListChats returns a page of chats, most recent first.
func (s *Store) ListChats(ctx context.Context, pageSize int32, pageToken, filter string) ([]*sgptpb.Chat, string, error) {
	listChatsRequest := &sgptservicepb.ListChatsRequest{
		PageSize:  pageSize,
		PageToken: pageToken,
		Filter:    filter,
		OrderBy:   "create_time desc",
	}
	listChatsResponse, err := s.chatServiceClient.ListChats(ctx, listChatsRequest)
	if err != nil {
		return nil, "", fmt.Errorf("listing chats: %w", err)
	}
	return listChatsResponse.Chats, listChatsResponse.NextPageToken, nil
}

// ListFavoriteChats returns the first page of favorite chats.
func (s *Store) ListFavoriteChats(ctx context.Context, pageSize int32) ([]*sgptpb.Chat, error) {
	chats, _, err := s.ListChats(ctx, pageSize, "", FavoriteFilter)
	return chats, err
}

// LatestChat returns the most recently created chat.
func (s *Store) LatestChat(ctx context.Context) (*sgptpb.Chat, error) {
	chats, _, err := s.ListChats(ctx, 1, "", "")
	if err != nil {
		return nil, err
	}
	if len(chats) == 0 {
		return nil, fmt.Errorf("no chat found")
	}
	return chats[0], nil
}

// SearchChats performs a full-text search over chat transcripts.
func (s *Store) SearchChats(ctx context.Context, query string, pageSize int32, pageToken string) ([]*sgptpb.Chat, string, error) {
	searchChatsRequest := &sgptservicepb.SearchChatsRequest{
		Query:     query,
		PageSize:  pageSize,
		PageToken: pageToken,
	}
	searchChatsResponse, err := s.chatServiceClient.SearchChats(ctx, searchChatsRequest)
	if err != nil {
		return nil, "", fmt.Errorf("searching chats: %w", err)
	}
	return searchChatsResponse.Chats, searchChatsResponse.NextPageToken, nil
}

// SetFavorite sets or clears the favorite tag on a chat and persists it.
func (s *Store) SetFavorite(ctx context.Context, chat *sgptpb.Chat, favorite bool) (*sgptpb.Chat, error) {
	SetTag(chat, FavoriteTag, favorite)
	return s.UpdateChat(ctx, chat, "tags")
}

// HasTag reports whether a chat carries the given tag.
func HasTag(chat *sgptpb.Chat, tag string) bool {
	for _, existingTag := range chat.GetTags() {
		if existingTag == tag {
			return true
		}
	}
	return false
}

// SetTag adds or removes a tag on a chat in place.
func SetTag(chat *sgptpb.Chat, tag string, present bool) {
	tags := make([]string, 0, len(chat.GetTags())+1)
	for _, existingTag := range chat.GetTags() {
		if existingTag != tag {
			tags = append(tags, existingTag)
		}
	}
	if present {
		tags = append(tags, tag)
	}
	chat.Tags = tags
}
