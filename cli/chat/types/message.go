package types

import (
	aipb "github.com/malonaz/core/genproto/ai/v1"
)

// StreamErrorMsg represents a streaming error (including EOF).
type StreamErrorMsg struct {
	Err error
}

// StreamDoneMsg indicates streaming has finished with final content for persistence.
type StreamDoneMsg struct {
	Err       error
	Response  string
	Reasoning string
	ToolCalls []*aipb.ToolCall
}

// StreamRenderMsg triggers a re-render of the viewport.
type StreamRenderMsg struct{}

// ChatSavedMsg is sent when the chat is saved successfully.
type ChatSavedMsg struct{}

// ToolResultMsg contains the result of a tool execution.
type ToolResultMsg struct {
	Result string
}

// ToolCancelledMsg is sent when tool execution is cancelled.
type ToolCancelledMsg struct{}
