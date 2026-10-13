package widget

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/malonaz/sgpt/cli/tui/format"
	"github.com/malonaz/sgpt/cli/tui/styles"
	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/tool/agent"
)

const (
	maxLauncherTitleWidth = 28
	// minAgentTitleWidth is the narrowest a sub-agent title shrinks to before
	// the strip falls back to glyphs alone.
	minAgentTitleWidth = 6
	agentEntryGap      = "  "
	agentSegmentGap    = "   "
)

var (
	agentLauncherStyle = lipgloss.NewStyle().Foreground(styles.ColorAgent)
	agentGlyphStyles   = map[sgptpb.AgentOperation_State]lipgloss.Style{
		sgptpb.AgentOperation_STATE_RUNNING:         lipgloss.NewStyle().Foreground(styles.ColorInfo),
		sgptpb.AgentOperation_STATE_AWAITING_REVIEW: lipgloss.NewStyle().Foreground(styles.ColorWarning).Bold(true),
		sgptpb.AgentOperation_STATE_SUCCEEDED:       lipgloss.NewStyle().Foreground(styles.ColorSuccess),
		sgptpb.AgentOperation_STATE_FAILED:          lipgloss.NewStyle().Foreground(styles.ColorDanger),
	}
)

// AgentStrip is the sub-agent tree around a chat: who launched it and the
// sub-agents it launched.
type AgentStrip struct {
	// Launcher is the launcher's title; empty for a chat the user opened.
	Launcher string
	Children []AgentEntry
	// Price is the children's total spend.
	Price float64
	// LauncherHint and ChildrenHint are the rendered keys that reach them.
	LauncherHint string
	ChildrenHint string
}

type AgentEntry struct {
	Title string
	State sgptpb.AgentOperation_State
}

// RenderAgentStrip renders the strip as one quiet line of the given width:
// the launcher on the left, the sub-agents and their spend on the right.
func RenderAgentStrip(strip AgentStrip, width int) string {
	inner := width - 2*statusPadding
	var left, right string
	if strip.Launcher != "" {
		left = agentLauncherStyle.Render(styles.GlyphChild+" from "+ansi.Truncate(strip.Launcher, maxLauncherTitleWidth, styles.GlyphEllipsis)) +
			agentSegmentGap + strip.LauncherHint
	}
	if len(strip.Children) > 0 {
		tail := agentSegmentGap + styles.MutedStyle.Render(format.Price(strip.Price)) + agentSegmentGap + strip.ChildrenHint
		budget := inner - lipgloss.Width(tail)
		if left != "" {
			budget -= lipgloss.Width(left) + statusGap
		}
		right = renderAgentEntries(strip.Children, budget) + tail
	}
	gap := max(0, inner-lipgloss.Width(left)-lipgloss.Width(right))
	return pad(ansi.Truncate(left+strings.Repeat(" ", gap)+right, max(0, inner), styles.GlyphEllipsis))
}

// renderAgentEntries fits the entries in budget columns: titles shrink
// together, and past a floor only the glyphs remain.
func renderAgentEntries(entries []AgentEntry, budget int) string {
	longest := 0
	for _, entry := range entries {
		longest = max(longest, ansi.StringWidth(entry.Title))
	}
	for limit := longest; limit >= minAgentTitleWidth; limit-- {
		// Each entry is "glyph title", separated by agentEntryGap.
		width := (2+len(agentEntryGap))*len(entries) - len(agentEntryGap)
		for _, entry := range entries {
			width += min(ansi.StringWidth(entry.Title), limit)
		}
		if width <= budget {
			parts := make([]string, len(entries))
			for i, entry := range entries {
				parts[i] = agentGlyph(entry.State) + " " + styles.MutedStyle.Render(ansi.Truncate(entry.Title, limit, styles.GlyphEllipsis))
			}
			return strings.Join(parts, agentEntryGap)
		}
	}
	glyphs := make([]string, len(entries))
	for i, entry := range entries {
		glyphs[i] = agentGlyph(entry.State)
	}
	return strings.Join(glyphs, " ")
}

func agentGlyph(state sgptpb.AgentOperation_State) string {
	glyph := agent.StateGlyph(state)
	if style, ok := agentGlyphStyles[state]; ok {
		return style.Render(glyph)
	}
	return styles.SubtleStyle.Render(glyph)
}
