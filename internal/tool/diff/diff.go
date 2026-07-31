package diff

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
// the file, so the model never has to count lines. Diff headers additionally
// support creating ('--- /dev/null'), deleting ('+++ /dev/null') and
// renaming (differing '+++' path) files.
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
	fail := func(err error) (*sgptpb.ToolCallMetadata, error) {
		metadata.DisplayMessage.Content = fmt.Sprintf("Edit will fail: %v", err)
		return metadata, nil
	}
	fileDiff, err := ParseUnifiedDiff(diffRequest.GetDiff())
	if err != nil {
		return fail(err)
	}
	path := diffRequest.GetPath()
	switch {
	case isCreate(path, fileDiff):
		if _, err := os.Stat(path); err == nil {
			return fail(fmt.Errorf("%s already exists", path))
		}
		content, err := NewFileContent(fileDiff)
		if err != nil {
			return fail(err)
		}
		metadata.Diff = RenderCreateDiff(path, content)
	case fileDiff.Delete:
		contentBytes, err := os.ReadFile(path)
		if err != nil {
			return fail(err)
		}
		metadata.Diff = RenderDeleteDiff(path, string(contentBytes))
	default:
		newPath := renameTarget(path, fileDiff)
		if newPath != path {
			if _, err := os.Stat(newPath); err == nil {
				return fail(fmt.Errorf("rename target %s already exists", newPath))
			}
		}
		contentBytes, err := os.ReadFile(path)
		if err != nil {
			return fail(err)
		}
		// Dry-run: the user reviews the healed diff that will actually apply,
		// not the model's raw (possibly misaligned) diff.
		_, diff, err := ApplyPatches(path, newPath, string(contentBytes), fileDiff.Patches)
		if err != nil {
			return fail(err)
		}
		metadata.Diff = diff
	}
	return metadata, nil
}

func (t *Tool) Execute(_ context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error) {
	diffRequest, err := parseArguments(toolCall)
	if err != nil {
		return nil, err
	}
	fileDiff, err := ParseUnifiedDiff(diffRequest.GetDiff())
	if err != nil {
		return nil, err
	}
	path := diffRequest.GetPath()
	diffResponse := &sgptpb.DiffResponse{
		Path:         path,
		HunksApplied: int32(len(fileDiff.Patches)),
	}
	switch {
	case isCreate(path, fileDiff):
		if _, err := os.Stat(path); err == nil {
			return nil, fmt.Errorf("%s already exists", path)
		}
		content, err := NewFileContent(fileDiff)
		if err != nil {
			return nil, err
		}
		if err := writeFile(path, content, 0o644); err != nil {
			return nil, err
		}
	case fileDiff.Delete:
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("deleting %s: %w", path, err)
		}
	default:
		newPath := renameTarget(path, fileDiff)
		if len(fileDiff.Patches) == 0 && newPath == path {
			return nil, fmt.Errorf("diff contains no hunks")
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", path, err)
		}
		contentBytes, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		// Re-apply at execution time: the file may have changed since review.
		patched, _, err := ApplyPatches(path, newPath, string(contentBytes), fileDiff.Patches)
		if err != nil {
			return nil, err
		}
		if newPath != path {
			if _, err := os.Stat(newPath); err == nil {
				return nil, fmt.Errorf("rename target %s already exists", newPath)
			}
		}
		if err := writeFile(newPath, patched, info.Mode()); err != nil {
			return nil, err
		}
		// Only remove the source once the destination is safely written.
		if newPath != path {
			if err := os.Remove(path); err != nil {
				return nil, fmt.Errorf("removing %s after rename: %w", path, err)
			}
		}
		diffResponse.Path = newPath
	}
	return tool.NewStructuredToolResult(toolCall, diffResponse)
}

// renameTarget resolves the destination path: the '+++' header path when it
// differs from the request path, otherwise the request path itself.
func renameTarget(path string, fileDiff *FileDiff) string {
	if fileDiff.NewPath != "" && fileDiff.NewPath != path {
		return fileDiff.NewPath
	}
	return path
}

// isCreate reports whether the diff should be applied as a file creation.
// Beyond an explicit '--- /dev/null' header, a diff whose target does not
// exist yet and whose hunks are pure additions is treated as a creation:
// models routinely omit the /dev/null header, and failing there would also
// skip the parent-directory creation the write needs.
func isCreate(path string, fileDiff *FileDiff) bool {
	if fileDiff.Create {
		return true
	}
	if fileDiff.Delete || len(fileDiff.Patches) == 0 {
		return false
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		return false
	}
	for _, patch := range fileDiff.Patches {
		if patch.Search != "" {
			return false
		}
	}
	return true
}

// writeFile writes content to path, creating any missing parent directories
// first: diffs regularly target new files in directories that do not exist.
func writeFile(path, content string, mode os.FileMode) error {
	if directory := filepath.Dir(path); directory != "." {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return fmt.Errorf("creating directories for %s: %w", path, err)
		}
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
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

// RenderHeader shows the file being changed instead of the tool name. It
// tolerates partial arguments so the header appears as soon as the path
// streams in.
func (t *Tool) RenderHeader(toolCall *aipb.ToolCall) (string, bool) {
	diffRequest := &sgptpb.DiffRequest{}
	if tool.UnmarshalArguments(toolCall, diffRequest) != nil || diffRequest.GetPath() == "" {
		return "", false
	}
	path := diffRequest.GetPath()
	// The verb tracks the operation once enough of the diff has streamed in
	// for its file headers to parse.
	if fileDiff, err := ParseUnifiedDiff(diffRequest.GetDiff()); err == nil {
		switch {
		case isCreate(path, fileDiff):
			return fmt.Sprintf("created `%s`", path), true
		case fileDiff.Delete:
			return fmt.Sprintf("deleted `%s`", path), true
		case renameTarget(path, fileDiff) != path:
			return fmt.Sprintf("renamed `%s` → `%s`", path, renameTarget(path, fileDiff)), true
		}
	}
	return fmt.Sprintf("edited `%s`", path), true
}

var (
	_ tool.Tool            = (*Tool)(nil)
	_ tool.RequestRenderer = (*Tool)(nil)
	_ tool.HeaderRenderer  = (*Tool)(nil)
)

func init() { tool.RegisterBuiltin(Definition) }
