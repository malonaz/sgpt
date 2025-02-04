package store

import (
	"github.com/malonaz/sgpt/internal/llm"
)

// Chat represents a chat.
type Chat struct {
	ID                string
	Title             *string
	CreationTimestamp int64
	UpdateTimestamp   int64
	Messages          []*llm.Message
	Files             []string
	Favorite          bool
	Tags              []string
}
