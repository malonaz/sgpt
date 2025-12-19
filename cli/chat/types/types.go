package types

import (
	aipb "github.com/malonaz/core/genproto/ai/v1"

	"github.com/malonaz/sgpt/internal/configuration"
	"github.com/malonaz/sgpt/internal/markdown"
)

// ChatOptions holds the options for the chat session.
type ChatOptions struct {
	Model           string
	Role            *configuration.Role
	MaxTokens       int32
	Temperature     float64
	ReasoningEffort aipb.ReasoningEffort
	EnableTools     bool
	ChatID          string
}

// RuntimeMessageType represents the type of runtime message.
type RuntimeMessageType int

const (
	// RuntimeMessageTypeUser represents a user message.
	RuntimeMessageTypeUser RuntimeMessageType = iota
	// RuntimeMessageTypeAssistant represents an assistant response.
	RuntimeMessageTypeAssistant
	// RuntimeMessageTypeThinking represents assistant reasoning/thinking.
	RuntimeMessageTypeThinking
	// RuntimeMessageTypeToolCall represents a tool call request.
	RuntimeMessageTypeToolCall
	// RuntimeMessageTypeToolResult represents a tool execution result.
	RuntimeMessageTypeToolResult
	// RuntimeMessageTypeSystem represents a system message.
	RuntimeMessageTypeSystem
)

// RuntimeMessage represents a displayable message in the chat UI.
// This is decoupled from the protobuf Message to allow for separate
// visual blocks (thinking, tool calls, etc.).
type RuntimeMessage struct {
	// Type indicates what kind of message this is.
	Type RuntimeMessageType

	// Content is the text content of the message.
	Content string

	// Blocks contains the parsed markdown blocks for rendering.
	Blocks []markdown.Block

	// ToolCall contains tool call details (for RuntimeMessageTypeToolCall).
	ToolCall *aipb.ToolCall

	// ToolCallID is the ID of the tool call this result is for (for RuntimeMessageTypeToolResult).
	ToolCallID string

	// Err contains any error associated with this message (e.g., interrupted, failed).
	Err error
}

// NewUserMessage creates a new user runtime message.
func NewUserMessage(content string) *RuntimeMessage {
	return &RuntimeMessage{
		Type:    RuntimeMessageTypeUser,
		Content: content,
		Blocks:  markdown.ParseBlocks(content),
	}
}

// NewAssistantMessage creates a new assistant runtime message.
func NewAssistantMessage(content string) *RuntimeMessage {
	return &RuntimeMessage{
		Type:    RuntimeMessageTypeAssistant,
		Content: content,
		Blocks:  markdown.ParseBlocks(content),
	}
}

// NewAssistantMessageWithError creates a new assistant runtime message with an error.
func NewAssistantMessageWithError(content string, err error) *RuntimeMessage {
	return &RuntimeMessage{
		Type:    RuntimeMessageTypeAssistant,
		Content: content,
		Blocks:  markdown.ParseBlocks(content),
		Err:     err,
	}
}

// NewThinkingMessage creates a new thinking/reasoning runtime message.
func NewThinkingMessage(content string) *RuntimeMessage {
	return &RuntimeMessage{
		Type:    RuntimeMessageTypeThinking,
		Content: content,
		Blocks:  markdown.ParseBlocks(content),
	}
}

// NewToolCallMessage creates a new tool call runtime message.
func NewToolCallMessage(toolCall *aipb.ToolCall) *RuntimeMessage {
	return &RuntimeMessage{
		Type:     RuntimeMessageTypeToolCall,
		Content:  toolCall.Arguments,
		Blocks:   markdown.ParseBlocks(toolCall.Arguments),
		ToolCall: toolCall,
	}
}

// NewToolResultMessage creates a new tool result runtime message.
func NewToolResultMessage(toolCallID, content string) *RuntimeMessage {
	return &RuntimeMessage{
		Type:       RuntimeMessageTypeToolResult,
		Content:    content,
		Blocks:     markdown.ParseBlocks(content),
		ToolCallID: toolCallID,
	}
}

// NewToolResultMessageWithError creates a new tool result runtime message with an error.
func NewToolResultMessageWithError(toolCallID, content string, err error) *RuntimeMessage {
	return &RuntimeMessage{
		Type:       RuntimeMessageTypeToolResult,
		Content:    content,
		Blocks:     markdown.ParseBlocks(content),
		ToolCallID: toolCallID,
		Err:        err,
	}
}

// NewSystemMessage creates a new system runtime message.
func NewSystemMessage(content string) *RuntimeMessage {
	return &RuntimeMessage{
		Type:    RuntimeMessageTypeSystem,
		Content: content,
		Blocks:  markdown.ParseBlocks(content),
	}
}

// UpdateContent updates the content and re-parses blocks.
func (rm *RuntimeMessage) UpdateContent(content string) {
	rm.Content = content
	rm.Blocks = markdown.ParseBlocks(content)
}

// RuntimeMessagesFromProto converts protobuf messages to runtime messages.
// Each proto message may generate multiple runtime messages (e.g., thinking + response + tool calls).
func RuntimeMessagesFromProto(messages []*aipb.Message) []*RuntimeMessage {
	var result []*RuntimeMessage

	for _, msg := range messages {
		switch msg.Role {
		case aipb.Role_ROLE_USER:
			result = append(result, NewUserMessage(msg.Content))

		case aipb.Role_ROLE_ASSISTANT:
			// Add thinking message if present
			if msg.Reasoning != "" {
				result = append(result, NewThinkingMessage(msg.Reasoning))
			}
			// Add assistant response if present
			if msg.Content != "" {
				result = append(result, NewAssistantMessage(msg.Content))
			}
			// Add tool calls as separate messages
			for _, tc := range msg.ToolCalls {
				result = append(result, NewToolCallMessage(tc))
			}

		case aipb.Role_ROLE_TOOL:
			result = append(result, NewToolResultMessage(msg.ToolCallId, msg.Content))

		case aipb.Role_ROLE_SYSTEM:
			result = append(result, NewSystemMessage(msg.Content))
		}
	}

	return result
}
