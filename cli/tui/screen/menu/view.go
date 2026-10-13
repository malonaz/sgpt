package menu

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	aipb "github.com/malonaz/core/genproto/ai/v1"

	"github.com/malonaz/sgpt/cli/tui/format"
	"github.com/malonaz/sgpt/cli/tui/styles"
	"github.com/malonaz/sgpt/cli/tui/timeline"
	"github.com/malonaz/sgpt/internal/store"
)

const (
	tagsMaxWidth = 18
	// rowMinTitleWidth is the title room below which tags are dropped.
	rowMinTitleWidth = 28
	// rowMinPriceWidth is the row width below which the price column goes.
	rowMinPriceWidth = 46
)

var (
	labelStyle     = lipgloss.NewStyle().Foreground(styles.ColorMuted).Bold(true)
	sectionStyle   = lipgloss.NewStyle().Foreground(styles.ColorSubtle).PaddingLeft(2)
	filterBoxStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	previewTitle   = lipgloss.NewStyle().Foreground(styles.ColorText).Bold(true)
	agentStyle     = lipgloss.NewStyle().Foreground(styles.ColorAgent)
	errorStyle     = lipgloss.NewStyle().Foreground(styles.ColorDanger)
)

func (m *Model) View() string {
	if !m.ready {
		return ""
	}
	left := strings.Join([]string{m.renderFilter(), m.renderList()}, "\n")
	body := left
	if detailWidth := m.detailWidth(); detailWidth > 0 && m.width >= minPreviewWidth {
		height := m.height - 1 - footerHeight
		separator := styles.SubtleStyle.Render(strings.TrimSuffix(strings.Repeat(styles.GlyphRule+"\n", height), "\n"))
		left = lipgloss.NewStyle().Width(m.listWidth()).Height(height).Render(left)
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, separator, m.detailViewport.View())
	}
	return strings.Join([]string{m.renderHeader(), body, m.renderFooter()}, "\n")
}

// renderHeader is a quiet label with the list's honest status at the right.
func (m *Model) renderHeader() string {
	left := " " + labelStyle.Render("Chats")
	if m.showAgents {
		left += styles.SubtleStyle.Render(" " + styles.GlyphSeparator + " incl. sub-agents")
	}
	right := styles.MutedStyle.Render(m.status()) + " "
	gap := max(1, m.width-lipgloss.Width(left)-lipgloss.Width(right))
	return left + strings.Repeat(" ", gap) + right
}

func (m *Model) status() string {
	switch {
	case m.err != nil:
		return ""
	case m.filterText != "" && (m.loading || m.loadingMore || m.nextPageToken != ""):
		return "Searching" + styles.GlyphEllipsis
	case m.loading:
		return "Loading" + styles.GlyphEllipsis
	case m.loadingMore:
		return "Loading more" + styles.GlyphEllipsis
	}
	noun := "chats"
	switch {
	case m.filterText != "" && len(m.displayed) == 1:
		noun = "match"
	case m.filterText != "":
		noun = "matches"
	}
	count := fmt.Sprintf("%d", len(m.displayed))
	if m.filterText == "" && m.nextPageToken != "" {
		// More pages exist server-side; they load as the cursor nears them.
		count += "+"
	}
	return count + " " + noun
}

func (m *Model) renderFilter() string {
	border := styles.ColorSubtle
	if m.focused && m.focusTarget == FocusFilter {
		border = styles.ColorBrand
	}
	return filterBoxStyle.BorderForeground(border).Width(m.listWidth() - 1).Render(m.filterInput.View())
}

func (m *Model) renderFooter() string {
	hints := []string{keyDown.Hint()}
	if m.focusTarget == FocusChatList {
		hints = []string{keyOpen.Hint(), keyDelete.Hint(), keyMenuFavorite.Hint(), keyBack.Hint()}
	}
	hints = append(hints, keyToggleAgents.Hint(), keyRefresh.Hint())
	// Drop trailing hints rather than wrap.
	for len(hints) > 1 && lipgloss.Width(styles.Hints(hints...)) > m.width-2 {
		hints = hints[:len(hints)-1]
	}
	return " " + styles.Hints(hints...)
}

// renderList materializes only the visible window of the virtualized list —
// O(list height), not O(loaded chats).
func (m *Model) renderList() string {
	if m.err != nil {
		message := ansi.Wordwrap(m.err.Error(), max(10, m.listWidth()-4), "")
		return "\n" + errorStyle.Render("  Couldn't load chats") + "\n" +
			lipgloss.NewStyle().Foreground(styles.ColorMuted).PaddingLeft(2).Render(message) + "\n\n  " + keyRefresh.Hint()
	}
	if len(m.displayed) == 0 {
		switch {
		case m.loading:
			return "\n" + styles.MutedStyle.Render("  Loading chats"+styles.GlyphEllipsis)
		case m.filterText != "" && m.nextPageToken != "":
			return "\n" + styles.MutedStyle.Render("  Searching"+styles.GlyphEllipsis)
		case m.filterText != "":
			return "\n" + styles.MutedStyle.Render(fmt.Sprintf("  No chats match %q", m.filterText))
		}
		return "\n" + styles.MutedStyle.Render("  No chats yet")
	}

	bottom := min(len(m.listLines), m.listYOffset+m.listHeight)
	visible := make([]string, 0, m.listHeight)
	for _, line := range m.listLines[m.listYOffset:bottom] {
		if line.chatIndex >= 0 {
			visible = append(visible, m.renderChatRow(m.displayed[line.chatIndex], line.chatIndex))
		} else {
			visible = append(visible, line.text)
		}
	}
	return strings.Join(visible, "\n")
}

// renderChatRow renders (and caches) a single row; selection participates in
// the cache key, so cursor moves are pure map lookups once both variants of
// a row have been rendered.
func (m *Model) renderChatRow(chat *aipb.Chat, index int) string {
	selected := m.focusTarget == FocusChatList && index == m.chatCursor
	cacheKey := chat.GetName()
	if selected {
		cacheKey += "\x00"
	}
	if row, ok := m.rowCache[cacheKey]; ok {
		return row
	}

	// Every segment carries the row background, so inner resets can't cut
	// the selection highlight short.
	base := lipgloss.NewStyle()
	bar := " "
	if selected {
		base = base.Background(styles.ColorSurface)
		bar = base.Foreground(styles.ColorBrand).Render(styles.GlyphSelected)
	}
	muted := base.Foreground(styles.ColorMuted)
	width := m.listWidth() - 1

	meta := fmt.Sprintf("%3s", format.Age(chat.GetUpdateTime().AsTime()))
	// Narrow lists keep the title readable; the preview still shows cost.
	if width >= rowMinPriceWidth {
		meta += fmt.Sprintf("  %6s", format.Price(chat.GetPrice()))
	}
	meta = muted.Render(meta)
	prefix := bar + base.Render(" ")
	if store.IsAgentChat(chat) {
		prefix += base.Foreground(styles.ColorAgent).Render(styles.GlyphChild + " ")
	}
	titleWidth := width - lipgloss.Width(prefix) - lipgloss.Width(meta) - 3
	if tags := strings.Join(store.Tags(chat), " "); tags != "" && titleWidth-tagsMaxWidth-2 >= rowMinTitleWidth {
		tags = ansi.Truncate(tags, min(tagsMaxWidth, lipgloss.Width(tags)), styles.GlyphEllipsis)
		meta = muted.Render(tags+"  ") + meta
		titleWidth -= lipgloss.Width(tags) + 2
	}
	title := base.Foreground(styles.ColorText).Render(ansi.Truncate(chatTitle(chat), max(1, titleWidth), styles.GlyphEllipsis))
	gap := max(1, width-lipgloss.Width(prefix)-lipgloss.Width(title)-lipgloss.Width(meta)-1)
	row := prefix + title + base.Render(strings.Repeat(" ", gap)) + meta + base.Render(" ")
	m.rowCache[cacheKey] = row
	return row
}

// chatTitle is the chat's title, else its ID: a chat may predate titling.
func chatTitle(chat *aipb.Chat) string {
	if title := chat.GetTitle(); title != "" {
		return title
	}
	return resourceID(chat.GetName())
}

func resourceID(name string) string {
	return name[strings.LastIndex(name, "/")+1:]
}

// renderDetail previews the selected chat: a header, then the conversation
// through the shared timeline items (the chat screen's rendering path).
// Results are cached per chat name in updateSelection.
func (m *Model) renderDetail() string {
	width := m.detailWidth()
	chat := m.selectedChat()
	if chat == nil {
		return ""
	}
	textWidth := max(1, width-4)
	indent := lipgloss.NewStyle().PaddingLeft(2)

	title := chatTitle(chat)
	if store.IsFavorite(chat) {
		title += " ★"
	}
	lines := []string{"", indent.Render(previewTitle.Width(textWidth).Render(title))}

	facts := []string{}
	if model := store.CurrentModel(chat); model != "" {
		facts = append(facts, resourceID(model))
	}
	facts = append(facts, format.Age(chat.GetUpdateTime().AsTime())+" ago", format.Price(chat.GetPrice()), resourceID(chat.GetName()))
	lines = append(lines, indent.Render(styles.MutedStyle.Width(textWidth).Render(strings.Join(facts, " "+styles.GlyphSeparator+" "))))
	if parent := m.parentChatLabel(chat); parent != "" {
		lines = append(lines, indent.Render(agentStyle.Render(ansi.Truncate(styles.GlyphChild+" launched by "+parent, textWidth, styles.GlyphEllipsis))))
	}
	if tags := store.Tags(chat); len(tags) > 0 {
		lines = append(lines, indent.Render(styles.MutedStyle.Width(textWidth).Render(strings.Join(tags, "  "))))
	}
	lines = append(lines, indent.Render(styles.SubtleStyle.Render(strings.Repeat("─", textWidth))), "")

	messages, loaded := m.messagesCache[chat.GetName()]
	items := timeline.BuildChatItems(messages, nil)
	switch {
	case !loaded:
		lines = append(lines, indent.Render(styles.MutedStyle.Render("Loading conversation"+styles.GlyphEllipsis)))
	case len(items) == 0:
		lines = append(lines, indent.Render(styles.MutedStyle.Render("No messages yet")))
	default:
		lines = append(lines, timeline.RenderItems(items, m.renderer, m.previewWidth()))
	}
	return strings.Join(lines, "\n")
}
