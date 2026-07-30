// Package session owns the chat lifecycle: streaming, tool handling and
// persistence (via the store). All methods that mutate state are blocking and
// sequential; the TUI drives the session from tea.Cmd goroutines.
package session

import (
	"context"
	"fmt"
	"sync"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"
	spb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/status"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/store"
	"github.com/malonaz/sgpt/internal/tools"
)

// State is the session lifecycle phase; the UI derives what to show
// (input, spinner, review hints) from it rather than from ad-hoc booleans.
type State int

const (
	StateIdle State = iota
	StateStreaming
	StateExecutingTools
	StateAwaitingReview
)

// Params bundles per-chat parameters for a session.
type Params struct {
	Model              *aipb.Model
	Role               *sgptpb.Role
	MaxTokens          int32
	Temperature        float64
	ReasoningEffort    aipb.ReasoningEffort
	Tools              []string
	Chat               string
	AdditionalMessages []*aipb.Message
	InjectedFiles      []string
}

// Session drives a single chat conversation.
type Session struct {
	ctx      context.Context
	params   Params
	store    *store.Store
	registry *tools.Registry

	mu                sync.Mutex
	chat              *sgptpb.Chat
	streamingMessage  *aipb.Message
	streamError       error
	state             State
	executingToolCall string
	cancelStream      context.CancelFunc

	totalModelUsage *aipb.ModelUsage
	lastModelUsage  *aipb.ModelUsage

	eventCh chan Event
}

func New(
	ctx context.Context,
	chatStore *store.Store,
	registry *tools.Registry,
	chat *sgptpb.Chat,
	params Params,
) *Session {
	return &Session{
		ctx:             ctx,
		params:          params,
		store:           chatStore,
		registry:        registry,
		chat:            chat,
		totalModelUsage: &aipb.ModelUsage{},
		lastModelUsage:  &aipb.ModelUsage{},
		eventCh:         make(chan Event, 64),
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
	// Errors must always reach the TUI; block until delivered.
	s.eventCh <- ErrorEvent{Err: err}
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

func (s *Session) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *Session) IsStreaming() bool {
	return s.State() == StateStreaming
}

// Busy reports whether the session is actively working (input disabled).
func (s *Session) Busy() bool {
	state := s.State()
	return state == StateStreaming || state == StateExecutingTools
}

// ExecutingToolCallID identifies the tool call currently in flight, if any.
func (s *Session) ExecutingToolCallID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.executingToolCall
}

func (s *Session) setState(state State) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
}

func (s *Session) setExecutingToolCall(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.executingToolCall = id
}

func (s *Session) Params() Params {
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
	s.state = StateStreaming
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
// Auto-execute tool calls run immediately (some already ran eagerly during
// streaming). Manual ones pause the turn for user review.
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
			s.setState(StateIdle)
			s.refresh()
			return
		}

		if !allAutoExecuted {
			// Manual tool calls remain pending for user accept/reject.
			s.refresh()
			return
		}

		// All auto-executed — loop to stream again with tool results.
		s.setState(StateStreaming)
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
		chat, err := s.store.CreateChat(s.ctx, s.chat)
		if err != nil {
			return err
		}
		s.chat = chat
		return nil
	}

	chat, err := s.store.UpdateChat(s.ctx, s.chat, "tags", "files", "metadata")
	if err != nil {
		return err
	}
	s.chat = chat
	return nil
}

// ToggleFavorite flips the favorite tag on the chat and persists.
// Returns true if the chat is now a favorite.
func (s *Session) ToggleFavorite() bool {
	s.mu.Lock()
	favorite := !store.HasTag(s.chat, store.FavoriteTag)
	store.SetTag(s.chat, store.FavoriteTag, favorite)
	s.mu.Unlock()

	if err := s.saveChat(); err != nil {
		s.emitError(fmt.Errorf("saving favorite: %w", err))
	}
	return favorite
}

func statusToProto(err error) *spb.Status {
	if err == nil {
		return nil
	}
	return status.Convert(err).Proto()
}

// Registry exposes the tool registry so the TUI can delegate tool-dictated
// request rendering (timeline.RequestRenderer).
func (s *Session) Registry() *tools.Registry {
	return s.registry
}
