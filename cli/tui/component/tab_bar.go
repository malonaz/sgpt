package component

import (
	"strings"

	"github.com/malonaz/sgpt/cli/tui/styles"
)

// Tab holds the display info for a single tab in the bar.
type Tab struct {
	ID        string
	Title     string
	Active    bool
	Streaming bool
}

func RenderTabBar(tabs []Tab, width int) string {
	var parts []string
	for _, tab := range tabs {
		style := styles.TabInactiveStyle
		if tab.Active {
			style = styles.TabActiveStyle
		}
		label := tab.Title
		if tab.Streaming {
			label = "● " + label
		}
		parts = append(parts, style.Render(label))
	}
	bar := strings.Join(parts, " ")
	return styles.TabBarStyle.Width(width).Render(bar)
}
