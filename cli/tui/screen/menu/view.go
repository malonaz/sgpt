package menu

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/malonaz/sgpt/cli/tui/styles"
	"github.com/malonaz/sgpt/cli/tui/timeline"
	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/store"
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
	header := styles.TitleStyle.Width(m.width).Render(fmt.Sprintf(" 📋 Chat History (%s) ", modeLabel))
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
	status := fmt.Sprintf("%d chats loaded", len(m.displayedChats()))
	if m.loadingMore {
		status += " (loading more...)"
	}
	helpText := fmt.Sprintf("C-p/C-n: navigate │ Enter: open │ Alt+d: delete │ Alt+f: favorite │ Alt+r: refresh │ Alt+h: help │ %s", status)
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
	favCount := m.displayedFavoriteCount()

	var b strings.Builder

	if favCount > 0 {
		sectionHeader := styles.MenuHeaderStyle.Width(listWidth).Render("⭐ Favorites")
		b.WriteString(sectionHeader)
		b.WriteString("\n")
		b.WriteString(m.renderChatRows(displayed[:favCount], listWidth, 0))
		if favCount < len(displayed) {
			b.WriteString("\n\n")
		}
	}

	if favCount < len(displayed) {
		sectionHeader := styles.MenuHeaderStyle.Width(listWidth).Render("📋 Chats")
		b.WriteString(sectionHeader)
		b.WriteString("\n")
		b.WriteString(m.renderChatRows(displayed[favCount:], listWidth, favCount))
	}

	if m.loadingMore {
		b.WriteString("\n")
		b.WriteString(styles.DimTextStyle.Render("  loading more..."))
	}

	return b.String()
}

func (m *Model) renderChatRows(chats []*sgptpb.Chat, listWidth int, globalIndexOffset int) string {
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

		globalIndex := globalIndexOffset + i
		style := styles.MenuItemStyle
		if m.focusTarget == FocusChatList && globalIndex == m.chatCursor {
			style = styles.MenuSelectedStyle
		}
		b.WriteString(style.Width(listWidth).Render(line + coloredTags))
		if i < len(chats)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// renderDetail previews the selected chat using the shared timeline items —
// same rendering path as the chat screen. Results are cached per chat name
// in updateSelection.
func (m *Model) renderDetail() string {
	detailWidth := m.detailWidth()

	chat := m.selectedChat()
	if chat == nil {
		return styles.DimTextStyle.Render(" Select a chat to preview")
	}

	var b strings.Builder
	title := chat.GetName()
	if store.HasTag(chat, store.FavoriteTag) {
		title = "⭐ " + title
	}
	b.WriteString(styles.MenuTitleStyle.Render(" " + styles.Truncate(title, detailWidth-2)))
	b.WriteString("\n")
	if model := chat.GetMetadata().GetCurrentModel(); model != "" {
		b.WriteString(styles.DimTextStyle.Render(" Model: " + model))
		b.WriteString("\n")
	}
	if tags := chat.GetTags(); len(tags) > 0 {
		b.WriteString(styles.MenuTagStyle.Render(" Tags: " + strings.Join(tags, ", ")))
		b.WriteString("\n")
	}
	b.WriteString(styles.DividerStyle.Render(strings.Repeat("─", detailWidth)))
	b.WriteString("\n")

	items := timeline.BuildChatItems(chat.GetMetadata().GetMessages(), nil, "", nil)
	if len(items) == 0 {
		b.WriteString(styles.DimTextStyle.Render(" No messages in this chat"))
		return b.String()
	}
	b.WriteString(timeline.RenderItems(items, m.renderer, detailWidth))
	return b.String()
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
