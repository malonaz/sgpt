package widget

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/malonaz/sgpt/cli/tui/styles"
	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/tool/agent"
)

const (
	maxLauncherTitleWidth = 24
	// minAgentTitleWidth is the narrowest a sub-agent title shrinks to before
	// the strip falls back to glyphs alone.
	minAgentTitleWidth = 4
	ellipsis           = "…"
)

var (
	agentStripSeparator = styles.DimTextStyle.Render(" │ ")
	agentGlyphStyles    = map[sgptpb.AgentOperation_State]lipgloss.Style{
		sgptpb.AgentOperation_STATE_RUNNING:         lipgloss.NewStyle().Foreground(styles.SecondaryColor),
		sgptpb.AgentOperation_STATE_AWAITING_REVIEW: lipgloss.NewStyle().Foreground(styles.AccentColor).Bold(true),
		sgptpb.AgentOperation_STATE_SUCCEEDED:       lipgloss.NewStyle().Foreground(styles.SuccessColor),
		sgptpb.AgentOperation_STATE_FAILED:          lipgloss.NewStyle().Foreground(styles.ErrorColor),
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
}

type AgentEntry struct {
	Title string
	State sgptpb.AgentOperation_State
}

// RenderAgentStrip renders the strip on one line of the given width.
func RenderAgentStrip(strip AgentStrip, width int) string {
	var segments []string
	if strip.Launcher != "" {
		launcher := "↖ " + ansi.Truncate(strip.Launcher, maxLauncherTitleWidth, ellipsis) + styles.DimTextStyle.Render(" (alt+u)")
		segments = append(segments, launcher)
	}
	if len(strip.Children) > 0 {
		price := fmt.Sprintf("$%.2f", strip.Price) + styles.DimTextStyle.Render(" (alt+g)")
		budget := width - lipgloss.Width(price) - lipgloss.Width(agentStripSeparator)
		for _, segment := range segments {
			budget -= lipgloss.Width(segment) + lipgloss.Width(agentStripSeparator)
		}
		segments = append(segments, renderAgentEntries(strip.Children, budget), price)
	}
	return ansi.Truncate(strings.Join(segments, agentStripSeparator), width, ellipsis)
}

// renderAgentEntries fits the entries in budget columns: titles shrink
// together, and past a floor only the glyphs remain.
func renderAgentEntries(entries []AgentEntry, budget int) string {
	longest := 0
	for _, entry := range entries {
		longest = max(longest, ansi.StringWidth(entry.Title))
	}
	for limit := longest; limit >= minAgentTitleWidth; limit-- {
		// Each entry is "glyph title", separated by two spaces.
		width := 4*len(entries) - 2
		for _, entry := range entries {
			width += min(ansi.StringWidth(entry.Title), limit)
		}
		if width <= budget {
			parts := make([]string, len(entries))
			for i, entry := range entries {
				parts[i] = agentGlyph(entry.State) + " " + ansi.Truncate(entry.Title, limit, ellipsis)
			}
			return strings.Join(parts, "  ")
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
	return styles.DimTextStyle.Render(glyph)
}
