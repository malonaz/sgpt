package chat

import (
	"strings"

	"github.com/malonaz/sgpt/cli/tui/component"
	"github.com/malonaz/sgpt/cli/tui/styles"
)

func (m *Model) View() string {
	if !m.ready {
		return "Initializing..."
	}

	var b strings.Builder

	b.WriteString(m.renderTitleBar())
	b.WriteString("\n")
	b.WriteString(styles.ViewportStyle.Render(m.viewport.View()))

	if m.awaitingConfirm && m.pendingToolArgs != nil {
		b.WriteString("\n")
		b.WriteString(component.RenderConfirmDialog(m.pendingToolArgs.Command, m.pendingToolArgs.WorkingDirectory))
		b.WriteString("\n")
		b.WriteString(styles.HelpStyle.Render("Press Y to confirm, N or Esc to cancel"))
	} else if !m.streaming {
		b.WriteString("\n")
		b.WriteString(styles.TextAreaStyle.Render(m.textarea.View()))
	}

	return b.String()
}
