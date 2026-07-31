// Package store is the single RPC boundary for chat and model operations.
// All request construction, ID generation, field masks and caching live here
// so the TUI and session layers never touch raw gRPC clients.
package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/aip"
	"github.com/malonaz/core/go/pbutil/pbfieldmask"
	"github.com/malonaz/core/go/uuid"
	"google.golang.org/protobuf/proto"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/search"
)

const (
	// FavoriteLabel marks a chat as a favorite. Labels (unlike annotations)
	// are server-filterable.
	FavoriteLabel = "favorite"
	// FavoriteFilter is the server-side filter matching favorite chats.
	FavoriteFilter = `labels.favorite = "true"`

	// TagsAnnotation stores chat tags, comma-separated. Tags such as GitHub
	// repos ("owner/repo") don't fit the label value pattern, hence annotations.
	TagsAnnotation = "sgpt.com/tags"
	// FilesAnnotation stores the chat's injected file paths, newline-separated.
	FilesAnnotation = "sgpt.com/files"
	// CurrentModelAnnotation stores the model resource name in use by the chat.
	CurrentModelAnnotation = "sgpt.com/current-model"
	// MessageErrorAnnotation records a message's generation error; messages
	// carrying it are excluded from provider requests.
	MessageErrorAnnotation = "sgpt.com/error"
)

// Store wraps the ai service client, which owns the chat data layer.
type Store struct {
	configuration   *sgptpb.Configuration
	aiServiceClient aiservicepb.AiServiceClient

	// searchIndex, when set, mirrors chat writes into the local search
	// index. Optional and strictly best-effort: indexing never fails a save.
	searchIndex *search.Index
}

// New instantiates a store.
func New(
	configuration *sgptpb.Configuration,
	aiServiceClient aiservicepb.AiServiceClient,
) *Store {
	return &Store{
		configuration:   configuration,
		aiServiceClient: aiServiceClient,
	}
}

// SetSearchIndex enables search-index mirroring of chat writes.
func (s *Store) SetSearchIndex(searchIndex *search.Index) {
	s.searchIndex = searchIndex
}

// SearchIndex returns the search index, or nil when search is disabled.
func (s *Store) SearchIndex() *search.Index {
	return s.searchIndex
}

// indexChat mirrors a chat into the search index. Errors are swallowed:
// the startup backfill reconciles any missed writes.
func (s *Store) indexChat(chat *aipb.Chat) {
	if s.searchIndex == nil {
		return
	}
	_ = s.searchIndex.IndexChat(chat)
}

// SyncSearchIndex reconciles the index with chats modified since the last
// sync (persisted watermark). First run (zero watermark) = full backfill.
func (s *Store) SyncSearchIndex(ctx context.Context) error {
	if s.searchIndex == nil {
		return nil
	}
	lastSyncTime := s.searchIndex.LastSyncTime()
	filter := ""
	if !lastSyncTime.IsZero() {
		filter = fmt.Sprintf(`update_time > %q`, lastSyncTime.Format(time.RFC3339))
	}
	listChatsRequest := &aiservicepb.ListChatsRequest{
		Parent:   s.parent(),
		PageSize: 100,
		Filter:   filter,
		// Ascending update_time keeps pagination stable and makes the
		// watermark resumable mid-backfill.
		OrderBy: "update_time asc",
	}
	watermark := lastSyncTime
	for chat, err := range aip.Iterator[*aipb.Chat](ctx, listChatsRequest, s.aiServiceClient.ListChats) {
		if err != nil {
			// Persist progress so the next run resumes from here.
			_ = s.searchIndex.SetLastSyncTime(watermark)
			return err
		}
		if err := s.searchIndex.IndexChat(chat); err != nil {
			continue
		}
		watermark = chat.GetUpdateTime().AsTime()
	}
	return s.searchIndex.SetLastSyncTime(watermark)
}

// parent is the user resource that owns all chats.
// Format: organizations/{organization}/users/{user}
func (s *Store) parent() string {
	return s.configuration.GetChat().GetUser()
}

// newChatID returns the last 8 characters of a v7 UUID. The first characters
// of a v7 UUID are a timestamp prefix that collides for chats created within
// the same ~65s window; the last ones are random.
func newChatID() string {
	chatID := uuid.MustNewV7().String()
	return chatID[len(chatID)-8:]
}

// CreateChat persists a new chat.
func (s *Store) CreateChat(ctx context.Context, chat *aipb.Chat) (*aipb.Chat, error) {
	createChatRequest := &aiservicepb.CreateChatRequest{
		Parent:    s.parent(),
		RequestId: uuid.MustNewV7().String(),
		ChatId:    newChatID(),
		Chat:      chat,
	}
	createdChat, err := s.aiServiceClient.CreateChat(ctx, createChatRequest)
	if err != nil {
		return nil, fmt.Errorf("creating chat: %w", err)
	}
	s.indexChat(createdChat)
	return createdChat, nil
}

// UpdateChat persists the given paths of a chat.
func (s *Store) UpdateChat(ctx context.Context, chat *aipb.Chat, paths ...string) (*aipb.Chat, error) {
	updateChatRequest := &aiservicepb.UpdateChatRequest{
		Chat:       chat,
		UpdateMask: pbfieldmask.FromPaths(paths...).MustValidate(&aipb.Chat{}).Proto(),
	}
	updatedChat, err := s.aiServiceClient.UpdateChat(ctx, updateChatRequest)
	if err != nil {
		return nil, fmt.Errorf("updating chat: %w", err)
	}
	s.indexChat(updatedChat)
	return updatedChat, nil
}

// GetChat fetches a chat by resource name.
func (s *Store) GetChat(ctx context.Context, name string) (*aipb.Chat, error) {
	getChatRequest := &aiservicepb.GetChatRequest{Name: name}
	chat, err := s.aiServiceClient.GetChat(ctx, getChatRequest)
	if err != nil {
		return nil, fmt.Errorf("getting chat: %w", err)
	}
	return chat, nil
}

// DeleteChat deletes a chat by resource name.
func (s *Store) DeleteChat(ctx context.Context, name string) error {
	deleteChatRequest := &aiservicepb.DeleteChatRequest{Name: name}
	if _, err := s.aiServiceClient.DeleteChat(ctx, deleteChatRequest); err != nil {
		return fmt.Errorf("deleting chat: %w", err)
	}
	if s.searchIndex != nil {
		// Best-effort: a leaked stale hit is dropped at search time when its
		// GetChat fails.
		_ = s.searchIndex.DeleteChat(name)
	}
	return nil
}

// ForkChat clones a chat into a new resource.
func (s *Store) ForkChat(ctx context.Context, chat *aipb.Chat) (*aipb.Chat, error) {
	forkedChat := proto.Clone(chat).(*aipb.Chat)
	forkedChat.Name = ""
	forkedChat.Etag = ""
	return s.CreateChat(ctx, forkedChat)
}

// ListChats returns a page of chats, most recent first.
func (s *Store) ListChats(ctx context.Context, pageSize int32, pageToken, filter string) ([]*aipb.Chat, string, error) {
	listChatsRequest := &aiservicepb.ListChatsRequest{
		Parent:    s.parent(),
		PageSize:  pageSize,
		PageToken: pageToken,
		Filter:    filter,
		OrderBy:   "create_time desc",
	}
	listChatsResponse, err := s.aiServiceClient.ListChats(ctx, listChatsRequest)
	if err != nil {
		return nil, "", fmt.Errorf("listing chats: %w", err)
	}
	return listChatsResponse.Chats, listChatsResponse.NextPageToken, nil
}

// ListFavoriteChats returns the first page of favorite chats.
func (s *Store) ListFavoriteChats(ctx context.Context, pageSize int32) ([]*aipb.Chat, error) {
	chats, _, err := s.ListChats(ctx, pageSize, "", FavoriteFilter)
	return chats, err
}

// LatestChat returns the most recently created chat.
func (s *Store) LatestChat(ctx context.Context) (*aipb.Chat, error) {
	chats, _, err := s.ListChats(ctx, 1, "", "")
	if err != nil {
		return nil, err
	}
	if len(chats) == 0 {
		return nil, fmt.Errorf("no chat found")
	}
	return chats[0], nil
}

// SetFavorite sets or clears the favorite label on a chat and persists it.
func (s *Store) SetFavorite(ctx context.Context, chat *aipb.Chat, favorite bool) (*aipb.Chat, error) {
	SetFavoriteLabel(chat, favorite)
	return s.UpdateChat(ctx, chat, "labels")
}

// IsFavorite reports whether a chat is marked as a favorite.
func IsFavorite(chat *aipb.Chat) bool {
	return chat.GetLabels()[FavoriteLabel] == "true"
}

// SetFavoriteLabel sets or clears the favorite label on a chat in place.
func SetFavoriteLabel(chat *aipb.Chat, favorite bool) {
	if !favorite {
		delete(chat.GetLabels(), FavoriteLabel)
		return
	}
	if chat.Labels == nil {
		chat.Labels = map[string]string{}
	}
	chat.Labels[FavoriteLabel] = "true"
}

// Tags returns a chat's tags.
func Tags(chat *aipb.Chat) []string {
	raw := chat.GetAnnotations()[TagsAnnotation]
	if raw == "" {
		return nil
	}
	return strings.Split(raw, ",")
}

// SetTags sets a chat's tags in place.
func SetTags(chat *aipb.Chat, tags []string) {
	setChatAnnotation(chat, TagsAnnotation, strings.Join(tags, ","))
}

// Files returns the chat's recorded injected file paths.
func Files(chat *aipb.Chat) []string {
	raw := chat.GetAnnotations()[FilesAnnotation]
	if raw == "" {
		return nil
	}
	return strings.Split(raw, "\n")
}

// SetFiles records the chat's injected file paths in place.
func SetFiles(chat *aipb.Chat, paths []string) {
	setChatAnnotation(chat, FilesAnnotation, strings.Join(paths, "\n"))
}

// CurrentModel returns the model resource name in use by the chat.
func CurrentModel(chat *aipb.Chat) string {
	return chat.GetAnnotations()[CurrentModelAnnotation]
}

// SetCurrentModel records the model in use by the chat in place.
func SetCurrentModel(chat *aipb.Chat, model string) {
	setChatAnnotation(chat, CurrentModelAnnotation, model)
}

func setChatAnnotation(chat *aipb.Chat, key, value string) {
	if value == "" {
		delete(chat.GetAnnotations(), key)
		return
	}
	if chat.Annotations == nil {
		chat.Annotations = map[string]string{}
	}
	chat.Annotations[key] = value
}

// MessageError returns the generation error recorded on a message, if any.
func MessageError(message *aipb.Message) string {
	return message.GetAnnotations()[MessageErrorAnnotation]
}

// SetMessageError records a generation error on a message in place.
func SetMessageError(message *aipb.Message, errText string) {
	if message.Annotations == nil {
		message.Annotations = map[string]string{}
	}
	message.Annotations[MessageErrorAnnotation] = errText
}
