package component

import (
	"fmt"
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

// RenderTabBar renders the horizontal tab bar.
func RenderTabBar(tabs []Tab, width int) string {
	var parts []string
	for _, tab := range tabs {
		style := styles.TabInactiveStyle
		if tab.Active {
			style = styles.TabActiveStyle
		}
		label := tab.Title
		if tab.Streaming {
			label = styles.TabStreamingIndicator.Render("⟳ ") + label
		}
		parts = append(parts, style.Render(label))
	}
	bar := strings.Join(parts, " ")
	return styles.TabBarStyle.Width(width).Render(bar)
}

// RenderTabBarHelp renders shortcut hints below the tab bar.
func RenderTabBarHelp() string {
	return styles.DimTextStyle.Render(fmt.Sprintf("Alt+1-9: switch tab │ Ctrl+T: new │ Ctrl+W: close"))
}
