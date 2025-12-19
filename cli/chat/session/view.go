package session

import (
	"errors"
	"fmt"
	"strings"

	aipb "github.com/malonaz/core/genproto/ai/v1"

	"github.com/malonaz/sgpt/cli/chat/styles"
)

// View renders the model.
func (m *Model) View() string {
	if m.quitting {
		return ""
	}

	if !m.ready {
		return "Initializing..."
	}

	if m.viewerMode && m.viewerModel != nil {
		return m.viewerModel.View()
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
			b.WriteString(fmt.Sprintf("%s Generating...\n", m.spinner.View()))
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

	return b.String()
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

func (m *Model) renderMessages() string {
	var b strings.Builder

	contentWidth := m.viewport.Width

	if len(m.injectedFiles) > 0 {
		for i, f := range m.injectedFiles {
			b.WriteString(styles.FileStyle.Width(contentWidth).Render(fmt.Sprintf("📎 File #%d: %s", i+1, f)))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	for i, rm := range m.runtimeMessages {
		if i > 0 {
			b.WriteString("\n\n")
		}
		msg := rm.Message
		switch msg.Role {
		case aipb.Role_ROLE_USER:
			rendered := m.renderer.ToMarkdown(msg.Content, i, true)
			b.WriteString(styles.UserMessageStyle.Render(rendered))

		case aipb.Role_ROLE_ASSISTANT:
			if msg.Reasoning != "" {
				b.WriteString(styles.ThoughtLabelStyle.Render("💭 Thinking:"))
				b.WriteString("\n")
				b.WriteString(styles.ThoughtStyle.Render(msg.Reasoning))
			}
			rendered := m.renderer.ToMarkdown(msg.Content, i, true)
			b.WriteString(styles.AIMessageStyle.Render(rendered))

			if len(msg.ToolCalls) > 0 {
				for _, tc := range msg.ToolCalls {
					b.WriteString("\n")
					b.WriteString(styles.ToolLabelStyle.Render(fmt.Sprintf("🔧 Tool: %s", tc.Name)))
					b.WriteString("\n")
					b.WriteString(styles.ToolCallStyle.Render(tc.Arguments))
				}
			}

			if rm.Err != nil {
				b.WriteString("\n")
				if errors.Is(rm.Err, errUserInterrupt) {
					b.WriteString(styles.MessageInterruptStyle.Render("⚡ Interrupted by user"))
				} else {
					b.WriteString(styles.MessageErrorStyle.Render(fmt.Sprintf("⚠️ %v", rm.Err)))
				}
			}

		case aipb.Role_ROLE_TOOL:
			b.WriteString(styles.ToolLabelStyle.Render("⚡ Tool Result:"))
			b.WriteString("\n")
			rendered := m.renderer.ToMarkdown(msg.Content, i, true)
			b.WriteString(styles.ToolResultStyle.Render(rendered))

		case aipb.Role_ROLE_SYSTEM:
			b.WriteString(styles.SystemStyle.Render(fmt.Sprintf("System: %s", styles.Truncate(msg.Content, styles.TruncateLength))))
		}
	}

	if m.streaming || m.currentResponse.Len() > 0 || m.currentReasoning.Len() > 0 {
		b.WriteString("\n\n")
		if m.currentReasoning.Len() > 0 {
			b.WriteString(styles.ThoughtLabelStyle.Render("💭 Thinking:"))
			b.WriteString("\n")
			b.WriteString(styles.ThoughtStyle.Render(m.currentReasoning.String()))
			b.WriteString("\n")
		}
		if m.currentResponse.Len() > 0 {
			rendered := m.renderer.ToMarkdown(m.currentResponse.String(), -1, false)
			b.WriteString(styles.AIMessageStyle.Render(rendered))
		}
		if m.streaming {
			b.WriteString(styles.SpinnerStyle.Render("▋"))
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
