package markdown

import (
	"charm.land/glamour/v2/ansi"
	glamourstyles "charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2/compat"
)

// theme is Dracula (glamour's light style on light terminals) with block
// margins and prefixes dropped, so text sits on the timeline's column grid.
func theme() ansi.StyleConfig {
	style := glamourstyles.LightStyleConfig
	if compat.HasDarkBackground {
		style = glamourstyles.DraculaStyleConfig
	}
	zero := uint(0)
	style.Document.Margin = &zero
	style.Document.BlockPrefix = ""
	style.Document.BlockSuffix = ""
	style.Paragraph.BlockPrefix = ""
	style.Paragraph.BlockSuffix = ""

	style.CodeBlock.Margin = &zero
	style.CodeBlock.Indent = &zero
	style.CodeBlock.Prefix = ""
	style.CodeBlock.BlockPrefix = ""

	style.Code.Margin = &zero
	style.Code.Indent = &zero
	style.Code.Prefix = ""
	style.Code.Suffix = ""
	return style
}
