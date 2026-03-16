// cli/tui/screen/menu/view.go
package menu

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/ai"

	"github.com/malonaz/sgpt/cli/tui/styles"
	"github.com/malonaz/sgpt/internal/markdown"
)

func (m *Model) View() string {
	if !m.ready {
		return "Loading..."
	}

	var b strings.Builder

	switch m.viewMode {
	case ViewModeSearch:
		header := styles.TitleStyle.Width(m.width).Render(" 🔍 Search Chats ")
		b.WriteString(header)
		b.WriteString("\n")
		b.WriteString(styles.SearchInputStyle.Width(m.listWidth() - 2).Render(m.searchInput.View()))
		b.WriteString("\n")
	default:
		header := styles.TitleStyle.Width(m.width).Render(fmt.Sprintf(" 📋 Chat History (page %d) ", m.currentPage()))
		b.WriteString(header)
		b.WriteString("\n")
		if m.filtering {
			b.WriteString(styles.SearchInputStyle.Width(m.listWidth() - 2).Render(m.filterInput.View()))
			b.WriteString("\n")
		}
	}

	listPanel := m.listViewport.View()
	detailPanel := m.detailViewport.View()
	separator := lipgloss.NewStyle().Foreground(styles.BorderColor).Render(
		strings.Repeat("│\n", m.listViewport.Height()),
	)

	joined := lipgloss.JoinHorizontal(lipgloss.Top, listPanel, separator, detailPanel)
	b.WriteString(joined)

	b.WriteString("\n")
	switch m.viewMode {
	case ViewModeSearch:
		b.WriteString(styles.HelpStyle.Render("Type to search │ C-p/C-n: navigate │ Enter: open │ Esc: clear/back"))
	default:
		var pagination strings.Builder
		if m.hasPreviousPage() {
			pagination.WriteString("◀ [ ")
		}
		pagination.WriteString(fmt.Sprintf("page %d", m.currentPage()))
		if m.hasNextPage() {
			pagination.WriteString(" ] ▶")
		}
		helpText := fmt.Sprintf("C-p/C-n: navigate │ Enter: open │ d: delete │ /: filter │ C-/: search │ r: refresh │ %s", pagination.String())
		b.WriteString(styles.HelpStyle.Render(helpText))
	}

	return b.String()
}

func (m *Model) renderList() string {
	if m.viewMode == ViewModeSearch {
		return m.renderSearchResults()
	}

	if m.loading {
		return styles.DimTextStyle.Render("Loading chats...")
	}
	if m.err != nil {
		return styles.ErrorStyle.Render(fmt.Sprintf("Error: %v", m.err))
	}

	filtered := m.filteredChats()
	if len(filtered) == 0 {
		if m.filterText != "" {
			return styles.DimTextStyle.Render("No chats match filter")
		}
		return styles.DimTextStyle.Render("No chats yet")
	}

	listWidth := m.listWidth()
	headerFormat := "  %-10s %-18s %-5s %-10s %s"
	headerLine := fmt.Sprintf(headerFormat, "ID", "Title", "Msgs", "Created", "Updated")
	var b strings.Builder
	b.WriteString(styles.MenuHeaderStyle.Width(listWidth).Render(headerLine))
	b.WriteString("\n")

	for i, chat := range filtered {
		title := chat.GetMetadata().GetTitle()
		title = styles.Truncate(title, 16)

		messageCount := len(chat.GetMetadata().GetMessages())
		created := chat.GetCreateTime().AsTime().Format(time.DateOnly)
		updated := relativeTime(chat.GetUpdateTime().AsTime())

		chatID := chat.Name
		if strings.HasPrefix(chatID, "chats/") {
			chatID = chatID[6:]
		}
		if len(chatID) > 8 {
			chatID = chatID[:8]
		}

		line := fmt.Sprintf("  %-10s %-18s %-5d %-10s %s", chatID, title, messageCount, created, updated)

		style := styles.MenuItemStyle
		if i == m.cursor {
			style = styles.MenuSelectedStyle
		}
		b.WriteString(style.Width(listWidth).Render(line))
		if i < len(filtered)-1 {
			b.WriteString("\n")
		}
	}

	return b.String()
}

func (m *Model) renderSearchResults() string {
	if m.searchLoading {
		return styles.DimTextStyle.Render("Searching...")
	}
	if m.searchErr != nil {
		return styles.ErrorStyle.Render(fmt.Sprintf("Error: %v", m.searchErr))
	}
	if m.lastSearchQuery == "" {
		return styles.DimTextStyle.Render("Type a query to search chat history")
	}
	if len(m.searchResults) == 0 {
		return styles.DimTextStyle.Render("No results found")
	}

	listWidth := m.listWidth()
	var b strings.Builder
	for i, chat := range m.searchResults {
		title := chat.GetMetadata().GetTitle()
		if title == "" {
			title = "(untitled)"
		}
		title = styles.Truncate(title, 16)

		chatID := chat.Name
		if strings.HasPrefix(chatID, "chats/") {
			chatID = chatID[6:]
		}
		if len(chatID) > 8 {
			chatID = chatID[:8]
		}

		messageCount := len(chat.GetMetadata().GetMessages())
		created := chat.GetCreateTime().AsTime().Format(time.DateOnly)

		line := fmt.Sprintf("  %-10s %-18s %d msgs  %s", chatID, title, messageCount, created)

		style := styles.MenuItemStyle
		if i == m.searchCursor {
			style = styles.MenuSelectedStyle
		}
		b.WriteString(style.Width(listWidth).Render(line))
		if i < len(m.searchResults)-1 {
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
					content, _ := ai.ParseToolResult(toolResult)
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
