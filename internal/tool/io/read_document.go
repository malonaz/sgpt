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

// maxDocumentBytes keeps the base64-inflated request, history included,
// under the strictest provider's inline request cap (Gemini's 20MB).
const maxDocumentBytes = 10 << 20

// documentMediaTypeSet holds PDF only: plain text is read_files' job.
var documentMediaTypeSet = map[string]bool{"application/pdf": true}

// ReadDocument is the tool definition for document reading, built from the
// ToolService.ReadDocument method.
var ReadDocument = tool.MustBuildTool("read_document", tool.HandlerIDReadDocument, "sgpt.v1.ToolService.ReadDocument")

func parseReadDocumentArguments(toolCall *aipb.ToolCall) (*sgptpb.ReadDocumentRequest, error) {
	readDocumentRequest := &sgptpb.ReadDocumentRequest{}
	if err := tool.UnmarshalArguments(toolCall, readDocumentRequest); err != nil {
		return nil, err
	}
	if readDocumentRequest.GetPath() == "" {
		return nil, fmt.Errorf("no path specified")
	}
	return readDocumentRequest, nil
}

// ReadDocumentTool shows the model a PDF from the user's system.
type ReadDocumentTool struct{}

func (t *ReadDocumentTool) Review(_ context.Context, toolCall *aipb.ToolCall) (*sgptpb.ToolCallMetadata, error) {
	if _, err := parseReadDocumentArguments(toolCall); err != nil {
		return nil, err
	}
	// No display message: the title (📄 basename) already covers it.
	return &sgptpb.ToolCallMetadata{
		DisplayMessage: &sgptpb.DisplayMessage{},
		AutoExecute:    tool.NoSideEffects(toolCall),
	}, nil
}

func (t *ReadDocumentTool) Execute(ctx context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error) {
	readDocumentRequest, err := parseReadDocumentArguments(toolCall)
	if err != nil {
		return nil, err
	}
	path := readDocumentRequest.GetPath()
	data, mediaType, err := readAttachable(path, maxDocumentBytes, documentMediaTypeSet)
	if err != nil {
		return nil, err
	}
	// The title is surfaced to the model as the file name, tying the
	// document back to this call.
	return attach(ctx, toolCall, ai.NewDocumentBlock(&aipb.Document{
		Source:    &aipb.Document_Data{Data: data},
		MediaType: mediaType,
		Title:     filepath.Base(path),
	}))
}

// RenderHeader shows the document's basename instead of the tool name.
func (t *ReadDocumentTool) RenderHeader(toolCall *aipb.ToolCall) (string, bool) {
	readDocumentRequest, err := parseReadDocumentArguments(toolCall)
	if err != nil {
		return "", false
	}
	return fmt.Sprintf("📄 `%s`", filepath.Base(readDocumentRequest.GetPath())), true
}

var (
	_ tool.Tool           = (*ReadDocumentTool)(nil)
	_ tool.HeaderRenderer = (*ReadDocumentTool)(nil)
)

func init() { tool.RegisterBuiltin(ReadDocument) }
