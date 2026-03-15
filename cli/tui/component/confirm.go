package component

import (
	"fmt"
	"strings"

	"github.com/malonaz/sgpt/cli/tui/styles"
)

// RenderConfirmDialog renders a Y/N confirmation overlay for a shell command.
func RenderConfirmDialog(command, workingDirectory string) string {
	var b strings.Builder
	b.WriteString(styles.ConfirmTitleStyle.Render("🔧 Execute Shell Command?"))
	b.WriteString("\n\n")
	b.WriteString(styles.ConfirmCommandStyle.Render(fmt.Sprintf("$ %s", command)))
	if workingDirectory != "" {
		b.WriteString("\n")
		b.WriteString(styles.DimTextStyle.Render(fmt.Sprintf("Working directory: %s", workingDirectory)))
	}
	return styles.ConfirmBoxStyle.Render(b.String())
}
