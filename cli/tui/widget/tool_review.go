package widget

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/pbutil"

	"github.com/malonaz/sgpt/cli/tui/styles"
	"github.com/malonaz/sgpt/internal/tools"
)

type ToolReview struct {
	toolCalls []*aipb.ToolCall
	cursor    int
	input     textarea.Model
	width     int
}

func NewToolReview() *ToolReview {
	ta := textarea.New()
	ta.Placeholder = "Empty = accept, type reason = reject (Ctrl+J to confirm)"
	ta.CharLimit = 0
	ta.SetHeight(1)
	ta.ShowLineNumbers = false
	ta.Prompt = ""

	return &ToolReview{
		input: ta,
	}
}

func (t *ToolReview) SetToolCalls(toolCalls []*aipb.ToolCall) {
	if len(toolCalls) == 0 {
		t.toolCalls = nil
		t.cursor = 0
		t.input.Blur()
		return
	}
	t.toolCalls = toolCalls
	t.cursor = 0
	t.input.Reset()
	t.input.Focus()
	t.advanceToNextPending()
}

func (t *ToolReview) Active() bool {
	for _, toolCall := range t.toolCalls {
		if tools.GetToolCallStatus(toolCall) == tools.ToolCallStatusPending {
			return true
		}
	}
	return false
}

func (t *ToolReview) AllResolved() bool {
	return !t.Active()
}

func (t *ToolReview) currentToolCall() *aipb.ToolCall {
	if t.cursor < 0 || t.cursor >= len(t.toolCalls) {
		return nil
	}
	return t.toolCalls[t.cursor]
}

func (t *ToolReview) NextToolCall() {
	for i := t.cursor + 1; i < len(t.toolCalls); i++ {
		if tools.GetToolCallStatus(t.toolCalls[i]) == tools.ToolCallStatusPending {
			t.cursor = i
			return
		}
	}
}

func (t *ToolReview) PrevToolCall() {
	for i := t.cursor - 1; i >= 0; i-- {
		if tools.GetToolCallStatus(t.toolCalls[i]) == tools.ToolCallStatusPending {
			t.cursor = i
			return
		}
	}
}

func (t *ToolReview) AcceptCurrent() {
	toolCall := t.currentToolCall()
	if toolCall == nil {
		return
	}
	tools.SetToolCallStatus(toolCall, tools.ToolCallStatusAccepted)
	t.advanceToNextPending()
}

func (t *ToolReview) RejectCurrent(reason string) {
	toolCall := t.currentToolCall()
	if toolCall == nil {
		return
	}
	tools.SetToolCallStatus(toolCall, tools.ToolCallStatusRejected)
	tools.SetToolCallRejectionReason(toolCall, reason)
	t.advanceToNextPending()
}

func (t *ToolReview) advanceToNextPending() {
	for i := 0; i < len(t.toolCalls); i++ {
		if tools.GetToolCallStatus(t.toolCalls[i]) == tools.ToolCallStatusPending {
			t.cursor = i
			return
		}
	}
}

func (t *ToolReview) ResetInput() {
	t.input.Reset()
}

func (t *ToolReview) InputValue() string {
	return strings.TrimSpace(t.input.Value())
}

func (t *ToolReview) SetWidth(width int) {
	t.width = width
	t.input.SetWidth(width - styles.TextAreaStyle.GetHorizontalPadding() - styles.TextAreaStyle.GetHorizontalBorderSize())
}

func (t *ToolReview) UpdateInput(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	t.input, cmd = t.input.Update(msg)
	return cmd
}

func (t *ToolReview) Height() int {
	return lipgloss.Height(t.View())
}

func (t *ToolReview) View() string {
	if !t.Active() {
		return ""
	}

	resolvedCount := 0
	for _, toolCall := range t.toolCalls {
		status := tools.GetToolCallStatus(toolCall)
		if status == tools.ToolCallStatusAccepted || status == tools.ToolCallStatusRejected {
			resolvedCount++
		}
	}

	var b strings.Builder

	var statusParts []string
	for i, toolCall := range t.toolCalls {
		name := toolCall.GetName()
		status := tools.GetToolCallStatus(toolCall)
		var marker string
		switch status {
		case tools.ToolCallStatusAccepted:
			marker = lipgloss.NewStyle().Foreground(styles.SuccessColor).Render("●")
		case tools.ToolCallStatusRejected:
			marker = lipgloss.NewStyle().Foreground(styles.ErrorColor).Render("●")
		default:
			if i == t.cursor {
				marker = "▶"
			} else {
				marker = lipgloss.NewStyle().Foreground(styles.MutedColor).Render("●")
			}
		}
		statusParts = append(statusParts, fmt.Sprintf("%s %s", marker, name))
	}

	headerStyle := lipgloss.NewStyle().
		Foreground(styles.AccentColor).
		Bold(true)
	b.WriteString(headerStyle.Render(fmt.Sprintf("Tool Review (%d/%d)", resolvedCount+1, len(t.toolCalls))))
	b.WriteString("\n")

	b.WriteString(styles.DimTextStyle.Render(strings.Join(statusParts, " │ ")))
	b.WriteString("\n")

	toolCall := t.currentToolCall()
	if toolCall != nil {
		metadata, _ := tools.ParseToolCallMetadata(toolCall)
		displayMessage := ""
		if metadata != nil && metadata.GetDisplayMessage().GetContent() != "" {
			displayMessage = metadata.GetDisplayMessage().GetContent()
		}

		detailStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(styles.AccentColor).
			Padding(0, 1).
			Width(t.width - 4)

		var detail strings.Builder
		detail.WriteString(styles.ToolLabelStyle.Render(fmt.Sprintf("🔧 %s", toolCall.GetName())))
		if displayMessage != "" {
			detail.WriteString("\n")
			detail.WriteString(displayMessage)
		} else {
			bytes, _ := pbutil.JSONMarshalPretty(toolCall.GetArguments())
			body := string(bytes)
			lines := strings.Split(body, "\n")
			if len(lines) > 15 {
				body = strings.Join(lines[:15], "\n") + fmt.Sprintf("\n... (%d more lines)", len(lines)-15)
			}
			detail.WriteString("\n")
			detail.WriteString(body)
		}
		b.WriteString(detailStyle.Render(detail.String()))
		b.WriteString("\n")
	}

	b.WriteString(styles.TextAreaStyle.Render(t.input.View()))
	b.WriteString("\n")
	b.WriteString(styles.HelpStyle.Render("Ctrl+J: accept/reject │ Ctrl+P/N: navigate │ Empty=accept, type reason=reject"))

	return b.String()
}
