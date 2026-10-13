package widget

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/malonaz/sgpt/cli/tui/styles"
)

// TabState is the activity a tab label signals.
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

type tabGlyph struct {
	glyph string
	style lipgloss.Style
}

var tabStateGlyphs = map[TabState]tabGlyph{
	TabReviewing: {styles.GlyphReview, lipgloss.NewStyle().Foreground(styles.ColorWarning).Bold(true)},
	// Busy's glyph is the live spinner frame, Tab.Spinner.
	TabBusy:   {"", lipgloss.NewStyle().Foreground(styles.ColorBrand)},
	TabDone:   {styles.GlyphSuccess, lipgloss.NewStyle().Foreground(styles.ColorSuccess)},
	TabFailed: {styles.GlyphFailure, lipgloss.NewStyle().Foreground(styles.ColorDanger)},
}

var (
	tabActiveStyle   = lipgloss.NewStyle().Foreground(styles.ColorBrand).Background(styles.ColorSurface).Bold(true)
	tabInactiveStyle = lipgloss.NewStyle().Foreground(styles.ColorMuted)
	tabDepthStyle    = lipgloss.NewStyle().Foreground(styles.ColorAgent)
	tabMarkerStyle   = styles.MutedStyle
	// A hidden tab awaiting review tints its overflow marker.
	tabMarkerReviewStyle = lipgloss.NewStyle().Foreground(styles.ColorWarning).Bold(true)
)

const (
	tabSeparator = " "
	// minTabTitleWidth is the narrowest titles shrink to before tabs scroll.
	minTabTitleWidth = 12
)

type Tab struct {
	Title  string
	Active bool
	State  TabState
	// Depth is the sub-agent nesting level; 0 for user-opened tabs.
	Depth int
	// Spinner is the current spinner frame, shown while busy.
	Spinner string
}

// RenderTabBar renders the tabs on one line. Titles share the width,
// shrinking longest first; past a floor, a window around the active tab is
// shown with a count of the hidden tabs on each side.
func RenderTabBar(tabs []Tab, width int) string {
	titleWidths := make([]int, len(tabs))
	chrome := make([]int, len(tabs))
	active := 0
	for i, tab := range tabs {
		titleWidths[i] = ansi.StringWidth(tab.Title)
		chrome[i] = lipgloss.Width(renderTab(tab, ""))
		if tab.Active {
			active = i
		}
	}
	limit := titleLimit(titleWidths, chrome, width)

	labels := make([]string, len(tabs))
	widths := make([]int, len(tabs))
	for i, tab := range tabs {
		labels[i] = renderTab(tab, ansi.Truncate(tab.Title, limit, styles.GlyphEllipsis))
		widths[i] = lipgloss.Width(labels[i])
	}

	lo, hi := visibleWindow(widths, active, width)
	var parts []string
	if lo > 0 {
		parts = append(parts, overflowMarker("‹", tabs[:lo]))
	}
	parts = append(parts, labels[lo:hi]...)
	if hi < len(labels) {
		parts = append(parts, overflowMarker("›", tabs[hi:]))
	}
	// A lone tab wider than the bar is cut rather than wrapped.
	return ansi.Truncate(strings.Join(parts, tabSeparator), width, styles.GlyphEllipsis)
}

// renderTab renders a tab's label: its depth and state marks, then title.
// Every piece carries the tab's base style, so marks keep its background.
func renderTab(tab Tab, title string) string {
	base := tabInactiveStyle
	if tab.Active {
		base = tabActiveStyle
	}
	var b strings.Builder
	b.WriteString(base.Render(" "))
	if tab.Depth > 0 {
		// Tabs are ordered as a tree: one arrow, deeper levels indented.
		b.WriteString(base.Render(strings.Repeat(" ", tab.Depth-1)))
		b.WriteString(tabDepthStyle.Inherit(base).Render(styles.GlyphChild))
		b.WriteString(base.Render(" "))
	}
	if mark, ok := tabStateGlyphs[tab.State]; ok {
		glyph := mark.glyph
		if tab.State == TabBusy {
			glyph = tab.Spinner
		}
		b.WriteString(mark.style.Inherit(base).Render(glyph))
		b.WriteString(base.Render(" "))
	}
	b.WriteString(base.Render(title + " "))
	return b.String()
}

// titleLimit is the widest title that lets every tab fit, floored at
// minTabTitleWidth.
func titleLimit(titleWidths, chrome []int, width int) int {
	limit := 0
	for _, w := range titleWidths {
		limit = max(limit, w)
	}
	for ; limit > minTabTitleWidth; limit-- {
		total := (len(titleWidths) - 1) * len(tabSeparator)
		for i, w := range titleWidths {
			total += chrome[i] + min(w, limit)
		}
		if total <= width {
			break
		}
	}
	return limit
}

func overflowMarker(arrow string, hidden []Tab) string {
	style := tabMarkerStyle
	for _, tab := range hidden {
		if tab.State == TabReviewing {
			style = tabMarkerReviewStyle
		}
	}
	if arrow == "‹" {
		return style.Render(arrow + strconv.Itoa(len(hidden)))
	}
	return style.Render(strconv.Itoa(len(hidden)) + arrow)
}

// overflowMarkerWidth bounds a marker's width: an arrow and up to two digits.
const overflowMarkerWidth = 3

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
			total += overflowMarkerWidth + len(tabSeparator)
		}
		if hi < len(widths) {
			total += overflowMarkerWidth + len(tabSeparator)
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
