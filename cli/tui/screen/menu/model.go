package menu

import (
	"context"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	aipb "github.com/malonaz/core/genproto/ai/v1"

	"github.com/malonaz/sgpt/cli/tui/screen"
	"github.com/malonaz/sgpt/cli/tui/styles"
	"github.com/malonaz/sgpt/cli/tui/widget"
	"github.com/malonaz/sgpt/internal/markdown"
	"github.com/malonaz/sgpt/internal/store"
)

const (
	// listPageSize is deliberately large: pages append into an
	// infinite-scroll list rather than paginate.
	listPageSize = 50
	// loadMoreThreshold triggers a background fetch when the cursor gets
	// within this many rows of the end of the loaded list.
	loadMoreThreshold = 10
	// previewCacheBudget bounds the per-chat preview caches (fetched message
	// histories and their rendered markdown). Infinite scroll means an
	// unbounded number of chats can be visited in one session.
	previewCacheBudget = 64
	// filterDebounce batches keystrokes into one server-side search.
	filterDebounce = 200 * time.Millisecond
	// minPreviewWidth is the narrowest terminal that still gets a preview
	// pane; below it the list takes the full width.
	minPreviewWidth = 70
)

type FocusTarget int

const (
	FocusFilter FocusTarget = iota
	FocusChatList
)

type chatsLoadedMsg struct {
	// generation ties the response to the request that asked for it, so
	// results for a superseded filter are dropped.
	generation    int
	Favorites     []*aipb.Chat
	Others        []*aipb.Chat
	NextPageToken string
	Err           error
	// Append marks background loads that extend the list (infinite scroll)
	// instead of replacing it.
	Append bool
}

// filterSettledMsg fires filterDebounce after a keystroke; only the latest
// keystroke's message triggers a search.
type filterSettledMsg struct {
	generation int
}

type chatDeletedMsg struct {
	Name string
	Err  error
}

type chatFavoriteToggledMsg struct {
	Name      string
	Favorited bool
	Err       error
}

// chatMessagesLoadedMsg delivers a chat's message history for the detail
// preview; messages are server-side resources, fetched lazily per chat.
type chatMessagesLoadedMsg struct {
	name     string
	messages []*aipb.Message
	err      error
}

// listLine is one visual line of the chat list. chatIndex >= 0 marks a chat
// row (rendered lazily through rowCache); otherwise text is pre-rendered
// (section headers, spacing).
type listLine struct {
	text      string
	chatIndex int
}

type Model struct {
	ctx   context.Context
	store *store.Store
	wrap  screen.WrapFunc

	favorites []*aipb.Chat
	others    []*aipb.Chat
	// displayed is favorites then others, rebuilt by refreshList.
	displayed []*aipb.Chat

	chatCursor    int
	loading       bool
	loadingMore   bool
	err           error
	nextPageToken string

	// listGeneration increments with every fresh list request; cancelList
	// aborts the request in flight when a newer one supersedes it.
	listGeneration int
	cancelList     context.CancelFunc

	filterInput textarea.Model
	// filterText is the query the list reflects (or is loading);
	// filterGeneration identifies the latest keystroke, so only it searches.
	filterText       string
	filterGeneration int
	// listFilter is the query behind the loaded list; it trails filterText
	// while a search is pending.
	listFilter string
	// showAgents includes sub-agent chats, hidden by default so they don't
	// bury the user's own conversations.
	showAgents bool

	// messagesCache holds each chat's fetched history for the detail
	// preview; loadingMessagesSet guards against duplicate fetches.
	messagesCache      map[string][]*aipb.Message
	loadingMessagesSet map[string]bool
	// previewOrder is the MRU order of cached chat names, driving eviction of
	// messagesCache/detailCache once previewCacheBudget is exceeded.
	previewOrder []string

	focusTarget      FocusTarget
	selectedChatName string

	// detailCache memoizes the (expensive) markdown render of each chat
	// preview, keyed by chat name; invalidated on resize/refresh.
	detailCache map[string]string

	// rowCache memoizes rendered chat rows keyed by name+selection state, so
	// a cursor move costs two map lookups instead of re-styling every row.
	rowCache map[string]string

	// Virtualized list state: listLines is the flattened layout the view
	// slices a window out of; chatIndexToLine locates a chat's row line for
	// cursor scrolling.
	listLines       []listLine
	chatIndexToLine []int
	listYOffset     int
	listHeight      int

	renderer       *markdown.Renderer
	detailViewport viewport.Model
	width          int
	height         int
	ready          bool
	focused        bool
}

func New(ctx context.Context, chatStore *store.Store, wrap screen.WrapFunc) *Model {
	renderer, _ := markdown.NewRenderer(styles.DefaultTextareaWidth)
	m := &Model{
		ctx:                ctx,
		store:              chatStore,
		wrap:               wrap,
		filterInput:        widget.NewFilterInput(""),
		renderer:           renderer,
		detailCache:        map[string]string{},
		rowCache:           map[string]string{},
		messagesCache:      map[string][]*aipb.Message{},
		loadingMessagesSet: map[string]bool{},
		focusTarget:        FocusFilter,
	}
	m.updatePlaceholder()
	return m
}

func (m *Model) updatePlaceholder() {
	if m.showAgents {
		m.filterInput.Placeholder = "Search chats and sub-agents" + styles.GlyphEllipsis
	} else {
		m.filterInput.Placeholder = "Search chats" + styles.GlyphEllipsis
	}
}

// toggleAgents flips sub-agent chat visibility and reloads, since the
// filter is applied server-side to keep pagination correct.
func (m *Model) toggleAgents() tea.Cmd {
	m.showAgents = !m.showAgents
	m.updatePlaceholder()
	m.detailCache = map[string]string{}
	return m.fetchChats("", false)
}

// parentChatLabel names the chat that launched an agent chat: its title if
// loaded, else its ID.
func (m *Model) parentChatLabel(chat *aipb.Chat) string {
	id := store.ParentChatID(chat)
	if id == "" {
		return ""
	}
	suffix := "/" + id
	for _, candidate := range m.displayed {
		if strings.HasSuffix(candidate.GetName(), suffix) && candidate.GetTitle() != "" {
			return candidate.GetTitle()
		}
	}
	return id
}

func (m *Model) Init() tea.Cmd {
	return m.fetchChats("", false)
}

func (m *Model) Title() string {
	return "Chats"
}

func (m *Model) ShortTitle() string {
	return "Chats"
}

func (m *Model) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.recalculateLayout()
}

func (m *Model) OnFocus() tea.Cmd {
	m.focused = true
	return m.applyFocus()
}

func (m *Model) OnBlur() {
	m.focused = false
	m.filterInput.Blur()
}

func (m *Model) applyFocus() tea.Cmd {
	m.filterInput.Blur()
	m.rowCache = map[string]string{}
	if m.focusTarget == FocusFilter {
		m.filterInput.Focus()
		return textarea.Blink
	}
	return nil
}

// applyChats replaces (fresh load) or extends (infinite scroll) the list.
func (m *Model) applyChats(msg *chatsLoadedMsg) {
	if msg.Append {
		m.loadingMore = false
		// Appended pages fold into the main section; favorites are only
		// meaningful on the first page.
		m.others = append(m.others, append(msg.Favorites, msg.Others...)...)
	} else {
		m.loading = false
		m.favorites = msg.Favorites
		m.others = msg.Others
		m.chatCursor = 0
	}
	m.nextPageToken = msg.NextPageToken
	m.err = nil
	m.refreshList()
}

// fetchChats loads a page of chats matching the current filter. A fresh load
// supersedes (and cancels) whatever list request is still in flight.
func (m *Model) fetchChats(pageToken string, appendPage bool) tea.Cmd {
	if appendPage {
		m.loadingMore = true
	} else {
		m.loading = true
		m.loadingMore = false
		m.listFilter = m.filterText
		m.listGeneration++
		if m.cancelList != nil {
			m.cancelList()
		}
	}
	ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
	if !appendPage {
		m.cancelList = cancel
	}
	wrap := m.wrap
	chatStore := m.store
	generation := m.listGeneration
	titleFilter := store.TitleFilter(m.listFilter)
	filter := store.AndFilters(store.UserChatFilter, titleFilter)
	if m.showAgents {
		filter = titleFilter
	}
	return func() tea.Msg {
		defer cancel()
		var favorites []*aipb.Chat
		var err error
		if !appendPage {
			favorites, err = chatStore.ListFavoriteChats(ctx, listPageSize, titleFilter)
			if err != nil {
				return wrap(chatsLoadedMsg{generation: generation, Err: err, Append: appendPage})
			}
		}
		others, nextPageToken, err := chatStore.ListChats(ctx, listPageSize, pageToken, filter)
		if err != nil {
			return wrap(chatsLoadedMsg{generation: generation, Err: err, Append: appendPage})
		}
		return wrap(chatsLoadedMsg{
			generation:    generation,
			Favorites:     favorites,
			Others:        others,
			NextPageToken: nextPageToken,
			Append:        appendPage,
		})
	}
}

// maybeLoadMore extends the list in the background as the cursor approaches
// the end — infinite scroll instead of explicit pagination.
func (m *Model) maybeLoadMore() tea.Cmd {
	if m.nextPageToken == "" || m.loadingMore || m.loading {
		return nil
	}
	if m.chatCursor < len(m.displayed)-loadMoreThreshold {
		return nil
	}
	return m.fetchChats(m.nextPageToken, true)
}

func (m *Model) deleteChat(name string) tea.Cmd {
	wrap := m.wrap
	chatStore := m.store
	ctx := m.ctx
	return func() tea.Msg {
		err := chatStore.DeleteChat(ctx, name)
		return wrap(chatDeletedMsg{Name: name, Err: err})
	}
}

// confirmDelete asks before deleting: a chat is unrecoverable from sgpt.
func (m *Model) confirmDelete(chat *aipb.Chat) tea.Cmd {
	return m.wrapCmd(screen.ConfirmMsg{
		Title:  "Delete " + chatTitle(chat) + "?",
		Body:   "The chat and its history leave your list for good.",
		Action: m.deleteChat(chat.GetName()),
	})
}

func (m *Model) toggleFavorite(chat *aipb.Chat) tea.Cmd {
	wrap := m.wrap
	chatStore := m.store
	ctx := m.ctx
	favorite := !store.IsFavorite(chat)
	return func() tea.Msg {
		_, err := chatStore.SetFavorite(ctx, chat, favorite)
		return wrap(chatFavoriteToggledMsg{
			Name:      chat.GetName(),
			Favorited: favorite,
			Err:       err,
		})
	}
}

// maybeLoadMessages fetches the selected chat's history for the preview,
// once per chat.
func (m *Model) maybeLoadMessages() tea.Cmd {
	name := m.selectedChatName
	if name == "" || m.loadingMessagesSet[name] {
		return nil
	}
	if _, ok := m.messagesCache[name]; ok {
		return nil
	}
	m.loadingMessagesSet[name] = true
	wrap := m.wrap
	chatStore := m.store
	parent := m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 10*time.Second)
		defer cancel()
		messages, err := chatStore.ListMessages(ctx, name)
		return wrap(chatMessagesLoadedMsg{name: name, messages: messages, err: err})
	}
}

// touchPreview records a chat as most-recently-used and evicts the coldest
// previews once the budget is exceeded.
func (m *Model) touchPreview(name string) {
	for i, cached := range m.previewOrder {
		if cached == name {
			m.previewOrder = append(m.previewOrder[:i], m.previewOrder[i+1:]...)
			break
		}
	}
	m.previewOrder = append(m.previewOrder, name)
	for len(m.previewOrder) > previewCacheBudget {
		coldest := m.previewOrder[0]
		m.previewOrder = m.previewOrder[1:]
		delete(m.messagesCache, coldest)
		delete(m.detailCache, coldest)
	}
}

func (m *Model) selectedChat() *aipb.Chat {
	if m.chatCursor >= 0 && m.chatCursor < len(m.displayed) {
		return m.displayed[m.chatCursor]
	}
	return nil
}

func (m *Model) updateSelection() {
	m.chatCursor = max(0, min(m.chatCursor, len(m.displayed)-1))
	m.selectedChatName = ""
	if chat := m.selectedChat(); chat != nil {
		m.selectedChatName = chat.GetName()
	}

	// Serve the preview from cache — rendering markdown for a whole chat on
	// every cursor move is what made navigation slow.
	content, ok := m.detailCache[m.selectedChatName]
	if !ok {
		content = m.renderDetail()
		// Only cache once the message history has landed; the "loading"
		// placeholder must not stick.
		if _, loaded := m.messagesCache[m.selectedChatName]; loaded && m.selectedChatName != "" {
			m.detailCache[m.selectedChatName] = content
			m.touchPreview(m.selectedChatName)
		}
	}
	m.detailViewport.SetContent(content)
	m.detailViewport.GotoTop()
}

// refreshList re-derives the displayed slice and line layout after any data
// change. Cursor-only moves don't need it: selection is applied at render
// time via rowCache.
func (m *Model) refreshList() {
	m.displayed = make([]*aipb.Chat, 0, len(m.favorites)+len(m.others))
	m.displayed = append(m.displayed, m.favorites...)
	// Favorites are pinned above, so they aren't repeated among the rest.
	for _, chat := range m.others {
		if !store.IsFavorite(chat) {
			m.displayed = append(m.displayed, chat)
		}
	}
	m.rowCache = map[string]string{}
	m.updateSelection()
	m.rebuildListLines()
	m.ensureCursorVisible()
}

// rebuildListLines flattens section labels and chat rows into the line
// layout the virtualized view slices.
func (m *Model) rebuildListLines() {
	favoriteCount := len(m.favorites)
	m.listLines = m.listLines[:0]
	m.chatIndexToLine = make([]int, len(m.displayed))

	appendSection := func(label string, start, end int) {
		if start == end {
			return
		}
		if len(m.listLines) > 0 {
			m.listLines = append(m.listLines, listLine{chatIndex: -1})
		}
		m.listLines = append(m.listLines, listLine{text: sectionStyle.Render(label), chatIndex: -1})
		for i := start; i < end; i++ {
			m.chatIndexToLine[i] = len(m.listLines)
			m.listLines = append(m.listLines, listLine{chatIndex: i})
		}
	}
	recent := "Recent"
	if m.listFilter != "" {
		recent = "Matches"
	}
	appendSection("★ Favorites", 0, favoriteCount)
	appendSection(recent, favoriteCount, len(m.displayed))
	m.clampListOffset()
}

// ensureCursorVisible scrolls the virtual window so the cursor row never
// runs off the page.
func (m *Model) ensureCursorVisible() {
	if !m.ready || m.focusTarget != FocusChatList {
		return
	}
	if m.chatCursor < 0 || m.chatCursor >= len(m.chatIndexToLine) {
		return
	}
	line := m.chatIndexToLine[m.chatCursor]
	// Keep a section label in view when its first row is selected.
	if line-1 >= 0 && m.listLines[line-1].chatIndex < 0 && line-1 < m.listYOffset {
		line--
	}
	if line < m.listYOffset {
		m.listYOffset = line
	} else if line >= m.listYOffset+m.listHeight {
		m.listYOffset = line - m.listHeight + 1
	}
	m.clampListOffset()
}

func (m *Model) clampListOffset() {
	m.listYOffset = max(0, min(m.listYOffset, len(m.listLines)-m.listHeight))
}

func (m *Model) listWidth() int {
	if m.width < minPreviewWidth {
		return m.width
	}
	return max(38, min(72, m.width*9/20))
}

// previewWidth is the width the conversation renders at inside the pane.
func (m *Model) previewWidth() int {
	return max(0, m.detailWidth()-1)
}

func (m *Model) detailWidth() int {
	// Never negative: a zero-size terminal (headless, pre-first-resize)
	// must not crash renderers doing strings.Repeat(width).
	return max(0, m.width-m.listWidth()-1)
}

// Rows above the list (label, filter box) and below it (hints).
const (
	headerHeight = 4
	footerHeight = 1
)

func (m *Model) recalculateLayout() {
	if m.width == 0 || m.height == 0 {
		return
	}
	m.listHeight = max(1, m.height-headerHeight-footerHeight)
	detailHeight := max(1, m.height-1-footerHeight)
	detailWidth := m.detailWidth()
	if !m.ready {
		m.detailViewport = viewport.New(viewport.WithWidth(detailWidth), viewport.WithHeight(detailHeight))
		m.ready = true
	} else {
		m.detailViewport.SetWidth(detailWidth)
		m.detailViewport.SetHeight(detailHeight)
	}
	// Mirrors timeline.Model's own renderer sizing for the preview width.
	m.renderer.SetWidth(max(10, m.previewWidth()-styles.MessageHorizontalFrameSize()-2))
	// Width changes invalidate every cached preview and row (wrapping changed).
	m.detailCache = map[string]string{}
	m.rowCache = map[string]string{}
	// messagesCache survives (fetches are width-independent), so previewOrder
	// stays in sync with it rather than being cleared here.
	m.updateSelection()
	m.rebuildListLines()
	m.ensureCursorVisible()
	m.filterInput.SetWidth(max(1, m.listWidth()-filterBoxStyle.GetHorizontalFrameSize()-2))
}

var _ screen.Screen = (*Model)(nil)
