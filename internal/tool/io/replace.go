package io

import (
	"context"
	"fmt"
	"os"
	"strings"

	aipb "github.com/malonaz/core/genproto/ai/v1"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/tool"
	"github.com/malonaz/sgpt/internal/tool/diff"
)

// Replace is the tool definition for patch-based file editing, built from
// the ToolService.Replace method.
var Replace = tool.MustBuildTool("replace", tool.HandlerIDReplace, "sgpt.v1.ToolService.Replace")

func parseReplaceArguments(toolCall *aipb.ToolCall) (*sgptpb.ReplaceRequest, error) {
	replaceRequest := &sgptpb.ReplaceRequest{}
	if err := tool.UnmarshalArguments(toolCall, replaceRequest); err != nil {
		return nil, err
	}
	if replaceRequest.GetPath() == "" {
		return nil, fmt.Errorf("no path specified")
	}
	if len(replaceRequest.GetPatches()) == 0 {
		return nil, fmt.Errorf("no patches specified")
	}
	return replaceRequest, nil
}

// toPatches converts proto patches into the diff package's patch type.
func toPatches(protoPatches []*sgptpb.Patch) []diff.Patch {
	patches := make([]diff.Patch, 0, len(protoPatches))
	for _, protoPatch := range protoPatches {
		patches = append(patches, diff.Patch{
			Search:     protoPatch.GetSearch(),
			Replace:    protoPatch.GetReplace(),
			ReplaceAll: protoPatch.GetReplaceAll(),
		})
	}
	return patches
}

// ReplaceTool applies search/replace patches to files on the user's system.
type ReplaceTool struct{}

func (t *ReplaceTool) Review(_ context.Context, toolCall *aipb.ToolCall) (*sgptpb.ToolCallMetadata, error) {
	replaceRequest, err := parseReplaceArguments(toolCall)
	if err != nil {
		return nil, err
	}
	// File mutation: never auto-execute.
	// The display message is reserved for problems; on success the row plus
	// the rendered diff already say everything.
	metadata := &sgptpb.ToolCallMetadata{
		DisplayMessage: &sgptpb.DisplayMessage{},
	}
	contentBytes, err := os.ReadFile(replaceRequest.GetPath())
	if err != nil {
		// Surface the failure in the review UI rather than erroring the turn;
		// Execute produces the error result the model can react to.
		metadata.DisplayMessage.Content = fmt.Sprintf("Edit will fail: %v", err)
		return metadata, nil
	}
	// Dry-run: the user reviews the exact diff that will apply.
	if _, diff, err := diff.ApplyPatches(replaceRequest.GetPath(), replaceRequest.GetPath(), string(contentBytes), toPatches(replaceRequest.GetPatches())); err != nil {
		metadata.DisplayMessage.Content = fmt.Sprintf("Edit will fail: %v", err)
	} else {
		metadata.Diff = diff
	}
	return metadata, nil
}

func (t *ReplaceTool) Execute(_ context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error) {
	replaceRequest, err := parseReplaceArguments(toolCall)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(replaceRequest.GetPath())
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", replaceRequest.GetPath(), err)
	}
	contentBytes, err := os.ReadFile(replaceRequest.GetPath())
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", replaceRequest.GetPath(), err)
	}
	// Re-apply at execution time: the file may have changed since review.
	patched, _, err := diff.ApplyPatches(replaceRequest.GetPath(), replaceRequest.GetPath(), string(contentBytes), toPatches(replaceRequest.GetPatches()))
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(replaceRequest.GetPath(), []byte(patched), info.Mode()); err != nil {
		return nil, fmt.Errorf("writing %s: %w", replaceRequest.GetPath(), err)
	}
	replaceResponse := &sgptpb.ReplaceResponse{
		Path:           replaceRequest.GetPath(),
		PatchesApplied: int32(len(replaceRequest.GetPatches())),
	}
	return tool.NewStructuredToolResult(toolCall, replaceResponse)
}

// Summarize names the edited file and, once applied, the size of the edit.
func (t *ReplaceTool) Summarize(toolCall *aipb.ToolCall, toolResult *aipb.ToolResult) (tool.Summary, bool) {
	summary := tool.Summary{Verb: "Edit", Subject: tool.DisplayPath(tool.Argument(toolCall, "path").GetStringValue())}
	if toolResult != nil {
		if metadata, err := tool.ParseToolCallMetadata(toolCall); err == nil && metadata.GetDiff() != "" {
			summary.Outcome = tool.DiffStat(metadata.GetDiff())
		}
	}
	return summary, true
}

// RenderRequest renders the review-time diff. The diff is persisted on the
// call's metadata so it survives chat reloads even though the file has since
// changed. While the call streams in (no review yet), the patches render as
// a diff of their search and replace text.
func (t *ReplaceTool) RenderRequest(toolCall *aipb.ToolCall) (string, bool) {
	if metadata, err := tool.ParseToolCallMetadata(toolCall); err == nil && metadata.GetDiff() != "" {
		return tool.Fence("diff", metadata.GetDiff()), true
	}
	replaceRequest := &sgptpb.ReplaceRequest{}
	if tool.UnmarshalArguments(toolCall, replaceRequest) != nil || len(replaceRequest.GetPatches()) == 0 {
		return "", false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", replaceRequest.GetPath(), replaceRequest.GetPath())
	for _, patch := range replaceRequest.GetPatches() {
		b.WriteString("@@\n")
		for _, line := range strings.Split(patch.GetSearch(), "\n") {
			b.WriteString("-" + line + "\n")
		}
		for _, line := range strings.Split(patch.GetReplace(), "\n") {
			b.WriteString("+" + line + "\n")
		}
	}
	return tool.Fence("diff", b.String()), true
}

var (
	_ tool.Tool            = (*ReplaceTool)(nil)
	_ tool.Summarizer      = (*ReplaceTool)(nil)
	_ tool.RequestRenderer = (*ReplaceTool)(nil)
)

func init() { tool.RegisterBuiltin(Replace) }
