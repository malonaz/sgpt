package widget

import (
	"cmp"
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	aipb "github.com/malonaz/core/genproto/ai/v1"

	"github.com/malonaz/sgpt/cli/tui/format"
	"github.com/malonaz/sgpt/cli/tui/styles"
	"github.com/malonaz/sgpt/internal/session"
)

const (
	infoMaxWidth   = 96
	infoGaugeWidth = 24
	infoColumnGap  = 4
)

var (
	infoBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(styles.ColorSubtle).
			Padding(1, 2)
	infoTitleStyle   = lipgloss.NewStyle().Foreground(styles.ColorText).Bold(true)
	infoSectionStyle = lipgloss.NewStyle().Foreground(styles.ColorMuted).Bold(true)
)

// RenderInfo renders the chat info modal: identity, context gauge, usage and
// cost, message tallies and everything currently in context. Tables flow
// side by side as width allows; the inventory lists take whatever height is
// left, so the modal fits any screen down to 80×24.
func RenderInfo(info *session.Info, width, height int) string {
	box := infoBoxStyle
	if height < 30 {
		box = box.Padding(0, 2)
	}
	innerWidth := max(20, min(infoMaxWidth, width-2)-box.GetHorizontalFrameSize())

	title := infoTitleStyle.Render("Chat info") + "  " + styles.MutedStyle.Render(ansi.Truncate(infoChatLabel(info), innerWidth-12, styles.GlyphEllipsis))
	blocks := []string{
		title,
		infoContextGauge(info, innerWidth),
		infoFlow(innerWidth, infoTable("Session", infoSessionRows(info), false), infoUsageTable(info)),
		infoMessages(info, innerWidth),
	}
	footer := styles.Hint("esc", "close")

	// Sections breathe with a blank line between them, unless that would
	// overflow a short screen.
	var modal string
	for _, gap := range []string{"\n\n", "\n"} {
		fixed := strings.Join(blocks, gap)
		// Fixed parts, the gaps around the lists, the footer and the frame.
		spare := height - 2 - box.GetVerticalFrameSize() - lipgloss.Height(fixed) - 2*lipgloss.Height(gap)
		modal = box.Render(fixed + gap + infoInventory(info, innerWidth, spare) + gap + footer)
		if lipgloss.Height(modal) <= height {
			break
		}
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
}

// RenderInfoSummary is the info modal's essentials stacked in a narrow
// column: session facts, context gauge, usage and what is in context.
func RenderInfoSummary(info *session.Info, width int) string {
	return strings.Join([]string{
		infoTable("Session", infoSessionRows(info), false),
		infoContextGauge(info, width),
		infoUsageTable(info),
		infoInventory(info, width, 0),
	}, "\n\n")
}

// infoChatLabel identifies the chat: its title when it has one, else the
// chat ID; a brand-new chat has neither.
func infoChatLabel(info *session.Info) string {
	label := info.Title
	if label == "" {
		label = info.ChatName[strings.LastIndex(info.ChatName, "/")+1:]
	}
	if label == "" {
		label = "unsaved chat"
	}
	if info.Favorite {
		label += " ★"
	}
	return label
}

// infoFlow lays blocks out left to right, wrapping onto a new row when the
// next block would overflow width.
func infoFlow(width int, blocks ...string) string {
	var rows, row []string
	rowWidth := 0
	for _, block := range blocks {
		blockWidth := lipgloss.Width(block)
		if len(row) > 0 && rowWidth+infoColumnGap+blockWidth > width {
			rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, row...))
			row, rowWidth = nil, 0
		}
		if len(row) > 0 {
			row = append(row, strings.Repeat(" ", infoColumnGap))
			rowWidth += infoColumnGap
		}
		row = append(row, block)
		rowWidth += blockWidth
	}
	rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, row...))
	return strings.Join(rows, "\n\n")
}

// infoTable renders a titled table: the first cell muted (the label), the
// rest in text ink, each column left-aligned to its widest cell.
// infoTable lays rows out in columns; numeric tables right-align every
// column after the label, so figures line up by their last digit.
func infoTable(title string, rows [][]string, numeric bool) string {
	var widths []int
	for _, row := range rows {
		for i, cell := range row {
			if i >= len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], lipgloss.Width(cell))
		}
	}
	lines := []string{infoSectionStyle.Render(title)}
	for _, row := range rows {
		var b strings.Builder
		for i, cell := range row {
			style := styles.TextStyle
			if i == 0 {
				style = styles.MutedStyle
			} else {
				b.WriteString("  ")
			}
			pad := strings.Repeat(" ", widths[i]-lipgloss.Width(cell))
			if numeric && i > 0 {
				b.WriteString(pad + style.Render(cell))
			} else {
				b.WriteString(style.Render(cell) + pad)
			}
		}
		lines = append(lines, strings.TrimRight(b.String(), " "))
	}
	return strings.Join(lines, "\n")
}

func infoSessionRows(info *session.Info) [][]string {
	model := info.Model
	if info.Provider != "" {
		model = info.Provider + "/" + info.Model
	}
	role := info.Role
	if role == "" {
		role = "anon"
	}
	rows := [][]string{
		{"model", ansi.Truncate(model, 32, styles.GlyphEllipsis)},
		{"role", role},
		{"reasoning", cmp.Or(session.ReasoningLabel(info.Reasoning), "model default")},
		{"capabilities", infoCapabilities(info)},
	}
	if info.OutputLimit > 0 {
		rows = append(rows, []string{"max output", format.Tokens(int64(info.OutputLimit))})
	}
	return rows
}

// infoUsageTable pairs each token category with what it cost: the two
// columns answer "where did the money go" at a glance.
func infoUsageTable(info *session.Info) string {
	usage := info.Usage
	input, cacheRead := usage.GetInputToken(), usage.GetInputTokenCacheRead()
	cacheWrite := usage.GetInputTokenCacheWrite()
	output, reasoning := usage.GetOutputToken(), usage.GetOutputReasoningToken()
	// Sub-categories indent under their parent and show their share of it.
	row := func(label string, consumption *aipb.ResourceConsumption, share int64) []string {
		percent := ""
		if share > 0 && consumption.GetQuantity() > 0 {
			percent = fmt.Sprintf("%.0f%%", float64(consumption.GetQuantity())/float64(share)*100)
		}
		price := ""
		if consumption.GetPrice() > 0 {
			price = format.Price(consumption.GetPrice())
		}
		return []string{label, format.Tokens(consumption.GetQuantity()), percent, price}
	}
	rows := [][]string{
		row("input", input, 0),
		row("  cache read", cacheRead, input.GetQuantity()+cacheRead.GetQuantity()),
		row("  cache write", cacheWrite, 0),
		row("output", output, 0),
		row("  reasoning", reasoning, output.GetQuantity()+reasoning.GetQuantity()),
	}
	rows = append(rows, []string{"total", "", "", format.Price(info.Price)})
	// The per-turn average is what predicts the next turn's cost.
	if turns := info.AssistantMessages; turns > 0 {
		rows = append(rows, []string{"per turn", "", "", format.Price(info.Price / float64(turns))})
	}
	return infoTable("Usage", rows, true)
}

// infoContextGauge shows how full the context window is, using the LAST
// turn's input tokens: the cumulative total says nothing about the window.
func infoContextGauge(info *session.Info, width int) string {
	used := session.ContextTokens(info.ContextUsage)
	label := infoSectionStyle.Render("Context") + "  "
	if info.ContextLimit <= 0 {
		return label + styles.MutedStyle.Render(format.Tokens(used)+" used (window size unknown)")
	}
	limit := int64(info.ContextLimit)
	ratio := min(max(float64(used)/float64(limit), 0), 1)
	detail := styles.TextStyle.Render(fmt.Sprintf("%.0f%%", ratio*100)) + styles.MutedStyle.Render(fmt.Sprintf(
		" %s %s / %s %s %s free", styles.GlyphSeparator, format.Tokens(used), format.Tokens(limit), styles.GlyphSeparator, format.Tokens(limit-used)))
	gaugeWidth := min(infoGaugeWidth, width-lipgloss.Width(label)-lipgloss.Width(detail)-2)
	if gaugeWidth < 6 {
		return label + detail
	}
	return label + infoGauge(ratio, gaugeWidth) + "  " + detail
}

// infoGauge renders a proportional bar colored by pressure: success while
// there is room, warning past 75%, danger past 90%.
func infoGauge(ratio float64, width int) string {
	filled := int(ratio * float64(width))
	color := styles.ColorSuccess
	switch {
	case ratio >= 0.9:
		color = styles.ColorDanger
	case ratio >= 0.75:
		color = styles.ColorWarning
	}
	return lipgloss.NewStyle().Foreground(color).Render(strings.Repeat("━", filled)) +
		styles.SubtleStyle.Render(strings.Repeat("━", width-filled))
}

// infoMessages tallies the history on one line.
func infoMessages(info *session.Info, width int) string {
	parts := []string{
		fmt.Sprintf("%d total", info.TotalMessages),
		fmt.Sprintf("%d user", info.UserMessages),
		fmt.Sprintf("%d assistant", info.AssistantMessages),
		fmt.Sprintf("%d tool", info.ToolMessages),
		fmt.Sprintf("%d context", info.ContextMessages),
		fmt.Sprintf("%d tool calls", info.ToolCalls),
	}
	if info.QueuedMessages > 0 {
		parts = append(parts, fmt.Sprintf("%d queued", info.QueuedMessages))
	}
	return infoListLine("Messages", infoInk(parts), lipgloss.Width("Messages"), width, 2)
}

// infoInventory lists what is in context: lores, files, tools. Each list
// gets at least one line; spare height goes to the longest first.
func infoInventory(info *session.Info, width, spare int) string {
	autoAcceptedSet := make(map[string]bool, len(info.AutoAcceptedTools))
	for _, name := range info.AutoAcceptedTools {
		autoAcceptedSet[name] = true
	}
	tools := make([]string, 0, len(info.EnabledTools))
	for _, name := range info.EnabledTools {
		tools = append(tools, infoToolEntry(name, info.ToolNameToCalls[name], autoAcceptedSet[name], false))
	}
	// Tools called but since disabled would otherwise vanish from the picture.
	for _, name := range infoOrphanToolCalls(info) {
		tools = append(tools, infoToolEntry(name, info.ToolNameToCalls[name], false, true))
	}

	lists := []struct {
		label   string
		entries []string
		empty   string
	}{
		{"Lores", infoInk(info.Lores), "none in context"},
		{"Files", infoInk(info.Files), "none in context"},
		{fmt.Sprintf("Tools %d/%d", len(info.EnabledTools), len(info.AvailableTools)), tools, "none enabled"},
	}
	labelWidth := 0
	lines := make([]int, len(lists))
	for i, list := range lists {
		lines[i] = 1
		labelWidth = max(labelWidth, lipgloss.Width(list.label))
	}
	for spare -= len(lists); spare > 0; spare-- {
		grown := false
		for i, list := range lists {
			if lines[i] < len(infoWrap(list.label, list.entries, labelWidth, width)) {
				lines[i]++
				grown = true
				break
			}
		}
		if !grown {
			break
		}
	}
	rendered := make([]string, len(lists))
	for i, list := range lists {
		if len(list.entries) == 0 {
			rendered[i] = infoWrap(list.label, []string{styles.MutedStyle.Render(list.empty)}, labelWidth, width)[0]
			continue
		}
		rendered[i] = infoListLine(list.label, list.entries, labelWidth, width, lines[i])
	}
	return strings.Join(rendered, "\n")
}

func infoToolEntry(name string, calls int, autoAccepted, disabled bool) string {
	entry := styles.TextStyle.Render(name)
	if disabled {
		entry = styles.MutedStyle.Render(name + " off")
	}
	var annotations []string
	if calls > 0 {
		annotations = append(annotations, fmt.Sprintf("%d×", calls))
	}
	if autoAccepted {
		annotations = append(annotations, "auto")
	}
	if len(annotations) > 0 {
		entry += " " + styles.MutedStyle.Render(strings.Join(annotations, " "))
	}
	return entry
}

// infoInk renders plain entries in text ink.
func infoInk(entries []string) []string {
	inked := make([]string, len(entries))
	for i, entry := range entries {
		inked[i] = styles.TextStyle.Render(entry)
	}
	return inked
}

// infoListLine flows pre-styled entries after a label, wrapped under a hanging indent
// and capped at maxLines, the overflow summarized as "+N more".
func infoListLine(label string, entries []string, labelWidth, width, maxLines int) string {
	wrapped := infoWrap(label, entries, labelWidth, width)
	if len(wrapped) <= maxLines {
		return strings.Join(wrapped, "\n")
	}
	// Re-wrap with fewer entries until the summary fits in maxLines.
	for shown := len(entries) - 1; shown > 0; shown-- {
		more := styles.MutedStyle.Render(fmt.Sprintf("+%d more", len(entries)-shown))
		candidate := infoWrap(label, append(append([]string{}, entries[:shown]...), more), labelWidth, width)
		if len(candidate) <= maxLines {
			return strings.Join(candidate, "\n")
		}
	}
	return wrapped[0]
}

// infoWrap flows entries after a label padded to labelWidth, wrapping under
// a hanging indent.
func infoWrap(label string, entries []string, labelWidth, width int) []string {
	labelWidth += 2
	indent := strings.Repeat(" ", labelWidth)
	separator := styles.SubtleStyle.Render(" " + styles.GlyphSeparator + " ")
	separatorWidth := lipgloss.Width(separator)

	line := infoSectionStyle.Render(label) + strings.Repeat(" ", labelWidth-lipgloss.Width(label))
	lineWidth, empty := labelWidth, true
	var lines []string
	for _, entry := range entries {
		entry = ansi.Truncate(entry, width-labelWidth, styles.GlyphEllipsis)
		entryWidth := lipgloss.Width(entry)
		if !empty && lineWidth+separatorWidth+entryWidth > width {
			lines = append(lines, line)
			line, lineWidth, empty = indent, labelWidth, true
		}
		if !empty {
			line += separator
			lineWidth += separatorWidth
		}
		line += entry
		lineWidth += entryWidth
		empty = false
	}
	return append(lines, line)
}

func infoOrphanToolCalls(info *session.Info) []string {
	enabledSet := make(map[string]bool, len(info.EnabledTools))
	for _, name := range info.EnabledTools {
		enabledSet[name] = true
	}
	var orphans []string
	for name := range info.ToolNameToCalls {
		if !enabledSet[name] {
			orphans = append(orphans, name)
		}
	}
	sort.Strings(orphans)
	return orphans
}

func infoCapabilities(info *session.Info) string {
	var capabilities []string
	if info.SupportsToolCall {
		capabilities = append(capabilities, "tools")
	}
	if info.SupportsReasoning {
		capabilities = append(capabilities, "reasoning")
	}
	if len(capabilities) == 0 {
		return "text"
	}
	return strings.Join(capabilities, ", ")
}
