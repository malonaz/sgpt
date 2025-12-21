package session

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/malonaz/sgpt/cli/chat/styles"
	"github.com/malonaz/sgpt/cli/chat/types"
)

// View renders the model.
func (m *Model) View() string {
	if m.quitting {
		return ""
	}

	if !m.ready {
		return "Initializing..."
	}

	var b strings.Builder

	b.WriteString(m.renderTitle())
	b.WriteString("\n")

	b.WriteString(styles.ViewportStyle.Render(m.viewport.View()))
	b.WriteString("\n")

	if m.awaitingConfirm && m.pendingToolArgs != nil {
		b.WriteString(m.renderConfirmDialog())
		b.WriteString("\n")
	} else {
		if !m.streaming {
			b.WriteString(styles.TextAreaStyle.Render(m.textarea.View()))
		}
	}

	if m.awaitingConfirm {
		b.WriteString(styles.HelpStyle.Render("Press Y to confirm, N or Esc to cancel"))
	}

	if m.err != nil {
		b.WriteString("\n")
		b.WriteString(styles.ErrorStyle.Render(fmt.Sprintf("Error: %v", m.err)))
	}

	return m.alertClipboardWrite.Render(b.String())
}

func (m *Model) renderTitle() string {
	rendered := styles.TitleStyle.Width(m.width).Render(m.title)
	m.titleHeight = lipgloss.Height(rendered)
	return rendered
}

func (m *Model) getStyle(style lipgloss.Style, messageIndex int) lipgloss.Style {
	style = style.Width(m.width - styles.MessageHorizontalFrameSize())
	if m.navigationMessageIndex == -1 {
		return style
	}
	fg := styles.MessageUnselectedColor
	if messageIndex == m.navigationMessageIndex {
		fg = styles.MessageSelectedColor
	}
	return style.BorderForeground(fg)
}

func (m *Model) renderMessages() string {
	currentLine := 0
	var b strings.Builder
	writeString := func(s string) {
		b.WriteString(s)
		currentLine += strings.Count(s, "\n")
	}

	contentWidth := m.viewport.Width

	if len(m.injectedFiles) > 0 {
		for i, f := range m.injectedFiles {
			line := styles.FileStyle.Width(contentWidth).Render(fmt.Sprintf("📎 File #%d: %s", i+1, f))
			writeString(line)
			writeString("\n")
		}
		writeString("\n")
	}

	// Reset viewport offsets
	m.messageViewportOffsets = make([]int, 0, len(m.runtimeMessages))

	for i, rm := range m.runtimeMessages {
		if i > 0 {
			writeString("\n\n")
		}
		m.messageViewportOffsets = append(m.messageViewportOffsets, currentLine)

		switch rm.Type {
		case types.RuntimeMessageTypeUser:
			rendered := m.renderer.ToMarkdown(rm.Blocks, i, !rm.IsStreaming)
			style := m.getStyle(styles.UserMessageStyle, i)
			writeString(style.Render(rendered))

		case types.RuntimeMessageTypeThinking:
			style := m.getStyle(styles.AIThoughtStyle, i)
			rendered := m.renderer.ToMarkdown(rm.Blocks, i, !rm.IsStreaming)
			writeString(style.Render(rendered))

		case types.RuntimeMessageTypeAssistant:
			style := m.getStyle(styles.AIMessageStyle, i)
			rendered := m.renderer.ToMarkdown(rm.Blocks, i, !rm.IsStreaming)
			writeString(style.Render(rendered))

			if rm.Err != nil {
				writeString("\n")
				if errors.Is(rm.Err, errUserInterrupt) {
					writeString(styles.MessageInterruptStyle.Render("⚡ Interrupted by user"))
				} else {
					writeString(styles.MessageErrorStyle.Render(fmt.Sprintf("⚠️ %v", rm.Err)))
				}
			}

		case types.RuntimeMessageTypeToolCall:
			writeString(styles.ToolLabelStyle.Render(fmt.Sprintf("🔧 Tool: %s", rm.ToolCall.Name)))
			writeString("\n")
			writeString(styles.ToolCallStyle.Render(rm.ToolCall.Arguments))

		case types.RuntimeMessageTypeToolResult:
			writeString(styles.ToolLabelStyle.Render("⚡ Tool Result:"))
			writeString("\n")
			if rm.Err != nil {
				writeString(styles.ToolResultStyle.Render(rm.Content()))
			} else {
				rendered := m.renderer.ToMarkdown(rm.Blocks, i, !rm.IsStreaming)
				writeString(styles.ToolResultStyle.Render(rendered))
			}

		case types.RuntimeMessageTypeSystem:
			writeString(styles.SystemStyle.Render(fmt.Sprintf("System: %s", styles.Truncate(rm.Content(), styles.TruncateLength))))
		}
	}

	return b.String()
}

func (m *Model) renderConfirmDialog() string {
	var b strings.Builder
	b.WriteString(styles.ConfirmTitleStyle.Render("🔧 Execute Shell Command?"))
	b.WriteString("\n\n")
	b.WriteString(styles.ConfirmCommandStyle.Render(fmt.Sprintf("$ %s", m.pendingToolArgs.Command)))
	if m.pendingToolArgs.WorkingDirectory != "" {
		b.WriteString("\n")
		b.WriteString(styles.DimTextStyle.Render(
			fmt.Sprintf("Working directory: %s", m.pendingToolArgs.WorkingDirectory)))
	}
	return styles.ConfirmBoxStyle.Render(b.String())
}
