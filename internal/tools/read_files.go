// internal/tools/read_files.go
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	jsonpb "github.com/malonaz/core/genproto/json/v1"
)

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

type ReadFilesArgs struct {
	Paths []string `json:"paths"`
}

func ParseReadFilesArgs(bytes []byte) (*ReadFilesArgs, error) {
	var args ReadFilesArgs
	if err := json.Unmarshal(bytes, &args); err != nil {
		return nil, fmt.Errorf("failed to parse tool arguments: %w", err)
	}
	if len(args.Paths) == 0 {
		return nil, fmt.Errorf("no paths specified")
	}
	return &args, nil
}

func ExecuteReadFiles(args *ReadFilesArgs) (string, error) {
	var results []string
	for _, path := range args.Paths {
		content, err := os.ReadFile(path)
		if err != nil {
			results = append(results, fmt.Sprintf("=== %s ===\nError: %v", path, err))
		} else {
			results = append(results, fmt.Sprintf("=== %s ===\n%s", path, string(content)))
		}
	}
	return strings.Join(results, "\n\n"), nil
}

type ReadFilesHandler struct{}

func (h *ReadFilesHandler) HandleToolCall(_ context.Context, toolCall *aipb.ToolCall) (*HandleResult, error) {
	bytes, err := json.Marshal(toolCall.Arguments.AsMap())
	if err != nil {
		return nil, fmt.Errorf("marshaling tool call arguments: %w", err)
	}
	args, err := ParseReadFilesArgs(bytes)
	if err != nil {
		return nil, err
	}
	// Read files are safe to auto-execute.
	return &HandleResult{
		Display:     fmt.Sprintf("Reading %d file(s): %s", len(args.Paths), strings.Join(args.Paths, ", ")),
		AutoExecute: true,
	}, nil
}

func (h *ReadFilesHandler) ProcessToolCall(_ context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error) {
	bytes, err := json.Marshal(toolCall.Arguments.AsMap())
	if err != nil {
		return nil, fmt.Errorf("marshaling tool call arguments: %w", err)
	}
	args, err := ParseReadFilesArgs(bytes)
	if err != nil {
		return nil, err
	}
	result, err := ExecuteReadFiles(args)
	if err != nil {
		return nil, err
	}
	return &aipb.ToolResult{
		ToolName:   toolCall.Name,
		ToolCallId: toolCall.Id,
		Result:     &aipb.ToolResult_Content{Content: result},
	}, nil
}
