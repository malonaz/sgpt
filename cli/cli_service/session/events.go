package session

import (
	aipb "github.com/malonaz/core/genproto/ai/v1"
	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
)

// Event is emitted by Session to notify the TUI of state changes.
type Event interface{ sessionEvent() }

// StreamChunkEvent signals a re-render is needed during streaming.
type StreamChunkEvent struct{}

func (StreamChunkEvent) sessionEvent() {}

// StreamDoneEvent signals the stream has finished.
type StreamDoneEvent struct {
	Err    error
	Blocks []*aipb.Block
}

func (StreamDoneEvent) sessionEvent() {}

// ChatSavedEvent signals the chat was persisted.
type ChatSavedEvent struct {
	Chat *sgptpb.Chat
}

func (ChatSavedEvent) sessionEvent() {}

// ToolCallsPendingEvent signals tool calls need user approval.
type ToolCallsPendingEvent struct {
	ToolCalls []*aipb.ToolCall
}

func (ToolCallsPendingEvent) sessionEvent() {}

// ToolResultEvent signals a single tool result arrived.
type ToolResultEvent struct {
	ToolResult *aipb.ToolResult
}

func (ToolResultEvent) sessionEvent() {}

// ErrorEvent signals a non-fatal error.
type ErrorEvent struct {
	Text string
}

func (ErrorEvent) sessionEvent() {}
