package search

import (
	"fmt"
	"strings"
	"time"

	"github.com/malonaz/sgpt/cli/tui/styles"
)

func (m *Model) View() string {
	if !m.ready {
		return "Loading..."
	}

	var b strings.Builder

	header := styles.TitleStyle.Width(m.width).Render(" 🔍 Search Chats ")
	b.WriteString(header)
	b.WriteString("\n")

	b.WriteString(styles.SearchInputStyle.Width(m.width - 2).Render(m.queryInput.View()))
	b.WriteString("\n")

	b.WriteString(m.viewport.View())

	helpText := "Type to search │ C-p/C-n: navigate results │ Enter: open │ Esc: clear/close"
	b.WriteString("\n")
	b.WriteString(styles.HelpStyle.Render(helpText))

	return b.String()
}

func (m *Model) renderResults() string {
	if m.loading {
		return styles.DimTextStyle.Render("Searching...")
	}
	if m.err != nil {
		return styles.ErrorStyle.Render(fmt.Sprintf("Error: %v", m.err))
	}
	if m.lastQuery == "" {
		return styles.DimTextStyle.Render("Type a query to search chat history")
	}
	if len(m.results) == 0 {
		return styles.DimTextStyle.Render("No results found")
	}

	var b strings.Builder
	for i, chat := range m.results {
		title := chat.GetMetadata().GetTitle()
		if title == "" {
			title = "(untitled)"
		}
		title = styles.Truncate(title, 40)

		chatID := chat.Name
		if strings.HasPrefix(chatID, "chats/") {
			chatID = chatID[6:]
		}
		if len(chatID) > 8 {
			chatID = chatID[:8]
		}

		messageCount := len(chat.GetMetadata().GetMessages())
		created := chat.GetCreateTime().AsTime().Format(time.DateOnly)

		line := fmt.Sprintf("  %-10s %-40s %d msgs  %s", chatID, title, messageCount, created)

		style := styles.MenuItemStyle
		if i == m.cursor {
			style = styles.MenuSelectedStyle
		}
		b.WriteString(style.Width(m.width).Render(line))
		if i < len(m.results)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}
