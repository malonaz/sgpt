package io

import (
	"context"
	"fmt"
	"net/http"
	"os"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"

	"github.com/malonaz/sgpt/internal/tool"
)

// readAttachable reads a file the model sees as content rather than text,
// enforcing a size cap and an allowed media type set. Caps matter: content a
// provider rejects fails the generation, and the requeued input would fail
// every retry with it.
func readAttachable(path string, maxBytes int64, mediaTypeSet map[string]bool) ([]byte, string, error) {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return nil, "", err
	}
	if fileInfo.Size() > maxBytes {
		return nil, "", fmt.Errorf("%s is %d bytes; the cap is %d bytes", path, fileInfo.Size(), maxBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	mediaType := http.DetectContentType(data)
	if !mediaTypeSet[mediaType] {
		return nil, "", fmt.Errorf("%s has unsupported media type %s", path, mediaType)
	}
	return data, mediaType, nil
}

// attach hands a content block to the executing turn and acknowledges the
// call: tool results cannot carry images or documents, so the block reaches
// the model as a user message right after the tool message.
func attach(ctx context.Context, toolCall *aipb.ToolCall, block *aipb.Block) (*aipb.ToolResult, error) {
	if !tool.Attach(ctx, block) {
		return nil, fmt.Errorf("%s only works within a chat turn", toolCall.GetName())
	}
	return ai.NewToolResult(toolCall.GetName(), toolCall.GetId(), "ok"), nil
}
