package widget

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	aipb "github.com/malonaz/core/genproto/ai/v1"

	"github.com/malonaz/sgpt/cli/tui/styles"
	"github.com/malonaz/sgpt/internal/session"
)

const (
	// infoGaugeWidth is the character width of a usage bar.
	infoGaugeWidth = 28
	// infoListLimit caps each list section; the remainder is summarized as
	// "+N more" so the modal never outgrows the screen.
	infoListLimit = 8
	// infoColumnGap separates the two top columns.
	infoColumnGap = 3
)

var (
	infoBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(styles.PrimaryColor).
			Padding(1, 3)

	infoTitleStyle = lipgloss.NewStyle().
			Foreground(styles.PrimaryColor).
			Bold(true)

	infoSectionStyle = lipgloss.NewStyle().
				Foreground(styles.SecondaryColor).
				Bold(true)

	infoLabelStyle = lipgloss.NewStyle().
			Foreground(styles.DimTextColor)

	infoValueStyle = lipgloss.NewStyle().
			Foreground(styles.TextColor)

	infoAccentStyle = lipgloss.NewStyle().
			Foreground(styles.AccentColor).
			Bold(true)

	infoGaugeFillStyle = lipgloss.NewStyle().
				Foreground(styles.SuccessColor)

	infoGaugeWarnStyle = lipgloss.NewStyle().
				Foreground(styles.AccentColor)

	infoGaugeFullStyle = lipgloss.NewStyle().
				Foreground(styles.ErrorColor)

	infoGaugeEmptyStyle = lipgloss.NewStyle().
				Foreground(styles.BorderColor)
)

// RenderInfo renders the chat info modal: identity, context gauge, token and
// cost breakdown, message tallies and everything currently in context.
func RenderInfo(info *session.Info, width, height int) string {
	var b strings.Builder
	b.WriteString(infoTitleStyle.Render("📊 Chat Info"))
	b.WriteString("  ")
	b.WriteString(styles.DimTextStyle.Render(infoChatLabel(info)))
	b.WriteString("\n\n")

	// Identity and message tallies read as two independent tables; placing
	// them side by side keeps the modal short enough for small terminals.
	b.WriteString(lipgloss.JoinHorizontal(
		lipgloss.Top,
		infoSection("Session", infoSessionRows(info)),
		strings.Repeat(" ", infoColumnGap),
		infoSection("Messages", infoMessageRows(info)),
	))
	b.WriteString("\n\n")

	b.WriteString(infoSectionStyle.Render("Context window"))
	b.WriteString("\n")
	b.WriteString(infoContextGauge(info))
	b.WriteString("\n\n")

	b.WriteString(lipgloss.JoinHorizontal(
		lipgloss.Top,
		infoSection("Tokens (chat total)", infoTokenRows(info)),
		strings.Repeat(" ", infoColumnGap),
		infoSection("Spend", infoSpendRows(info)),
	))
	b.WriteString("\n\n")

	b.WriteString(infoLists(info))
	b.WriteString("\n\n")
	b.WriteString(styles.DimTextStyle.Render("press any key to close"))

	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, infoBoxStyle.Render(b.String()))
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
		label += "  ★"
	}
	return styles.Truncate(label, 48)
}

type infoRow struct {
	label string
	value string
}

// infoSection renders a titled, column-aligned label/value table.
func infoSection(title string, rows []infoRow) string {
	labelWidth := 0
	for _, row := range rows {
		labelWidth = max(labelWidth, lipgloss.Width(row.label))
	}
	var b strings.Builder
	b.WriteString(infoSectionStyle.Render(title))
	for _, row := range rows {
		b.WriteString("\n")
		b.WriteString(infoLabelStyle.Render(fmt.Sprintf("  %-*s", labelWidth, row.label)))
		b.WriteString("  ")
		b.WriteString(infoValueStyle.Render(row.value))
	}
	return b.String()
}

func infoSessionRows(info *session.Info) []infoRow {
	model := info.Model
	if info.Provider != "" {
		model = info.Provider + "/" + info.Model
	}
	role := info.Role
	if role == "" {
		role = "anon"
	}
	rows := []infoRow{
		{label: "model", value: model},
		{label: "role", value: role},
		{label: "reasoning", value: infoReasoning(info.Reasoning)},
		{label: "capabilities", value: infoCapabilities(info)},
	}
	if info.OutputLimit > 0 {
		rows = append(rows, infoRow{label: "max output", value: infoTokens(info.OutputLimit)})
	}
	return rows
}

func infoMessageRows(info *session.Info) []infoRow {
	rows := []infoRow{
		{label: "total", value: fmt.Sprintf("%d", info.TotalMessages)},
		{label: "user", value: fmt.Sprintf("%d", info.UserMessages)},
		{label: "assistant", value: fmt.Sprintf("%d", info.AssistantMessages)},
		{label: "tool", value: fmt.Sprintf("%d", info.ToolMessages)},
		{label: "context", value: fmt.Sprintf("%d", info.ContextMessages)},
		{label: "tool calls", value: fmt.Sprintf("%d", info.ToolCalls)},
	}
	if info.QueuedMessages > 0 {
		rows = append(rows, infoRow{label: "queued", value: fmt.Sprintf("%d", info.QueuedMessages)})
	}
	return rows
}

func infoTokenRows(info *session.Info) []infoRow {
	usage := info.Usage
	input := usage.GetInputToken().GetQuantity()
	cacheRead := usage.GetInputTokenCacheRead().GetQuantity()
	cacheWrite := usage.GetInputTokenCacheWrite().GetQuantity()
	output := usage.GetOutputToken().GetQuantity()
	reasoning := usage.GetOutputReasoningToken().GetQuantity()

	rows := []infoRow{
		{label: "↑ input", value: infoTokens(input)},
		{label: "  ├ cache read", value: infoTokensWithShare(cacheRead, input+cacheRead)},
		{label: "  └ cache write", value: infoTokens(cacheWrite)},
		{label: "↓ output", value: infoTokens(output)},
		{label: "  └ reasoning", value: infoTokensWithShare(reasoning, output+reasoning)},
	}
	if images := usage.GetInputImageToken().GetQuantity() + usage.GetOutputImageToken().GetQuantity(); images > 0 {
		rows = append(rows, infoRow{label: "image", value: infoTokens(images)})
	}
	return rows
}

func infoSpendRows(info *session.Info) []infoRow {
	rows := []infoRow{{label: "chat cost", value: infoAccentStyle.Render(fmt.Sprintf("$%.4f", info.Price))}}
	// Per-turn averages are what actually predicts the next turn's cost.
	if turns := info.AssistantMessages; turns > 0 {
		rows = append(rows, infoRow{label: "per turn", value: fmt.Sprintf("$%.4f", info.Price/float64(turns))})
	}
	total := info.Usage.GetInputToken().GetQuantity() + info.Usage.GetInputTokenCacheRead().GetQuantity() +
		info.Usage.GetOutputToken().GetQuantity() + info.Usage.GetOutputReasoningToken().GetQuantity()
	if total > 0 {
		rows = append(rows, infoRow{label: "per 1m tokens", value: fmt.Sprintf("$%.2f", info.Price/float64(total)*1_000_000)})
	}
	if cacheRead := info.Usage.GetInputTokenCacheRead().GetQuantity(); cacheRead > 0 {
		billedInput := info.Usage.GetInputToken().GetQuantity() + cacheRead
		rows = append(rows, infoRow{
			label: "cache hit rate",
			value: fmt.Sprintf("%.0f%%", float64(cacheRead)/float64(billedInput)*100),
		})
	}
	return rows
}

// infoContextGauge shows how full the context window is, using the LAST
// turn's input tokens — the cumulative total says nothing about the window.
func infoContextGauge(info *session.Info) string {
	used := info.ContextUsage.GetInputToken().GetQuantity() + info.ContextUsage.GetInputTokenCacheRead().GetQuantity()
	if info.ContextLimit <= 0 {
		return infoLabelStyle.Render(fmt.Sprintf("  %s used (window size unknown)", infoTokens(used)))
	}
	ratio := float64(used) / float64(info.ContextLimit)
	return fmt.Sprintf(
		"  %s %s %s",
		infoGauge(ratio),
		infoValueStyle.Render(fmt.Sprintf("%.1f%%", ratio*100)),
		infoLabelStyle.Render(fmt.Sprintf("(%s / %s · %s free)",
			infoTokens(used), infoTokens(info.ContextLimit), infoTokens(info.ContextLimit-used))),
	)
}

// infoGauge renders a proportional bar, colored by pressure: green while
// there is room, amber past 75%, red past 90%.
func infoGauge(ratio float64) string {
	ratio = min(max(ratio, 0), 1)
	filled := int(ratio * infoGaugeWidth)
	style := infoGaugeFillStyle
	switch {
	case ratio >= 0.9:
		style = infoGaugeFullStyle
	case ratio >= 0.75:
		style = infoGaugeWarnStyle
	}
	return style.Render(strings.Repeat("█", filled)) +
		infoGaugeEmptyStyle.Render(strings.Repeat("░", infoGaugeWidth-filled))
}

// infoLists renders the context inventory: lores, files, tools.
func infoLists(info *session.Info) string {
	var b strings.Builder
	b.WriteString(infoList("📚 Lores", "no lores in context", info.Lores, nil))
	b.WriteString("\n")
	b.WriteString(infoList("📎 Files", "no files in context", info.Files, nil))
	b.WriteString("\n")

	// Tools are annotated with their call count and auto-accept status, so
	// the list doubles as a picture of what the model has been doing.
	autoAcceptedSet := make(map[string]bool, len(info.AutoAcceptedTools))
	for _, name := range info.AutoAcceptedTools {
		autoAcceptedSet[name] = true
	}
	b.WriteString(infoList(
		fmt.Sprintf("🔧 Tools (%d/%d enabled)", len(info.EnabledTools), len(info.AvailableTools)),
		"no tools enabled",
		info.EnabledTools,
		func(name string) string {
			var annotations []string
			if calls := info.ToolNameToCalls[name]; calls > 0 {
				annotations = append(annotations, fmt.Sprintf("%d calls", calls))
			}
			if autoAcceptedSet[name] {
				annotations = append(annotations, "auto")
			}
			if len(annotations) == 0 {
				return ""
			}
			return strings.Join(annotations, ", ")
		},
	))

	// Tools called but no longer enabled would otherwise vanish from the
	// picture entirely.
	if orphans := infoOrphanToolCalls(info); len(orphans) > 0 {
		b.WriteString("\n")
		b.WriteString(infoList("⚠️  Called, now disabled", "", orphans, func(name string) string {
			return fmt.Sprintf("%d calls", info.ToolNameToCalls[name])
		}))
	}
	return b.String()
}

// infoList renders a titled, comma-separated list, truncated to infoListLimit
// entries. annotate optionally suffixes each entry.
func infoList(title, emptyText string, entries []string, annotate func(string) string) string {
	var b strings.Builder
	b.WriteString(infoSectionStyle.Render(title))
	if len(entries) == 0 {
		b.WriteString(" ")
		b.WriteString(styles.DimTextStyle.Render(emptyText))
		return b.String()
	}
	shown := entries
	if len(shown) > infoListLimit {
		shown = shown[:infoListLimit]
	}
	for _, entry := range shown {
		b.WriteString("\n")
		b.WriteString(infoValueStyle.Render("  • " + styles.Truncate(entry, 60)))
		if annotate != nil {
			if annotation := annotate(entry); annotation != "" {
				b.WriteString(" ")
				b.WriteString(styles.DimTextStyle.Render("(" + annotation + ")"))
			}
		}
	}
	if remaining := len(entries) - len(shown); remaining > 0 {
		b.WriteString("\n")
		b.WriteString(styles.DimTextStyle.Render(fmt.Sprintf("  … +%d more", remaining)))
	}
	return b.String()
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

func infoReasoning(effort aipb.ReasoningEffort) string {
	switch effort {
	case aipb.ReasoningEffort_REASONING_EFFORT_LOW:
		return "low"
	case aipb.ReasoningEffort_REASONING_EFFORT_MEDIUM:
		return "medium"
	case aipb.ReasoningEffort_REASONING_EFFORT_HIGH:
		return "high"
	default:
		return "none"
	}
}

func infoTokens(count int32) string {
	return formatTokenCount(count)
}

// infoTokensWithShare annotates a token count with its share of a total —
// cache reads and reasoning tokens only mean something relative to it.
func infoTokensWithShare(count, total int32) string {
	if total <= 0 || count == 0 {
		return infoTokens(count)
	}
	return fmt.Sprintf("%s (%.0f%%)", infoTokens(count), float64(count)/float64(total)*100)
}
