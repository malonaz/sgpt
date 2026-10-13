package widget

import (
	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"

	"github.com/malonaz/sgpt/cli/tui/keymap"

	"github.com/malonaz/sgpt/cli/tui/styles"
)

// confirmMaxWidth keeps the body a readable measure on wide terminals.
const confirmMaxWidth = 56

// KeyConfirm accepts a confirm modal; any other key cancels it.
var KeyConfirm = keymap.Binding{Key: key.NewBinding(key.WithKeys("y", "enter")), Help: "Confirm", Short: "confirm"}

var (
	// Warning: a confirm is the app waiting on the user.
	confirmBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(styles.ColorWarning).
			Padding(1, 2)
	confirmTitleStyle = lipgloss.NewStyle().Foreground(styles.ColorText).Bold(true)
	confirmBodyStyle  = lipgloss.NewStyle().Foreground(styles.ColorMuted)
)

// RenderConfirm renders a centered yes/no modal.
func RenderConfirm(title, body string, width, height int) string {
	textWidth := max(1, min(confirmMaxWidth, width-confirmBoxStyle.GetHorizontalFrameSize()-2))
	content := confirmTitleStyle.Width(textWidth).Render(title)
	if body != "" {
		content += "\n\n" + confirmBodyStyle.Width(textWidth).Render(body)
	}
	content += "\n\n" + styles.Hints(KeyConfirm.Hint(), styles.Hint("any key", "cancel"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, confirmBoxStyle.Render(content))
}
