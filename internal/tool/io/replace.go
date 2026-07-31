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
	// The display message is reserved for problems; on success the title
	// (✏️ path) plus the rendered diff already say everything.
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

// RenderRequest renders the review-time diff instead of raw JSON arguments.
// The diff is persisted on the call's metadata so it survives chat reloads
// even though the underlying file has since changed.
func (t *ReplaceTool) RenderRequest(toolCall *aipb.ToolCall) (string, bool) {
	metadata, err := tool.ParseToolCallMetadata(toolCall)
	if err != nil || metadata.GetDiff() == "" {
		return "", false
	}
	return fmt.Sprintf("```diff\n%s\n```", strings.TrimSuffix(metadata.GetDiff(), "\n")), true
}

// RenderHeader shows the file being edited instead of the tool name. It
// tolerates partial arguments so the header appears as soon as the path
// streams in.
func (t *ReplaceTool) RenderHeader(toolCall *aipb.ToolCall) (string, bool) {
	replaceRequest := &sgptpb.ReplaceRequest{}
	if tool.UnmarshalArguments(toolCall, replaceRequest) != nil || replaceRequest.GetPath() == "" {
		return "", false
	}
	return fmt.Sprintf("edited `%s`", replaceRequest.GetPath()), true
}

var (
	_ tool.Tool            = (*ReplaceTool)(nil)
	_ tool.RequestRenderer = (*ReplaceTool)(nil)
	_ tool.HeaderRenderer  = (*ReplaceTool)(nil)
)

func init() { tool.RegisterBuiltin(Replace) }
