package diff

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	jsonpb "github.com/malonaz/core/genproto/json/v1"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/tool"
)

// Definition is the tool definition for unified-diff file editing.
var Definition = &aipb.Tool{
	Name:        "diff",
	Description: "Edit a file by applying a unified diff. Start each hunk with an '@@' line; line numbers in hunk headers are ignored, so they may be wrong or omitted. Prefix unchanged context lines with a space, removed lines with '-' and added lines with '+'. Include enough context lines to anchor each hunk uniquely within the file. Hunks are applied sequentially.",
	JsonSchema: &jsonpb.Schema{
		Type: "object",
		Properties: map[string]*jsonpb.Schema{
			"path": {Type: "string", Description: "Path of the file to edit"},
			"diff": {Type: "string", Description: "Unified diff to apply ('@@' line numbers are ignored)"},
		},
		Required: []string{"path", "diff"},
	},
	Annotations: map[string]string{
		tool.ToolHandlerIDAnnotation: tool.HandlerIDDiff,
	},
}

type arguments struct {
	Path string `json:"path"`
	Diff string `json:"diff"`
}

func parseArguments(toolCall *aipb.ToolCall) (*arguments, error) {
	bytes, err := tool.ArgumentsJSON(toolCall)
	if err != nil {
		return nil, err
	}
	arguments := &arguments{}
	if err := json.Unmarshal(bytes, arguments); err != nil {
		return nil, fmt.Errorf("parsing tool arguments: %w", err)
	}
	if arguments.Path == "" {
		return nil, fmt.Errorf("no path specified")
	}
	if strings.TrimSpace(arguments.Diff) == "" {
		return nil, fmt.Errorf("no diff specified")
	}
	return arguments, nil
}

// Tool applies model-written unified diffs to files on the user's
// system. Hunks are converted to search/replace patches and healed against
// the file, so the model never has to count lines.
type Tool struct{}

func (t *Tool) Review(_ context.Context, toolCall *aipb.ToolCall) (*sgptpb.ToolCallMetadata, error) {
	arguments, err := parseArguments(toolCall)
	if err != nil {
		return nil, err
	}
	// File mutation: never auto-execute.
	// The display message is reserved for problems; on success the title
	// (✏️ path) plus the rendered diff already say everything.
	metadata := &sgptpb.ToolCallMetadata{
		DisplayMessage: &sgptpb.DisplayMessage{},
	}
	// Surface failures in the review UI rather than erroring the turn;
	// Execute produces the error result the model can react to.
	patches, err := ParseUnifiedDiff(arguments.Diff)
	if err != nil {
		metadata.DisplayMessage.Content = fmt.Sprintf("Edit will fail: %v", err)
		return metadata, nil
	}
	contentBytes, err := os.ReadFile(arguments.Path)
	if err != nil {
		metadata.DisplayMessage.Content = fmt.Sprintf("Edit will fail: %v", err)
		return metadata, nil
	}
	// Dry-run: the user reviews the healed diff that will actually apply,
	// not the model's raw (possibly misaligned) diff.
	if _, diff, err := ApplyPatches(arguments.Path, string(contentBytes), patches); err != nil {
		metadata.DisplayMessage.Content = fmt.Sprintf("Edit will fail: %v", err)
	} else {
		metadata.Diff = diff
	}
	return metadata, nil
}

func (t *Tool) Execute(_ context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error) {
	arguments, err := parseArguments(toolCall)
	if err != nil {
		return nil, err
	}
	patches, err := ParseUnifiedDiff(arguments.Diff)
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
	patched, _, err := ApplyPatches(arguments.Path, string(contentBytes), patches)
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
			Content: fmt.Sprintf("Applied %d hunk(s) to %s", len(patches), arguments.Path),
		},
	}, nil
}

// RenderRequest renders the review-time healed diff when available. While the
// call is still streaming (no review metadata yet), it renders the raw diff
// argument directly so the edit is readable as it arrives; the healed diff
// replaces it once review runs on completion.
func (t *Tool) RenderRequest(toolCall *aipb.ToolCall) (string, bool) {
	metadata, err := tool.ParseToolCallMetadata(toolCall)
	if err == nil && metadata.GetDiff() != "" {
		return fmt.Sprintf("```diff\n%s\n```", strings.TrimSuffix(metadata.GetDiff(), "\n")), true
	}
	bytes, err := tool.ArgumentsJSON(toolCall)
	if err != nil {
		return "", false
	}
	arguments := &arguments{}
	// Partial arguments: tolerate missing fields, only require some diff text.
	if json.Unmarshal(bytes, arguments) != nil || arguments.Diff == "" {
		return "", false
	}
	header := ""
	if arguments.Path != "" {
		header = fmt.Sprintf("--- a/%s\n+++ b/%s\n", arguments.Path, arguments.Path)
	}
	return fmt.Sprintf("```diff\n%s%s\n```", header, strings.TrimSuffix(arguments.Diff, "\n")), true
}

// RenderHeader shows the file being edited instead of the tool name. It
// tolerates partial arguments so the header appears as soon as the path
// streams in.
func (t *Tool) RenderHeader(toolCall *aipb.ToolCall) (string, bool) {
	bytes, err := tool.ArgumentsJSON(toolCall)
	if err != nil {
		return "", false
	}
	arguments := &arguments{}
	if json.Unmarshal(bytes, arguments) != nil || arguments.Path == "" {
		return "", false
	}
	return fmt.Sprintf("edited `%s`", arguments.Path), true
}

var (
	_ tool.Tool            = (*Tool)(nil)
	_ tool.RequestRenderer = (*Tool)(nil)
	_ tool.HeaderRenderer  = (*Tool)(nil)
)

func init() { tool.RegisterBuiltin(Definition) }
