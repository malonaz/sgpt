package session

import (
	"context"
	"fmt"
	"io"
	"time"

	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/malonaz/sgpt/internal/debug"
	"github.com/malonaz/sgpt/internal/tool"
)

const renderThrottleInterval = 66 * time.Millisecond

// stream runs a single generation against the AI service. The input messages
// (user text, tool results) are persisted server-side along with the
// generated assistant message. Blocks until the stream completes or errors.
// Returns the persisted assistant message.
func (s *Session) stream(inputMessages []*aipb.Message) (*aipb.Message, error) {
	streamCtx, cancel := context.WithCancel(s.ctx)
	defer cancel()

	s.mu.Lock()
	s.cancelStream = cancel
	s.mu.Unlock()

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
	stream, err := s.store.StreamGenerateMessage(streamCtx, generateMessageRequest)
	if err != nil {
		s.finalizeStream(nil, err)
		return nil, fmt.Errorf("opening stream: %w", err)
	}

	accumulator := ai.NewMessageAccumulator()
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
			s.finalizeStream(accumulator.Message, streamCtx.Err())
			return nil, fmt.Errorf("stream cancelled: %w", streamCtx.Err())
		default:
		}

		response, err := stream.Recv()
		if err != nil {
			if pendingRender {
				s.refresh()
			}
			if err == io.EOF {
				generatedMessage := accumulator.Message
				s.finalizeStream(generatedMessage, nil)
				return generatedMessage, nil
			}
			s.finalizeStream(accumulator.Message, err)
			return nil, fmt.Errorf("receiving stream: %w", err)
		}
		debug.LogProto("response", response)

		// The final event carries the persisted assistant message; its tool
		// call blocks are fresh server-side copies without the review state
		// (status, metadata, results) attached client-side during streaming.
		// Swap in our accumulated ToolCall objects so identity — and the UI
		// state hanging off it — survives the replacement.
		if generatedMessage := response.GetGeneratedMessage(); generatedMessage != nil {
			mergeToolCallState(accumulator.Message, generatedMessage)
		}

		if err := accumulator.Add(response); err != nil {
			if pendingRender {
				s.refresh()
			}
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

		// Review new tool calls eagerly as they arrive during streaming.
		toolCallBlocks := ai.FilterBlocks(accumulator.Message.GetBlocks(), ai.BlockTypeToolCall)
		for len(toolCallBlocks) > reviewedToolCallCount {
			s.reviewToolCallEagerly(toolCallBlocks[reviewedToolCallCount].GetToolCall())
			reviewedToolCallCount++
		}

		checkRender()
	}
}

// mergeToolCallState replaces the persisted message's tool call blocks with
// the locally accumulated ones (matched by id), which carry the client-side
// review annotations and any eagerly attached results.
func mergeToolCallState(localMessage, persistedMessage *aipb.Message) {
	toolCallIDToToolCall := map[string]*aipb.ToolCall{}
	for _, block := range ai.FilterBlocks(localMessage.GetBlocks(), ai.BlockTypeToolCall) {
		toolCallIDToToolCall[block.GetToolCall().GetId()] = block.GetToolCall()
	}
	for _, block := range persistedMessage.GetBlocks() {
		persistedToolCall := block.GetToolCall()
		if persistedToolCall == nil {
			continue
		}
		if localToolCall, ok := toolCallIDToToolCall[persistedToolCall.GetId()]; ok {
			block.Content = &aipb.Block_ToolCall{ToolCall: localToolCall}
		}
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
		tool.SetToolCallStatus(toolCall, tool.ToolCallStatusFailed)
		s.refresh()
		return
	}
	// Review can attach a result directly (e.g. discovery tools); executing
	// those through the registry would fail with a parse-type mismatch.
	if toolCall.GetResult() != nil {
		tool.SetToolCallStatus(toolCall, tool.ToolCallStatusAccepted)
		s.refresh()
		return
	}
	metadata, err := tool.ParseToolCallMetadata(toolCall)
	if err != nil {
		return
	}
	// User-whitelisted tools execute eagerly, just like read-only ones.
	if !metadata.GetAutoExecute() && !s.IsToolAutoAccepted(toolCall.GetName()) {
		return
	}

	tool.SetToolCallStatus(toolCall, tool.ToolCallStatusAccepted)
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
			// Don't clobber statuses set during streaming (e.g. failed).
			if tool.GetToolCallStatus(toolCall) != "" {
				continue
			}
			switch {
			case toolCall.GetResult() != nil:
				tool.SetToolCallStatus(toolCall, tool.ToolCallStatusAccepted)
			case err != nil:
				// The stream died: the server excluded this message from
				// provider history, so a review verdict could never reach the
				// model. Resolve as failed instead of leaving an unreviewable
				// pending call.
				toolCall.Result = ai.NewErrorToolResult(toolCall.Name, toolCall.Id, err)
				tool.SetToolCallStatus(toolCall, tool.ToolCallStatusFailed)
			default:
				tool.SetToolCallStatus(toolCall, tool.ToolCallStatusPending)
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
	s.state = StateIdle
	s.cancelStream = nil
	s.streamError = err
}
