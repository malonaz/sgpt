package menu

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	aipb "github.com/malonaz/core/genproto/ai/v1"

	"github.com/malonaz/sgpt/cli/tui/styles"
	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/markdown"
)

func (m *Model) View() string {
	if !m.ready {
		return "Loading..."
	}

	var b strings.Builder

	modeLabel := "List"
	if m.searchQuery != "" {
		modeLabel = "Search"
	}
	header := styles.TitleStyle.Width(m.width).Render(fmt.Sprintf(" 📋 Chat History (%s, page %d) ", modeLabel, m.currentPage()))
	b.WriteString(header)
	b.WriteString("\n")

	var leftPanel strings.Builder
	filterStyle := m.inputStyle(FocusFilter)
	leftPanel.WriteString(filterStyle.Width(m.listWidth() - 2).Render(m.filterInput.View()))
	leftPanel.WriteString("\n")
	searchStyle := m.inputStyle(FocusSearch)
	leftPanel.WriteString(searchStyle.Width(m.listWidth() - 2).Render(m.searchInput.View()))
	leftPanel.WriteString("\n")
	leftPanel.WriteString(m.listViewport.View())

	detailPanel := m.detailViewport.View()
	separator := lipgloss.NewStyle().Foreground(styles.BorderColor).Render(
		strings.Repeat("│\n", m.height-3),
	)

	joined := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel.String(), separator, detailPanel)
	b.WriteString(joined)

	b.WriteString("\n")
	var pagination strings.Builder
	if m.hasPreviousPage() {
		pagination.WriteString("◀ [ ")
	}
	pagination.WriteString(fmt.Sprintf("page %d", m.currentPage()))
	if m.hasNextPage() {
		pagination.WriteString(" ] ▶")
	}
	helpText := fmt.Sprintf("C-p/C-n: navigate │ Enter: open │ Alt+d: delete │ Alt+r: refresh │ %s", pagination.String())
	b.WriteString(styles.HelpStyle.Render(helpText))

	return b.String()
}

func (m *Model) inputStyle(target FocusTarget) lipgloss.Style {
	if m.focusTarget == target {
		return styles.SearchInputStyle.BorderForeground(styles.PrimaryColor)
	}
	return styles.SearchInputStyle.BorderForeground(styles.BorderColor)
}

func (m *Model) renderList() string {
	if m.loading {
		return styles.DimTextStyle.Render("Loading chats...")
	}
	if m.err != nil {
		return styles.ErrorStyle.Render(fmt.Sprintf("Error: %v", m.err))
	}

	displayed := m.displayedChats()
	if len(displayed) == 0 {
		if m.searchQuery != "" {
			return styles.DimTextStyle.Render("No search results")
		}
		if m.filterText != "" {
			return styles.DimTextStyle.Render("No chats match filter")
		}
		return styles.DimTextStyle.Render("No chats yet")
	}

	listWidth := m.listWidth()

	headerFormat := "%-30s %-5s %-10s %s"
	headerLine := fmt.Sprintf(headerFormat, "Title", "Msgs", "Updated", "Tags")
	var b strings.Builder
	b.WriteString(styles.MenuHeaderStyle.Width(listWidth).Render(headerLine))
	b.WriteString("\n")
	b.WriteString(m.renderChatRows(displayed, listWidth))
	return b.String()
}

func (m *Model) renderChatRows(chats []*sgptpb.Chat, listWidth int) string {
	var b strings.Builder
	for i, chat := range chats {
		title := chat.GetMetadata().GetTitle()
		title = styles.Truncate(title, 28)

		messageCount := len(chat.GetMetadata().GetMessages())
		updated := relativeTime(chat.GetUpdateTime().AsTime())

		tags := strings.Join(chat.GetTags(), ",")
		tags = styles.Truncate(tags, 15)

		line := fmt.Sprintf("%-30s %-5d %-10s", title, messageCount, updated)
		coloredTags := styles.MenuTagStyle.Render(tags)

		style := styles.MenuItemStyle
		if m.focusTarget == FocusChatList && i == m.chatCursor {
			style = styles.MenuSelectedStyle
		}
		b.WriteString(style.Width(listWidth).Render(line + coloredTags))
		if i < len(chats)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (m *Model) renderDetail() string {
	detailWidth := m.detailWidth()

	chat := m.selectedChat()
	if chat == nil {
		return styles.DimTextStyle.Render(" Select a chat to preview")
	}

	messages := chat.GetMetadata().GetMessages()
	if len(messages) == 0 {
		return styles.DimTextStyle.Render(" No messages in this chat")
	}

	var b strings.Builder
	title := chat.GetName()
	b.WriteString(styles.MenuTitleStyle.Render(fmt.Sprintf(" %s", styles.Truncate(title, detailWidth-2))))
	b.WriteString("\n")
	model := chat.GetMetadata().GetCurrentModel()
	if model != "" {
		b.WriteString(styles.DimTextStyle.Render(fmt.Sprintf(" Model: %s", model)))
		b.WriteString("\n")
	}
	if tags := chat.GetTags(); len(tags) > 0 {
		b.WriteString(styles.MenuTagStyle.Render(fmt.Sprintf(" Tags: %s", strings.Join(tags, ", "))))
		b.WriteString("\n")
	}
	b.WriteString(styles.DividerStyle.Render(strings.Repeat("─", detailWidth)))
	b.WriteString("\n")

	contentWidth := detailWidth - 4

	for i, chatMessage := range messages {
		if i > 0 {
			b.WriteString("\n")
		}
		message := chatMessage.GetMessage()
		switch message.GetRole() {
		case aipb.Role_ROLE_USER:
			b.WriteString(styles.UserLabelStyle.Render(" You:"))
			b.WriteString("\n")
			for _, block := range message.GetBlocks() {
				text := block.GetText()
				if text != "" {
					blocks := markdown.ParseBlocks(text)
					rendered := m.renderer.ToMarkdown(-1, false, blocks...)
					b.WriteString("  " + strings.ReplaceAll(rendered, "\n", "\n  "))
					b.WriteString("\n")
				}
			}

		case aipb.Role_ROLE_ASSISTANT:
			b.WriteString(styles.AILabelStyle.Render(" Assistant:"))
			b.WriteString("\n")
			for _, block := range message.GetBlocks() {
				if thought := block.GetThought(); thought != "" {
					blocks := markdown.ParseBlocks(thought)
					rendered := m.renderer.ToMarkdown(-1, false, blocks...)
					b.WriteString(styles.ThoughtStyle.Render("  " + strings.ReplaceAll(rendered, "\n", "\n  ")))
					b.WriteString("\n")
				}
				if text := block.GetText(); text != "" {
					blocks := markdown.ParseBlocks(text)
					rendered := m.renderer.ToMarkdown(-1, false, blocks...)
					b.WriteString("  " + strings.ReplaceAll(rendered, "\n", "\n  "))
					b.WriteString("\n")
				}
				if toolCall := block.GetToolCall(); toolCall != nil {
					b.WriteString(styles.ToolLabelStyle.Render(fmt.Sprintf("  🔧 %s", toolCall.Name)))
					b.WriteString("\n")
				}
			}

		case aipb.Role_ROLE_TOOL:
			b.WriteString(styles.ToolLabelStyle.Render(" ⚡ Tool Result"))
			b.WriteString("\n")
			for _, block := range message.GetBlocks() {
				if toolResult := block.GetToolResult(); toolResult != nil {
					var content string
					if toolResult.GetError() != nil {
						content = fmt.Sprintf("Error: %s", toolResult.GetError().GetMessage())
					} else if structured := toolResult.GetStructuredContent(); structured != nil {
						bytes, _ := structured.MarshalJSON()
						content = string(bytes)
					} else {
						content = toolResult.GetContent()
					}
					if content != "" {
						truncated := styles.Truncate(content, contentWidth*2)
						b.WriteString(styles.DimTextStyle.Render("  " + strings.ReplaceAll(truncated, "\n", "\n  ")))
						b.WriteString("\n")
					}
				}
			}

		case aipb.Role_ROLE_SYSTEM:
			b.WriteString(styles.SystemStyle.Render(fmt.Sprintf(" System: %s", styles.Truncate(blockText(message), 60))))
			b.WriteString("\n")
		}
	}

	return b.String()
}

func blockText(message *aipb.Message) string {
	for _, block := range message.GetBlocks() {
		if text := block.GetText(); text != "" {
			return text
		}
	}
	return ""
}

func relativeTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo ago", int(d.Hours()/(24*30)))
	default:
		return fmt.Sprintf("%dy ago", int(d.Hours()/(24*365)))
	}
}
