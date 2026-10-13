package timeline

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	aipb "github.com/malonaz/core/genproto/ai/v1"

	"github.com/malonaz/sgpt/cli/tui/styles"
	"github.com/malonaz/sgpt/internal/markdown"
	"github.com/malonaz/sgpt/internal/store"
	"github.com/malonaz/sgpt/internal/tool"
)

func renderMarkdown(ctx RenderContext, seq int, finalized bool, blocks ...markdown.Block) string {
	if ctx.Static {
		// Sentinel index disables the renderer's cache for static previews.
		seq, finalized = -1, false
	}
	return ctx.Renderer.ToMarkdown(seq, finalized, blocks...)
}

// ---- TextItem: one API block, containing individually navigable fences ----

// voice is who a text item speaks for; it picks the marker rule.
type voice int

const (
	voiceAssistant voice = iota
	voiceUser
	voiceAgent
)

var (
	userPrompt  = lipgloss.NewStyle().Foreground(styles.ColorBrand).Bold(true).Render(styles.GlyphPrompt) + " "
	agentPrompt = lipgloss.NewStyle().Foreground(styles.ColorAgent).Bold(true).Render(styles.GlyphPrompt) + " "
	agentLabel  = lipgloss.NewStyle().Foreground(styles.ColorAgent).Bold(true)
)

type TextItem struct {
	id        string
	seq       int
	voice     voice
	blocks    []markdown.Block
	finalized bool
	// messageName is the resource name of the message this item renders.
	messageName string
	// sender, when set, heads a message another agent sent; note is its
	// bracketed first line, addressed to the model (operation and outcome).
	sender string
	note   string
	// created is when the message was persisted; zero while streaming.
	created time.Time
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

// prompt is the turn's opening glyph; assistant prose has none.
func (i *TextItem) prompt() string {
	switch i.voice {
	case voiceUser:
		return userPrompt
	case voiceAgent:
		return agentPrompt
	}
	return blankMarker
}

func (i *TextItem) SubSpans(ctx RenderContext) []span {
	spans := make([]span, len(i.blocks))
	line := len(i.headerLines(ctx))
	if i.isTurn() {
		line++ // the band's top padding
	}
	for j, block := range i.blocks {
		if j > 0 {
			line++ // the blank line between blocks
		}
		start := line
		if j == 0 {
			start = 0 // the header belongs to the first block
		}
		line += strings.Count(renderMarkdown(ctx, i.seq+j, i.blockFinalized(j), block), "\n") + 1
		spans[j] = span{start, line}
	}
	return spans
}

// Render: a turn (user or agent) is a padded band opened by a prompt glyph;
// assistant prose is plain.
func (i *TextItem) Render(ctx RenderContext) string {
	var lines []string
	write := func(sub int, text string) {
		for _, line := range strings.Split(text, "\n") {
			marker := blankMarker
			if len(lines) == 0 {
				marker = i.prompt()
			}
			lines = append(lines, i.line(ctx, sub, marker+line))
		}
	}
	for _, line := range i.headerLines(ctx) {
		write(0, line)
	}
	for j, block := range i.blocks {
		if j > 0 {
			// Index -1 lights the separator only when the whole item is.
			write(-1, "")
		}
		write(j, renderMarkdown(ctx, i.seq+j, i.blockFinalized(j), block))
	}
	if i.isTurn() {
		// Padding makes the turn read as a block, not a stripe.
		padding := i.line(ctx, -1, "")
		lines = append(append([]string{padding}, lines...), padding)
	}
	return strings.Join(lines, "\n")
}

func (i *TextItem) isTurn() bool { return i.voice != voiceAssistant }

// line gutters a line and bands it when the item is a turn. A selected turn
// keeps its band: the gutter bar marks the selection, so navigating never
// swaps the turn's background.
func (i *TextItem) line(ctx RenderContext, sub int, line string) string {
	line = subGutter(ctx, sub) + line
	if !i.isTurn() {
		return line
	}
	surface := styles.ColorSurface
	if i.voice == voiceUser {
		surface = styles.ColorSurfaceTurn
	}
	return band(line, ctx.bandWidth(), surface)
}

// headerLines name an agent message's sender, then its muted note.
func (i *TextItem) headerLines(ctx RenderContext) []string {
	if i.sender == "" {
		return nil
	}
	lines := []string{agentLabel.Render(styles.GlyphChild + " " + i.sender)}
	if i.note != "" {
		width := min(ctx.bodyWidth(), markdown.MaxProseWidth)
		for _, line := range strings.Split(lipgloss.Wrap(i.note, width, ""), "\n") {
			lines = append(lines, styles.MutedStyle.Render(line))
		}
	}
	return lines
}

// ---- ThoughtItem: collapsible reasoning ----

var thoughtStyle = styles.MutedStyle.Italic(true)

type ThoughtItem struct {
	id        string
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

// A folded thought is one row, so it sits flush in a run of tool rows.
func (i *ThoughtItem) Compact(ctx RenderContext) bool { return ctx.Collapsed }

func (i *ThoughtItem) Render(ctx RenderContext) string {
	label := "Thinking"
	if i.finalized {
		label = "Thought"
	}
	header := labelRow(ctx, "∴", styles.MutedStyle, label, plural(strings.Count(i.text, "\n")+1, "line"))
	if ctx.Collapsed {
		return header
	}
	// Plain wrapped text, not markdown: reasoning is muted background reading.
	var b strings.Builder
	b.WriteString(header)
	wrapped := lipgloss.Wrap(strings.TrimSpace(i.text), min(ctx.bodyWidth(), markdown.MaxProseWidth), "")
	for _, line := range strings.Split(wrapped, "\n") {
		prefixLines(&b, gutter(ctx)+blankMarker, thoughtStyle.Render(line))
	}
	return b.String()
}

// ---- LineItem: single-line entries (errors, queued, cancelled) ----

type LineItem struct {
	id     string
	glyph  string
	style  lipgloss.Style
	label  string
	detail string
	// wrap shows the label in full across lines instead of truncating it.
	wrap bool
}

func NewErrorItem(id, text string) *LineItem {
	return &LineItem{
		id:    id,
		glyph: styles.GlyphFailure,
		style: lipgloss.NewStyle().Foreground(styles.ColorDanger),
		label: strings.TrimSpace(text),
		wrap:  true,
	}
}

// NewQueuedItem renders a message waiting to join the next generation (typed
// or delivered while a turn was running), naming its sender when another
// agent sent it.
func NewQueuedItem(id string, message *aipb.Message) *LineItem {
	label := "Queued"
	if sender := store.MessageSender(message); sender != "" {
		label = "Queued " + styles.GlyphChild + " " + sender
	}
	var texts []string
	for _, block := range message.GetBlocks() {
		if text := block.GetText(); text != "" {
			texts = append(texts, text)
		}
	}
	return &LineItem{id: id, glyph: styles.GlyphQueued, style: styles.MutedStyle, label: label, detail: flatten(strings.Join(texts, " "))}
}

// NewCancelledItem marks a user-cancelled turn — an interruption, not a
// failure: the turn's inputs are re-queued and resume on the next send.
func NewCancelledItem(id string) *LineItem {
	return &LineItem{
		id:     id,
		glyph:  styles.GlyphCancelled,
		style:  styles.MutedStyle,
		label:  "Turn cancelled",
		detail: "your messages are queued and resume on the next send",
	}
}

func (i *LineItem) ID() string { return i.id }

func (i *LineItem) Content() (string, string) {
	if i.detail == "" {
		return i.label, ""
	}
	return i.label + ": " + i.detail, ""
}

// CacheKey includes the text: the same ID (e.g. "stream-error") can carry
// different messages over time.
func (i *LineItem) CacheKey() string { return i.id + "|" + i.label + "|" + i.detail }

func (i *LineItem) Render(ctx RenderContext) string {
	if !i.wrap {
		return labelRow(ctx, i.glyph, i.style, i.label, i.detail)
	}
	var b strings.Builder
	wrapped := lipgloss.Wrap(i.label, min(ctx.bodyWidth(), markdown.MaxProseWidth), "")
	for index, line := range strings.Split(wrapped, "\n") {
		marker := blankMarker
		if index == 0 {
			marker = markerStyle(ctx, i.style).Render(i.glyph) + " "
		}
		prefixLines(&b, gutter(ctx)+marker, i.style.Render(line))
	}
	return b.String()
}

// ---- SystemItem: the system prompt ----

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
	if ctx.Collapsed {
		return labelRow(ctx, " ", styles.MutedStyle, "System prompt", flatten(i.text))
	}
	var b strings.Builder
	b.WriteString(labelRow(ctx, " ", styles.MutedStyle, "System prompt", ""))
	prefixLines(&b, gutter(ctx)+blankMarker, renderMarkdown(ctx, i.seq, true, markdown.ParseBlocks(i.text)...))
	return b.String()
}

// ---- InjectedFileItem: an injected-file message, rendered in-place ----
// An ordinary timeline entry (position = injection order); only the
// rendering differs: an @path row instead of the content.

var fileStyle = lipgloss.NewStyle().Foreground(styles.ColorFile)

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
// injection costs one row instead of one per file.
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
		// Headed and fenced, so a group reads as distinct files.
		sections := make([]string, len(i.paths))
		for index, path := range i.paths {
			sections[index] = "### " + path + "\n\n" + tool.Fence(strings.TrimPrefix(filepath.Ext(path), "."), i.contents[index])
		}
		return strings.Join(sections, "\n\n"), "md"
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
	if ctx.Collapsed && len(i.paths) > 1 {
		return labelRow(ctx, "@", fileStyle, fmt.Sprintf("%d files", len(i.paths)), strings.Join(basenames(i.paths), ", "))
	}
	var b strings.Builder
	width := ctx.rowWidth() - markerWidth
	for index, path := range i.paths {
		// Muted directory, colored basename: the part that matters pops.
		path = truncate(path, width)
		directory, name := filepath.Split(path)
		line := subGutter(ctx, index) + fileStyle.Render("@") + " " + styles.MutedStyle.Render(directory) + fileStyle.Render(name)
		prefixLines(&b, "", line)
	}
	return b.String()
}

func basenames(paths []string) []string {
	names := make([]string, 0, len(paths))
	for _, path := range paths {
		names = append(names, filepath.Base(path))
	}
	return names
}

func (i *InjectedFileItem) SubSpans(RenderContext) []span {
	spans := make([]span, len(i.paths))
	for index := range i.paths {
		spans[index] = span{index, index + 1}
	}
	return spans
}

func plural(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", count, noun)
}
