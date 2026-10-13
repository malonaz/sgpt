package widget

import (
	"charm.land/bubbles/v2/textarea"
	"charm.land/lipgloss/v2"

	"github.com/malonaz/sgpt/cli/tui/styles"
)

// NewFilterInput returns the single-line filter field shared by the chat
// list and the pickers: a brand prompt while focused, subtle when not.
func NewFilterInput(placeholder string) textarea.Model {
	input := textarea.New()
	input.Placeholder = placeholder
	input.CharLimit = 256
	input.SetHeight(1)
	input.ShowLineNumbers = false
	input.Prompt = styles.GlyphPrompt + " "

	state := textarea.StyleState{
		Base:        lipgloss.NewStyle(),
		Text:        styles.TextStyle,
		CursorLine:  styles.TextStyle,
		Placeholder: styles.SubtleStyle,
		Prompt:      lipgloss.NewStyle().Foreground(styles.ColorBrand),
	}
	blurred := state
	blurred.Prompt = styles.SubtleStyle
	blurred.Text = styles.MutedStyle
	blurred.CursorLine = styles.MutedStyle
	inputStyles := input.Styles()
	inputStyles.Focused = state
	inputStyles.Blurred = blurred
	inputStyles.Cursor.Color = styles.ColorBrand
	input.SetStyles(inputStyles)
	return input
}
