package diff

import (
	"context"
	"fmt"
	"os"
	"strings"

	aipb "github.com/malonaz/core/genproto/ai/v1"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/tool"
)

// Definition is the tool definition for unified-diff file editing, built
// from the ToolService.Diff method.
var Definition = tool.MustBuildTool("diff", tool.HandlerIDDiff, "sgpt.v1.ToolService.Diff")

func parseArguments(toolCall *aipb.ToolCall) (*sgptpb.DiffRequest, error) {
	diffRequest := &sgptpb.DiffRequest{}
	if err := tool.UnmarshalArguments(toolCall, diffRequest); err != nil {
		return nil, err
	}
	if diffRequest.GetPath() == "" {
		return nil, fmt.Errorf("no path specified")
	}
	if strings.TrimSpace(diffRequest.GetDiff()) == "" {
		return nil, fmt.Errorf("no diff specified")
	}
	return diffRequest, nil
}

// Tool applies model-written unified diffs to files on the user's
// system. Hunks are converted to search/replace patches and healed against
// the file, so the model never has to count lines.
type Tool struct{}

func (t *Tool) Review(_ context.Context, toolCall *aipb.ToolCall) (*sgptpb.ToolCallMetadata, error) {
	diffRequest, err := parseArguments(toolCall)
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
	patches, err := ParseUnifiedDiff(diffRequest.GetDiff())
	if err != nil {
		metadata.DisplayMessage.Content = fmt.Sprintf("Edit will fail: %v", err)
		return metadata, nil
	}
	contentBytes, err := os.ReadFile(diffRequest.GetPath())
	if err != nil {
		metadata.DisplayMessage.Content = fmt.Sprintf("Edit will fail: %v", err)
		return metadata, nil
	}
	// Dry-run: the user reviews the healed diff that will actually apply,
	// not the model's raw (possibly misaligned) diff.
	if _, diff, err := ApplyPatches(diffRequest.GetPath(), string(contentBytes), patches); err != nil {
		metadata.DisplayMessage.Content = fmt.Sprintf("Edit will fail: %v", err)
	} else {
		metadata.Diff = diff
	}
	return metadata, nil
}

func (t *Tool) Execute(_ context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error) {
	diffRequest, err := parseArguments(toolCall)
	if err != nil {
		return nil, err
	}
	patches, err := ParseUnifiedDiff(diffRequest.GetDiff())
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(diffRequest.GetPath())
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", diffRequest.GetPath(), err)
	}
	contentBytes, err := os.ReadFile(diffRequest.GetPath())
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", diffRequest.GetPath(), err)
	}
	// Re-apply at execution time: the file may have changed since review.
	patched, _, err := ApplyPatches(diffRequest.GetPath(), string(contentBytes), patches)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(diffRequest.GetPath(), []byte(patched), info.Mode()); err != nil {
		return nil, fmt.Errorf("writing %s: %w", diffRequest.GetPath(), err)
	}
	diffResponse := &sgptpb.DiffResponse{
		Path:         diffRequest.GetPath(),
		HunksApplied: int32(len(patches)),
	}
	return tool.NewStructuredToolResult(toolCall, diffResponse)
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
	diffRequest := &sgptpb.DiffRequest{}
	// Partial arguments: tolerate missing fields, only require some diff text.
	if tool.UnmarshalArguments(toolCall, diffRequest) != nil || diffRequest.GetDiff() == "" {
		return "", false
	}
	header := ""
	if diffRequest.GetPath() != "" {
		header = fmt.Sprintf("--- a/%s\n+++ b/%s\n", diffRequest.GetPath(), diffRequest.GetPath())
	}
	return fmt.Sprintf("```diff\n%s%s\n```", header, strings.TrimSuffix(diffRequest.GetDiff(), "\n")), true
}

// RenderHeader shows the file being edited instead of the tool name. It
// tolerates partial arguments so the header appears as soon as the path
// streams in.
func (t *Tool) RenderHeader(toolCall *aipb.ToolCall) (string, bool) {
	diffRequest := &sgptpb.DiffRequest{}
	if tool.UnmarshalArguments(toolCall, diffRequest) != nil || diffRequest.GetPath() == "" {
		return "", false
	}
	return fmt.Sprintf("edited `%s`", diffRequest.GetPath()), true
}

var (
	_ tool.Tool            = (*Tool)(nil)
	_ tool.RequestRenderer = (*Tool)(nil)
	_ tool.HeaderRenderer  = (*Tool)(nil)
)

func init() { tool.RegisterBuiltin(Definition) }
