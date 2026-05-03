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
// The TUI reads state via accessor methods; mutations happen through actions.
type Session struct {
	ctx    context.Context
	params cliservice.SessionParams

	aiClient   aiservicepb.AiServiceClient
	chatClient sgptservicepb.SgptServiceClient
	config     *sgptpb.Configuration

	chatMu           sync.Mutex
	chat             *sgptpb.Chat
	streamingMessage *aipb.Message
	streamError      error
	streaming        bool
	cancelStream     context.CancelFunc

	totalModelUsage *aipb.ModelUsage
	lastModelUsage  *aipb.ModelUsage

	toolHandlerIDToHandler map[string]tools.Handler

	// Buffered channel so events aren't lost between reads.
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

// Events returns the channel the TUI reads session events from.
func (s *Session) Events() <-chan Event {
	return s.eventCh
}

func (s *Session) emit(event Event) {
	select {
	case s.eventCh <- event:
	default:
	}
}

// Chat returns the current chat proto (source of truth).
func (s *Session) Chat() *sgptpb.Chat {
	s.chatMu.Lock()
	defer s.chatMu.Unlock()
	return s.chat
}

func (s *Session) StreamingMessage() *aipb.Message {
	return s.streamingMessage
}

func (s *Session) StreamError() error {
	return s.streamError
}

func (s *Session) IsStreaming() bool {
	return s.streaming
}

func (s *Session) Params() cliservice.SessionParams {
	return s.params
}

func (s *Session) TotalModelUsage() *aipb.ModelUsage {
	return s.totalModelUsage
}

func (s *Session) LastModelUsage() *aipb.ModelUsage {
	return s.lastModelUsage
}

func (s *Session) SetReasoningEffort(effort aipb.ReasoningEffort) {
	s.params.ReasoningEffort = effort
}

// PendingToolCalls derives pending tool calls from proto annotations.
func (s *Session) PendingToolCalls() []*aipb.ToolCall {
	s.chatMu.Lock()
	defer s.chatMu.Unlock()
	messages := s.chat.GetMetadata().GetMessages()
	if len(messages) == 0 {
		return nil
	}
	// Walk backwards to find the last assistant message with pending tool calls.
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

// SendMessage appends a user message and starts streaming.
func (s *Session) SendMessage(text string) {
	userMessage := ai.NewUserMessage(ai.NewTextBlock(text))

	s.chatMu.Lock()
	s.chat.Metadata.Messages = append(s.chat.Metadata.Messages, &sgptpb.Message{Message: userMessage})
	s.chatMu.Unlock()

	s.streaming = true
	s.streamError = nil
	s.startStreaming()
}

// CancelStream cancels an active stream.
func (s *Session) CancelStream() {
	if s.cancelStream != nil {
		s.cancelStream()
	}
}

// SaveChat persists the chat to the backend.
func (s *Session) SaveChat() {
	go func() {
		s.chatMu.Lock()
		defer s.chatMu.Unlock()

		if s.chat.GetName() == "" {
			createChatRequest := &sgptservicepb.CreateChatRequest{
				RequestId: uuid.MustNewV7().String(),
				ChatId:    uuid.MustNewV7().String()[:8],
				Chat:      s.chat,
			}
			chat, err := s.chatClient.CreateChat(s.ctx, createChatRequest)
			if err != nil {
				s.emit(ErrorEvent{Text: fmt.Sprintf("Failed to create chat: %v", err)})
				return
			}
			s.chat = chat
		} else {
			updateChatRequest := &sgptservicepb.UpdateChatRequest{
				Chat:       s.chat,
				UpdateMask: pbfieldmask.FromPaths("tags", "files", "metadata").MustValidate(&sgptpb.Chat{}).Proto(),
			}
			chat, err := s.chatClient.UpdateChat(s.ctx, updateChatRequest)
			if err != nil {
				s.emit(ErrorEvent{Text: fmt.Sprintf("Failed to update chat: %v", err)})
				return
			}
			s.chat = chat
		}
		s.emit(ChatSavedEvent{Chat: s.chat})
	}()
}

// messagesForAPI builds the message list to send to the AI provider.
func (s *Session) messagesForAPI() []*aipb.Message {
	s.chatMu.Lock()
	defer s.chatMu.Unlock()

	messages := make([]*aipb.Message, 0, len(s.params.AdditionalMessages)+len(s.chat.Metadata.Messages))
	messages = append(messages, s.params.AdditionalMessages...)
	for _, chatMessage := range s.chat.Metadata.Messages {
		if chatMessage.Error == nil {
			messages = append(messages, chatMessage.Message)
		}
	}
	return messages
}

func statusToProto(err error) *spb.Status {
	if err == nil {
		return nil
	}
	return status.Convert(err).Proto()
}

func (s *Session) allTools() []*aipb.Tool {
	var toolsList []*aipb.Tool
	if s.params.EnableTools {
		toolsList = append(toolsList, tools.ShellCommand, tools.ReadFiles)
	}
	if s.params.ToolEngineManager != nil {
		toolsList = append(toolsList, s.params.ToolEngineManager.GetTools()...)
	}
	return toolsList
}
