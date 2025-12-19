package types

import (
	aipb "github.com/malonaz/core/genproto/ai/v1"

	"github.com/malonaz/sgpt/internal/configuration"
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

// RuntimeMessage wraps a message with optional error state.
// Messages with errors are displayed but not persisted.
type RuntimeMessage struct {
	Message *aipb.Message
	Err     error
}
