package widget

import (
	"charm.land/lipgloss/v2"

	"github.com/malonaz/sgpt/cli/tui/styles"
)

// RenderConfirm renders a centered yes/no modal.
func RenderConfirm(title, body string, width, height int) string {
	content := styles.ConfirmTitleStyle.Render(title) + "\n\n" +
		body + "\n\n" +
		styles.DimTextStyle.Render("y / enter: confirm · any other key: cancel")
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, styles.ConfirmBoxStyle.Render(content))
}
