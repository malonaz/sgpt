package io

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	jsonpb "github.com/malonaz/core/genproto/json/v1"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/tool"
)

// ReadFiles is the tool definition for file reading.
var ReadFiles = &aipb.Tool{
	Name:        "read_files",
	Description: "Read the contents of one or more files. Use this to examine file contents before making changes or to understand code structure.",
	JsonSchema: &jsonpb.Schema{
		Type: "object",
		Properties: map[string]*jsonpb.Schema{
			"paths": {
				Type:        "array",
				Description: "List of file paths to read",
				Items:       &jsonpb.Schema{Type: "string"},
			},
		},
		Required: []string{"paths"},
	},
	Annotations: map[string]string{
		tool.ToolHandlerIDAnnotation: tool.HandlerIDReadFiles,
	},
}

type readFilesArguments struct {
	Paths []string `json:"paths"`
}

func parseReadFilesArguments(toolCall *aipb.ToolCall) (*readFilesArguments, error) {
	bytes, err := tool.ArgumentsJSON(toolCall)
	if err != nil {
		return nil, err
	}
	arguments := &readFilesArguments{}
	if err := json.Unmarshal(bytes, arguments); err != nil {
		return nil, fmt.Errorf("parsing tool arguments: %w", err)
	}
	if len(arguments.Paths) == 0 {
		return nil, fmt.Errorf("no paths specified")
	}
	return arguments, nil
}

// ReadFilesTool reads files from the user's system.
type ReadFilesTool struct{}

func (t *ReadFilesTool) Review(_ context.Context, toolCall *aipb.ToolCall) (*sgptpb.ToolCallMetadata, error) {
	// Validate arguments even though the metadata no longer uses them.
	if _, err := parseReadFilesArguments(toolCall); err != nil {
		return nil, err
	}
	// Reads have no side effects: safe to auto-execute.
	// No display message: the title (📖 basenames) already covers it.
	return &sgptpb.ToolCallMetadata{
		DisplayMessage: &sgptpb.DisplayMessage{},
		AutoExecute:    true,
	}, nil
}

func (t *ReadFilesTool) Execute(_ context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error) {
	arguments, err := parseReadFilesArguments(toolCall)
	if err != nil {
		return nil, err
	}
	results := make([]string, 0, len(arguments.Paths))
	for _, path := range arguments.Paths {
		content, err := os.ReadFile(path)
		if err != nil {
			results = append(results, fmt.Sprintf("=== %s ===\nError: %v", path, err))
			continue
		}
		results = append(results, fmt.Sprintf("=== %s ===\n%s", path, string(content)))
	}
	return &aipb.ToolResult{
		ToolName:   toolCall.Name,
		ToolCallId: toolCall.Id,
		Result:     &aipb.ToolResult_Content{Content: strings.Join(results, "\n\n")},
	}, nil
}

// RenderHeader shows the basenames being read instead of the tool name.
func (t *ReadFilesTool) RenderHeader(toolCall *aipb.ToolCall) (string, bool) {
	arguments, err := parseReadFilesArguments(toolCall)
	if err != nil {
		return "", false
	}
	names := make([]string, 0, len(arguments.Paths))
	for _, path := range arguments.Paths {
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
