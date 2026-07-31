package io

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	aipb "github.com/malonaz/core/genproto/ai/v1"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/tool"
)

// ReadFiles is the tool definition for file reading, built from the
// ToolService.ReadFiles method.
var ReadFiles = tool.MustBuildTool("read_files", tool.HandlerIDReadFiles, "sgpt.v1.ToolService.ReadFiles")

func parseReadFilesArguments(toolCall *aipb.ToolCall) (*sgptpb.ReadFilesRequest, error) {
	readFilesRequest := &sgptpb.ReadFilesRequest{}
	if err := tool.UnmarshalArguments(toolCall, readFilesRequest); err != nil {
		return nil, err
	}
	if len(readFilesRequest.GetPaths()) == 0 {
		return nil, fmt.Errorf("no paths specified")
	}
	return readFilesRequest, nil
}

// ReadFilesTool reads files from the user's system.
type ReadFilesTool struct{}

func (t *ReadFilesTool) Review(_ context.Context, toolCall *aipb.ToolCall) (*sgptpb.ToolCallMetadata, error) {
	// Validate arguments even though the metadata no longer uses them.
	if _, err := parseReadFilesArguments(toolCall); err != nil {
		return nil, err
	}
	// Auto-execution is declared on the proto method (NO_SIDE_EFFECTS).
	// No display message: the title (📖 basenames) already covers it.
	return &sgptpb.ToolCallMetadata{
		DisplayMessage: &sgptpb.DisplayMessage{},
		AutoExecute:    tool.NoSideEffects(toolCall),
	}, nil
}

func (t *ReadFilesTool) Execute(_ context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error) {
	readFilesRequest, err := parseReadFilesArguments(toolCall)
	if err != nil {
		return nil, err
	}
	files := make([]*sgptpb.ReadFilesResponse_File, 0, len(readFilesRequest.GetPaths()))
	for _, path := range readFilesRequest.GetPaths() {
		file := &sgptpb.ReadFilesResponse_File{Path: path}
		content, err := os.ReadFile(path)
		if err != nil {
			// Per-file errors are part of the result so one bad path doesn't
			// sink the whole read.
			file.Error = err.Error()
		} else {
			file.Content = string(content)
		}
		files = append(files, file)
	}
	readFilesResponse := &sgptpb.ReadFilesResponse{Files: files}
	return tool.NewStructuredToolResult(toolCall, readFilesResponse)
}

// RenderHeader shows the basenames being read instead of the tool name.
func (t *ReadFilesTool) RenderHeader(toolCall *aipb.ToolCall) (string, bool) {
	readFilesRequest, err := parseReadFilesArguments(toolCall)
	if err != nil {
		return "", false
	}
	names := make([]string, 0, len(readFilesRequest.GetPaths()))
	for _, path := range readFilesRequest.GetPaths() {
		names = append(names, fmt.Sprintf("`%s`", filepath.Base(path)))
	}
	// Keep the header to one scannable line; the payload has the full list.
	if len(names) > 3 {
		names = append(names[:3], fmt.Sprintf("+%d more", len(names)-3))
	}
	return "📖 " + strings.Join(names, ", "), true
}

var (
	_ tool.Tool           = (*ReadFilesTool)(nil)
	_ tool.HeaderRenderer = (*ReadFilesTool)(nil)
)

func init() { tool.RegisterBuiltin(ReadFiles) }
