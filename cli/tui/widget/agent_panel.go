package widget

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/malonaz/sgpt/cli/tui/format"
	"github.com/malonaz/sgpt/cli/tui/styles"
	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/session"
)

const (
	// agentTailLines caps the live tail of the selected agent's output.
	agentTailLines = 10
	// agentRowHeight is a row's title line over its activity line.
	agentRowHeight   = 2
	agentModalWidth  = 64
	agentModalHeight = 32
)

var (
	agentDockStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(styles.ColorSubtle).
			Padding(0, 1)
	agentModalStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(styles.ColorAgent).
			Padding(0, 1)
	agentCursorStyle        = lipgloss.NewStyle().Foreground(styles.ColorBrand)
	agentCursorBlurredStyle = styles.SubtleStyle
)

// AgentPanel is the agent tree around the active chat and the live tail of
// the selected agent's output.
type AgentPanel struct {
	// Rows is the tree in display order; empty when the chat has no agents.
	Rows     []AgentRow
	Selected int
	// Focused panels show their cursor in the accent, and their hints.
	Focused bool
	Hints   []string
	// Spinner is the frame running agents show.
	Spinner string
	// Tail is the selected agent's latest output.
	Tail string
	// Info stands in for the tree when the chat has no agents.
	Info *session.Info
}

// AgentRow is one agent of the tree.
type AgentRow struct {
	Title string
	// State is UNSPECIFIED for an idle chat the user opened.
	State sgptpb.AgentOperation_State
	Depth int
	// Elapsed is its current or last turn's run time; zero hides it.
	Elapsed  time.Duration
	Activity string
}

// RenderAgentDock wears the panel as a full-height column on the right.
func RenderAgentDock(panel AgentPanel, width, height int) string {
	return wearAgentPanel(agentDockStyle.Height(height), panel, width, height)
}

// RenderAgentModal wears the panel as an overlay centered on the screen.
func RenderAgentModal(panel AgentPanel, width, height int) string {
	modal := wearAgentPanel(agentModalStyle, panel, min(agentModalWidth, width-2), min(agentModalHeight, height-2))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
}

// wearAgentPanel renders the panel inside container, within width×height
// cells: the wearings differ only in their container.
func wearAgentPanel(container lipgloss.Style, panel AgentPanel, width, height int) string {
	innerWidth := max(1, width-container.GetHorizontalFrameSize())
	innerHeight := max(1, height-container.GetVerticalFrameSize())
	body := renderAgentPanel(panel, innerWidth, innerHeight)
	return container.Render(lipgloss.NewStyle().Width(innerWidth).MaxHeight(innerHeight).Render(body))
}

// renderAgentPanel stacks the tree, the output tail and the hints; the tree
// takes what height the others leave. A chat without agents shows its info.
func renderAgentPanel(panel AgentPanel, width, height int) string {
	if len(panel.Rows) == 0 {
		if panel.Info == nil {
			return ""
		}
		return joinSections(infoSectionStyle.Render("Chat"), RenderInfoSummary(panel.Info, width))
	}
	var below []string
	if tail := tailLines(panel.Tail, width, agentTailLines); len(tail) > 0 {
		below = append(below, infoSectionStyle.Render("Output")+"\n"+strings.Join(tail, "\n"))
	}
	if panel.Focused {
		below = append(below, styles.Hints(panel.Hints...))
	}
	// Each section below costs its lines plus the blank line above it.
	treeHeight := height - 1 - lipgloss.Height(joinSections(below...)) - len(below)
	tree := infoSectionStyle.Render("Agents") + "\n" + renderAgentRows(panel, width, treeHeight)
	return joinSections(append([]string{tree}, below...)...)
}

// joinSections separates sections with a blank line.
func joinSections(sections ...string) string {
	return strings.Join(sections, "\n\n")
}

// renderAgentRows renders the rows that fit height, scrolled to keep the
// selection in view.
func renderAgentRows(panel AgentPanel, width, height int) string {
	fit := max(1, height/agentRowHeight)
	start := min(max(0, panel.Selected-fit+1), max(0, len(panel.Rows)-fit))
	end := min(len(panel.Rows), start+fit)
	rows := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		rows = append(rows, renderAgentRow(panel.Rows[i], panel.cursor(i), panel.Spinner, width))
	}
	return strings.Join(rows, "\n")
}

// cursor marks the selected row, in the accent while the panel has focus.
func (p AgentPanel) cursor(row int) string {
	switch {
	case row != p.Selected:
		return " "
	case p.Focused:
		return agentCursorStyle.Render(styles.GlyphSelected)
	}
	return agentCursorBlurredStyle.Render(styles.GlyphSelected)
}

// renderAgentRow lays out "cursor indent glyph title  elapsed" over the
// activity, which hangs under the title.
func renderAgentRow(row AgentRow, cursor, spinner string, width int) string {
	indent := ""
	if row.Depth > 0 {
		indent = strings.Repeat("  ", row.Depth-1) + agentLauncherStyle.Render(styles.GlyphChild) + " "
	}
	lead := cursor + indent + agentRowGlyph(row.State, spinner) + " "
	hang := strings.Repeat(" ", lipgloss.Width(lead))

	elapsed := ""
	if row.Elapsed > 0 {
		elapsed = " " + styles.MutedStyle.Render(format.Duration(row.Elapsed))
	}
	titleWidth := max(1, width-lipgloss.Width(lead)-lipgloss.Width(elapsed))
	title := styles.TextStyle.Width(titleWidth).Render(ansi.Truncate(row.Title, titleWidth, styles.GlyphEllipsis))
	activity := styles.MutedStyle.Render(ansi.Truncate(row.Activity, max(1, width-len(hang)), styles.GlyphEllipsis))
	return lead + title + elapsed + "\n" + hang + activity
}

// agentRowGlyph is the agents' state glyph, animated while running; an idle
// user chat has none.
func agentRowGlyph(state sgptpb.AgentOperation_State, spinner string) string {
	switch state {
	case sgptpb.AgentOperation_STATE_UNSPECIFIED:
		return " "
	case sgptpb.AgentOperation_STATE_RUNNING:
		return agentGlyphStyles[state].Render(spinner)
	}
	return agentGlyph(state)
}

// tailLines wraps text to width and keeps its last n lines. Only the last n
// source lines are wrapped: a long output stays cheap to render every frame.
func tailLines(text string, width, n int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	wrapped := strings.Split(ansi.Wrap(strings.Join(lines[max(0, len(lines)-n):], "\n"), width, ""), "\n")
	tail := wrapped[max(0, len(wrapped)-n):]
	for i, line := range tail {
		tail[i] = styles.TextStyle.Render(line)
	}
	return tail
}
