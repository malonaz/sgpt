package timeline

import (
	"fmt"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/pbutil"

	"github.com/malonaz/sgpt/cli/tui/styles"
	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/markdown"
	"github.com/malonaz/sgpt/internal/tools"
)

// frame renders content in a role-styled bordered box, recoloring the border
// to reflect selection when the timeline is focused.
func frame(ctx RenderContext, style lipgloss.Style, content string) string {
	style = style.Width(ctx.Width - styles.MessageHorizontalFrameSize())
	if ctx.Focused {
		borderColor := styles.MessageUnselectedColor
		if ctx.Selected {
			borderColor = styles.MessageSelectedColor
		}
		style = style.BorderForeground(borderColor)
	}
	return style.Render(content)
}

func renderMarkdown(ctx RenderContext, seq int, finalized bool, blocks ...markdown.Block) string {
	if ctx.Static {
		// Sentinel index disables the renderer's cache for static previews.
		seq, finalized = -1, false
	}
	return ctx.Renderer.ToMarkdown(seq, finalized, blocks...)
}

// ---- TextItem: one API block, containing individually navigable fences ----

type TextItem struct {
	id        string
	seq       int
	style     lipgloss.Style
	blocks    []markdown.Block
	finalized bool
}

func (i *TextItem) ID() string { return i.id }

// CacheKey opts out while streaming: content is still mutating.
func (i *TextItem) CacheKey() string {
	if !i.finalized {
		return ""
	}
	return i.id
}

func (i *TextItem) Content() (string, string) {
	var b strings.Builder
	for j, block := range i.blocks {
		if j > 0 {
			b.WriteString("\n")
		}
		b.WriteString(block.Content())
	}
	return b.String(), "md"
}

func (i *TextItem) SubCount() int { return len(i.blocks) }

func (i *TextItem) SubContent(index int) (string, string) {
	if index < 0 || index >= len(i.blocks) {
		return i.Content()
	}
	return i.blocks[index].Content(), i.blocks[index].Extension()
}

// blockFinalized: only the trailing block of a streaming message is still
// mutating; earlier blocks are complete. Rendering them as non-finalized
// thrashes the renderer's incremental state and re-runs glamour every tick.
func (i *TextItem) blockFinalized(index int) bool {
	return i.finalized || index < len(i.blocks)-1
}

func (i *TextItem) SubOffsets(ctx RenderContext) []int {
	offsets := make([]int, len(i.blocks))
	line := 1 // account for the frame's top border
	for j, block := range i.blocks {
		offsets[j] = line
		line += strings.Count(renderMarkdown(ctx, i.seq+j, i.blockFinalized(j), block), "\n") + 1
	}
	return offsets
}

func (i *TextItem) Render(ctx RenderContext) string {
	var b strings.Builder
	for j, block := range i.blocks {
		if j > 0 {
			b.WriteString("\n")
		}
		rendered := renderMarkdown(ctx, i.seq+j, i.blockFinalized(j), block)
		b.WriteString(i.gutter(ctx, j, rendered))
	}
	return frame(ctx, i.style, b.String())
}

// gutter prefixes every line with a permanent two-column gutter so layout
// never shifts on selection; the indicator only lights up while navigating —
// purple on the selected fence, dim on its siblings.
func (i *TextItem) gutter(ctx RenderContext, index int, rendered string) string {
	prefix := "  "
	if ctx.Selected && ctx.SelectedSub >= 0 {
		style := styles.BlockIndicatorStyle
		if index == ctx.SelectedSub {
			style = styles.BlockIndicatorSelectedStyle
		}
		prefix = style.Render(styles.BlockIndicatorChar) + " "
	}
	var b strings.Builder
	for lineIndex, line := range strings.Split(rendered, "\n") {
		if lineIndex > 0 {
			b.WriteString("\n")
		}
		b.WriteString(prefix)
		b.WriteString(line)
	}
	return b.String()
}

// ---- ThoughtItem: collapsible reasoning ----

type ThoughtItem struct {
	id        string
	seq       int
	text      string
	finalized bool
}

func (i *ThoughtItem) ID() string                { return i.id }
func (i *ThoughtItem) Content() (string, string) { return i.text, "md" }

// CacheKey opts out while streaming: content is still mutating.
func (i *ThoughtItem) CacheKey() string {
	if !i.finalized {
		return ""
	}
	return i.id
}

// Finalized thoughts fold away by default: they are rarely re-read.
func (i *ThoughtItem) DefaultCollapsed() bool { return i.finalized }
func (i *ThoughtItem) Render(ctx RenderContext) string {
	if ctx.Collapsed {
		summary := styles.ThoughtLabelStyle.Render(
			fmt.Sprintf("🧠 thought (%d lines) — alt+z to expand", strings.Count(i.text, "\n")+1))
		return frame(ctx, styles.AIThoughtStyle, summary)
	}
	return frame(ctx, styles.AIThoughtStyle, renderMarkdown(ctx, i.seq, i.finalized, markdown.ParseBlocks(i.text)...))
}

// RequestRenderer lets a tool dictate how its request renders in the
// timeline; unset (or declining) falls back to raw JSON arguments.
type RequestRenderer interface {
	RenderRequest(toolCall *aipb.ToolCall) (string, bool)
}

// ---- ToolCallItem: request/response pair rendered adjacently ----

type ToolCallItem struct {
	id        string
	seq       int
	ToolCall  *aipb.ToolCall
	Result    *aipb.ToolResult
	Executing bool
	// RequestRenderer, when set, overrides the raw-JSON request rendering.
	RequestRenderer RequestRenderer
}

func (i *ToolCallItem) ID() string { return i.id }

// CacheKey: result attachment, execution and review status all change the render.
func (i *ToolCallItem) CacheKey() string {
	return fmt.Sprintf("%s|r%t|e%t|s%v", i.id, i.Result != nil, i.Executing, tools.GetToolCallStatus(i.ToolCall))
}

// Resolved calls fold to a one-line summary; pending/executing stay expanded.
func (i *ToolCallItem) DefaultCollapsed() bool {
	return i.Result != nil && !i.Executing
}

func (i *ToolCallItem) Content() (string, string) {
	var b strings.Builder
	bytes, _ := pbutil.JSONMarshalPretty(i.ToolCall.GetArguments())
	b.WriteString(fmt.Sprintf("Tool Call: %s\n%s", i.ToolCall.GetName(), string(bytes)))
	if i.Result != nil {
		b.WriteString("\n\nResult:\n")
		b.WriteString(toolResultText(i.Result))
	}
	return b.String(), "json"
}

func (i *ToolCallItem) Render(ctx RenderContext) string {
	var b strings.Builder
	b.WriteString(i.header(ctx))
	if !ctx.Collapsed {
		b.WriteString("\n")
		b.WriteString(i.request(ctx))
		if i.Result != nil {
			b.WriteString("\n")
			b.WriteString(i.response(ctx))
		}
	}
	return frame(ctx, styles.AIMessageStyle, b.String())
}

func (i *ToolCallItem) header(ctx RenderContext) string {
	var suffix string
	switch {
	case i.Executing:
		suffix = " " + styles.ThoughtLabelStyle.Render("⏳ running...")
	case i.Result == nil && tools.GetToolCallStatus(i.ToolCall) == tools.ToolCallStatusPending:
		suffix = " " + styles.ErrorStyle.Render("▶ pending review")
	case ctx.Collapsed:
		suffix = " " + styles.DimTextStyle.Render("(alt+z to expand)")
	}
	header := fmt.Sprintf("%s %s%s",
		toolCallStatusIndicator(i.ToolCall),
		styles.ToolLabelStyle.Render("tool: "+i.ToolCall.GetName()),
		suffix,
	)
	if metadata, _ := tools.ParseToolCallMetadata(i.ToolCall); metadata.GetDisplayMessage().GetContent() != "" {
		header += "\n" + styles.DimTextStyle.Render(metadata.GetDisplayMessage().GetContent())
	}
	return header
}

func (i *ToolCallItem) request(ctx RenderContext) string {
	// Tools may dictate their own presentation (e.g. edit_file's diff).
	if i.RequestRenderer != nil {
		if md, ok := i.RequestRenderer.RenderRequest(i.ToolCall); ok {
			return renderMarkdown(ctx, i.seq, true, markdown.ParseBlocks(md)...)
		}
	}
	// Full payload — inspection during review was the whole point.
	bytes, _ := pbutil.JSONMarshalPretty(i.ToolCall.GetArguments())
	fenced := fmt.Sprintf("```json\n%s\n```", string(bytes))
	return renderMarkdown(ctx, i.seq, true, markdown.ParseBlocks(fenced)...)
}

func (i *ToolCallItem) response(ctx RenderContext) string {
	label := lipgloss.NewStyle().Foreground(styles.SecondaryColor).Bold(true).Render("↳ result")
	body := toolResultText(i.Result)
	if i.Result.GetError() != nil {
		return label + "\n" + styles.ErrorStyle.Render(body)
	}
	fenced := fmt.Sprintf("```json\n%s\n```", body)
	return label + "\n" + renderMarkdown(ctx, i.seq+1, true, markdown.ParseBlocks(fenced)...)
}

func toolResultText(toolResult *aipb.ToolResult) string {
	if toolResult.GetError() != nil {
		return fmt.Sprintf("Error: %s", toolResult.GetError().GetMessage())
	}
	if structured := toolResult.GetStructuredContent(); structured != nil {
		bytes, _ := pbutil.JSONMarshalPretty(structured)
		return string(bytes)
	}
	return toolResult.GetContent()
}

func toolCallStatusIndicator(toolCall *aipb.ToolCall) string {
	switch tools.GetToolCallStatus(toolCall) {
	case tools.ToolCallStatusAccepted:
		return lipgloss.NewStyle().Foreground(styles.SuccessColor).Render("●")
	case tools.ToolCallStatusRejected:
		return lipgloss.NewStyle().Foreground(styles.ErrorColor).Render("●")
	default:
		return lipgloss.NewStyle().Foreground(styles.MutedColor).Render("●")
	}
}

// ---- LineItem: single-line entries (system, errors) ----

type LineItem struct {
	id    string
	text  string
	style lipgloss.Style
}

func NewErrorItem(id, text string) *LineItem {
	return &LineItem{id: id, text: "Error: " + text, style: styles.MessageErrorStyle}
}

func NewSystemItem(id, text string) *LineItem {
	return &LineItem{id: id, text: "System: " + styles.Truncate(text, styles.TruncateLength), style: styles.SystemStyle}
}

func (i *LineItem) ID() string                { return i.id }
func (i *LineItem) Content() (string, string) { return i.text, "" }

// CacheKey includes the text: the same ID (e.g. "stream-error") can carry
// different messages over time.
func (i *LineItem) CacheKey() string { return i.id + "|" + i.text }

func (i *LineItem) Render(ctx RenderContext) string {
	prefix := "  "
	if ctx.Selected {
		prefix = styles.BlockIndicatorSelectedStyle.Render(styles.BlockIndicatorChar) + " "
	}
	return prefix + i.style.Render(i.text)
}

// ---- InjectedFilesItem: one navigable entry for ALL injected files ----
// Opening it in $EDITOR shows the list of paths.

type InjectedFilesItem struct {
	paths []string
}

func NewInjectedFilesItem(paths []string) *InjectedFilesItem {
	return &InjectedFilesItem{paths: paths}
}

func (i *InjectedFilesItem) ID() string { return "injected-files" }

// CacheKey: paths are fixed for the lifetime of a session.
func (i *InjectedFilesItem) CacheKey() string { return i.ID() }

func (i *InjectedFilesItem) Content() (string, string) {
	return i.numberedList(), "txt"
}
func (i *InjectedFilesItem) numberedList() string {
	var b strings.Builder
	for index, path := range i.paths {
		if index > 0 {
			b.WriteString("\n")
		}
		b.WriteString(fmt.Sprintf("%d. %s", index+1, path))
	}
	return b.String()
}
func (i *InjectedFilesItem) Render(ctx RenderContext) string {
	// Rendered as a regular bordered block, like every other timeline item.
	style := styles.AIMessageStyle.BorderForeground(styles.FileColor)
	fileNameStyle := lipgloss.NewStyle().Foreground(styles.FileColor).Bold(true)

	var b strings.Builder
	b.WriteString(fileNameStyle.Render(fmt.Sprintf("📎 Injected Files (%d)", len(i.paths))))
	for index, path := range i.paths {
		b.WriteString("\n")
		// Dim number + directory, bold pink basename — the part that matters pops.
		directory, name := filepath.Split(path)
		b.WriteString(styles.DimTextStyle.Render(fmt.Sprintf("%2d. ", index+1)))
		b.WriteString(styles.FileStyle.Render(directory))
		b.WriteString(fileNameStyle.Render(name))
	}
	return frame(ctx, style, b.String())
}

// ---- Builder ----

// BuildChatItems converts chat messages (plus an optional in-flight streaming
// message) into timeline items. Every tool result is paired with its
// originating call so request/response render adjacently, in call order.
func BuildChatItems(messages []*sgptpb.Message, streamingMessage *aipb.Message, executingToolCallID string, requestRenderer RequestRenderer) []Item {
	toolCallIDToResult := map[string]*aipb.ToolResult{}
	for _, chatMessage := range messages {
		message := chatMessage.GetMessage()
		if message.GetRole() != aipb.Role_ROLE_TOOL {
			continue
		}
		for _, block := range message.GetBlocks() {
			if toolResult := block.GetToolResult(); toolResult != nil {
				toolCallIDToResult[toolResult.GetToolCallId()] = toolResult
			}
		}
	}

	var items []Item
	for messageIndex, chatMessage := range messages {
		items = appendMessageItems(items, chatMessage.GetMessage(), messageIndex, true, toolCallIDToResult, executingToolCallID, requestRenderer)
		if chatMessage.GetError() != nil {
			items = append(items, NewErrorItem(fmt.Sprintf("m%d-error", messageIndex), chatMessage.GetError().GetMessage()))
		}
	}
	if streamingMessage != nil {
		items = appendMessageItems(items, streamingMessage, len(messages), false, toolCallIDToResult, executingToolCallID, requestRenderer)
	}
	return items
}

func appendMessageItems(
	items []Item,
	message *aipb.Message,
	messageIndex int,
	finalized bool,
	toolCallIDToResult map[string]*aipb.ToolResult,
	executingToolCallID string,
	requestRenderer RequestRenderer,
) []Item {
	baseSeq := messageIndex * 1000
	switch message.GetRole() {
	case aipb.Role_ROLE_USER:
		// One rectangle per user message; fences navigable within it.
		var mdBlocks []markdown.Block
		for _, block := range message.GetBlocks() {
			if text := block.GetText(); text != "" {
				mdBlocks = append(mdBlocks, markdown.ParseBlocks(text)...)
			}
		}
		if len(mdBlocks) > 0 {
			items = append(items, &TextItem{
				id:        fmt.Sprintf("m%d-user", messageIndex),
				seq:       baseSeq,
				style:     styles.UserMessageStyle,
				blocks:    mdBlocks,
				finalized: finalized,
			})
		}

	case aipb.Role_ROLE_ASSISTANT:
		for blockIndex, block := range message.GetBlocks() {
			seq := baseSeq + blockIndex*20
			if thought := block.GetThought(); thought != "" {
				items = append(items, &ThoughtItem{
					id:        fmt.Sprintf("m%d-b%d-thought", messageIndex, blockIndex),
					seq:       seq,
					text:      thought,
					finalized: finalized,
				})
			} else if text := block.GetText(); text != "" {
				// One rectangle per API text block; fences navigable within it.
				items = append(items, &TextItem{
					id:        fmt.Sprintf("m%d-b%d-text", messageIndex, blockIndex),
					seq:       seq,
					style:     styles.AIMessageStyle,
					blocks:    markdown.ParseBlocks(text),
					finalized: finalized,
				})
			} else if toolCall := block.GetToolCall(); toolCall != nil {
				result := toolCall.GetResult()
				if result == nil {
					result = toolCallIDToResult[toolCall.GetId()]
				}
				items = append(items, &ToolCallItem{
					id:              fmt.Sprintf("m%d-b%d-tool", messageIndex, blockIndex),
					seq:             seq,
					ToolCall:        toolCall,
					Result:          result,
					Executing:       executingToolCallID != "" && toolCall.GetId() == executingToolCallID,
					RequestRenderer: requestRenderer,
				})
			}
		}

	case aipb.Role_ROLE_TOOL:
		// Results are rendered inline with their calls — nothing to add.

	case aipb.Role_ROLE_SYSTEM:
		for _, block := range message.GetBlocks() {
			if text := block.GetText(); text != "" {
				items = append(items, NewSystemItem(fmt.Sprintf("m%d-system", messageIndex), text))
				break
			}
		}
	}
	return items
}

// ConversationText renders the entire chat as plain markdown — used by
// alt+shift+o to open the full conversation in $EDITOR.
func ConversationText(messages []*sgptpb.Message) string {
	var b strings.Builder
	for _, chatMessage := range messages {
		message := chatMessage.GetMessage()
		if message == nil {
			continue
		}
		switch message.GetRole() {
		case aipb.Role_ROLE_USER:
			b.WriteString("## User\n\n")
			for _, block := range message.GetBlocks() {
				if text := block.GetText(); text != "" {
					b.WriteString(text)
					b.WriteString("\n\n")
				}
			}
		case aipb.Role_ROLE_ASSISTANT:
			b.WriteString("## Assistant\n\n")
			for _, block := range message.GetBlocks() {
				if thought := block.GetThought(); thought != "" {
					b.WriteString("*Thinking:* ")
					b.WriteString(thought)
					b.WriteString("\n\n")
				}
				if text := block.GetText(); text != "" {
					b.WriteString(text)
					b.WriteString("\n\n")
				}
				if toolCall := block.GetToolCall(); toolCall != nil {
					bytes, _ := pbutil.JSONMarshalPretty(toolCall.GetArguments())
					b.WriteString(fmt.Sprintf("Tool Call: %s\n```json\n%s\n```\n\n", toolCall.GetName(), string(bytes)))
				}
			}
		case aipb.Role_ROLE_TOOL:
			b.WriteString("## Tool Result\n\n")
			for _, block := range message.GetBlocks() {
				if toolResult := block.GetToolResult(); toolResult != nil {
					b.WriteString(toolResultText(toolResult))
					b.WriteString("\n\n")
				}
			}
		case aipb.Role_ROLE_SYSTEM:
			b.WriteString("## System\n\n")
			for _, block := range message.GetBlocks() {
				if text := block.GetText(); text != "" {
					b.WriteString(text)
					b.WriteString("\n\n")
				}
			}
		}
	}
	return b.String()
}
