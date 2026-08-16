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
	// messageName is the resource name of the message this item renders.
	messageName string
}

func (i *TextItem) ID() string { return i.id }

func (i *TextItem) MessageName() string { return i.messageName }

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
	// messageName is the resource name of the message this item renders.
	messageName string
}

func (i *ThoughtItem) ID() string                { return i.id }
func (i *ThoughtItem) Content() (string, string) { return i.text, "md" }
func (i *ThoughtItem) MessageName() string       { return i.messageName }

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

// ResultRenderer lets a tool dictate how its result renders in the
// timeline; unset (or declining) falls back to raw JSON.
type ResultRenderer interface {
	RenderResult(toolCall *aipb.ToolCall, toolResult *aipb.ToolResult) (string, bool)
}

// ---- ToolCallItem: request/response pair rendered adjacently ----

type ToolCallItem struct {
	id        string
	seq       int
	ToolCall  *aipb.ToolCall
	Result    *aipb.ToolResult
	Executing bool
	// Pending marks a call awaiting the user's verdict: the turn goroutine is
	// blocked on it right now.
	Pending bool
	// Partial marks a call still streaming in; arguments may be incomplete.
	Partial bool
	// RequestRenderer, when set, overrides the raw-JSON request rendering.
	RequestRenderer RequestRenderer
	// lastGoodRequests persists the last successful tool-dictated render,
	// keyed by tool call ID — streaming items are reconstructed every tick,
	// so this state must live outside the item (owned by the Builder).
	lastGoodRequests map[string]string
	// messageName is the resource name of the assistant message this call
	// belongs to.
	messageName string
}

func (i *ToolCallItem) ID() string { return i.id }

func (i *ToolCallItem) MessageName() string { return i.messageName }

// CacheKey: result attachment, execution and review status all change the render.
func (i *ToolCallItem) CacheKey() string {
	// Partial calls mutate every tick — opt out of render caching.
	if i.Partial {
		return ""
	}
	return fmt.Sprintf("%s|r%t|e%t|p%t", i.id, i.Result != nil, i.Executing, i.Pending)
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
	case i.Pending:
		suffix = " " + styles.ErrorStyle.Render("▶ pending review")
	}
	header := fmt.Sprintf("%s %s%s",
		i.statusIndicator(),
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
	// Tools may dictate their own result presentation (e.g. search_lores
	// renders matches with highlights).
	if renderer, ok := i.RequestRenderer.(ResultRenderer); ok {
		if md, ok := renderer.RenderResult(i.ToolCall, i.Result); ok {
			return label + "\n" + renderMarkdown(ctx, i.seq+1, true, markdown.ParseBlocks(md)...)
		}
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

// statusIndicator colours the leading dot: a result is terminal (green, or red
// when it carries an error), anything else is still in flight.
func (i *ToolCallItem) statusIndicator() string {
	color := styles.MutedColor
	switch {
	case i.Result.GetError() != nil:
		color = styles.ErrorColor
	case i.Result != nil:
		color = styles.SuccessColor
	}
	return lipgloss.NewStyle().Foreground(color).Render("●")
}

// ---- LineItem: single-line entries (system, errors) ----

type LineItem struct {
	id    string
	text  string
	style lipgloss.Style
	// summary, when set, makes the item collapsible: it folds to this single
	// line by default (system prompts are long and rarely re-read).
	summary string
}

func NewErrorItem(id, text string) *LineItem {
	return &LineItem{id: id, text: "Error: " + text, style: styles.MessageErrorStyle}
}

// NewQueuedItem renders a user interjection waiting to join the next
// generation (typed while a turn was running).
func NewQueuedItem(id, text string) *LineItem {
	return &LineItem{id: id, text: "⏳ Queued: " + flatten(text), style: styles.DimTextStyle}
}

// NewCancelledItem marks a user-cancelled turn — an interruption, not a
// failure: the turn's inputs are re-queued and resume on the next send.
func NewCancelledItem(id string) *LineItem {
	return &LineItem{id: id, text: "Turn cancelled — your messages are queued and resume on the next send", style: styles.DimTextStyle}
}

func (i *LineItem) ID() string                { return i.id }
func (i *LineItem) Content() (string, string) { return i.text, "" }

// CacheKey includes the text: the same ID (e.g. "stream-error") can carry
// different messages over time.
func (i *LineItem) CacheKey() string { return i.id + "|" + i.text }

// DefaultCollapsed folds summarizable items (system prompts) out of the way;
// items without a summary aren't collapsible at all.
func (i *LineItem) DefaultCollapsed() bool { return i.summary != "" }

func (i *LineItem) Render(ctx RenderContext) string {
	prefix := "  "
	if ctx.Selected {
		prefix = styles.BlockIndicatorSelectedStyle.Render(styles.BlockIndicatorChar) + " "
	}
	text := i.text
	if ctx.Collapsed && i.summary != "" {
		text = i.summary
	}
	return prefix + i.style.Render(text)
}

// flatten collapses whitespace so a multi-line prompt fits on one line.
func flatten(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// ---- SystemItem: the system prompt, as a regular bordered block ----

type SystemItem struct {
	id   string
	seq  int
	text string
	// messageName is the resource name of the message this item renders.
	messageName string
}

func NewSystemItem(id string, seq int, text, messageName string) *SystemItem {
	return &SystemItem{id: id, seq: seq, text: text, messageName: messageName}
}

func (i *SystemItem) ID() string                { return i.id }
func (i *SystemItem) Content() (string, string) { return i.text, "md" }
func (i *SystemItem) MessageName() string       { return i.messageName }

// CacheKey includes the text: a chat's system prompt can change between runs.
func (i *SystemItem) CacheKey() string { return i.id + "|" + i.text }

// The prompt is long and rarely re-read: fold it away by default.
func (i *SystemItem) DefaultCollapsed() bool { return true }

func (i *SystemItem) Render(ctx RenderContext) string {
	// Grey border keeps it visually subordinate to user/assistant messages.
	style := styles.AIMessageStyle.BorderForeground(styles.BorderColor)
	label := styles.SystemStyle.Render("⚙ system prompt")
	if ctx.Collapsed {
		summary := fmt.Sprintf("%s %s", label,
			styles.DimTextStyle.Render(styles.Truncate(flatten(i.text), styles.TruncateLength)))
		return frame(ctx, style, summary)
	}
	body := renderMarkdown(ctx, i.seq, true, markdown.ParseBlocks(i.text)...)
	return frame(ctx, style, label+"\n"+body)
}

// ---- InjectedFileItem: an injected-file message, rendered in-place ----
// An ordinary timeline entry (position = injection order); only the
// rendering differs: file-colored border, filename instead of content.

type InjectedFileItem struct {
	id      string
	path    string
	content string
	// paths/contents hold every file of a consecutive injection run; path and
	// content are the first one (kept for the single-file case).
	paths    []string
	contents []string
	// messageNames parallels paths: each injected file is its own message, so
	// a grouped item owns several.
	messageNames []string
}

func NewInjectedFileItem(id, path, content, messageName string) *InjectedFileItem {
	return &InjectedFileItem{
		id:           id,
		path:         path,
		content:      content,
		paths:        []string{path},
		contents:     []string{content},
		messageNames: []string{messageName},
	}
}

// add folds a consecutively injected file into this group so a large
// injection costs a few lines of vertical space instead of one box each.
func (i *InjectedFileItem) add(path, content, messageName string) {
	i.paths = append(i.paths, path)
	i.contents = append(i.contents, content)
	i.messageNames = append(i.messageNames, messageName)
}

func (i *InjectedFileItem) ID() string { return i.id }

// MessageName returns the first file's message: a group has no single owner,
// so deleting the item targets the file the cursor landed on (see
// MessageNameAt).
func (i *InjectedFileItem) MessageName() string {
	if len(i.messageNames) == 0 {
		return ""
	}
	return i.messageNames[0]
}

// MessageNameAt returns the message of the file at the given sub-index, so
// alt+d removes exactly the file the cursor is on inside a grouped item.
func (i *InjectedFileItem) MessageNameAt(index int) string {
	if index < 0 || index >= len(i.messageNames) {
		return i.MessageName()
	}
	return i.messageNames[index]
}

// CacheKey: injected-file messages are immutable, but the group grows as
// consecutive files are folded in.
func (i *InjectedFileItem) CacheKey() string {
	return fmt.Sprintf("%s|%d|%s", i.id, len(i.paths), strings.Join(i.paths, ","))
}

// Content exposes the injected content for copy / open-in-editor; a group
// concatenates its files.
func (i *InjectedFileItem) Content() (string, string) {
	if len(i.paths) > 1 {
		return strings.Join(i.contents, "\n\n"), "md"
	}
	return i.content, strings.TrimPrefix(filepath.Ext(i.path), ".")
}

// Groups are navigable file-by-file (alt+[ / alt+]).
func (i *InjectedFileItem) SubCount() int { return len(i.paths) }

func (i *InjectedFileItem) SubContent(index int) (string, string) {
	if index < 0 || index >= len(i.paths) {
		return i.Content()
	}
	return i.contents[index], strings.TrimPrefix(filepath.Ext(i.paths[index]), ".")
}

// A group of files collapses to a one-line count; a lone file is already one line.
func (i *InjectedFileItem) DefaultCollapsed() bool { return len(i.paths) > 1 }

func (i *InjectedFileItem) Render(ctx RenderContext) string {
	style := styles.AIMessageStyle.BorderForeground(styles.FileColor)
	fileNameStyle := lipgloss.NewStyle().Foreground(styles.FileColor).Bold(true)
	if ctx.Collapsed && len(i.paths) > 1 {
		summary := fileNameStyle.Render(fmt.Sprintf("📎 %d files", len(i.paths))) + " " +
			styles.DimTextStyle.Render(styles.Truncate(strings.Join(basenames(i.paths), ", "), styles.TruncateLength))
		return frame(ctx, style, summary)
	}
	var b strings.Builder
	for index, path := range i.paths {
		if index > 0 {
			b.WriteString("\n")
		}
		// Dim directory, bold basename — the part that matters pops.
		directory, name := filepath.Split(path)
		prefix := "📎 "
		if ctx.Selected && ctx.SelectedSub >= 0 && len(i.paths) > 1 {
			indicatorStyle := styles.BlockIndicatorStyle
			if index == ctx.SelectedSub {
				indicatorStyle = styles.BlockIndicatorSelectedStyle
			}
			prefix = indicatorStyle.Render(styles.BlockIndicatorChar) + " 📎 "
		}
		b.WriteString(fileNameStyle.Render(prefix) + styles.FileStyle.Render(directory) + fileNameStyle.Render(name))
	}
	return frame(ctx, style, b.String())
}

func basenames(paths []string) []string {
	names := make([]string, 0, len(paths))
	for _, path := range paths {
		names = append(names, filepath.Base(path))
	}
	return names
}

func (i *InjectedFileItem) SubOffsets(ctx RenderContext) []int {
	offsets := make([]int, len(i.paths))
	for index := range i.paths {
		offsets[index] = index + 1 // account for the frame's top border
	}
	return offsets
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
func (b *Builder) Build(
	messages []*aipb.Message,
	streamingMessage *aipb.Message,
	executingToolCallID string,
	pendingToolCallIDs map[string]bool,
	requestRenderer RequestRenderer,
) []Item {
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
			messageItems = appendMessageItems(messageItems, message, messageIndex, true, b.toolCallIDToResult, requestRenderer, b.toolCallIDToLastGoodRequest)
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
			toolCallID := toolCallItem.ToolCall.GetId()
			toolCallItem.Executing = executingToolCallID != "" && toolCallID == executingToolCallID
			toolCallItem.Pending = pendingToolCallIDs[toolCallID]
		}
		items = append(items, entry.items...)
	}
	if streamingMessage != nil {
		// Still mutating — never cached; finalization lands it in messages.
		items = appendMessageItems(items, streamingMessage, len(messages), false, b.toolCallIDToResult, requestRenderer, b.toolCallIDToLastGoodRequest)
	}
	return groupInjectedFiles(items)
}

// groupInjectedFiles folds runs of consecutive injected-file items into a
// single collapsible group: injecting 30 files must not cost 30 boxes of
// vertical space. Grouping happens here (not in appendMessageItems) because
// each file is its own message and items are cached per message.
func groupInjectedFiles(items []Item) []Item {
	grouped := make([]Item, 0, len(items))
	var current *InjectedFileItem
	for _, item := range items {
		fileItem, ok := item.(*InjectedFileItem)
		if !ok {
			current = nil
			grouped = append(grouped, item)
			continue
		}
		if current != nil {
			current.add(fileItem.path, fileItem.content, fileItem.MessageName())
			continue
		}
		// Copy: the cached per-message item must not accumulate siblings.
		current = NewInjectedFileItem(fileItem.id, fileItem.path, fileItem.content, fileItem.MessageName())
		grouped = append(grouped, current)
	}
	return grouped
}

// BuildChatItems is the uncached one-shot variant — used by read-only
// previews (menu detail pane). Stateful callers should hold a Builder.
func BuildChatItems(messages []*aipb.Message, requestRenderer RequestRenderer) []Item {
	return NewBuilder().Build(messages, nil, "", nil, requestRenderer)
}

func appendMessageItems(
	items []Item,
	message *aipb.Message,
	messageIndex int,
	finalized bool,
	toolCallIDToResult map[string]*aipb.ToolResult,
	requestRenderer RequestRenderer,
	toolCallIDToLastGoodRequest map[string]string,
) []Item {
	baseSeq := messageIndex * 1000
	messageName := message.GetName()
	switch message.GetRole() {
	case aipb.Role_ROLE_USER:
		// Injected files render in-place (injection order), label-detected:
		// same message as any other, only the presentation differs.
		if path := store.InjectedFilePath(message); path != "" {
			var content string
			for _, block := range message.GetBlocks() {
				if text := block.GetText(); text != "" {
					content = text
					break
				}
			}
			items = append(items, NewInjectedFileItem(fmt.Sprintf("m%d-file", messageIndex), path, content, messageName))
			return items
		}
		// One rectangle per user message; fences navigable within it.
		var mdBlocks []markdown.Block
		for _, block := range message.GetBlocks() {
			if text := block.GetText(); text != "" {
				mdBlocks = append(mdBlocks, markdown.ParseBlocks(text)...)
			}
		}
		if len(mdBlocks) > 0 {
			items = append(items, &TextItem{
				id:          fmt.Sprintf("m%d-user", messageIndex),
				seq:         baseSeq,
				style:       styles.UserMessageStyle,
				blocks:      mdBlocks,
				finalized:   finalized,
				messageName: messageName,
			})
		}

	case aipb.Role_ROLE_ASSISTANT:
		for blockIndex, block := range message.GetBlocks() {
			seq := baseSeq + blockIndex*20
			if thought := block.GetThought(); thought != "" {
				items = append(items, &ThoughtItem{
					id:          fmt.Sprintf("m%d-b%d-thought", messageIndex, blockIndex),
					seq:         seq,
					text:        thought,
					finalized:   finalized,
					messageName: messageName,
				})
			} else if text := block.GetText(); text != "" {
				// One rectangle per API text block; fences navigable within it.
				items = append(items, &TextItem{
					id:          fmt.Sprintf("m%d-b%d-text", messageIndex, blockIndex),
					seq:         seq,
					style:       styles.AIMessageStyle,
					blocks:      markdown.ParseBlocks(text),
					finalized:   finalized,
					messageName: messageName,
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
					RequestRenderer:  requestRenderer,
					lastGoodRequests: toolCallIDToLastGoodRequest,
					messageName:      messageName,
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
					messageName:      messageName,
				})
			}
		}

	case aipb.Role_ROLE_TOOL:
		// Results are rendered inline with their calls — nothing to add.

	case aipb.Role_ROLE_SYSTEM:
		for _, block := range message.GetBlocks() {
			if text := block.GetText(); text != "" {
				items = append(items, NewSystemItem(fmt.Sprintf("m%d-system", messageIndex), baseSeq, text, messageName))
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
