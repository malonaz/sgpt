package widget

import (
	"strings"

	"github.com/malonaz/sgpt/cli/tui/styles"
)

type Tab struct {
	ID        string
	Title     string
	Active    bool
	Streaming bool
	// Reviewing marks a tab whose turn is parked on a tool call verdict.
	Reviewing bool
}

func RenderTabBar(tabs []Tab, width int) string {
	var parts []string
	for _, tab := range tabs {
		style := styles.TabInactiveStyle
		if tab.Active {
			style = styles.TabActiveStyle
		}
		label := tab.Title
		switch {
		case tab.Reviewing:
			label = "▶ " + label
		case tab.Streaming:
			label = "● " + label
		}
		parts = append(parts, style.Render(label))
	}
	bar := strings.Join(parts, " ")
	return styles.TabBarStyle.Width(width).Render(bar)
}
