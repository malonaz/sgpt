package diff

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
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

// RenderRequestRaw renders the healed review-time diff as a GitHub-style
// side-by-side view. Streaming calls (no metadata yet) and narrow widths
// return false, falling back to the unified markdown rendering.
func (t *Tool) RenderRequestRaw(toolCall *aipb.ToolCall, width int) (string, bool) {
	metadata, err := tool.ParseToolCallMetadata(toolCall)
	if err != nil || metadata.GetDiff() == "" {
		return "", false
	}
	return RenderSideBySide(metadata.GetDiff(), width)
}

// GitHub-style split-view palette; backgrounds fill whole cells so changes
// read as colored bands. Terminals have no alpha, so the base bands are the
// emphasis colors pre-blended at 25% over a dark panel (#1e1e1e):
// 0.25×#870000 + 0.75×#1e1e1e = #381717, and likewise #173817 for green —
// the same hues as the emphasis shades, just "transparent".
var (
	sideBySideRemovedStyle = lipgloss.NewStyle().Background(lipgloss.Color("#381717")).Foreground(lipgloss.Color("224"))
	sideBySideAddedStyle   = lipgloss.NewStyle().Background(lipgloss.Color("#173817")).Foreground(lipgloss.Color("194"))
	// Emphasis shades mark the intra-line changed core, GitHub-style.
	sideBySideRemovedEmphasisStyle = lipgloss.NewStyle().Background(lipgloss.Color("88")).Foreground(lipgloss.Color("231"))
	sideBySideAddedEmphasisStyle   = lipgloss.NewStyle().Background(lipgloss.Color("28")).Foreground(lipgloss.Color("231"))
	sideBySideContextStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	sideBySideEmptyStyle           = lipgloss.NewStyle().Background(lipgloss.Color("236"))
	sideBySideNumberStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
	sideBySideSeparatorStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
)

type sideBySideRowKind int

const (
	sideBySideRowContext sideBySideRowKind = iota
	sideBySideRowChange
	sideBySideRowHunkBreak
)

type sideBySideRow struct {
	kind                 sideBySideRowKind
	oldNumber, newNumber int
	oldText, newText     string
	hasOld, hasNew       bool
}

// RenderSideBySide renders a unified diff as a GitHub-style split view:
// removals left, additions right, change runs aligned. Returns false when
// the diff has no hunks or the width is too narrow to split usefully, so
// callers can fall back to unified rendering.
func RenderSideBySide(diff string, width int) (string, bool) {
	rows := parseSideBySideRows(strings.TrimSuffix(diff, "\n"))
	if len(rows) == 0 {
		return "", false
	}
	numberWidth := 3
	for _, row := range rows {
		if digits := len(strconv.Itoa(max(row.oldNumber, row.newNumber))); digits > numberWidth {
			numberWidth = digits
		}
	}
	// Each half: number column + space + content + trailing space; halves
	// joined by a single separator column.
	contentWidth := (width-1)/2 - numberWidth - 2
	if contentWidth < 10 {
		return "", false
	}
	halfWidth := numberWidth + contentWidth + 2
	separator := sideBySideSeparatorStyle.Render("│")

	var b strings.Builder
	for i, row := range rows {
		if i > 0 {
			b.WriteString("\n")
		}
		if row.kind == sideBySideRowHunkBreak {
			b.WriteString(sideBySideSeparatorStyle.Render(strings.Repeat("┈", halfWidth*2+1)))
			continue
		}
		oldStyle, newStyle := sideBySideContextStyle, sideBySideContextStyle
		oldEmphasisStyle, newEmphasisStyle := oldStyle, newStyle
		// Expand tabs before computing emphasis so rune indexes line up with
		// what the cell renderer truncates and pads.
		oldText := strings.ReplaceAll(row.oldText, "\t", "    ")
		newText := strings.ReplaceAll(row.newText, "\t", "    ")
		var oldFrom, oldTo, newFrom, newTo int
		if row.kind == sideBySideRowChange {
			oldStyle, newStyle = sideBySideRemovedStyle, sideBySideAddedStyle
			oldEmphasisStyle, newEmphasisStyle = sideBySideRemovedEmphasisStyle, sideBySideAddedEmphasisStyle
			// Intra-line emphasis only makes sense on modified pairs; pure
			// adds/removes stay a uniform band, as on GitHub.
			if row.hasOld && row.hasNew {
				oldFrom, oldTo, newFrom, newTo = intralineRanges(oldText, newText)
			}
		}
		b.WriteString(renderSideBySideCell(row.hasOld, row.oldNumber, oldText, numberWidth, contentWidth, oldStyle, oldEmphasisStyle, oldFrom, oldTo))
		b.WriteString(separator)
		b.WriteString(renderSideBySideCell(row.hasNew, row.newNumber, newText, numberWidth, contentWidth, newStyle, newEmphasisStyle, newFrom, newTo))
	}
	return b.String(), true
}

// intralineRanges locates the differing core of a modified line pair by
// trimming the runes common to both ends. Cheap, and matches GitHub's
// within-line highlighting for the typical single-span edit; multi-span
// edits emphasize one slightly-too-wide region instead of several.
func intralineRanges(oldText, newText string) (oldFrom, oldTo, newFrom, newTo int) {
	oldRunes, newRunes := []rune(oldText), []rune(newText)
	prefix := 0
	for prefix < len(oldRunes) && prefix < len(newRunes) && oldRunes[prefix] == newRunes[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(oldRunes)-prefix && suffix < len(newRunes)-prefix &&
		oldRunes[len(oldRunes)-1-suffix] == newRunes[len(newRunes)-1-suffix] {
		suffix++
	}
	return prefix, len(oldRunes) - suffix, prefix, len(newRunes) - suffix
}

func renderSideBySideCell(present bool, number int, text string, numberWidth, contentWidth int, style, emphasisStyle lipgloss.Style, emphasisFrom, emphasisTo int) string {
	if !present {
		// Filler: no counterpart line on this side (pure add/remove).
		return sideBySideEmptyStyle.Render(strings.Repeat(" ", numberWidth+contentWidth+2))
	}
	numberText := strings.Repeat(" ", numberWidth)
	if number > 0 {
		numberText = fmt.Sprintf("%*d", numberWidth, number)
	}
	runes := []rune(text)
	if len(runes) > contentWidth {
		runes = append(runes[:contentWidth-1], '…')
	}
	// Clamp emphasis to the visible (possibly truncated) runes; padding is
	// never emphasized.
	emphasisFrom = min(max(emphasisFrom, 0), len(runes))
	emphasisTo = min(max(emphasisTo, emphasisFrom), len(runes))
	padding := strings.Repeat(" ", contentWidth-len(runes))
	if emphasisFrom == emphasisTo {
		return sideBySideNumberStyle.Render(numberText) + style.Render(" "+string(runes)+padding+" ")
	}
	return sideBySideNumberStyle.Render(numberText) +
		style.Render(" "+string(runes[:emphasisFrom])) +
		emphasisStyle.Render(string(runes[emphasisFrom:emphasisTo])) +
		style.Render(string(runes[emphasisTo:])+padding+" ")
}

// parseSideBySideRows pairs each hunk's removed/added runs index-wise so
// changed lines sit opposite each other. Hunk header numbers are trusted:
// callers render the healed diff produced by ApplyPatches, whose headers are
// computed, not model-written.
func parseSideBySideRows(diff string) []sideBySideRow {
	var rows []sideBySideRow
	var removed, added []string
	oldLine, newLine := 0, 0
	flush := func() {
		for i := 0; i < len(removed) || i < len(added); i++ {
			row := sideBySideRow{kind: sideBySideRowChange}
			if i < len(removed) {
				row.oldText, row.oldNumber, row.hasOld = removed[i], oldLine, true
				oldLine++
			}
			if i < len(added) {
				row.newText, row.newNumber, row.hasNew = added[i], newLine, true
				newLine++
			}
			rows = append(rows, row)
		}
		removed, added = nil, nil
	}
	inHunk := false
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "@@"):
			flush()
			if inHunk {
				rows = append(rows, sideBySideRow{kind: sideBySideRowHunkBreak})
			}
			inHunk = true
			oldLine, newLine = parseHunkHeader(line)
		case !inHunk:
			// File headers, prose — not rendered.
		case strings.HasPrefix(line, `\`):
			// "\ No newline at end of file".
		case strings.HasPrefix(line, "-"):
			removed = append(removed, line[1:])
		case strings.HasPrefix(line, "+"):
			added = append(added, line[1:])
		default:
			flush()
			text := strings.TrimPrefix(line, " ")
			rows = append(rows, sideBySideRow{
				kind:    sideBySideRowContext,
				oldText: text, newText: text,
				oldNumber: oldLine, newNumber: newLine,
				hasOld: true, hasNew: true,
			})
			oldLine++
			newLine++
		}
	}
	flush()
	return rows
}

func parseHunkHeader(line string) (int, int) {
	var oldStart, oldCount, newStart, newCount int
	if _, err := fmt.Sscanf(line, "@@ -%d,%d +%d,%d @@", &oldStart, &oldCount, &newStart, &newCount); err != nil {
		return 0, 0 // unknown — cells render without numbers
	}
	return oldStart, newStart
}

var (
	_ tool.Tool               = (*Tool)(nil)
	_ tool.RequestRenderer    = (*Tool)(nil)
	_ tool.HeaderRenderer     = (*Tool)(nil)
	_ tool.RawRequestRenderer = (*Tool)(nil)
)

func init() { tool.RegisterBuiltin(Definition) }
