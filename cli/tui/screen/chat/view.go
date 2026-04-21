package chat

import (
	"strings"

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

	if !m.streaming {
		b.WriteString("\n")
		b.WriteString(styles.TextAreaStyle.Render(m.textarea.View()))
		if m.HasPendingToolCalls() {
			b.WriteString("\n")
			b.WriteString(styles.HelpStyle.Render("Tool call pending: Ctrl+J to accept, type a message + Ctrl+J to reject"))
		}
	}

	return b.String()
}
