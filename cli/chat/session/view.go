package session

import (
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

	return m.alertClipboardWrite.Render(b.String())
}

func (m *Model) renderTitle() string {
	rendered := styles.TitleStyle.Width(m.width).Render(m.title)
	m.titleHeight = lipgloss.Height(rendered)
	return rendered
}

// getBlockIndicatorStyle returns the style for a block indicator.
func (m *Model) getBlockIndicatorStyle(messageIndex, blockIndex int) lipgloss.Style {
	if m.focusedComponent != FocusViewport {
		return styles.BlockIndicatorStyle
	}
	if m.navigationMessageIndex != messageIndex || m.navigationBlockIndex != blockIndex {
		return styles.BlockIndicatorStyle
	}
	return styles.BlockIndicatorSelectedStyle
}

// renderBlockWithIndicator renders a block with a left indicator bar.
func (m *Model) renderBlockWithIndicator(content string, messageIndex, blockIndex int) string {
	indicatorStyle := m.getBlockIndicatorStyle(messageIndex, blockIndex)
	lines := strings.Split(content, "\n")
	var result strings.Builder
	for i, line := range lines {
		if i > 0 {
			result.WriteString("\n")
		}
		result.WriteString(indicatorStyle.Render(styles.BlockIndicatorChar))
		result.WriteString(" ")
		result.WriteString(line)
	}
	return result.String()
}

func (m *Model) getStyle(style lipgloss.Style, messageIndex int) lipgloss.Style {
	style = style.Width(m.width - styles.MessageHorizontalFrameSize())
	if m.focusedComponent != FocusViewport {
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
	m.blockViewportOffsets = make([][]int, 0, len(m.runtimeMessages))

	for i, rm := range m.runtimeMessages {
		if i > 0 {
			writeString("\n\n")
		}
		m.messageViewportOffsets = append(m.messageViewportOffsets, currentLine)

		// Track block offsets for this message
		blockOffsets := make([]int, 0, len(rm.Blocks))

		switch rm.Type {
		case types.RuntimeMessageTypeUser:
			// Always render blocks with indicators
			var blockContent strings.Builder
			for bi, block := range rm.Blocks {
				if bi > 0 {
					blockContent.WriteString("\n")
				}
				blockOffsets = append(blockOffsets, currentLine+strings.Count(blockContent.String(), "\n"))
				rendered := m.renderer.ToMarkdown(i*1000+bi, !rm.IsStreaming, block)
				blockContent.WriteString(m.renderBlockWithIndicator(rendered, i, bi))
			}
			style := m.getStyle(styles.UserMessageStyle, i)
			writeString(style.Render(blockContent.String()))

		case types.RuntimeMessageTypeThinking:
			// Always render blocks with indicators
			var blockContent strings.Builder
			for bi, block := range rm.Blocks {
				if bi > 0 {
					blockContent.WriteString("\n")
				}
				blockOffsets = append(blockOffsets, currentLine+strings.Count(blockContent.String(), "\n"))
				rendered := m.renderer.ToMarkdown(i*1000+bi, !rm.IsStreaming, block)
				blockContent.WriteString(m.renderBlockWithIndicator(rendered, i, bi))
			}
			style := m.getStyle(styles.AIThoughtStyle, i)
			writeString(style.Render(blockContent.String()))

		case types.RuntimeMessageTypeAssistant:
			// Always render blocks with indicators
			var blockContent strings.Builder
			for bi, block := range rm.Blocks {
				if bi > 0 {
					blockContent.WriteString("\n")
				}
				blockOffsets = append(blockOffsets, currentLine+strings.Count(blockContent.String(), "\n"))
				rendered := m.renderer.ToMarkdown(i*1000+bi, !rm.IsStreaming, block)
				blockContent.WriteString(m.renderBlockWithIndicator(rendered, i, bi))
			}
			style := m.getStyle(styles.AIMessageStyle, i)
			writeString(style.Render(blockContent.String()))

		case types.RuntimeMessageTypeToolCall:
			blockOffsets = append(blockOffsets, currentLine)
			writeString(styles.ToolLabelStyle.Render(fmt.Sprintf("🔧 Tool: %s", rm.ToolCall.Name)))
			writeString("\n")
			writeString(styles.ToolCallStyle.Render(rm.ToolCall.Arguments))

		case types.RuntimeMessageTypeToolResult:
			blockOffsets = append(blockOffsets, currentLine)
			writeString(styles.ToolLabelStyle.Render("⚡ Tool Result:"))
			writeString("\n")
			if rm.Err != nil {
				writeString(styles.ToolResultStyle.Render(rm.Content()))
			} else {
				rendered := m.renderer.ToMarkdown(i, !rm.IsStreaming, rm.Blocks...)
				writeString(styles.ToolResultStyle.Render(rendered))
			}

		case types.RuntimeMessageTypeSystem:
			blockOffsets = append(blockOffsets, currentLine)
			writeString(styles.SystemStyle.Render(fmt.Sprintf("System: %s", styles.Truncate(rm.Content(), styles.TruncateLength))))
		}

		m.blockViewportOffsets = append(m.blockViewportOffsets, blockOffsets)
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
