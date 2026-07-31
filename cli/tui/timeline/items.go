package timeline

import (
	"fmt"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/pbutil"

	"github.com/malonaz/sgpt/cli/tui/styles"
	"github.com/malonaz/sgpt/internal/markdown"
	"github.com/malonaz/sgpt/internal/store"
	"github.com/malonaz/sgpt/internal/tool"
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
			fmt.Sprintf("🧠 thought (%d lines)", strings.Count(i.text, "\n")+1))
		return frame(ctx, styles.AIThoughtStyle, summary)
	}
	return frame(ctx, styles.AIThoughtStyle, renderMarkdown(ctx, i.seq, i.finalized, markdown.ParseBlocks(i.text)...))
}

// RequestRenderer lets a tool dictate how its request renders in the
// timeline; unset (or declining) falls back to raw JSON arguments.
type RequestRenderer interface {
	RenderRequest(toolCall *aipb.ToolCall) (string, bool)
}

// HeaderRenderer lets a tool render its call's header as arbitrary markdown
// (e.g. ✏️ `file.go`); unset (or declining) falls back to the tool's name.
type HeaderRenderer interface {
	RenderHeader(toolCall *aipb.ToolCall) (string, bool)
}

// ---- ToolCallItem: request/response pair rendered adjacently ----

type ToolCallItem struct {
	id        string
	seq       int
	ToolCall  *aipb.ToolCall
	Result    *aipb.ToolResult
	Executing bool
	// Partial marks a call still streaming in; arguments may be incomplete.
	Partial bool
	// RequestRenderer, when set, overrides the raw-JSON request rendering.
	RequestRenderer RequestRenderer
	// lastGoodRequests persists the last successful tool-dictated render,
	// keyed by tool call ID — streaming items are reconstructed every tick,
	// so this state must live outside the item (owned by the Builder).
	lastGoodRequests map[string]string
}

func (i *ToolCallItem) ID() string { return i.id }

// CacheKey: result attachment, execution and review status all change the render.
func (i *ToolCallItem) CacheKey() string {
	// Partial calls mutate every tick — opt out of render caching.
	if i.Partial {
		return ""
	}
	return fmt.Sprintf("%s|r%t|e%t|s%v", i.id, i.Result != nil, i.Executing, tool.GetToolCallStatus(i.ToolCall))
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
	case i.Partial:
		suffix = " " + styles.ThoughtLabelStyle.Render("⏳ streaming...")
	case i.Executing:
		suffix = " " + styles.ThoughtLabelStyle.Render("⏳ running...")
	case i.Result == nil && tool.GetToolCallStatus(i.ToolCall) == tool.ToolCallStatusPending:
		suffix = " " + styles.ErrorStyle.Render("▶ pending review")
	// Verdicts stay editable until the turn resolves — say so.
	case i.Result == nil && tool.GetToolCallStatus(i.ToolCall) == tool.ToolCallStatusAccepted:
		suffix = " " + styles.DimTextStyle.Render("✓ accepted — runs once review completes")
	case i.Result == nil && tool.GetToolCallStatus(i.ToolCall) == tool.ToolCallStatusRejected:
		suffix = " " + styles.DimTextStyle.Render("✗ rejected")
	}
	header := fmt.Sprintf("%s %s%s",
		toolCallStatusIndicator(i.ToolCall),
		i.headerContent(ctx),
		suffix,
	)
	metadata, _ := tool.ParseToolCallMetadata(i.ToolCall)
	displayMessage := metadata.GetDisplayMessage().GetContent()
	if displayMessage == "" {
		return header
	}
	if ctx.Collapsed {
		// Collapsed items must stay a single line: flatten the display
		// message and fold it inline, truncated, instead of a second line.
		flattened := strings.Join(strings.Fields(displayMessage), " ")
		return header + " " + styles.DimTextStyle.Render(styles.Truncate(flattened, 80))
	}
	return header + "\n" + styles.DimTextStyle.Render(displayMessage)
}

// headerContent lets the tool render its header as markdown (one line, so
// code spans like `file.go` get the nice inline-code treatment);
// RequestRenderer is the registry, which routes to the tool implementation.
// Falls back to the styled tool name.
func (i *ToolCallItem) headerContent(ctx RenderContext) string {
	if renderer, ok := i.RequestRenderer.(HeaderRenderer); ok {
		if md, ok := renderer.RenderHeader(i.ToolCall); ok {
			// seq+2: request uses seq, response seq+1.
			return strings.TrimSpace(renderMarkdown(ctx, i.seq+2, !i.Partial, markdown.ParseBlocks(md)...))
		}
	}
	return styles.ToolLabelStyle.Render("🛠 " + i.ToolCall.GetName())
}

func (i *ToolCallItem) request(ctx RenderContext) string {
	// Tools may dictate their own presentation (e.g. the diff tool's diff).
	if i.RequestRenderer != nil {
		if md, ok := i.RequestRenderer.RenderRequest(i.ToolCall); ok {
			if i.lastGoodRequests != nil {
				i.lastGoodRequests[i.ToolCall.GetId()] = md
			}
			return renderMarkdown(ctx, i.seq, !i.Partial, markdown.ParseBlocks(md)...)
		}
		// Partial arguments may be momentarily unparsable by the tool's
		// renderer; fall back to the last render that succeeded.
		if md, ok := i.lastGoodRequests[i.ToolCall.GetId()]; ok {
			return renderMarkdown(ctx, i.seq, false, markdown.ParseBlocks(md)...)
		}
	}
	// Full payload — inspection during review was the whole point.
	bytes, _ := pbutil.JSONMarshalPretty(i.ToolCall.GetArguments())
	fenced := fmt.Sprintf("```json\n%s\n```", string(bytes))
	return renderMarkdown(ctx, i.seq, !i.Partial, markdown.ParseBlocks(fenced)...)
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
	switch tool.GetToolCallStatus(toolCall) {
	case tool.ToolCallStatusAccepted:
		return lipgloss.NewStyle().Foreground(styles.SuccessColor).Render("●")
	case tool.ToolCallStatusRejected:
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

// Builder memoizes per-message item construction so a streaming refresh costs
// O(streaming message) instead of re-parsing the entire history every tick.
type Builder struct {
	messageIndexToEntry map[int]builderEntry
	toolCallIDToResult  map[string]*aipb.ToolResult
	// toolCallIDToLastGoodRequest survives item reconstruction so partial
	// tool call renders can fall back to the last successful one.
	toolCallIDToLastGoodRequest map[string]string
	scannedMessageCount         int
}

type builderEntry struct {
	// message is compared by pointer: saves replace the chat's protos, and
	// cached items would otherwise keep referencing (and mutating) stale copies.
	message *aipb.Message
	items   []Item
}

func NewBuilder() *Builder {
	return &Builder{
		messageIndexToEntry:         map[int]builderEntry{},
		toolCallIDToResult:          map[string]*aipb.ToolResult{},
		toolCallIDToLastGoodRequest: map[string]string{},
	}
}

// Build converts chat messages (plus an optional in-flight streaming message)
// into timeline items, reusing cached items for unchanged messages. Every tool
// result is paired with its originating call so request/response render
// adjacently, in call order.
func (b *Builder) Build(messages []*aipb.Message, streamingMessage *aipb.Message, executingToolCallID string, requestRenderer RequestRenderer) []Item {
	if b.scannedMessageCount > len(messages) {
		// History shrank (chat replaced) — every cache is invalid.
		b.messageIndexToEntry = map[int]builderEntry{}
		b.toolCallIDToResult = map[string]*aipb.ToolResult{}
		b.toolCallIDToLastGoodRequest = map[string]string{}
		b.scannedMessageCount = 0
	}
	// Messages are append-only; scan only the unseen tail for tool results.
	for _, message := range messages[b.scannedMessageCount:] {
		if message.GetRole() != aipb.Role_ROLE_TOOL {
			continue
		}
		for _, block := range message.GetBlocks() {
			if toolResult := block.GetToolResult(); toolResult != nil {
				b.toolCallIDToResult[toolResult.GetToolCallId()] = toolResult
			}
		}
	}
	b.scannedMessageCount = len(messages)

	var items []Item
	for messageIndex, message := range messages {
		entry, ok := b.messageIndexToEntry[messageIndex]
		if !ok || entry.message != message {
			var messageItems []Item
			messageItems = appendMessageItems(messageItems, message, messageIndex, true, b.toolCallIDToResult, executingToolCallID, requestRenderer, b.toolCallIDToLastGoodRequest)
			if errText := store.MessageError(message); errText != "" {
				messageItems = append(messageItems, NewErrorItem(fmt.Sprintf("m%d-error", messageIndex), errText))
			}
			entry = builderEntry{message: message, items: messageItems}
			b.messageIndexToEntry[messageIndex] = entry
		}
		// Results and execution state land after an item is first built; patch
		// the cached items instead of re-parsing the whole message.
		for _, item := range entry.items {
			toolCallItem, ok := item.(*ToolCallItem)
			if !ok {
				continue
			}
			if toolCallItem.Result == nil {
				if result := toolCallItem.ToolCall.GetResult(); result != nil {
					toolCallItem.Result = result
				} else {
					toolCallItem.Result = b.toolCallIDToResult[toolCallItem.ToolCall.GetId()]
				}
			}
			toolCallItem.Executing = executingToolCallID != "" && toolCallItem.ToolCall.GetId() == executingToolCallID
		}
		items = append(items, entry.items...)
	}
	if streamingMessage != nil {
		// Still mutating — never cached; finalization lands it in messages.
		items = appendMessageItems(items, streamingMessage, len(messages), false, b.toolCallIDToResult, executingToolCallID, requestRenderer, b.toolCallIDToLastGoodRequest)
	}
	return items
}

// BuildChatItems is the uncached one-shot variant — used by read-only
// previews (menu detail pane). Stateful callers should hold a Builder.
func BuildChatItems(messages []*aipb.Message, streamingMessage *aipb.Message, executingToolCallID string, requestRenderer RequestRenderer) []Item {
	return NewBuilder().Build(messages, streamingMessage, executingToolCallID, requestRenderer)
}

func appendMessageItems(
	items []Item,
	message *aipb.Message,
	messageIndex int,
	finalized bool,
	toolCallIDToResult map[string]*aipb.ToolResult,
	executingToolCallID string,
	requestRenderer RequestRenderer,
	toolCallIDToLastGoodRequest map[string]string,
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
					id:               fmt.Sprintf("m%d-b%d-tool", messageIndex, blockIndex),
					seq:              seq,
					ToolCall:         toolCall,
					Result:           result,
					Executing:        executingToolCallID != "" && toolCall.GetId() == executingToolCallID,
					RequestRenderer:  requestRenderer,
					lastGoodRequests: toolCallIDToLastGoodRequest,
				})
			} else if partialToolCall := block.GetPartialToolCall(); partialToolCall != nil {
				// Same ID as the completed call so collapse/selection state
				// carries over when the partial upgrades to a full call.
				items = append(items, &ToolCallItem{
					id:               fmt.Sprintf("m%d-b%d-tool", messageIndex, blockIndex),
					seq:              seq,
					ToolCall:         partialToolCall,
					Partial:          true,
					RequestRenderer:  requestRenderer,
					lastGoodRequests: toolCallIDToLastGoodRequest,
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
func ConversationText(messages []*aipb.Message) string {
	var b strings.Builder
	for _, message := range messages {
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
