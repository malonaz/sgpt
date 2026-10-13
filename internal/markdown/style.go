package markdown

import (
	"fmt"
	"image/color"

	"charm.land/glamour/v2/ansi"
	glamourstyles "charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2/compat"

	"github.com/malonaz/sgpt/cli/tui/styles"
)

// hex resolves an adaptive palette token to the hex string glamour expects.
func hex(c color.Color) *string {
	r, g, b, _ := c.RGBA()
	value := fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
	return &value
}

func boolPtr(value bool) *bool { return &value }

// theme is glamour's base for the terminal's background, tuned to the palette:
// calm text-ink headings, inline code on Surface, links in Info.
func theme() ansi.StyleConfig {
	style := glamourstyles.LightStyleConfig
	if compat.HasDarkBackground {
		style = glamourstyles.DarkStyleConfig
	}
	zero := uint(0)
	style.Document.Margin = &zero
	style.Document.BlockPrefix = ""
	style.Document.BlockSuffix = ""
	style.Document.Color = hex(styles.ColorText)
	style.Paragraph.BlockPrefix = ""
	style.Paragraph.BlockSuffix = ""

	style.Heading.Color = hex(styles.ColorText)
	style.Heading.Bold = boolPtr(true)
	style.H1 = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Color: hex(styles.ColorBrand), Bold: boolPtr(true)}}
	style.H2 = ansi.StyleBlock{}
	style.H3 = ansi.StyleBlock{}
	style.H4 = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Color: hex(styles.ColorMuted)}}
	style.H5 = style.H4
	style.H6 = style.H4

	style.Code.Prefix = ""
	style.Code.Suffix = ""
	style.Code.Color = hex(styles.ColorText)
	style.Code.BackgroundColor = hex(styles.ColorSurface)

	style.CodeBlock.Margin = &zero
	style.CodeBlock.Indent = &zero
	style.CodeBlock.Prefix = ""
	style.CodeBlock.BlockPrefix = ""

	style.Link.Color = hex(styles.ColorInfo)
	style.LinkText.Color = hex(styles.ColorInfo)
	style.BlockQuote.Color = hex(styles.ColorMuted)
	style.HorizontalRule.Color = hex(styles.ColorSubtle)
	style.Item.BlockPrefix = "• "
	return style
}
