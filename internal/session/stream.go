package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/malonaz/sgpt/internal/debug"
)

// renderInterval paces refresh events: token streams emit far faster than a
// terminal can usefully render.
const renderInterval = 66 * time.Millisecond

// stream runs a single generation against the AI service. The input messages
// (user text, tool results) are persisted server-side along with the
// generated assistant message. Blocks until the stream completes or errors.
// Returns the assistant message.
func (t *turn) stream(inputMessages []*aipb.Message) (*aipb.Message, error) {
	s := t.session

	// Snapshot once: the UI goroutine mutates params (reasoning effort, tool
	// selection) while this turn runs.
	params := s.Params()
	generateMessageRequest := &aiservicepb.GenerateMessageRequest{
		Parent:   s.Chat().GetName(),
		Model:    params.Model.GetName(),
		Messages: inputMessages,
		// Filtered by the user's runtime tool selection (SetEnabledTools).
		Tools:    s.advertisedTools(),
		ToolSets: s.advertisedToolSets(),
		Configuration: &aiservicepb.MessageGenerationConfiguration{
			MaxTokens:       params.MaxTokens,
			Temperature:     params.Temperature,
			ReasoningEffort: params.ReasoningEffort,
			// Stream partial tool calls so the TUI can render them as they build.
			StreamPartialToolCalls: true,
		},
	}
	debug.LogProto("request", generateMessageRequest, "messages", "tools")
	stream, err := s.store.StreamGenerateMessage(t.ctx, generateMessageRequest)
	if err != nil {
		s.finalizeStream(nil, err)
		return nil, fmt.Errorf("opening stream: %w", err)
	}

	accumulator := ai.NewMessageAccumulator()
	resolvedToolCallCount := 0

	for {
		select {
		case <-t.ctx.Done():
			s.finalizeStream(accumulator.Message, t.ctx.Err())
			return nil, fmt.Errorf("stream cancelled: %w", t.ctx.Err())
		default:
		}

		response, err := stream.Recv()
		if err == io.EOF {
			generatedMessage := accumulator.Message
			s.finalizeStream(generatedMessage, nil)
			return generatedMessage, nil
		}
		if err != nil {
			s.finalizeStream(accumulator.Message, err)
			return nil, fmt.Errorf("receiving stream: %w", err)
		}
		debug.LogProto("response", response)

		// The final event carries the persisted assistant message. The
		// locally accumulated blocks are the richer truth — they hold the
		// results and review state attached during streaming — so adopt only
		// the fields the server owns instead of replacing the message.
		if persistedMessage := response.GetGeneratedMessage(); persistedMessage != nil {
			adoptPersistedIdentity(accumulator.Message, persistedMessage)
			continue
		}

		if err := accumulator.Add(response); err != nil {
			s.finalizeStream(accumulator.Message, err)
			return nil, fmt.Errorf("accumulating stream response: %w", err)
		}

		s.mu.Lock()
		s.streamingMessage = accumulator.Message
		s.mu.Unlock()

		if modelUsage := response.GetModelUsage(); modelUsage != nil {
			// The server streams cumulative usage: replace, don't add.
			s.mu.Lock()
			s.lastModelUsage = proto.CloneOf(modelUsage)
			s.mu.Unlock()
		}

		// Resolve tool calls eagerly as they complete mid-stream: read-only
		// and whitelisted tools run immediately, in arrival order, so the
		// turn keeps moving instead of waiting for the full response.
		toolCallBlocks := ai.FilterBlocks(accumulator.Message.GetBlocks(), ai.BlockTypeToolCall)
		for len(toolCallBlocks) > resolvedToolCallCount {
			toolCall := toolCallBlocks[resolvedToolCallCount].GetToolCall()
			debug.LogProto("eager", toolCall)
			s.resolveToolCall(t.ctx, toolCall, true)
			resolvedToolCallCount++
		}

		s.refresh()
	}
}

// adoptPersistedIdentity copies the server-owned fields of the persisted
// assistant message onto the locally accumulated one. Blocks deliberately
// stay local: they carry the tool results and review state attached during
// streaming, which the server copy does not have.
func adoptPersistedIdentity(localMessage, persistedMessage *aipb.Message) {
	localMessage.Name = persistedMessage.GetName()
	localMessage.Etag = persistedMessage.GetEtag()
	localMessage.CreateTime = persistedMessage.GetCreateTime()
	localMessage.UpdateTime = persistedMessage.GetUpdateTime()
	localMessage.Labels = persistedMessage.GetLabels()
	localMessage.Model = persistedMessage.GetModel()
	localMessage.ModelUsage = persistedMessage.GetModelUsage()
	localMessage.Price = persistedMessage.GetPrice()
}

// IsCancelError reports whether err represents a user-initiated cancellation
// (local context or gRPC Canceled), so the UI can render it calmly as an
// interruption instead of a failure.
func IsCancelError(err error) bool {
	return errors.Is(err, context.Canceled) || grpcstatus.Code(err) == codes.Canceled
}

// finalizeStream commits the streamed message to the local history and
// resets stream state. On failure the server has already flagged the
// persisted input (and partial assistant) messages with an error status;
// the local copy mirrors that for display.
func (s *Session) finalizeStream(message *aipb.Message, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if message != nil && (len(message.GetBlocks()) > 0 || err != nil) {
		// Drop partial tool calls left over from a cancelled/failed stream:
		// they fail assistant-message validation and can never be resolved.
		blocks := make([]*aipb.Block, 0, len(message.GetBlocks()))
		for _, block := range message.GetBlocks() {
			if block.GetPartialToolCall() == nil {
				blocks = append(blocks, block)
			}
		}
		message.Blocks = blocks

		for _, block := range ai.FilterBlocks(blocks, ai.BlockTypeToolCall) {
			toolCall := block.GetToolCall()
			// The stream died: the server excluded this message from provider
			// history, so a verdict could never reach the model. Resolve as
			// failed rather than leaving an unreviewable call.
			if err != nil && toolCall.GetResult() == nil {
				toolCall.Result = ai.NewErrorToolResult(toolCall.Name, toolCall.Id, err)
			}
		}

		if err != nil {
			// Mirror the server-side error status locally for display.
			message.Status = grpcstatus.Convert(err).Proto()
		}
		if len(message.GetBlocks()) > 0 || err != nil {
			s.messages = append(s.messages, message)
			s.invalidatePrice()
		}
	}

	s.streamingMessage = nil
	s.streamError = err
}
