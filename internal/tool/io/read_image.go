package io

import (
	"context"
	"fmt"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/tool"
)

// maxImageBytes is the strictest provider's per-image limit (Anthropic's).
const maxImageBytes = 5 << 20

// imageMediaTypeSet holds the formats every provider accepts.
var imageMediaTypeSet = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

// ReadImage is the tool definition for image reading, built from the
// ToolService.ReadImage method.
var ReadImage = tool.MustBuildTool("read_image", tool.HandlerIDReadImage, "sgpt.v1.ToolService.ReadImage")

func parseReadImageArguments(toolCall *aipb.ToolCall) (*sgptpb.ReadImageRequest, error) {
	readImageRequest := &sgptpb.ReadImageRequest{}
	if err := tool.UnmarshalArguments(toolCall, readImageRequest); err != nil {
		return nil, err
	}
	if readImageRequest.GetPath() == "" {
		return nil, fmt.Errorf("no path specified")
	}
	return readImageRequest, nil
}

// ReadImageTool shows the model an image from the user's system.
type ReadImageTool struct{}

func (t *ReadImageTool) Review(_ context.Context, toolCall *aipb.ToolCall) (*sgptpb.ToolCallMetadata, error) {
	if _, err := parseReadImageArguments(toolCall); err != nil {
		return nil, err
	}
	// No display message: the row already names the file.
	return &sgptpb.ToolCallMetadata{
		DisplayMessage: &sgptpb.DisplayMessage{},
		AutoExecute:    tool.NoSideEffects(toolCall),
	}, nil
}

func (t *ReadImageTool) Execute(ctx context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error) {
	readImageRequest, err := parseReadImageArguments(toolCall)
	if err != nil {
		return nil, err
	}
	data, mediaType, err := readAttachable(readImageRequest.GetPath(), maxImageBytes, imageMediaTypeSet)
	if err != nil {
		return nil, err
	}
	return attach(ctx, toolCall, ai.NewImageBlock(ai.NewImageFromData(data, mediaType)))
}

// Summarize names the image; the model sees it as an attachment.
func (t *ReadImageTool) Summarize(toolCall *aipb.ToolCall, toolResult *aipb.ToolResult) (tool.Summary, bool) {
	summary := tool.Summary{Verb: "View", Subject: tool.DisplayPath(tool.Argument(toolCall, "path").GetStringValue())}
	if toolResult != nil {
		summary.Outcome = "attached"
	}
	return summary, true
}

var (
	_ tool.Tool       = (*ReadImageTool)(nil)
	_ tool.Summarizer = (*ReadImageTool)(nil)
)

func init() { tool.RegisterBuiltin(ReadImage) }
