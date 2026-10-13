package widget

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/malonaz/sgpt/cli/tui/styles"
)

// TabState is the activity a tab label signals, most urgent first.
type TabState int

const (
	TabIdle TabState = iota
	// TabReviewing: a tool call is parked on the user's verdict.
	TabReviewing
	// TabBusy: a turn is in flight.
	TabBusy
	// TabDone and TabFailed: a turn ended while the tab was not focused.
	TabDone
	TabFailed
)

var tabStateGlyphs = map[TabState]string{
	TabReviewing: "▶ ",
	TabBusy:      "● ",
	TabDone:      "✓ ",
	TabFailed:    "✗ ",
}

const (
	tabSeparator   = " "
	tabMarkerLeft  = "‹"
	tabMarkerRight = "›"
)

type Tab struct {
	ID     string
	Title  string
	Active bool
	State  TabState
	// Depth is the sub-agent nesting level; 0 for user-opened tabs.
	Depth int
}

// RenderTabBar renders the tabs on one line; when they overflow, a window
// around the active tab is shown with markers for the hidden ones.
func RenderTabBar(tabs []Tab, width int) string {
	labels := make([]string, len(tabs))
	widths := make([]int, len(tabs))
	active := 0
	for i, tab := range tabs {
		style := styles.TabInactiveStyle
		if tab.Active {
			style = styles.TabActiveStyle
			active = i
		}
		labels[i] = style.Render(strings.Repeat("↳", tab.Depth) + tabStateGlyphs[tab.State] + tab.Title)
		widths[i] = lipgloss.Width(labels[i])
	}
	lo, hi := visibleWindow(widths, active, width)
	var parts []string
	if lo > 0 {
		parts = append(parts, tabMarkerLeft)
	}
	parts = append(parts, labels[lo:hi]...)
	if hi < len(labels) {
		parts = append(parts, tabMarkerRight)
	}
	// A lone tab wider than the bar is cut rather than wrapped.
	bar := ansi.Truncate(strings.Join(parts, tabSeparator), width, ellipsis)
	return styles.TabBarStyle.Width(width).Render(bar)
}

// visibleWindow returns the range [lo, hi) of tabs to show within width:
// always the active tab, grown alternately right and left while it fits,
// counting a marker for each hidden side.
func visibleWindow(widths []int, active, width int) (lo, hi int) {
	if len(widths) == 0 {
		return 0, 0
	}
	cost := func(lo, hi int) int {
		total := (hi - lo - 1) * len(tabSeparator)
		for _, w := range widths[lo:hi] {
			total += w
		}
		if lo > 0 {
			total += ansi.StringWidth(tabMarkerLeft) + len(tabSeparator)
		}
		if hi < len(widths) {
			total += ansi.StringWidth(tabMarkerRight) + len(tabSeparator)
		}
		return total
	}
	lo, hi = active, active+1
	for grew := true; grew; {
		grew = false
		if hi < len(widths) && cost(lo, hi+1) <= width {
			hi++
			grew = true
		}
		if lo > 0 && cost(lo-1, hi) <= width {
			lo--
			grew = true
		}
	}
	return lo, hi
}
