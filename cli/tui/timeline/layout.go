package timeline

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/malonaz/sgpt/cli/tui/styles"
	"github.com/malonaz/sgpt/internal/markdown"
)

// Every line is gutter (selection) + marker (rule or glyph) + text, so text
// aligns in one column across prose, turns and tool rows.
const (
	gutterWidth = 2
	markerWidth = 2
	indent      = gutterWidth + markerWidth
)

var (
	selectedEdge   = lipgloss.NewStyle().Foreground(styles.ColorBrand).Render(styles.GlyphSelected)
	selectedGutter = selectedEdge + " "
	siblingGutter  = styles.SubtleStyle.Render(styles.GlyphSelected) + " "
	blankGutter    = strings.Repeat(" ", gutterWidth)
	blankMarker    = strings.Repeat(" ", markerWidth)
	ruleMarker     = styles.SubtleStyle.Render(styles.GlyphRule) + " "
)

// gutter lights the item's lines while it is selected.
func gutter(ctx RenderContext) string {
	if ctx.Selected {
		return selectedGutter
	}
	return blankGutter
}

// subGutter lights only the selected fence in fence navigation, dimming its
// siblings so the item's extent stays visible.
func subGutter(ctx RenderContext, index int) string {
	switch {
	case !ctx.Selected:
		return blankGutter
	case ctx.SelectedSub < 0 || ctx.SelectedSub == index:
		return selectedGutter
	default:
		return siblingGutter
	}
}

// bodyWidth is the text column's width, past gutter and marker.
func (ctx RenderContext) bodyWidth() int { return max(ctx.Width-indent, 1) }

// rowWidth caps one-line rows like prose, so outcomes stay near their subject.
func (ctx RenderContext) rowWidth() int {
	return max(min(ctx.Width-gutterWidth, markdown.MaxProseWidth+markerWidth), markerWidth+1)
}

// prefixLines prepends prefix to every line of body.
func prefixLines(b *strings.Builder, prefix, body string) {
	for index, line := range strings.Split(body, "\n") {
		if b.Len() > 0 || index > 0 {
			b.WriteString("\n")
		}
		b.WriteString(prefix)
		b.WriteString(line)
	}
}

// truncate shortens plain text to width cells.
func truncate(text string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(text, width, styles.GlyphEllipsis)
}

// flatten collapses whitespace so multi-line text fits one row.
func flatten(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// labelRow renders a one-line item: a marker glyph, a label and a subtle
// detail after a separator, truncated to fit.
func labelRow(ctx RenderContext, glyph string, style lipgloss.Style, label, detail string) string {
	width := ctx.rowWidth() - markerWidth
	label = truncate(label, width)
	row := gutter(ctx) + style.Render(glyph) + " " + style.Render(label)
	if rest := width - lipgloss.Width(label) - 3; detail != "" && rest > 1 {
		row += styles.SubtleStyle.Render(" "+styles.GlyphSeparator+" ") + styles.MutedStyle.Render(truncate(detail, rest))
	}
	return row
}
