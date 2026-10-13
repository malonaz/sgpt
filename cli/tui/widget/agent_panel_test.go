package widget

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/session"
)

func TestRenderAgentDockFillsItsBox(t *testing.T) {
	rows := []AgentRow{
		{Title: "launcher", State: sgptpb.AgentOperation_STATE_UNSPECIFIED},
		{Title: "a sub-agent title far too long for the panel", State: sgptpb.AgentOperation_STATE_RUNNING, Depth: 1, Elapsed: 90 * time.Second, Activity: "Running a very long tool call summary"},
		{Title: "nested", State: sgptpb.AgentOperation_STATE_AWAITING_REVIEW, Depth: 2, Activity: "Review"},
	}
	for _, tc := range []struct {
		name  string
		panel AgentPanel
	}{
		{name: "tree", panel: AgentPanel{Rows: rows, Spinner: "⠋"}},
		{name: "tree with tail and hints", panel: AgentPanel{Rows: rows, Selected: 2, Focused: true, Hints: []string{"enter open"}, Tail: strings.Repeat("streamed output line\n", 30)}},
		{name: "chat info", panel: AgentPanel{Info: &session.Info{Model: "gpt", Files: []string{"main.go"}}}},
		{name: "no chat", panel: AgentPanel{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const width, height = 40, 30
			dock := RenderAgentDock(tc.panel, width, height)
			if got := lipgloss.Height(dock); got != height {
				t.Errorf("height = %d, want %d", got, height)
			}
			for i, line := range strings.Split(dock, "\n") {
				if got := lipgloss.Width(line); got != width {
					t.Errorf("line %d width = %d, want %d: %q", i, got, width, line)
				}
			}
		})
	}
}

func TestRenderAgentRowsKeepsSelectionInView(t *testing.T) {
	rows := make([]AgentRow, 10)
	for i := range rows {
		rows[i] = AgentRow{Title: string(rune('a' + i)), State: sgptpb.AgentOperation_STATE_SUCCEEDED}
	}
	// Room for three rows: selecting the eighth scrolls it to the bottom.
	view := renderAgentRows(AgentPanel{Rows: rows, Selected: 7}, 20, 3*agentRowHeight)
	titles := ""
	for _, line := range strings.Split(ansi.Strip(view), "\n") {
		if fields := strings.Fields(line); len(fields) > 1 {
			titles += fields[len(fields)-1]
		}
	}
	if titles != "fgh" {
		t.Errorf("visible rows = %q, want %q", titles, "fgh")
	}
}
