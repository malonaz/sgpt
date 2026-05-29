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

	messages := s.messagesForAPI()

	textToTextStreamRequest := &aiservicepb.TextToTextStreamRequest{
		Model:    s.params.Model.Name,
		Messages: messages,
		Tools:    s.allTools(),
		ToolSets: s.allToolSets(),
		Configuration: &aiservicepb.TextToTextConfiguration{
			MaxTokens:       s.params.MaxTokens,
			Temperature:     s.params.Temperature,
			ReasoningEffort: s.params.ReasoningEffort,
		},
	}
	debug.LogProto("request", textToTextStreamRequest, "messages", "tools")
	stream, err := s.aiClient.TextToTextStream(streamCtx, textToTextStreamRequest)
	if err != nil {
		s.finalizeStream(nil, err)
		return nil, fmt.Errorf("opening stream: %w", err)
	}

	accumulator := ai.NewTextToTextAccumulator()
	lastRender := time.Now()
	pendingRender := false
	handledToolCallCount := 0

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

		switch content := response.Content.(type) {
		case *aiservicepb.TextToTextStreamResponse_ModelUsage:
			s.mu.Lock()
			proto.Merge(s.lastModelUsage, content.ModelUsage)
			s.mu.Unlock()
		default:
		}

		// Handle new tool calls eagerly as they arrive during streaming.
		toolCallBlocks := ai.FilterBlocks(accumulator.Message.GetBlocks(), ai.BlockTypeToolCall)
		for len(toolCallBlocks) > handledToolCallCount {
			toolCall := toolCallBlocks[handledToolCallCount].GetToolCall()
			s.handleToolCallEagerly(toolCall)
			handledToolCallCount++
		}

		checkRender()
	}
}

// handleToolCallEagerly labels a tool call with metadata/annotations as soon as
// it appears in the stream, without waiting for the stream to complete.
func (s *Session) handleToolCallEagerly(toolCall *aipb.ToolCall) {
	debug.LogProto("eager", toolCall)
	handlerID := toolCall.GetAnnotations()[tools.ToolHandlerIDAnnotation]
	handler, ok := s.toolHandlerIDToHandler[handlerID]
	if !ok {
		return
	}
	if _, err := handler.HandleToolCall(s.ctx, toolCall); err != nil {
		s.emitError(fmt.Errorf("eagerly handling tool call %q: %w", toolCall.Name, err))
	}
}

// finalizeStream commits the streamed message to the chat and resets stream state.
func (s *Session) finalizeStream(blocks []*aipb.Block, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.streamingMessage != nil {
		assistantMessage := ai.NewAssistantMessage(blocks...)

		for _, block := range ai.FilterBlocks(blocks, ai.BlockTypeToolCall) {
			tools.SetToolCallStatus(block.GetToolCall(), tools.ToolCallStatusPending)
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
	s.streaming = false
	s.cancelStream = nil
	s.streamError = err
}
