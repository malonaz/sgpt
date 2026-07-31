package menu

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	aipb "github.com/malonaz/core/genproto/ai/v1"

	"github.com/malonaz/sgpt/cli/tui/styles"
	"github.com/malonaz/sgpt/cli/tui/timeline"
	"github.com/malonaz/sgpt/internal/store"
)

func (m *Model) View() string {
	if !m.ready {
		return "Loading..."
	}

	var b strings.Builder

	header := styles.TitleStyle.Width(m.width).Render(" 📋 Chat History ")
	b.WriteString(header)
	b.WriteString("\n")

	var leftPanel strings.Builder
	filterStyle := m.inputStyle(FocusFilter)
	leftPanel.WriteString(filterStyle.Width(m.listWidth() - 2).Render(m.filterInput.View()))
	leftPanel.WriteString("\n")
	leftPanel.WriteString(m.renderList())

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

// renderList materializes only the visible window of the virtualized list —
// O(list height), not O(loaded chats).
func (m *Model) renderList() string {
	if m.loading {
		return styles.DimTextStyle.Render("Loading chats...")
	}
	if m.err != nil {
		return styles.ErrorStyle.Render(fmt.Sprintf("Error: %v", m.err))
	}

	displayed := m.displayedChats()
	if len(displayed) == 0 {
		if m.filterText != "" {
			return styles.DimTextStyle.Render("No chats match filter")
		}
		return styles.DimTextStyle.Render("No chats yet")
	}

	top := m.listYOffset
	bottom := top + m.listHeight
	if bottom > len(m.listLines) {
		bottom = len(m.listLines)
	}
	visible := make([]string, 0, m.listHeight)
	for _, line := range m.listLines[top:bottom] {
		if line.chatIndex >= 0 {
			visible = append(visible, m.renderChatRow(displayed[line.chatIndex], line.chatIndex))
		} else {
			visible = append(visible, line.text)
		}
	}
	// Pad so the horizontal join keeps the separator at full height.
	for len(visible) < m.listHeight {
		visible = append(visible, "")
	}
	return strings.Join(visible, "\n")
}

// renderChatRow renders (and caches) a single row; selection participates in
// the cache key, so cursor moves are pure map lookups once both variants of
// a row have been rendered.
func (m *Model) renderChatRow(chat *aipb.Chat, globalIndex int) string {
	selected := m.focusTarget == FocusChatList && globalIndex == m.chatCursor
	cacheKey := fmt.Sprintf("%s|%t", chat.GetName(), selected)
	if row, ok := m.rowCache[cacheKey]; ok {
		return row
	}

	title := styles.Truncate(chat.GetTitle(), 28)
	messageCount := len(chat.GetMetadata().GetMessages())
	updated := relativeTime(chat.GetUpdateTime().AsTime())
	tags := styles.Truncate(strings.Join(store.Tags(chat), ","), 15)

	line := fmt.Sprintf("%-30s %-5d %-10s", title, messageCount, updated)
	coloredTags := styles.MenuTagStyle.Render(tags)

	style := styles.MenuItemStyle
	if selected {
		style = styles.MenuSelectedStyle
	}
	row := style.Width(m.listWidth()).Render(line + coloredTags)
	m.rowCache[cacheKey] = row
	return row
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
	if store.IsFavorite(chat) {
		title = "⭐ " + title
	}
	b.WriteString(styles.MenuTitleStyle.Render(" " + styles.Truncate(title, detailWidth-2)))
	b.WriteString("\n")
	if model := store.CurrentModel(chat); model != "" {
		b.WriteString(styles.DimTextStyle.Render(" Model: " + model))
		b.WriteString("\n")
	}
	if tags := store.Tags(chat); len(tags) > 0 {
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

// renderFragments shows why the selected chat matched the search query,
// above the regular preview. Fragments carry ANSI highlighting from bleve.
func (m *Model) renderFragments(fragments []string) string {
	detailWidth := m.detailWidth()
	var b strings.Builder
	b.WriteString(styles.MenuTitleStyle.Render(" 🔍 Matches"))
	b.WriteString("\n")
	for _, fragment := range fragments {
		// Flatten: multi-line fragments would blow up the pane.
		b.WriteString(" " + strings.Join(strings.Fields(fragment), " "))
		b.WriteString("\n")
	}
	b.WriteString(styles.DividerStyle.Render(strings.Repeat("─", detailWidth)))
	b.WriteString("\n")
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
