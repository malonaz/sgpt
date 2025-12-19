package session

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	aipb "github.com/malonaz/core/genproto/ai/v1"

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
		if m.streaming {
			//b.WriteString(fmt.Sprintf("%s Generating...\n", m.spinner.View()))
		} else {
			b.WriteString(styles.TextAreaStyle.Render(m.textarea.View()))
			b.WriteString("\n")
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
	roleName := "anon"
	if m.opts.Role != nil {
		roleName = m.opts.Role.Name
	}

	reasoningStr := "none"
	switch m.opts.ReasoningEffort {
	case aipb.ReasoningEffort_REASONING_EFFORT_LOW:
		reasoningStr = "low"
	case aipb.ReasoningEffort_REASONING_EFFORT_MEDIUM:
		reasoningStr = "medium"
	case aipb.ReasoningEffort_REASONING_EFFORT_HIGH:
		reasoningStr = "high"
	}

	toolsStr := ""
	if m.opts.EnableTools {
		toolsStr = " 🔧"
	}

	title := fmt.Sprintf(" 🤖 %s │ 👤 %s │ 💬 %s │ 🧠 %s%s ",
		m.opts.Model, roleName, m.opts.ChatID, reasoningStr, toolsStr)

	return styles.TitleStyle.Width(m.width).Render(title)
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
			rendered := m.renderer.ToMarkdown(rm.Content, i, true)
			style := m.getStyle(styles.UserMessageStyle, i)
			writeString(style.Render(rendered))

		case types.RuntimeMessageTypeThinking:
			style := m.getStyle(styles.AIThoughtStyle, i)
			rendered := m.renderer.ToMarkdown(rm.Content, i, true)
			writeString(style.Render(rendered))

		case types.RuntimeMessageTypeAssistant:
			style := m.getStyle(styles.AIMessageStyle, i)
			rendered := m.renderer.ToMarkdown(rm.Content, i, true)
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
				writeString(styles.ToolResultStyle.Render(rm.Content))
			} else {
				rendered := m.renderer.ToMarkdown(rm.Content, i, true)
				writeString(styles.ToolResultStyle.Render(rendered))
			}

		case types.RuntimeMessageTypeSystem:
			writeString(styles.SystemStyle.Render(fmt.Sprintf("System: %s", styles.Truncate(rm.Content, styles.TruncateLength))))
		}
	}

	// Render streaming content
	if m.streaming || m.currentResponse.Len() > 0 || m.currentReasoning.Len() > 0 {
		writeString("\n\n")
		if m.currentReasoning.Len() > 0 {
			writeString(styles.ThoughtLabelStyle.Render("💭 Thinking:"))
			writeString("\n")
			writeString(styles.ThoughtStyle.Render(m.currentReasoning.String()))
			writeString("\n")
		}
		if m.currentResponse.Len() > 0 {
			rendered := m.renderer.ToMarkdown(m.currentResponse.String(), -1, false)
			style := m.getStyle(styles.AIMessageStyle, -1)
			writeString(style.Render(rendered))
		}
		if m.streaming {
			writeString(styles.SpinnerStyle.Render("▋"))
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
