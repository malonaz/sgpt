package types

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

type RenderStreamChunkTickMsg struct{}
