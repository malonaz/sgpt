package session

import (
	"context"
	"fmt"
	"sync"

	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"
	"github.com/malonaz/core/go/pbutil/pbfieldmask"
	"github.com/malonaz/core/go/uuid"
	spb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/status"

	cliservice "github.com/malonaz/sgpt/cli/cli_service"
	sgptservicepb "github.com/malonaz/sgpt/genproto/sgpt/sgpt_service/v1"
	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/tools"
)

// Session owns the chat lifecycle: streaming, tool handling, persistence.
// All methods that mutate state are blocking and sequential. The TUI
// drives the session from tea.Cmd goroutines managed by bubbletea.
type Session struct {
	ctx     context.Context
	params  cliservice.SessionParams
	service *cliservice.Service

	aiClient   aiservicepb.AiServiceClient
	chatClient sgptservicepb.SgptServiceClient
	config     *sgptpb.Configuration

	mu               sync.Mutex
	chat             *sgptpb.Chat
	streamingMessage *aipb.Message
	streamError      error
	streaming        bool
	cancelStream     context.CancelFunc

	totalModelUsage *aipb.ModelUsage
	lastModelUsage  *aipb.ModelUsage

	toolHandlerIDToHandler map[string]tools.Handler

	eventCh chan Event
}

func New(
	ctx context.Context,
	service *cliservice.Service,
	chat *sgptpb.Chat,
	params cliservice.SessionParams,
) *Session {
	toolHandlerIDToHandler := map[string]tools.Handler{
		tools.HandlerIDShell:     &tools.ShellHandler{},
		tools.HandlerIDReadFiles: &tools.ReadFilesHandler{},
	}
	if params.ToolEngineManager != nil {
		toolHandlerIDToHandler[tools.HandlerIDEngine] = params.ToolEngineManager
	}

	return &Session{
		ctx:                    ctx,
		params:                 params,
		service:                service,
		aiClient:               service.AIClient,
		chatClient:             service.ChatClient,
		config:                 service.Config,
		chat:                   chat,
		totalModelUsage:        &aipb.ModelUsage{},
		lastModelUsage:         &aipb.ModelUsage{},
		toolHandlerIDToHandler: toolHandlerIDToHandler,
		eventCh:                make(chan Event, 64),
	}
}

func (s *Session) Events() <-chan Event {
	return s.eventCh
}

func (s *Session) emit(event Event) {
	select {
	case s.eventCh <- event:
	default:
	}
}

func (s *Session) refresh() {
	s.emit(RefreshEvent{})
}

func (s *Session) emitError(err error) {
	s.emit(ErrorEvent{Err: err})
}

func (s *Session) Chat() *sgptpb.Chat {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chat
}

func (s *Session) StreamingMessage() *aipb.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streamingMessage
}

func (s *Session) StreamError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streamError
}

func (s *Session) IsStreaming() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streaming
}

func (s *Session) Params() cliservice.SessionParams {
	return s.params
}

func (s *Session) TotalModelUsage() *aipb.ModelUsage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.totalModelUsage
}

func (s *Session) LastModelUsage() *aipb.ModelUsage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastModelUsage
}

func (s *Session) SetReasoningEffort(effort aipb.ReasoningEffort) {
	s.params.ReasoningEffort = effort
}

func (s *Session) PendingToolCalls() []*aipb.ToolCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pendingToolCallsLocked()
}

func (s *Session) pendingToolCallsLocked() []*aipb.ToolCall {
	messages := s.chat.GetMetadata().GetMessages()
	if len(messages) == 0 {
		return nil
	}
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i].GetMessage()
		if message.GetRole() != aipb.Role_ROLE_ASSISTANT {
			continue
		}
		var pending []*aipb.ToolCall
		for _, block := range ai.FilterBlocks(message.GetBlocks(), ai.BlockTypeToolCall) {
			if tools.GetToolCallStatus(block.GetToolCall()) == tools.ToolCallStatusPending {
				pending = append(pending, block.GetToolCall())
			}
		}
		return pending
	}
	return nil
}

func (s *Session) SendMessage(text string) {
	userMessage := ai.NewUserMessage(ai.NewTextBlock(text))

	s.mu.Lock()
	s.chat.Metadata.Messages = append(s.chat.Metadata.Messages, &sgptpb.Message{Message: userMessage})
	s.streaming = true
	s.streamError = nil
	s.mu.Unlock()

	s.refresh()
	s.runTurn()
}

func (s *Session) CancelStream() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancelStream != nil {
		s.cancelStream()
	}
}

// runTurn executes a complete turn: stream → process tool calls → save.
// Auto-execute tool calls are handled immediately. Non-auto ones pause for user.
// Loops if all tool calls in a turn were auto-executed.
func (s *Session) runTurn() {
	for {
		blocks, err := s.stream()

		s.mu.Lock()
		ai.AggregateModelUsage(s.totalModelUsage, s.lastModelUsage)
		*s.lastModelUsage = aipb.ModelUsage{}
		s.mu.Unlock()

		if err != nil {
			s.refresh()
			return
		}

		var toolCalls []*aipb.ToolCall
		for _, block := range ai.FilterBlocks(blocks, ai.BlockTypeToolCall) {
			toolCalls = append(toolCalls, block.GetToolCall())
		}

		if len(toolCalls) == 0 {
			if err := s.saveChat(); err != nil {
				s.emitError(fmt.Errorf("saving chat: %w", err))
			}
			s.refresh()
			return
		}

		allAutoExecuted, err := s.processToolCallsAfterStream(toolCalls)
		if err != nil {
			s.emitError(fmt.Errorf("processing tool calls: %w", err))
			s.refresh()
			return
		}

		if !allAutoExecuted {
			// Manual tool calls remain pending for user accept/reject.
			s.refresh()
			return
		}

		// All auto-executed — loop to stream again with tool results.
		s.mu.Lock()
		s.streaming = true
		s.mu.Unlock()
	}
}

func (s *Session) messagesForAPI() []*aipb.Message {
	s.mu.Lock()
	defer s.mu.Unlock()

	messages := make([]*aipb.Message, 0, len(s.params.AdditionalMessages)+len(s.chat.Metadata.Messages))
	messages = append(messages, s.params.AdditionalMessages...)
	for _, chatMessage := range s.chat.Metadata.Messages {
		if chatMessage.Error == nil {
			messages = append(messages, chatMessage.Message)
		}
	}
	return messages
}

func (s *Session) saveChat() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.chat.GetName() == "" {
		createChatRequest := &sgptservicepb.CreateChatRequest{
			RequestId: uuid.MustNewV7().String(),
			ChatId:    uuid.MustNewV7().String()[:8],
			Chat:      s.chat,
		}
		chat, err := s.chatClient.CreateChat(s.ctx, createChatRequest)
		if err != nil {
			return fmt.Errorf("creating chat: %w", err)
		}
		s.chat = chat
		return nil
	}

	updateChatRequest := &sgptservicepb.UpdateChatRequest{
		Chat:       s.chat,
		UpdateMask: pbfieldmask.FromPaths("tags", "files", "metadata").MustValidate(&sgptpb.Chat{}).Proto(),
	}
	chat, err := s.chatClient.UpdateChat(s.ctx, updateChatRequest)
	if err != nil {
		return fmt.Errorf("updating chat: %w", err)
	}
	s.chat = chat
	return nil
}

// ToggleFavorite adds or removes the "favorite" tag from the chat and persists.
// Returns true if the chat is now a favorite.
func (s *Session) ToggleFavorite() bool {
	s.mu.Lock()
	tags := s.chat.GetTags()
	isFavorite := false
	for _, tag := range tags {
		if tag == "favorite" {
			isFavorite = true
			break
		}
	}

	if isFavorite {
		filtered := make([]string, 0, len(tags)-1)
		for _, tag := range tags {
			if tag != "favorite" {
				filtered = append(filtered, tag)
			}
		}
		s.chat.Tags = filtered
	} else {
		s.chat.Tags = append(s.chat.Tags, "favorite")
	}
	nowFavorite := !isFavorite
	s.mu.Unlock()

	if err := s.saveChat(); err != nil {
		s.emitError(fmt.Errorf("saving favorite: %w", err))
	}
	return nowFavorite
}

func statusToProto(err error) *spb.Status {
	if err == nil {
		return nil
	}
	return status.Convert(err).Proto()
}

func (s *Session) allTools() []*aipb.Tool {
	var toolsList []*aipb.Tool
	if s.params.ToolEngineManager != nil {
		toolsList = append(toolsList, s.params.ToolEngineManager.GetTools()...)
	}
	return toolsList
}
