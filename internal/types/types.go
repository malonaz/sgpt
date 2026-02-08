package types

import (
	"strings"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"
	"github.com/malonaz/core/go/pbutil"
	"google.golang.org/grpc/status"

	chatpb "github.com/malonaz/sgpt/genproto/chat/v1"
	"github.com/malonaz/sgpt/internal/configuration"
	"github.com/malonaz/sgpt/internal/markdown"
)

// ChatOptions holds the options for the chat session.
type ChatOptions struct {
	Model           *aipb.Model
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
	content string

	// Blocks contains the parsed markdown blocks for rendering.
	Blocks []markdown.Block

	// ToolCall contains tool call details (for RuntimeMessageTypeToolCall).
	ToolCall *aipb.ToolCall

	// ToolCallID is the ID of the tool call this result is for (for RuntimeMessageTypeToolResult).
	ToolCallID string

	// IsStreaming indicates this message is currently being streamed.
	IsStreaming bool

	// Err contains any error associated with this message (e.g., interrupted, failed).
	Err error
}

func (m *RuntimeMessage) Content() string {
	return strings.Trim(m.content, "\n")
}

func (m *RuntimeMessage) WithError(err error) *RuntimeMessage {
	m.Err = err
	return m
}

func (m *RuntimeMessage) WithStreaming() *RuntimeMessage {
	m.IsStreaming = true
	return m
}

// NewUserMessage creates a new user runtime message.
func NewUserMessage(content string) *RuntimeMessage {
	return &RuntimeMessage{
		Type:    RuntimeMessageTypeUser,
		content: content,
		Blocks:  markdown.ParseBlocks(content),
	}
}

// NewAssistantMessage creates a new assistant runtime message.
func NewAssistantMessage(content string) *RuntimeMessage {
	return &RuntimeMessage{
		Type:    RuntimeMessageTypeAssistant,
		content: content,
		Blocks:  markdown.ParseBlocks(content),
	}
}

// NewThinkingMessage creates a new thinking/reasoning runtime message.
func NewThinkingMessage(content string) *RuntimeMessage {
	return &RuntimeMessage{
		Type:    RuntimeMessageTypeThinking,
		content: content,
		Blocks:  markdown.ParseBlocks(content),
	}
}

// NewToolCallMessage creates a new tool call runtime message.
func NewToolCallMessage(toolCall *aipb.ToolCall) (*RuntimeMessage, error) {
	bytes, err := pbutil.JSONMarshalPretty(toolCall.Arguments)
	if err != nil {
		return nil, err
	}
	str := string(bytes)
	return &RuntimeMessage{
		Type:     RuntimeMessageTypeToolCall,
		content:  str,
		Blocks:   markdown.ParseBlocks(str),
		ToolCall: toolCall,
	}, nil
}

// NewToolResultMessage creates a new tool result runtime message.
func NewToolResultMessage(toolCallID, content string) *RuntimeMessage {
	return &RuntimeMessage{
		Type:       RuntimeMessageTypeToolResult,
		content:    content,
		Blocks:     markdown.ParseBlocks(content),
		ToolCallID: toolCallID,
	}
}

// NewSystemMessage creates a new system runtime message.
func NewSystemMessage(content string) *RuntimeMessage {
	return &RuntimeMessage{
		Type:    RuntimeMessageTypeSystem,
		content: content,
		Blocks:  markdown.ParseBlocks(content),
	}
}

// RuntimeMessagesFromProto converts protobuf messages to runtime messages.
// Each proto message may generate multiple runtime messages (e.g., thinking + response + tool calls).
func RuntimeMessagesFromProto(messages []*chatpb.Message) []*RuntimeMessage {
	var result []*RuntimeMessage

	for _, msg := range messages {
		aiMsg := msg.Message
		if aiMsg == nil {
			continue
		}

		for _, block := range aiMsg.Blocks {
			switch c := block.Content.(type) {
			case *aipb.Block_Text:
				switch aiMsg.Role {
				case aipb.Role_ROLE_SYSTEM:
					result = append(result, NewSystemMessage(c.Text))
				case aipb.Role_ROLE_USER:
					result = append(result, NewUserMessage(c.Text))
				case aipb.Role_ROLE_ASSISTANT:
					result = append(result, NewAssistantMessage(c.Text))
				}
			case *aipb.Block_Thought:
				result = append(result, NewThinkingMessage(c.Thought))
			case *aipb.Block_ToolCall:
				if tcMsg, err := NewToolCallMessage(c.ToolCall); err == nil {
					result = append(result, tcMsg)
				}
			case *aipb.Block_ToolResult:
				content, _ := ai.ParseToolResult(c.ToolResult)
				result = append(result, NewToolResultMessage(c.ToolResult.ToolCallId, content))
			}
		}

		if msg.Error != nil && len(result) > 0 {
			result[len(result)-1].WithError(status.ErrorProto(msg.Error))
		}
	}

	return result
}

// AppendContent appends to the content and re-parses blocks.
func (rm *RuntimeMessage) AppendContent(content string) {
	rm.content += content
	rm.Blocks = markdown.ParseBlocks(rm.content)
}

// Finalize marks the message as no longer streaming and sets an error if provided.
func (rm *RuntimeMessage) Finalize(err error) {
	rm.IsStreaming = false
	rm.Err = err
}
