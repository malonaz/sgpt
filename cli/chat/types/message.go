package types

import (
	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
)

// StreamChunkMsg represents a chunk of streamed response.
type StreamChunkMsg struct {
	Response *aiservicepb.TextToTextStreamResponse
}

// StreamErrorMsg represents a streaming error (including EOF).
type StreamErrorMsg struct {
	Err error
}

// ChatSavedMsg is sent when the chat is saved successfully.
type ChatSavedMsg struct{}

// ToolResultMsg contains the result of a tool execution.
type ToolResultMsg struct {
	Result string
}

// ToolCancelledMsg is sent when tool execution is cancelled.
type ToolCancelledMsg struct{}
