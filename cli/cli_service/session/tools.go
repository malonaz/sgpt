package session

import (
	"fmt"
	"strings"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/tools"
)

func (s *Session) handleToolCalls(toolCalls []*aipb.ToolCall) {
	allToolDefs := s.allTools()
	toolNameToTool := map[string]*aipb.Tool{}
	for _, tool := range allToolDefs {
		toolNameToTool[tool.Name] = tool
	}

	autoExecuteAll := true
	for _, toolCall := range toolCalls {
		tool, ok := toolNameToTool[toolCall.Name]
		if !ok {
			autoExecuteAll = false
			continue
		}
		handlerID := tool.GetAnnotations()[tools.ToolHandlerIDAnnotation]
		handler, ok := s.toolHandlerIDToHandler[handlerID]
		if !ok {
			autoExecuteAll = false
			continue
		}
		handleResult, err := handler.HandleToolCall(s.ctx, toolCall)
		if err != nil || !handleResult.AutoExecute {
			autoExecuteAll = false
		}
	}

	if autoExecuteAll {
		s.AcceptToolCalls()
		return
	}

	s.emit(ToolCallsPendingEvent{ToolCalls: toolCalls})
}

// AcceptToolCalls marks pending tool calls as accepted and executes them.
func (s *Session) AcceptToolCalls() {
	pending := s.PendingToolCalls()
	if len(pending) == 0 {
		return
	}

	// Mark accepted in annotations.
	for _, toolCall := range pending {
		tools.SetToolCallStatus(toolCall, tools.ToolCallStatusAccepted)
	}

	allToolDefs := s.allTools()
	toolNameToTool := map[string]*aipb.Tool{}
	for _, tool := range allToolDefs {
		toolNameToTool[tool.Name] = tool
	}

	go func() {
		for _, toolCall := range pending {
			tool, ok := toolNameToTool[toolCall.Name]
			if !ok {
				s.appendToolResult(ai.NewErrorToolResult(toolCall.Name, toolCall.Id, fmt.Errorf("unknown tool: %s", toolCall.Name)))
				continue
			}
			handlerID := tool.GetAnnotations()[tools.ToolHandlerIDAnnotation]
			handler, ok := s.toolHandlerIDToHandler[handlerID]
			if !ok {
				s.appendToolResult(ai.NewErrorToolResult(toolCall.Name, toolCall.Id, fmt.Errorf("no handler for tool %s", toolCall.Name)))
				continue
			}
			toolResult, err := handler.ProcessToolCall(s.ctx, toolCall)
			if err != nil {
				s.appendToolResult(ai.NewErrorToolResult(toolCall.Name, toolCall.Id, err))
				continue
			}
			s.appendToolResult(toolResult)
		}
	}()
}

// RejectToolCalls marks pending tool calls as rejected and appends error results.
func (s *Session) RejectToolCalls(reason string) {
	pending := s.PendingToolCalls()
	s.chatMu.Lock()
	for _, toolCall := range pending {
		tools.SetToolCallStatus(toolCall, tools.ToolCallStatusRejected)
		errorMessage := fmt.Sprintf("rejected by user: %s", strings.TrimSpace(reason))
		toolMessage := ai.NewToolMessage(ai.NewToolResultBlock(ai.NewErrorToolResult(toolCall.Name, toolCall.Id, fmt.Errorf(errorMessage))))
		s.chat.Metadata.Messages = append(s.chat.Metadata.Messages, &sgptpb.Message{Message: toolMessage})
	}
	s.chatMu.Unlock()

	s.emit(StreamChunkEvent{})
}

// RejectAndResend rejects pending tool calls then starts a new stream.
func (s *Session) RejectAndResend(reason string) {
	s.RejectToolCalls(reason)
	s.streaming = true
	s.startStreaming()
}

func (s *Session) appendToolResult(toolResult *aipb.ToolResult) {
	s.chatMu.Lock()
	toolMessage := ai.NewToolMessage(ai.NewToolResultBlock(toolResult))
	s.chat.Metadata.Messages = append(s.chat.Metadata.Messages, &sgptpb.Message{Message: toolMessage})
	s.chatMu.Unlock()

	s.emit(ToolResultEvent{ToolResult: toolResult})

	// If not an error result, auto-continue streaming.
	if toolResult.GetError() == nil {
		s.streaming = true
		s.startStreaming()
	}
}
