package store

import (
	aipb "github.com/malonaz/core/genproto/ai/v1"
)

// Chat represents a chat.
type Chat struct {
	ID                string
	Title             *string
	CreationTimestamp int64
	UpdateTimestamp   int64
	Messages          []*aipb.Message
	Files             []string
	Favorite          bool
	Tags              []string
}
