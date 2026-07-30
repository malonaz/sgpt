package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	jsonpb "github.com/malonaz/core/genproto/json/v1"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
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
		ToolHandlerIDAnnotation: HandlerIDReadFiles,
	},
}

type readFilesArguments struct {
	Paths []string `json:"paths"`
}

func parseReadFilesArguments(toolCall *aipb.ToolCall) (*readFilesArguments, error) {
	bytes, err := toolCallArgumentsJSON(toolCall)
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
	arguments, err := parseReadFilesArguments(toolCall)
	if err != nil {
		return nil, err
	}
	// Reads have no side effects: safe to auto-execute.
	return &sgptpb.ToolCallMetadata{
		DisplayMessage: &sgptpb.DisplayMessage{
			Content: fmt.Sprintf("Reading %d file(s): %s", len(arguments.Paths), strings.Join(arguments.Paths, ", ")),
		},
		AutoExecute: true,
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

var _ Tool = (*ReadFilesTool)(nil)
