package io

import (
	"context"
	"fmt"
	"path/filepath"

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
	// No display message: the title (🖼 basename) already covers it.
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

// RenderHeader shows the image's basename instead of the tool name.
func (t *ReadImageTool) RenderHeader(toolCall *aipb.ToolCall) (string, bool) {
	readImageRequest, err := parseReadImageArguments(toolCall)
	if err != nil {
		return "", false
	}
	return fmt.Sprintf("🖼 `%s`", filepath.Base(readImageRequest.GetPath())), true
}

var (
	_ tool.Tool           = (*ReadImageTool)(nil)
	_ tool.HeaderRenderer = (*ReadImageTool)(nil)
)

func init() { tool.RegisterBuiltin(ReadImage) }
