package session

import (
	"context"
	"fmt"
	"io"
	"time"

	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"
	"google.golang.org/protobuf/proto"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/debug"
	"github.com/malonaz/sgpt/internal/tools"
)

const renderThrottleInterval = 66 * time.Millisecond

// stream runs a single streaming request to the AI provider. Blocks until the
// stream completes or errors. Returns the finalized blocks.
func (s *Session) stream() ([]*aipb.Block, error) {
	streamCtx, cancel := context.WithCancel(s.ctx)
	defer cancel()

	s.mu.Lock()
	s.cancelStream = cancel
	s.mu.Unlock()

	textToTextStreamRequest := &aiservicepb.TextToTextStreamRequest{
		Model:    s.params.Model.Name,
		Messages: s.messagesForAPI(),
		Tools:    s.registry.Tools(),
		ToolSets: s.registry.ToolSets(),
		Configuration: &aiservicepb.TextToTextConfiguration{
			MaxTokens:       s.params.MaxTokens,
			Temperature:     s.params.Temperature,
			ReasoningEffort: s.params.ReasoningEffort,
		},
	}
	debug.LogProto("request", textToTextStreamRequest, "messages", "tools")
	stream, err := s.store.TextToTextStream(streamCtx, textToTextStreamRequest)
	if err != nil {
		s.finalizeStream(nil, err)
		return nil, fmt.Errorf("opening stream: %w", err)
	}

	accumulator := ai.NewTextToTextAccumulator()
	lastRender := time.Now()
	pendingRender := false
	reviewedToolCallCount := 0

	checkRender := func() {
		if time.Since(lastRender) >= renderThrottleInterval {
			s.refresh()
			lastRender = time.Now()
			pendingRender = false
		} else {
			pendingRender = true
		}
	}

	for {
		select {
		case <-streamCtx.Done():
			if pendingRender {
				s.refresh()
			}
			s.finalizeStream(accumulator.Message.GetBlocks(), streamCtx.Err())
			return nil, fmt.Errorf("stream cancelled: %w", streamCtx.Err())
		default:
		}

		response, err := stream.Recv()
		if err != nil {
			if pendingRender {
				s.refresh()
			}
			if err == io.EOF {
				blocks := accumulator.Message.GetBlocks()
				s.finalizeStream(blocks, nil)
				return blocks, nil
			}
			s.finalizeStream(accumulator.Message.GetBlocks(), err)
			return nil, fmt.Errorf("receiving stream: %w", err)
		}
		debug.LogProto("response", response)

		if err := accumulator.Add(response); err != nil {
			if pendingRender {
				s.refresh()
			}
			s.finalizeStream(accumulator.Message.GetBlocks(), err)
			return nil, fmt.Errorf("accumulating stream response: %w", err)
		}

		s.mu.Lock()
		s.streamingMessage = accumulator.Message
		s.mu.Unlock()

		if modelUsage := response.GetModelUsage(); modelUsage != nil {
			s.mu.Lock()
			proto.Merge(s.lastModelUsage, modelUsage)
			s.mu.Unlock()
		}

		// Review new tool calls eagerly as they arrive during streaming.
		toolCallBlocks := ai.FilterBlocks(accumulator.Message.GetBlocks(), ai.BlockTypeToolCall)
		for len(toolCallBlocks) > reviewedToolCallCount {
			s.reviewToolCallEagerly(toolCallBlocks[reviewedToolCallCount].GetToolCall())
			reviewedToolCallCount++
		}

		checkRender()
	}
}

// reviewToolCallEagerly attaches display/auto-execute metadata to a tool call
// as soon as it appears in the stream. Read-only (auto-execute) tools are
// executed immediately — sequentially, in arrival order — so the turn keeps
// moving instead of waiting for the full response.
func (s *Session) reviewToolCallEagerly(toolCall *aipb.ToolCall) {
	debug.LogProto("eager", toolCall)
	if !s.registry.Handles(toolCall) {
		return
	}
	if _, err := s.registry.Review(s.ctx, toolCall); err != nil {
		// Feed the failure back to the model as a tool result instead of
		// aborting the turn; leaving the call unresolved poisons the history.
		toolCall.Result = ai.NewErrorToolResult(toolCall.Name, toolCall.Id, err)
		tools.SetToolCallStatus(toolCall, tools.ToolCallStatusFailed)
		s.refresh()
		return
	}
	// Review can attach a result directly (e.g. discovery tools); executing
	// those through the registry would fail with a parse-type mismatch.
	if toolCall.GetResult() != nil {
		tools.SetToolCallStatus(toolCall, tools.ToolCallStatusAccepted)
		s.refresh()
		return
	}
	metadata, err := tools.ParseToolCallMetadata(toolCall)
	if err != nil || !metadata.GetAutoExecute() {
		return
	}

	tools.SetToolCallStatus(toolCall, tools.ToolCallStatusAccepted)
	s.setExecutingToolCall(toolCall.GetId())
	s.refresh()
	toolResult, err := s.registry.Execute(s.ctx, toolCall)
	if err != nil {
		toolResult = ai.NewErrorToolResult(toolCall.Name, toolCall.Id, err)
	}
	toolCall.Result = toolResult
	s.setExecutingToolCall("")
	s.refresh()
}

// finalizeStream commits the streamed message to the chat and resets stream state.
func (s *Session) finalizeStream(blocks []*aipb.Block, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Persist even if no tokens arrived — gRPC stream errors typically surface
	// on the first Recv(), when streamingMessage is still nil. Without this,
	// the error would only live in the ephemeral streamError and could vanish.
	if s.streamingMessage != nil || err != nil {
		assistantMessage := ai.NewAssistantMessage(blocks...)

		for _, block := range ai.FilterBlocks(blocks, ai.BlockTypeToolCall) {
			// Don't clobber statuses set during streaming (e.g. failed).
			if tools.GetToolCallStatus(block.GetToolCall()) != "" {
				continue
			}
			if block.GetToolCall().GetResult() != nil {
				tools.SetToolCallStatus(block.GetToolCall(), tools.ToolCallStatusAccepted)
			} else {
				tools.SetToolCallStatus(block.GetToolCall(), tools.ToolCallStatusPending)
			}
		}

		chatMessage := &sgptpb.Message{
			Message: assistantMessage,
		}
		if err != nil {
			chatMessage.Error = statusToProto(err)
		}
		s.chat.Metadata.Messages = append(s.chat.Metadata.Messages, chatMessage)
	}

	s.streamingMessage = nil
	s.state = StateIdle
	s.cancelStream = nil
	s.streamError = err
}
