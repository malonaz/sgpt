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

const diffContextLines = 3

type patch struct {
	Search     string `json:"search"`
	Replace    string `json:"replace"`
	ReplaceAll bool   `json:"replace_all"`
}

// applyPatches applies search/replace patches sequentially and builds a
// unified-style diff. Patches whose search text has no exact match are healed
// against the content with whitespace-lenient matching. The same code path
// serves review (dry-run) and execution, so the diff the user approves is
// exactly what gets applied.
func applyPatches(path, content string, patches []patch) (string, string, error) {
	var diff strings.Builder
	diff.WriteString(fmt.Sprintf("--- a/%s\n+++ b/%s\n", path, path))
	// Line drift from earlier patches, so later hunk headers stay accurate.
	lineDelta := 0

	for i, p := range patches {
		if p.Search == "" {
			return "", "", fmt.Errorf("patch %d: empty search text", i+1)
		}
		// Heal against the current content so hunks with stale whitespace or
		// indentation still land, and later hunks see earlier hunks' edits.
		p = healPatch(content, p)
		count := strings.Count(content, p.Search)
		if count == 0 {
			return "", "", fmt.Errorf("patch %d: search text not found in %s", i+1, path)
		}
		if count > 1 && !p.ReplaceAll {
			return "", "", fmt.Errorf("patch %d: search text matches %d locations in %s; add more context or set replace_all", i+1, count, path)
		}
		occurrences := 1
		if p.ReplaceAll {
			occurrences = count
		}

		searchFrom := 0
		for range occurrences {
			index := strings.Index(content[searchFrom:], p.Search) + searchFrom
			// Expand the match to full lines so the diff shows complete lines.
			lineStart := strings.LastIndex(content[:index], "\n") + 1
			lineEnd := index + len(p.Search)
			if newline := strings.Index(content[lineEnd:], "\n"); newline != -1 {
				lineEnd += newline
			} else {
				lineEnd = len(content)
			}

			removed := strings.Split(content[lineStart:lineEnd], "\n")
			// Index-based replacement: the expanded region may contain other
			// occurrences of the search text.
			added := strings.Split(content[lineStart:index]+p.Replace+content[index+len(p.Search):lineEnd], "\n")

			allLines := strings.Split(content, "\n")
			startLine := strings.Count(content[:lineStart], "\n") // 0-based
			contextStart := max(0, startLine-diffContextLines)
			afterRemoved := startLine + len(removed)
			contextEnd := min(len(allLines), afterRemoved+diffContextLines)

			oldCount := (startLine - contextStart) + len(removed) + (contextEnd - afterRemoved)
			newCount := (startLine - contextStart) + len(added) + (contextEnd - afterRemoved)
			diff.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", contextStart+1, oldCount, contextStart+1+lineDelta, newCount))
			for _, line := range allLines[contextStart:startLine] {
				diff.WriteString(" " + line + "\n")
			}
			for _, line := range removed {
				diff.WriteString("-" + line + "\n")
			}
			for _, line := range added {
				diff.WriteString("+" + line + "\n")
			}
			for _, line := range allLines[afterRemoved:contextEnd] {
				diff.WriteString(" " + line + "\n")
			}

			lineDelta += len(added) - len(removed)
			content = content[:index] + p.Replace + content[index+len(p.Search):]
			// Resume past the replacement so a replace containing the search
			// text cannot loop forever.
			searchFrom = index + len(p.Replace)
		}
	}
	return content, diff.String(), nil
}

// healPatch relocates a patch whose search text has no exact match by
// comparing lines with whitespace leniency, then rebuilds the search from the
// file's actual lines. This absorbs the most common model diff mistakes:
// stale trailing whitespace and wrong indentation. Ambiguous or absent
// matches leave the patch untouched so applyPatches reports the error.
func healPatch(content string, p patch) patch {
	if strings.Contains(content, p.Search) {
		return p
	}
	searchLines := strings.Split(p.Search, "\n")
	contentLines := strings.Split(content, "\n")

	// Tier 1: ignore trailing whitespace only.
	if actual, ok := findUniqueLines(contentLines, searchLines, trimTrailingWhitespace); ok {
		p.Search = strings.Join(actual, "\n")
		return p
	}
	// Tier 2: ignore all surrounding whitespace, then re-indent the
	// replacement so inserted lines match the file's actual indentation.
	actual, ok := findUniqueLines(contentLines, searchLines, strings.TrimSpace)
	if !ok {
		return p
	}
	searchIndent := leadingWhitespace(searchLines[0])
	actualIndent := leadingWhitespace(actual[0])
	replaceLines := strings.Split(p.Replace, "\n")
	for i, line := range replaceLines {
		if strings.HasPrefix(line, searchIndent) && strings.TrimSpace(line) != "" {
			replaceLines[i] = actualIndent + line[len(searchIndent):]
		}
	}
	p.Search = strings.Join(actual, "\n")
	p.Replace = strings.Join(replaceLines, "\n")
	return p
}

// findUniqueLines slides a window over contentLines and returns the actual
// lines of the single window equal to searchLines under normalize; false if
// zero or multiple windows match.
func findUniqueLines(contentLines, searchLines []string, normalize func(string) string) ([]string, bool) {
	matchStart, matchCount := -1, 0
	for i := 0; i+len(searchLines) <= len(contentLines); i++ {
		match := true
		for j, searchLine := range searchLines {
			if normalize(contentLines[i+j]) != normalize(searchLine) {
				match = false
				break
			}
		}
		if match {
			matchStart = i
			if matchCount++; matchCount > 1 {
				return nil, false
			}
		}
	}
	if matchCount != 1 {
		return nil, false
	}
	return contentLines[matchStart : matchStart+len(searchLines)], true
}

func trimTrailingWhitespace(line string) string { return strings.TrimRight(line, " \t") }

func leadingWhitespace(line string) string {
	return line[:len(line)-len(strings.TrimLeft(line, " \t"))]
}

// parseUnifiedDiff converts a unified diff into search/replace patches, one
// per hunk. Line numbers in '@@' headers are ignored entirely — models
// miscount them constantly — so each hunk is anchored purely by its context
// and removed lines.
func parseUnifiedDiff(diff string) ([]patch, error) {
	var patches []patch
	var search, replace []string
	inHunk := false
	flush := func() error {
		if !inHunk {
			return nil
		}
		// Drop trailing blank context (usually an artifact of the diff
		// string's final newline) so hunks anchor on real content.
		for len(search) > 0 && len(replace) > 0 &&
			search[len(search)-1] == "" && replace[len(replace)-1] == "" {
			search = search[:len(search)-1]
			replace = replace[:len(replace)-1]
		}
		if len(search) == 0 {
			return fmt.Errorf("hunk %d has no context or removed lines to anchor on", len(patches)+1)
		}
		patches = append(patches, patch{
			Search:  strings.Join(search, "\n"),
			Replace: strings.Join(replace, "\n"),
		})
		search, replace = nil, nil
		return nil
	}
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "@@"):
			if err := flush(); err != nil {
				return nil, err
			}
			inHunk = true
		case !inHunk:
			// Tolerate anything before the first hunk: '---'/'+++' file
			// headers, 'diff --git' lines, prose.
		case strings.HasPrefix(line, `\`):
			// "\ No newline at end of file" markers.
		case strings.HasPrefix(line, "+"):
			replace = append(replace, line[1:])
		case strings.HasPrefix(line, "-"):
			search = append(search, line[1:])
		case strings.HasPrefix(line, " "):
			search = append(search, line[1:])
			replace = append(replace, line[1:])
		default:
			// Context line missing its leading space — a common model slip,
			// notably on blank lines.
			search = append(search, line)
			replace = append(replace, line)
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if len(patches) == 0 {
		return nil, fmt.Errorf("diff contains no hunks")
	}
	return patches, nil
}

// ---- edit_file: unified-diff editing built on the patch engine above ----

// EditFile is the tool definition for unified-diff file editing.
var EditFile = &aipb.Tool{
	Name:        "edit_file",
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
		ToolHandlerIDAnnotation: HandlerIDEditFile,
	},
}

type editFileArguments struct {
	Path string `json:"path"`
	Diff string `json:"diff"`
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
	if strings.TrimSpace(arguments.Diff) == "" {
		return nil, fmt.Errorf("no diff specified")
	}
	return arguments, nil
}

// EditFileTool applies model-written unified diffs to files on the user's
// system. Hunks are converted to search/replace patches and healed against
// the file, so the model never has to count lines.
type EditFileTool struct{}

func (t *EditFileTool) Review(_ context.Context, toolCall *aipb.ToolCall) (*sgptpb.ToolCallMetadata, error) {
	arguments, err := parseEditFileArguments(toolCall)
	if err != nil {
		return nil, err
	}
	// File mutation: never auto-execute.
	metadata := &sgptpb.ToolCallMetadata{
		DisplayMessage: &sgptpb.DisplayMessage{
			Content: fmt.Sprintf("Editing %s", arguments.Path),
		},
	}
	// Surface failures in the review UI rather than erroring the turn;
	// Execute produces the error result the model can react to.
	patches, err := parseUnifiedDiff(arguments.Diff)
	if err != nil {
		metadata.DisplayMessage.Content = fmt.Sprintf("Edit will fail: %v", err)
		return metadata, nil
	}
	metadata.DisplayMessage.Content = fmt.Sprintf("Editing %s (%d hunk(s))", arguments.Path, len(patches))
	contentBytes, err := os.ReadFile(arguments.Path)
	if err != nil {
		metadata.DisplayMessage.Content = fmt.Sprintf("Edit will fail: %v", err)
		return metadata, nil
	}
	// Dry-run: the user reviews the healed diff that will actually apply,
	// not the model's raw (possibly misaligned) diff.
	if _, diff, err := applyPatches(arguments.Path, string(contentBytes), patches); err != nil {
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
	patches, err := parseUnifiedDiff(arguments.Diff)
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
	patched, _, err := applyPatches(arguments.Path, string(contentBytes), patches)
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

// RenderRequest renders the review-time healed diff instead of the raw
// arguments. The diff is persisted on the call's metadata so it survives chat
// reloads even though the underlying file has since changed.
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
