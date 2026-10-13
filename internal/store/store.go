// Package store is the single RPC boundary for chat, message and model
// operations. All request construction, ID generation and field masks live
// here so the TUI and session layers never touch raw gRPC clients.
package store

import (
	"context"
	"fmt"
	"strings"

	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"
	"github.com/malonaz/core/go/aip"
	"github.com/malonaz/core/go/pbutil"
	"github.com/malonaz/core/go/pbutil/pbfieldmask"
	"github.com/malonaz/core/go/uuid"
	"google.golang.org/protobuf/proto"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
)

// FavoriteFilter is the server-side filter matching favorite chats.
// Label keys are codegen'd from sgpt/v1/labels.proto.
// AIP-160 requires map keys containing special characters (dots, slashes) to
// be quoted as string literals: labels."sgpt.com/favorite" = "true".
var FavoriteFilter = fmt.Sprintf("labels.%q = %q", sgptpb.Labels.Favorite.GetKey(), aip.LabelValueTrue)

// VisibleFilter matches conversations worth reopening — the user's own and
// sub-agents' — excluding machinery chats such as title generation.
// Pre-label chats were backfilled to "chat".
var VisibleFilter = fmt.Sprintf("(labels.%q = %q OR labels.%q = %q)",
	sgptpb.Labels.Category.GetKey(), sgptpb.Labels.Category.Chat,
	sgptpb.Labels.Category.GetKey(), sgptpb.Labels.Category.Agent)

// UserChatFilter narrows ListChats to the user's own conversations, hiding
// sub-agent chats.
var UserChatFilter = fmt.Sprintf("labels.%q = %q", sgptpb.Labels.Category.GetKey(), sgptpb.Labels.Category.Chat)

const (
	// TagsAnnotation stores chat tags, comma-separated. Tags such as GitHub
	// repos ("owner/repo") don't fit the label value pattern, hence annotations.
	TagsAnnotation = "sgpt.com/tags"
	// FilesAnnotation stores the chat's injected file paths, newline-separated.
	FilesAnnotation = "sgpt.com/files"
	// CurrentModelAnnotation stores the model resource name in use by the chat.
	CurrentModelAnnotation = "sgpt.com/current-model"
	// FilePathAnnotation stores, on an injected-file message, the path of the
	// file whose content the message carries.
	FilePathAnnotation = "sgpt.com/file-path"
	// SenderAnnotation stores, on a message another agent sent (a launcher's
	// task, a sub-agent's report), who sent it, for display.
	SenderAnnotation = "sgpt.com/sender"
	// AgentMetadataAnnotation stores, on a sub-agent chat, its
	// sgpt.v1.AgentMetadata as JSON.
	AgentMetadataAnnotation = "sgpt.com/agent-metadata"
)

// Store wraps the ai service client, which owns the chat data layer.
// The store holds no locks: write sequencing is structural — the session
// only calls UpdateChat at the end of a stream turn (or while idle), so
// writes can never race a generation.
type Store struct {
	configuration   *sgptpb.Configuration
	aiServiceClient aiservicepb.AiServiceClient
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

// parent is the user resource that owns all chats.
// Format: organizations/{organization}/users/{user}
func (s *Store) parent() string {
	return s.configuration.GetChat().GetUser()
}

// newResourceID returns the last 8 characters of a v7 UUID. The first
// characters of a v7 UUID are a timestamp prefix that collides for resources
// created within the same ~65s window; the last ones are random.
func newResourceID() string {
	id := uuid.MustNewV7().String()
	return id[len(id)-8:]
}

// CreateChat persists a new chat.
func (s *Store) CreateChat(ctx context.Context, chat *aipb.Chat) (*aipb.Chat, error) {
	// Never mutate the caller's chat: other goroutines may be reading it.
	chat = proto.CloneOf(chat)
	if _, ok := aip.GetLabel(chat, sgptpb.Labels.Category.GetKey()); !ok {
		SetCategory(chat, sgptpb.Labels.Category.Chat)
	}
	createChatRequest := &aiservicepb.CreateChatRequest{
		Parent:    s.parent(),
		RequestId: uuid.MustNewV7().String(),
		ChatId:    newResourceID(),
		Chat:      chat,
	}
	createdChat, err := s.aiServiceClient.CreateChat(ctx, createChatRequest)
	if err != nil {
		return nil, fmt.Errorf("creating chat: %w", err)
	}
	return createdChat, nil
}

// UpdateChat persists the given paths of a chat.
// Writes are masked and etag-less; the server returns the full chat payload,
// which callers should adopt as their new local state.
func (s *Store) UpdateChat(ctx context.Context, chat *aipb.Chat, paths ...string) (*aipb.Chat, error) {
	// The server bumps the chat during generation (price, update_time), so a
	// local etag is always stale; masked writes bound last-write-wins anyway.
	chat = proto.CloneOf(chat)
	chat.Etag = ""
	updateChatRequest := &aiservicepb.UpdateChatRequest{
		Chat:       chat,
		UpdateMask: pbfieldmask.FromPaths(paths...).MustValidate(&aipb.Chat{}).Proto(),
	}
	updatedChat, err := s.aiServiceClient.UpdateChat(ctx, updateChatRequest)
	if err != nil {
		return nil, fmt.Errorf("updating chat: %w", err)
	}
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
	return nil
}

// ListChats returns a page of visible chats, most recent first.
func (s *Store) ListChats(ctx context.Context, pageSize int32, pageToken, filter string) ([]*aipb.Chat, string, error) {
	listChatsRequest := &aiservicepb.ListChatsRequest{
		Parent:    s.parent(),
		PageSize:  pageSize,
		PageToken: pageToken,
		Filter:    andFilters(VisibleFilter, filter),
		OrderBy:   "create_time desc",
	}
	listChatsResponse, err := s.aiServiceClient.ListChats(ctx, listChatsRequest)
	if err != nil {
		return nil, "", fmt.Errorf("listing chats: %w", err)
	}
	return listChatsResponse.Chats, listChatsResponse.NextPageToken, nil
}

// ListChildChats returns the sub-agent chats launched by any of the chats
// with the given IDs, oldest first.
func (s *Store) ListChildChats(ctx context.Context, parentChatIDs []string) ([]*aipb.Chat, error) {
	// No clause would match every chat.
	if len(parentChatIDs) == 0 {
		return nil, nil
	}
	clauses := make([]string, len(parentChatIDs))
	for i, id := range parentChatIDs {
		clauses[i] = fmt.Sprintf("labels.%q = %q", sgptpb.Labels.ParentChat.GetKey(), id)
	}
	listChatsRequest := &aiservicepb.ListChatsRequest{
		Parent:  s.parent(),
		Filter:  strings.Join(clauses, " OR "),
		OrderBy: "create_time asc",
	}
	chats, err := aip.Paginate[*aipb.Chat](ctx, listChatsRequest, s.aiServiceClient.ListChats)
	if err != nil {
		return nil, fmt.Errorf("listing sub-agent chats: %w", err)
	}
	return chats, nil
}

// ListFavoriteChats returns the first page of favorite chats.
func (s *Store) ListFavoriteChats(ctx context.Context, pageSize int32) ([]*aipb.Chat, error) {
	chats, _, err := s.ListChats(ctx, pageSize, "", FavoriteFilter)
	return chats, err
}

// andFilters conjoins the non-empty AIP-160 filters.
func andFilters(filters ...string) string {
	var clauses []string
	for _, filter := range filters {
		if filter != "" {
			clauses = append(clauses, "("+filter+")")
		}
	}
	return strings.Join(clauses, " AND ")
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
	chat = proto.CloneOf(chat)
	SetFavoriteLabel(chat, favorite)
	return s.UpdateChat(ctx, chat, "labels")
}

// ===================== Messages =====================

// ListMessages returns the full message history of a chat, oldest first.
// Messages carrying an error status are included — the caller decides how to
// display them; the server already excludes them from generation.
func (s *Store) ListMessages(ctx context.Context, chatName string) ([]*aipb.Message, error) {
	listMessagesRequest := &aiservicepb.ListMessagesRequest{
		Parent: chatName,
		// create_time asc keeps the conversation in order and stable while paginating.
		OrderBy: "create_time asc",
	}
	messages, err := aip.Paginate[*aipb.Message](ctx, listMessagesRequest, s.aiServiceClient.ListMessages)
	if err != nil {
		return nil, fmt.Errorf("listing messages: %w", err)
	}
	return messages, nil
}

// CreateMessage persists a message under a chat.
func (s *Store) CreateMessage(ctx context.Context, chatName string, message *aipb.Message) (*aipb.Message, error) {
	createMessageRequest := &aiservicepb.CreateMessageRequest{
		Parent:    chatName,
		RequestId: uuid.MustNewV7().String(),
		Message:   message,
	}
	createdMessage, err := s.aiServiceClient.CreateMessage(ctx, createMessageRequest)
	if err != nil {
		return nil, fmt.Errorf("creating message: %w", err)
	}
	return createdMessage, nil
}

// UpdateMessage persists the given paths of a message.
func (s *Store) UpdateMessage(ctx context.Context, message *aipb.Message, paths ...string) (*aipb.Message, error) {
	// Etag-less for the same reason as UpdateChat: masked last-write-wins.
	message.Etag = ""
	updateMessageRequest := &aiservicepb.UpdateMessageRequest{
		Message:    message,
		UpdateMask: pbfieldmask.FromPaths(paths...).MustValidate(&aipb.Message{}).Proto(),
	}
	updatedMessage, err := s.aiServiceClient.UpdateMessage(ctx, updateMessageRequest)
	if err != nil {
		return nil, fmt.Errorf("updating message: %w", err)
	}
	return updatedMessage, nil
}

// DeleteMessage soft-deletes a message; the server excludes it from the
// conversation history sent to providers.
func (s *Store) DeleteMessage(ctx context.Context, name string) error {
	deleteMessageRequest := &aiservicepb.DeleteMessageRequest{
		Name:         name,
		AllowMissing: true,
	}
	if _, err := s.aiServiceClient.DeleteMessage(ctx, deleteMessageRequest); err != nil {
		return fmt.Errorf("deleting message: %w", err)
	}
	return nil
}

// StreamGenerateMessage opens a streaming generation against the AI service.
func (s *Store) StreamGenerateMessage(
	ctx context.Context,
	generateMessageRequest *aiservicepb.GenerateMessageRequest,
) (aiservicepb.AiService_StreamGenerateMessageClient, error) {
	return s.aiServiceClient.StreamGenerateMessage(ctx, generateMessageRequest)
}

// ===================== Chat helpers =====================

// IsFavorite reports whether a chat is marked as a favorite.
func IsFavorite(chat *aipb.Chat) bool {
	value, _ := aip.GetLabel(chat, sgptpb.Labels.Favorite.GetKey())
	return value == aip.LabelValueTrue
}

// SetFavoriteLabel sets or clears the favorite label on a chat in place.
func SetFavoriteLabel(chat *aipb.Chat, favorite bool) {
	if !favorite {
		aip.DeleteLabel(chat, sgptpb.Labels.Favorite.GetKey())
		return
	}
	aip.SetLabel(chat, sgptpb.Labels.Favorite.GetKey(), aip.LabelValueTrue)
}

// SetCategory labels what a chat is for (see sgpt.com/category).
func SetCategory(chat *aipb.Chat, category string) {
	aip.SetLabel(chat, sgptpb.Labels.Category.GetKey(), category)
}

// IsAgentChat reports whether a chat is a sub-agent's conversation.
func IsAgentChat(chat *aipb.Chat) bool {
	category, _ := aip.GetLabel(chat, sgptpb.Labels.Category.GetKey())
	return category == sgptpb.Labels.Category.Agent
}

// ParentChatID returns the ID of the chat that launched this sub-agent chat.
func ParentChatID(chat *aipb.Chat) string {
	value, _ := aip.GetLabel(chat, sgptpb.Labels.ParentChat.GetKey())
	return value
}

// SetParentChatID labels a chat with the ID segment of the chat that
// launched it. No-op when the parent is unnamed (not yet persisted).
func SetParentChatID(chat *aipb.Chat, parentChatName string) {
	if id := ChatID(parentChatName); id != "" {
		aip.SetLabel(chat, sgptpb.Labels.ParentChat.GetKey(), id)
	}
}

// ChatID returns the ID segment of a chat resource name; "" for anything
// else, such as the empty name of a chat not yet persisted.
func ChatID(chatName string) string {
	chatRn := &aipb.ChatRn{}
	if err := chatRn.UnmarshalString(chatName); err != nil {
		return ""
	}
	return chatRn.Chat
}

// AgentMetadata returns a sub-agent chat's metadata; nil when it has none —
// a user's chat, or one launched before it was recorded — or it is
// unreadable.
func AgentMetadata(chat *aipb.Chat) *sgptpb.AgentMetadata {
	raw, ok := chat.GetAnnotations()[AgentMetadataAnnotation]
	if !ok {
		return nil
	}
	metadata := &sgptpb.AgentMetadata{}
	if err := pbutil.JSONUnmarshal([]byte(raw), metadata); err != nil {
		return nil
	}
	return metadata
}

// SetAgentMetadata records a sub-agent chat's metadata in place.
func SetAgentMetadata(chat *aipb.Chat, metadata *sgptpb.AgentMetadata) error {
	bytes, err := pbutil.JSONMarshal(metadata)
	if err != nil {
		return fmt.Errorf("marshaling agent metadata: %w", err)
	}
	setChatAnnotation(chat, AgentMetadataAnnotation, string(bytes))
	return nil
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

// ===================== Message helpers =====================

// NewInjectedFileMessage builds a user message carrying a file's content,
// labeled so the TUI can recognize and manage it.
func NewInjectedFileMessage(path, content string) *aipb.Message {
	message := &aipb.Message{
		Role:   aipb.Role_ROLE_USER,
		Blocks: []*aipb.Block{{Content: &aipb.Block_Text{Text: content}}},
		Annotations: map[string]string{
			FilePathAnnotation: path,
		},
	}
	aip.SetLabel(message, sgptpb.Labels.InjectedFile.GetKey(), aip.LabelValueTrue)
	aip.SetLabel(message, sgptpb.Labels.Context.GetKey(), aip.LabelValueTrue)
	return message
}

// NewAttachmentMessage builds the user message carrying tool-attached
// content (images, documents), labeled as context: the user did not type it.
func NewAttachmentMessage(blocks []*aipb.Block) *aipb.Message {
	message := ai.NewUserMessage(blocks...)
	aip.SetLabel(message, sgptpb.Labels.Context.GetKey(), aip.LabelValueTrue)
	return message
}

// NewAgentMessage builds a message another agent sends this chat; sender
// describes it for display, the text carries what the model must know.
func NewAgentMessage(sender, text string) *aipb.Message {
	message := ai.NewUserMessage(ai.NewTextBlock(text))
	message.Annotations = map[string]string{SenderAnnotation: sender}
	return message
}

// MessageSender returns who sent a message from another agent, or "" when
// the user typed it.
func MessageSender(message *aipb.Message) string {
	return message.GetAnnotations()[SenderAnnotation]
}

// InjectedFilePath returns the injected file path of a message, or "" when
// the message is not an injected-file message.
func InjectedFilePath(message *aipb.Message) string {
	if value, _ := aip.GetLabel(message, sgptpb.Labels.InjectedFile.GetKey()); value != aip.LabelValueTrue {
		return ""
	}
	return message.GetAnnotations()[FilePathAnnotation]
}

// IsContextMessage reports whether a message was injected by sgpt as context
// (system prompt, injected files) rather than typed by the user.
func IsContextMessage(message *aipb.Message) bool {
	value, _ := aip.GetLabel(message, sgptpb.Labels.Context.GetKey())
	return value == aip.LabelValueTrue
}

// MessageError returns the generation error recorded on a message, if any.
// The server sets `status` on the input (and partial assistant) messages of
// a failed generation and excludes them from future generations.
func MessageError(message *aipb.Message) string {
	return message.GetStatus().GetMessage()
}

// OrphanedToolCalls returns the tool calls of the history that never received
// a result, in call order.
//
// Providers reject a conversation whose assistant `tool_use` block has no
// matching `tool_result` immediately after it (Anthropic answers 400
// "tool_use ids were found without tool_result blocks"). The history can end
// up in that state whenever a turn dies between persisting the assistant
// message and persisting its tool message — a crash, a killed process, or a
// tab closed while a call awaited review.
//
// Soft-deleted messages are skipped: the server already excludes them from
// the provider history, so their calls need no result.
func OrphanedToolCalls(messages []*aipb.Message) []*aipb.ToolCall {
	toolCallIDToToolCall := map[string]*aipb.ToolCall{}
	var orderedToolCallIDs []string
	resolvedToolCallIDSet := map[string]bool{}
	for _, message := range messages {
		if message.GetDeleteTime() != nil {
			continue
		}
		// A message the server flagged as failed is excluded from the
		// provider history, so its calls can never need answering.
		if MessageError(message) != "" {
			continue
		}
		for _, block := range message.GetBlocks() {
			if toolCall := block.GetToolCall(); toolCall != nil {
				toolCallIDToToolCall[toolCall.GetId()] = toolCall
				orderedToolCallIDs = append(orderedToolCallIDs, toolCall.GetId())
				// A result attached to the call itself still has to be
				// mirrored into a tool message to reach the provider.
				continue
			}
			if toolResult := block.GetToolResult(); toolResult != nil {
				resolvedToolCallIDSet[toolResult.GetToolCallId()] = true
			}
		}
	}

	var orphanedToolCalls []*aipb.ToolCall
	for _, toolCallID := range orderedToolCallIDs {
		if resolvedToolCallIDSet[toolCallID] {
			continue
		}
		orphanedToolCalls = append(orphanedToolCalls, toolCallIDToToolCall[toolCallID])
		// Guard against a call id repeated across the history.
		resolvedToolCallIDSet[toolCallID] = true
	}
	return orphanedToolCalls
}
