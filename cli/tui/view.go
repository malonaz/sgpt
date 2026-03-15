package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/malonaz/core/go/pbutil"

	"github.com/malonaz/sgpt/cli/tui/styles"
	"github.com/malonaz/sgpt/internal/types"
)

// View renders the full TUI frame as a declarative tea.View: title bar, viewport
// (scrollable message area), and either the tool confirmation dialog or the textarea
// input, plus any active alert overlay. AltScreen and ReportFocus are set declaratively.
func (m *Model) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}

	if !m.ready {
		return tea.NewView("Initializing...")
	}

	var b strings.Builder

	b.WriteString(m.renderTitle())

	b.WriteString("\n")
	b.WriteString(styles.ViewportStyle.Render(m.viewport.View()))

	if m.awaitingConfirm && m.pendingToolArgs != nil {
		b.WriteString("\n")
		b.WriteString(m.renderConfirmDialog())
	} else {
		if !m.streaming {
			b.WriteString("\n")
			b.WriteString(styles.TextAreaStyle.Render(m.textarea.View()))
		}
	}

	if m.awaitingConfirm {
		b.WriteString(styles.HelpStyle.Render("Press Y to confirm, N or Esc to cancel"))
	}

	content := m.renderAlert(b.String())

	view := tea.NewView(content)
	view.AltScreen = true
	view.ReportFocus = true
	return view
}

// renderTitle renders the title bar at the current terminal width and caches
// its height (in lines) for layout calculations.
func (m *Model) renderTitle() string {
	rendered := styles.TitleStyle.Width(m.width).Render(m.title)
	m.titleHeight = lipgloss.Height(rendered)
	return rendered
}

// getBlockIndicatorStyle returns the appropriate indicator style for a block based
// on whether the viewport is focused and whether this specific block (or its parent
// message in select-all mode) is currently selected.
func (m *Model) getBlockIndicatorStyle(messageIndex, blockIndex int) lipgloss.Style {
	if m.focusedComponent != FocusViewport {
		return styles.BlockIndicatorStyle
	}
	if m.navigationMessageIndex != messageIndex {
		return styles.BlockIndicatorStyle
	}
	if m.navigationBlockIndex == -1 || m.navigationBlockIndex == blockIndex {
		return styles.BlockIndicatorSelectedStyle
	}
	return styles.BlockIndicatorStyle
}

// renderBlockWithIndicator prepends a vertical bar indicator to each line of the
// given content string. The indicator color reflects whether the block is selected.
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

// getStyle returns the message border style with the appropriate border color
// based on whether the viewport is focused and whether this message is selected.
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

// renderMessages builds the full viewport content string from all runtime messages.
// It populates messageViewportOffsets and blockViewportOffsets as a side effect so
// that navigation and scrolling can map message/block indices to viewport line numbers.
func (m *Model) renderMessages() string {
	currentLine := 0
	var b strings.Builder
	writeString := func(s string) {
		b.WriteString(s)
		currentLine += strings.Count(s, "\n")
	}

	contentWidth := m.viewport.Width()

	if len(m.injectedFiles) > 0 {
		for i, f := range m.injectedFiles {
			line := styles.FileStyle.Width(contentWidth).Render(fmt.Sprintf("📎 File #%d: %s", i+1, f))
			writeString(line)
			writeString("\n")
		}
		writeString("\n")
	}

	m.messageViewportOffsets = make([]int, 0, len(m.runtimeMessages))
	m.blockViewportOffsets = make([][]int, 0, len(m.runtimeMessages))

	for i, rm := range m.runtimeMessages {
		if i > 0 {
			writeString("\n\n")
		}
		m.messageViewportOffsets = append(m.messageViewportOffsets, currentLine)

		blockOffsets := make([]int, 0, len(rm.Blocks))

		switch rm.Type {
		case types.RuntimeMessageTypeUser:
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
			bytes, _ := pbutil.JSONMarshalPretty(rm.ToolCall.Arguments)
			writeString(styles.ToolLabelStyle.Render(fmt.Sprintf("🔧 Tool: %s", rm.ToolCall.Name)))
			writeString("\n")
			writeString(styles.ToolCallStyle.Render(string(bytes)))

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

		if rm.Err != nil {
			writeString(styles.MessageErrorStyle.Render("\nError: " + rm.Err.Error()))
		}
	}
	return b.String()
}

// renderConfirmDialog renders the tool call confirmation dialog box showing the
// command to be executed and its optional working directory.
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
