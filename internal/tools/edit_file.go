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

// EditFile is the tool definition for patch-based file editing.
var EditFile = &aipb.Tool{
	Name:        "edit_file",
	Description: "Edit a file by applying one or more patches. Each patch replaces an exact, unique occurrence of `search` with `replace`. Include enough surrounding context in `search` to make it unique within the file. Patches are applied sequentially.",
	JsonSchema: &jsonpb.Schema{
		Type: "object",
		Properties: map[string]*jsonpb.Schema{
			"path": {Type: "string", Description: "Path of the file to edit"},
			"patches": {
				Type:        "array",
				Description: "Patches applied sequentially, each an exact search/replace",
				Items: &jsonpb.Schema{
					Type: "object",
					Properties: map[string]*jsonpb.Schema{
						"search":  {Type: "string", Description: "Exact text to find (must match exactly once)"},
						"replace": {Type: "string", Description: "Replacement text"},
					},
					Required: []string{"search", "replace"},
				},
			},
		},
		Required: []string{"path", "patches"},
	},
	Annotations: map[string]string{
		ToolHandlerIDAnnotation: HandlerIDEditFile,
	},
}

type editFileArguments struct {
	Path    string  `json:"path"`
	Patches []patch `json:"patches"`
}

func parseEditFileArguments(toolCall *aipb.ToolCall) (*editFileArguments, error) {
	bytes, err := toolCallArgumentsJSON(toolCall)
	if err != nil {
		return nil, err
	}
	arguments := &editFileArguments{}
	if err := json.Unmarshal(bytes, arguments); err != nil {
		return nil, fmt.Errorf("parsing tool arguments: %w", err)
	}
	if arguments.Path == "" {
		return nil, fmt.Errorf("no path specified")
	}
	if len(arguments.Patches) == 0 {
		return nil, fmt.Errorf("no patches specified")
	}
	return arguments, nil
}

// EditFileTool applies search/replace patches to files on the user's system.
type EditFileTool struct{}

func (t *EditFileTool) Review(_ context.Context, toolCall *aipb.ToolCall) (*sgptpb.ToolCallMetadata, error) {
	arguments, err := parseEditFileArguments(toolCall)
	if err != nil {
		return nil, err
	}
	// File mutation: never auto-execute.
	metadata := &sgptpb.ToolCallMetadata{
		DisplayMessage: &sgptpb.DisplayMessage{
			Content: fmt.Sprintf("Editing %s (%d patch(es))", arguments.Path, len(arguments.Patches)),
		},
	}
	contentBytes, err := os.ReadFile(arguments.Path)
	if err != nil {
		// Surface the failure in the review UI rather than erroring the turn;
		// Execute produces the error result the model can react to.
		metadata.DisplayMessage.Content = fmt.Sprintf("Edit will fail: %v", err)
		return metadata, nil
	}
	// Dry-run: the user reviews the exact diff that will apply.
	if _, diff, err := applyPatches(arguments.Path, string(contentBytes), arguments.Patches); err != nil {
		metadata.DisplayMessage.Content = fmt.Sprintf("Edit will fail: %v", err)
	} else {
		metadata.Diff = diff
	}
	return metadata, nil
}

func (t *EditFileTool) Execute(_ context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error) {
	arguments, err := parseEditFileArguments(toolCall)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(arguments.Path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", arguments.Path, err)
	}
	contentBytes, err := os.ReadFile(arguments.Path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", arguments.Path, err)
	}
	// Re-apply at execution time: the file may have changed since review.
	patched, _, err := applyPatches(arguments.Path, string(contentBytes), arguments.Patches)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(arguments.Path, []byte(patched), info.Mode()); err != nil {
		return nil, fmt.Errorf("writing %s: %w", arguments.Path, err)
	}
	return &aipb.ToolResult{
		ToolName:   toolCall.Name,
		ToolCallId: toolCall.Id,
		Result: &aipb.ToolResult_Content{
			Content: fmt.Sprintf("Applied %d patch(es) to %s", len(arguments.Patches), arguments.Path),
		},
	}, nil
}

// RenderRequest renders the review-time diff instead of raw JSON arguments.
// The diff is persisted on the call's metadata so it survives chat reloads
// even though the underlying file has since changed.
func (t *EditFileTool) RenderRequest(toolCall *aipb.ToolCall) (string, bool) {
	metadata, err := ParseToolCallMetadata(toolCall)
	if err != nil || metadata.GetDiff() == "" {
		return "", false
	}
	return fmt.Sprintf("```diff\n%s\n```", strings.TrimSuffix(metadata.GetDiff(), "\n")), true
}

var (
	_ Tool            = (*EditFileTool)(nil)
	_ RequestRenderer = (*EditFileTool)(nil)
)
