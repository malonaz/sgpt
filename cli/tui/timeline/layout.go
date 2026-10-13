package timeline

import (
	"image/color"
	"regexp"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/malonaz/sgpt/cli/tui/styles"
	"github.com/malonaz/sgpt/internal/markdown"
)

// Every line is gutter (selection) + marker (glyph) + text, so text aligns in
// one column across prose, turns and tool rows. Nested items (a work row's
// children) shift right by nestInset.
const (
	gutterWidth = 2
	markerWidth = 2
	indent      = gutterWidth + markerWidth
	nestInset   = 2
)

var (
	selectedEdge   = lipgloss.NewStyle().Foreground(styles.ColorBrand).Render(styles.GlyphSelected)
	selectedGutter = selectedEdge + " "
	siblingGutter  = styles.SubtleStyle.Render(styles.GlyphSelected) + " "
	blankGutter    = strings.Repeat(" ", gutterWidth)
	blankMarker    = strings.Repeat(" ", markerWidth)
)

// selects reports whether the selection covers sub-block index; -1 asks
// about the item as a whole.
func (ctx RenderContext) selects(index int) bool {
	return ctx.Selected && (ctx.SelectedSub < 0 || ctx.SelectedSub == index)
}

// gutter lights the item's lines while it is selected.
func gutter(ctx RenderContext) string { return subGutter(ctx, -1) }

// subGutter lights only the selected fence in fence navigation, dimming its
// siblings so the item's extent stays visible.
func subGutter(ctx RenderContext, index int) string {
	inset := strings.Repeat(" ", ctx.Inset)
	switch {
	case !ctx.Selected:
		return blankGutter + inset
	case ctx.selects(index):
		return selectedGutter + inset
	default:
		return siblingGutter + inset
	}
}

// bodyWidth is the text column's width, past gutter and marker.
func (ctx RenderContext) bodyWidth() int { return max(ctx.Width-indent-ctx.Inset, 1) }

// rowWidth caps one-line rows like prose, so mechanics stay near the title.
func (ctx RenderContext) rowWidth() int {
	return max(min(ctx.Width-gutterWidth-ctx.Inset, markdown.MaxProseWidth+markerWidth), markerWidth+1)
}

// bandWidth stops backgrounds at the text column (prose wraps at
// MaxProseWidth) rather than the terminal's edge.
func (ctx RenderContext) bandWidth() int {
	return min(ctx.Width, indent+ctx.Inset+markdown.MaxProseWidth+bandPadding)
}

// bandPadding keeps the longest line off the band's right edge.
const bandPadding = 2

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

// band paints a background behind an already styled line across exactly
// width cells. The band owns the background: every reset, and every
// background the line sets itself (inline code), is rewritten to the band's
// color, so nothing punches through it.
func band(line string, width int, background color.Color) string {
	on := ansi.Style{}.BackgroundColor(background).String()
	parameter := strings.TrimSuffix(strings.TrimPrefix(on, "\x1b["), "m")
	line = sgrSequence.ReplaceAllStringFunc(line, func(sequence string) string {
		return rebackground(sequence, parameter)
	})
	if overflow := ansi.StringWidth(line) - width; overflow > 0 {
		line = ansi.Truncate(line, width, "")
	}
	return on + line + strings.Repeat(" ", max(width-ansi.StringWidth(line), 0)) + "\x1b[m"
}

var sgrSequence = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

// rebackground drops the background parameters of one SGR sequence and, if it
// set a background or reset, re-applies the band's.
func rebackground(sequence, background string) string {
	parameters := strings.Split(sequence[len("\x1b["):len(sequence)-1], ";")
	kept := make([]string, 0, len(parameters)+1)
	touched := false
	for index := 0; index < len(parameters); index++ {
		parameter := parameters[index]
		code, _ := strconv.Atoi(parameter)
		switch {
		case parameter == "" || parameter == "0":
			kept, touched = append(kept, "0"), true
		case parameter == "48" && index+1 < len(parameters):
			// 48;5;n or 48;2;r;g;b
			index += map[string]int{"5": 2, "2": 4}[parameters[index+1]]
			touched = true
		case code == 49 || (code >= 40 && code <= 47) || (code >= 100 && code <= 107) || strings.HasPrefix(parameter, "48:"):
			touched = true
		default:
			kept = append(kept, parameter)
		}
	}
	if !touched {
		return sequence
	}
	return "\x1b[" + strings.Join(append(kept, background), ";") + "m"
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

// markerStyle brightens a selected item's marker.
func markerStyle(ctx RenderContext, style lipgloss.Style) lipgloss.Style {
	if ctx.Selected {
		return style.Bold(true)
	}
	return style
}

// labelRow renders a one-line item: a marker glyph, a label and a muted
// detail after a separator, truncated to fit.
func labelRow(ctx RenderContext, glyph string, style lipgloss.Style, label, detail string) string {
	width := ctx.rowWidth() - markerWidth
	label = truncate(label, width)
	row := gutter(ctx) + markerStyle(ctx, style).Render(glyph) + " " + style.Render(label)
	if rest := width - lipgloss.Width(label) - 3; detail != "" && rest > 1 {
		row += separator + styles.MutedStyle.Render(truncate(detail, rest))
	}
	return row
}

var separator = styles.SubtleStyle.Render(" " + styles.GlyphSeparator + " ")
