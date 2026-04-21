package tools

import (
	"context"

	aipb "github.com/malonaz/core/genproto/ai/v1"
)

const ToolHandlerIDAnnotation = "sgpt.com/tool-handler-id"

const (
	HandlerIDShell     = "shell"
	HandlerIDReadFiles = "read_files"
	HandlerIDEngine    = "engine"
)

type Handler interface {
	HandleToolCall(ctx context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error)
}
