package session

import (
	"context"
	"io"
	"time"

	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"
	"google.golang.org/protobuf/proto"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/tools"
)

const renderThrottleInterval = 66 * time.Millisecond

func (s *Session) startStreaming() {
	streamCtx, cancel := context.WithCancel(s.ctx)
	s.cancelStream = cancel

	messages := s.messagesForAPI()

	textToTextStreamRequest := &aiservicepb.TextToTextStreamRequest{
		Model:    s.params.Model.Name,
		Messages: messages,
		Tools:    s.allTools(),
		Configuration: &aiservicepb.TextToTextConfiguration{
			MaxTokens:       s.params.MaxTokens,
			Temperature:     s.params.Temperature,
			ReasoningEffort: s.params.ReasoningEffort,
		},
	}

	go func() {
		defer func() {
			ai.AggregateModelUsage(s.totalModelUsage, s.lastModelUsage)
			*s.lastModelUsage = aipb.ModelUsage{}
			s.emit(StreamChunkEvent{})
		}()

		stream, err := s.aiClient.TextToTextStream(streamCtx, textToTextStreamRequest)
		if err != nil {
			s.finalizeStream(nil, err)
			cancel()
			return
		}

		accumulator := ai.NewTextToTextAccumulator()
		lastRender := time.Now()
		pendingRender := false

		checkRender := func() {
			if time.Since(lastRender) >= renderThrottleInterval {
				s.emit(StreamChunkEvent{})
				lastRender = time.Now()
				pendingRender = false
			} else {
				pendingRender = true
			}
		}

		finalize := func(err error) {
			if pendingRender {
				s.emit(StreamChunkEvent{})
			}
			s.finalizeStream(accumulator.Message.GetBlocks(), err)
			cancel()
		}

		for {
			select {
			case <-streamCtx.Done():
				finalize(streamCtx.Err())
				return
			default:
			}

			response, err := stream.Recv()
			if err != nil {
				if err == io.EOF {
					finalize(nil)
				} else {
					finalize(err)
				}
				return
			}
			if err := accumulator.Add(response); err != nil {
				finalize(err)
				return
			}

			s.streamingMessage = accumulator.Message

			switch content := response.Content.(type) {
			case *aiservicepb.TextToTextStreamResponse_ModelUsage:
				proto.Merge(s.lastModelUsage, content.ModelUsage)
			default:
			}

			checkRender()
		}
	}()
}

func (s *Session) finalizeStream(blocks []*aipb.Block, err error) {
	s.chatMu.Lock()

	if s.streamingMessage != nil {
		assistantMessage := ai.NewAssistantMessage(blocks...)

		// Annotate tool calls with pending status.
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
	s.chatMu.Unlock()

	// Extract tool calls from the finalized blocks.
	var toolCalls []*aipb.ToolCall
	for _, block := range ai.FilterBlocks(blocks, ai.BlockTypeToolCall) {
		toolCalls = append(toolCalls, block.GetToolCall())
	}

	s.emit(StreamDoneEvent{Err: err, Blocks: blocks})

	if len(toolCalls) > 0 && err == nil {
		s.SaveChat()
		s.handleToolCalls(toolCalls)
		return
	}
	if err == nil {
		s.SaveChat()
	}
}
