package timeline

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/pbutil"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/malonaz/sgpt/cli/tui/styles"
	"github.com/malonaz/sgpt/internal/markdown"
	"github.com/malonaz/sgpt/internal/tool"
)

// RequestRenderer lets a tool dictate how its request renders in the
// timeline; unset (or declining) falls back to key: value lines.
type RequestRenderer interface {
	RenderRequest(toolCall *aipb.ToolCall) (string, bool)
}

// ResultRenderer lets a tool dictate how its result renders in the
// timeline; unset (or declining) falls back to compact text.
type ResultRenderer interface {
	RenderResult(toolCall *aipb.ToolCall, toolResult *aipb.ToolResult) (string, bool)
}

// Summarizer gives a call its one-row identity; unset (or declining) falls
// back to the tool name, its first short string argument and any error.
type Summarizer interface {
	Summarize(toolCall *aipb.ToolCall, toolResult *aipb.ToolResult) (tool.Summary, bool)
}

// RenderVersioner reports a version that changes whenever a resolved call's
// rendering may have; "" when it is fixed once resolved.
type RenderVersioner interface {
	RenderVersion(toolCall *aipb.ToolCall) string
}

const (
	// resultLineCap bounds an expanded result; alt+o shows all of it.
	resultLineCap = 12
	// verbWidth aligns subjects down a run for the common verbs.
	verbWidth = 6
)

var (
	spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

	successStyle = lipgloss.NewStyle().Foreground(styles.ColorSuccess)
	dangerStyle  = lipgloss.NewStyle().Foreground(styles.ColorDanger)
	warningStyle = lipgloss.NewStyle().Foreground(styles.ColorWarning)
	infoStyle    = lipgloss.NewStyle().Foreground(styles.ColorInfo)
	displayStyle = styles.MutedStyle.Italic(true)
)

// ---- ToolCallItem: request/response pair rendered as one row ----

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
	// Origin is the sub-agent chat a call awaiting review here came from;
	// empty for this chat's own calls.
	Origin string
	// RequestRenderer is the tool registry: it also summarizes and renders
	// results when the tool supports it.
	RequestRenderer RequestRenderer
	// lastGoodRequests persists the last successful tool-dictated render,
	// keyed by tool call ID — streaming items are reconstructed every tick,
	// so this state must live outside the item (owned by the Builder).
	lastGoodRequests map[string]string
	// messageName is the resource name of the assistant message this call
	// belongs to.
	messageName string
}

// NewEscalatedToolCallItem renders a sub-agent's call awaiting the user's
// verdict in the root chat.
func NewEscalatedToolCallItem(toolCall *aipb.ToolCall, origin string, requestRenderer RequestRenderer) *ToolCallItem {
	return &ToolCallItem{
		id:              "review-" + toolCall.GetId(),
		ToolCall:        toolCall,
		Pending:         true,
		Origin:          origin,
		RequestRenderer: requestRenderer,
	}
}

func (i *ToolCallItem) ID() string { return i.id }

func (i *ToolCallItem) MessageName() string { return i.messageName }

// live calls animate their spinner, so they render every frame.
func (i *ToolCallItem) live() bool { return i.Partial || i.Executing }

// resolved calls are done: they may fold into a run.
func (i *ToolCallItem) resolved() bool { return i.Result != nil && !i.live() && !i.Pending }

// CacheKey: result attachment, review status and live render versions all
// change the render; live renders opt out (spinner).
func (i *ToolCallItem) CacheKey() string {
	if i.live() {
		return ""
	}
	key := fmt.Sprintf("%s|r%t|p%t", i.id, i.Result != nil, i.Pending)
	if version := i.renderVersion(); version != "" {
		key += "|v" + version
	}
	return key
}

// renderVersion is non-empty for calls whose rendering changes after they
// resolved (e.g. sub-agent operation states).
func (i *ToolCallItem) renderVersion() string {
	if versioner, ok := i.RequestRenderer.(RenderVersioner); ok {
		return versioner.RenderVersion(i.ToolCall)
	}
	return ""
}

// Only a call awaiting review opens by default; running ones are a spinner
// row, since their body would only repeat the request.
func (i *ToolCallItem) DefaultCollapsed() bool { return !i.Pending }

// Folded rows sit flush against each other: a run reads as one list.
func (i *ToolCallItem) Compact(ctx RenderContext) bool { return ctx.Collapsed && !i.Pending }

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
	switch {
	case i.Origin != "":
		// Not in this chat's history, so outside its render-cache index space.
		ctx.Static = true
	case i.renderVersion() != "":
		// The markdown cache is keyed by seq alone; live renders change under it.
		ctx.Static = true
	}
	if i.Pending {
		return i.reviewCard(ctx)
	}
	summary := i.summary()
	if ctx.Collapsed {
		return gutter(ctx) + i.row(ctx, summary, ctx.rowWidth(), true)
	}
	var b strings.Builder
	b.WriteString(gutter(ctx) + i.row(ctx, summary, ctx.rowWidth(), false))
	prefixLines(&b, gutter(ctx)+ruleMarker, i.body(ctx))
	return b.String()
}

// summary is the call's one-row identity, from the tool or the fallback.
func (i *ToolCallItem) summary() tool.Summary {
	if summarizer, ok := i.RequestRenderer.(Summarizer); ok {
		if summary, ok := summarizer.Summarize(i.ToolCall, i.Result); ok {
			return summary
		}
	}
	summary := tool.Summary{Verb: humanize(i.ToolCall.GetName()), Subject: firstShortString(i.ToolCall.GetArguments())}
	if err := i.Result.GetError(); err != nil {
		summary.Outcome, _, _ = strings.Cut(strings.TrimSpace(err.GetMessage()), "\n")
		summary.Failed = true
	}
	return summary
}

func (i *ToolCallItem) failed(summary tool.Summary) bool {
	return summary.Failed || i.Result.GetError() != nil
}

// glyph shows state: spinner while live, ▶ awaiting review, ✓ / ✗ resolved.
func (i *ToolCallItem) glyph(ctx RenderContext, summary tool.Summary) string {
	frame := spinnerFrames[ctx.Frame%len(spinnerFrames)]
	switch {
	case i.Partial:
		return styles.MutedStyle.Render(frame)
	case i.Executing:
		return infoStyle.Render(frame)
	case i.Pending:
		return warningStyle.Render(styles.GlyphReview)
	case i.Result == nil:
		return styles.SubtleStyle.Render(styles.GlyphQueued)
	case i.failed(summary):
		return dangerStyle.Render(styles.GlyphFailure)
	}
	return successStyle.Render(styles.GlyphSuccess)
}

// row lays out `glyph Verb Subject  display … Outcome` in exactly width
// cells, truncating the subject (then the display message) so it never wraps.
func (i *ToolCallItem) row(ctx RenderContext, summary tool.Summary, width int, inlineDisplay bool) string {
	var left strings.Builder
	left.WriteString(i.glyph(ctx, summary) + " ")
	used := markerWidth
	if i.Origin != "" {
		origin := truncate(styles.GlyphChild+" "+i.Origin, width/4)
		left.WriteString(agentLabel.Render(origin) + " ")
		used += lipgloss.Width(origin) + 1
	}
	verb := truncate(summary.Verb, width/3)
	left.WriteString(styles.TextStyle.Bold(true).Render(verb))
	used += lipgloss.Width(verb)
	if pad := verbWidth - lipgloss.Width(verb); pad > 0 {
		left.WriteString(strings.Repeat(" ", pad))
		used += pad
	}

	outcome := truncate(flatten(summary.Outcome), width/3)
	outcomeWidth := 0
	if outcome != "" {
		outcomeWidth = lipgloss.Width(outcome) + 2
	}
	room := width - used - outcomeWidth - 1
	if subject := truncate(flatten(summary.Subject), room); subject != "" {
		left.WriteString(" " + styles.MutedStyle.Render(subject))
		used += lipgloss.Width(subject) + 1
		room -= lipgloss.Width(subject) + 1
	}
	if display := i.displayMessage(); inlineDisplay && display != "" && room > 8 {
		display = truncate(flatten(display), room-2)
		left.WriteString("  " + displayStyle.Render(display))
		used += lipgloss.Width(display) + 2
	}
	if outcome == "" {
		return left.String()
	}
	style := styles.MutedStyle
	if i.failed(summary) {
		style = dangerStyle
	}
	return left.String() + strings.Repeat(" ", max(width-used-lipgloss.Width(outcome), 2)) + style.Render(outcome)
}

func (i *ToolCallItem) displayMessage() string {
	metadata, _ := tool.ParseToolCallMetadata(i.ToolCall)
	return strings.TrimSpace(metadata.GetDisplayMessage().GetContent())
}

// body is the expanded call: display message, request, then capped result.
func (i *ToolCallItem) body(ctx RenderContext) string {
	var sections []string
	if display := i.displayMessage(); display != "" {
		sections = append(sections, displayStyle.Render(lipgloss.Wrap(display, min(ctx.bodyWidth(), markdown.MaxProseWidth), "")))
	}
	if request, rendered := i.request(ctx); request != "" && (rendered || !i.restatesSubject()) {
		sections = append(sections, request)
	}
	if i.Result != nil {
		sections = append(sections, i.response(ctx))
	}
	return strings.Join(sections, "\n\n")
}

// restatesSubject reports whether the default request would only repeat the
// row: a lone argument that is the subject. The review card still shows it,
// since the row truncates.
func (i *ToolCallItem) restatesSubject() bool {
	fields := i.ToolCall.GetArguments().GetFields()
	if len(fields) != 1 {
		return false
	}
	for _, value := range fields {
		return value.GetStringValue() == i.summary().Subject
	}
	return false
}

// request renders the call's arguments; rendered reports the tool's own
// presentation rather than the key: value fallback.
func (i *ToolCallItem) request(ctx RenderContext) (_ string, rendered bool) {
	// Tools may dictate their own presentation (e.g. the diff tool's diff).
	if i.RequestRenderer != nil {
		if md, ok := i.RequestRenderer.RenderRequest(i.ToolCall); ok {
			if i.lastGoodRequests != nil {
				i.lastGoodRequests[i.ToolCall.GetId()] = md
			}
			return renderMarkdown(ctx, i.seq, !i.Partial, markdown.ParseBlocks(md)...), true
		}
		// Partial arguments may be momentarily unparsable by the tool's
		// renderer; fall back to the last render that succeeded.
		if md, ok := i.lastGoodRequests[i.ToolCall.GetId()]; ok {
			return renderMarkdown(ctx, i.seq, false, markdown.ParseBlocks(md)...), true
		}
	}
	return formatFields(i.ToolCall.GetArguments().GetFields(), ctx.bodyWidth()), false
}

func (i *ToolCallItem) response(ctx RenderContext) string {
	if err := i.Result.GetError(); err != nil {
		wrapped := lipgloss.Wrap(strings.TrimSpace(err.GetMessage()), min(ctx.bodyWidth(), markdown.MaxProseWidth), "")
		return capLines(ctx, dangerStyle.Render(wrapped))
	}
	// Tools may dictate their own result presentation (e.g. search_lores
	// renders matches with highlights).
	if renderer, ok := i.RequestRenderer.(ResultRenderer); ok {
		if md, ok := renderer.RenderResult(i.ToolCall, i.Result); ok {
			return capLines(ctx, renderMarkdown(ctx, i.seq+1, true, markdown.ParseBlocks(md)...))
		}
	}
	if structured := i.Result.GetStructuredContent().GetStructValue(); structured != nil {
		return capLines(ctx, formatFields(structured.GetFields(), ctx.bodyWidth()))
	}
	// Raw output keeps its shape: lines truncate rather than re-wrap.
	content := strings.ReplaceAll(strings.TrimRight(toolResultText(i.Result), "\n"), "\t", "    ")
	if content == "" {
		return styles.SubtleStyle.Render("(no output)")
	}
	lines := strings.Split(content, "\n")
	for index, line := range lines {
		lines[index] = styles.TextStyle.Render(truncate(line, ctx.bodyWidth()))
	}
	return capLines(ctx, strings.Join(lines, "\n"))
}

// capLines keeps the first resultLineCap lines and says how to see the rest.
func capLines(ctx RenderContext, rendered string) string {
	lines := strings.Split(rendered, "\n")
	if len(lines) <= resultLineCap+1 {
		return rendered
	}
	more := styles.SubtleStyle.Render(fmt.Sprintf("%s %d more lines", styles.GlyphEllipsis, len(lines)-resultLineCap))
	hint := styles.Hint(KeyOpenEditor.Key.Keys()[0], "open")
	return strings.Join(lines[:resultLineCap], "\n") + "\n" + styles.Hints(more, hint)
}

var reviewBorder = lipgloss.NewStyle().Foreground(styles.ColorWarning)

// reviewCard is the one boxed element: the call that needs you, in full.
// The row rides in the top border; the body fills the text column exactly.
func (i *ToolCallItem) reviewCard(ctx RenderContext) string {
	inner := ctx.bodyWidth()
	summary := i.summary()
	row := i.row(ctx, summary, inner-1, false)
	fill := max(inner-1-lipgloss.Width(row), 0)

	var sections []string
	if display := i.displayMessage(); display != "" {
		sections = append(sections, displayStyle.Render(lipgloss.Wrap(display, min(inner, markdown.MaxProseWidth), "")))
	}
	if request, _ := i.request(ctx); request != "" {
		sections = append(sections, request)
	}

	// The border takes the gutter's second column, the selection the first.
	edge := " "
	if ctx.Selected {
		edge = selectedEdge
	}
	var b strings.Builder
	b.WriteString(edge + reviewBorder.Render("╭ ") + row + " " + reviewBorder.Render(strings.Repeat("─", fill)+"╮"))
	side := reviewBorder.Render("│")
	for _, line := range strings.Split(strings.Join(sections, "\n\n"), "\n") {
		width := lipgloss.Width(line)
		if width > inner {
			line = truncate(line, inner)
			width = inner
		}
		b.WriteString("\n" + edge + side + " " + line + strings.Repeat(" ", inner-width) + side)
	}
	b.WriteString("\n" + edge + reviewBorder.Render("╰"+strings.Repeat("─", inner+1)+"╯"))
	return b.String()
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

// humanize turns a tool name into a verb-ish label: read_files → Read files.
func humanize(name string) string {
	name = strings.ReplaceAll(name, "_", " ")
	if name == "" {
		return "Tool"
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

// firstShortString picks the argument that best names what a call acts on:
// the first one-line string (or list of strings), by key order.
func firstShortString(arguments *structpb.Struct) string {
	fields := arguments.GetFields()
	for _, key := range sortedKeys(fields) {
		switch value := fields[key].GetKind().(type) {
		case *structpb.Value_StringValue:
			if text := value.StringValue; text != "" && !strings.Contains(text, "\n") && len(text) <= 200 {
				return text
			}
		case *structpb.Value_ListValue:
			var texts []string
			for _, element := range value.ListValue.GetValues() {
				if text, ok := element.GetKind().(*structpb.Value_StringValue); ok {
					texts = append(texts, text.StringValue)
				}
			}
			if len(texts) > 0 {
				return strings.Join(texts, ", ")
			}
		}
	}
	return ""
}

func sortedKeys(fields map[string]*structpb.Value) []string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// formatFields renders a struct as `key: value` lines, nesting by indent,
// so arguments read as data rather than JSON.
func formatFields(fields map[string]*structpb.Value, width int) string {
	var lines []string
	appendFields(&lines, fields, 0, width)
	return strings.Join(lines, "\n")
}

func appendFields(lines *[]string, fields map[string]*structpb.Value, depth, width int) {
	for _, key := range sortedKeys(fields) {
		appendValue(lines, styles.MutedStyle.Render(key+":"), lipgloss.Width(key)+1, fields[key], depth, width)
	}
}

func appendValue(lines *[]string, label string, labelWidth int, value *structpb.Value, depth, width int) {
	pad := strings.Repeat("  ", depth)
	switch kind := value.GetKind().(type) {
	case *structpb.Value_StructValue:
		*lines = append(*lines, pad+label)
		appendFields(lines, kind.StructValue.GetFields(), depth+1, width)
	case *structpb.Value_ListValue:
		*lines = append(*lines, pad+label)
		for _, element := range kind.ListValue.GetValues() {
			appendValue(lines, styles.SubtleStyle.Render("-"), 1, element, depth+1, width)
		}
	default:
		text := scalarText(value)
		room := width - len(pad) - labelWidth - 1
		if !strings.Contains(text, "\n") && lipgloss.Width(text) <= room {
			*lines = append(*lines, pad+label+" "+styles.TextStyle.Render(text))
			return
		}
		// Long or multi-line values hang below their key.
		*lines = append(*lines, pad+label)
		inner := pad + "  "
		for _, line := range strings.Split(lipgloss.Wrap(text, max(width-len(inner), 1), ""), "\n") {
			*lines = append(*lines, inner+styles.TextStyle.Render(line))
		}
	}
}

func scalarText(value *structpb.Value) string {
	switch kind := value.GetKind().(type) {
	case *structpb.Value_StringValue:
		return strings.ReplaceAll(kind.StringValue, "\t", "    ")
	case *structpb.Value_NumberValue:
		return fmt.Sprint(kind.NumberValue)
	case *structpb.Value_BoolValue:
		return fmt.Sprint(kind.BoolValue)
	}
	return "null"
}
