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
)

type FocusTarget int

const (
	FocusFilter FocusTarget = iota
	FocusChatList
)

type chatsLoadedMsg struct {
	Favorites     []*aipb.Chat
	Others        []*aipb.Chat
	NextPageToken string
	Err           error
	// Append marks background loads that extend the list (infinite scroll)
	// instead of replacing it.
	Append bool
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
// (section headers, separators).
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

	chatCursor    int
	loading       bool
	loadingMore   bool
	err           error
	nextPageToken string

	filterInput textarea.Model
	filterText  string

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

	// displayed memoizes filtered favorites+others; a single keypress
	// previously recomputed the filter several times.
	displayed         []*aipb.Chat
	displayedFavCount int
	displayedValid    bool

	renderer       *markdown.Renderer
	detailViewport viewport.Model
	width          int
	height         int
	ready          bool
	focused        bool
}

func New(ctx context.Context, chatStore *store.Store, wrap screen.WrapFunc) *Model {
	filterInput := textarea.New()
	filterInput.Placeholder = "Filter chats..."
	filterInput.CharLimit = 256
	filterInput.SetHeight(1)
	filterInput.ShowLineNumbers = false
	filterInput.Prompt = "/ "

	renderer, _ := markdown.NewRenderer(styles.DefaultTextareaWidth)

	return &Model{
		ctx:                ctx,
		store:              chatStore,
		wrap:               wrap,
		filterInput:        filterInput,
		renderer:           renderer,
		detailCache:        map[string]string{},
		rowCache:           map[string]string{},
		messagesCache:      map[string][]*aipb.Message{},
		loadingMessagesSet: map[string]bool{},
		focusTarget:        FocusFilter,
	}
}

func (m *Model) Init() tea.Cmd {
	return m.fetchChats("", false)
}

func (m *Model) Title() string {
	return "Menu"
}

func (m *Model) ShortTitle() string {
	return "Menu"
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

func (m *Model) fetchChats(pageToken string, appendPage bool) tea.Cmd {
	if appendPage {
		m.loadingMore = true
	} else {
		m.loading = true
	}
	wrap := m.wrap
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
		defer cancel()

		var favorites []*aipb.Chat
		var err error
		if !appendPage {
			favorites, err = m.store.ListFavoriteChats(ctx, listPageSize)
			if err != nil {
				return wrap(chatsLoadedMsg{Err: err, Append: appendPage})
			}
		}
		others, nextPageToken, err := m.store.ListChats(ctx, listPageSize, pageToken, "")
		if err != nil {
			return wrap(chatsLoadedMsg{Err: err, Append: appendPage})
		}
		return wrap(chatsLoadedMsg{
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
	if m.chatCursor < len(m.displayedChats())-loadMoreThreshold {
		return nil
	}
	return m.fetchChats(m.nextPageToken, true)
}

func (m *Model) deleteChat(name string) tea.Cmd {
	wrap := m.wrap
	return func() tea.Msg {
		err := m.store.DeleteChat(m.ctx, name)
		return wrap(chatDeletedMsg{Name: name, Err: err})
	}
}

func (m *Model) toggleFavorite(chat *aipb.Chat) tea.Cmd {
	wrap := m.wrap
	favorite := !store.IsFavorite(chat)
	return func() tea.Msg {
		_, err := m.store.SetFavorite(m.ctx, chat, favorite)
		return wrap(chatFavoriteToggledMsg{
			Name:      chat.GetName(),
			Favorited: favorite,
			Err:       err,
		})
	}
}

// displayedChats returns favorites then others, with client-side filter
// applied; memoized until refreshList invalidates it.
func (m *Model) displayedChats() []*aipb.Chat {
	if !m.displayedValid {
		favorites := m.applyFilter(m.favorites)
		others := m.applyFilter(m.others)
		m.displayed = make([]*aipb.Chat, 0, len(favorites)+len(others))
		m.displayed = append(m.displayed, favorites...)
		m.displayed = append(m.displayed, others...)
		m.displayedFavCount = len(favorites)
		m.displayedValid = true
	}
	return m.displayed
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
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
		defer cancel()
		messages, err := chatStore.ListMessages(ctx, name)
		return wrap(chatMessagesLoadedMsg{name: name, messages: messages, err: err})
	}
}

func (m *Model) displayedFavoriteCount() int {
	m.displayedChats()
	return m.displayedFavCount
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

func (m *Model) applyFilter(chats []*aipb.Chat) []*aipb.Chat {
	if m.filterText == "" {
		return chats
	}
	lowerFilter := strings.ToLower(m.filterText)
	var result []*aipb.Chat
	for _, chat := range chats {
		title := chat.GetTitle()
		if strings.Contains(strings.ToLower(title), lowerFilter) || strings.Contains(strings.ToLower(chat.Name), lowerFilter) {
			result = append(result, chat)
		}
	}
	return result
}

func (m *Model) selectedChat() *aipb.Chat {
	displayed := m.displayedChats()
	if m.chatCursor >= 0 && m.chatCursor < len(displayed) {
		return displayed[m.chatCursor]
	}
	return nil
}

func (m *Model) updateSelection() {
	displayed := m.displayedChats()
	if m.chatCursor >= len(displayed) {
		m.chatCursor = len(displayed) - 1
	}
	if m.chatCursor < 0 {
		m.chatCursor = 0
	}
	if m.chatCursor < len(displayed) {
		m.selectedChatName = displayed[m.chatCursor].Name
	} else {
		m.selectedChatName = ""
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
// or filter change. Cursor-only moves don't need it: selection is applied at
// render time via rowCache.
func (m *Model) refreshList() {
	m.displayedValid = false
	m.rowCache = map[string]string{}
	m.updateSelection()
	m.rebuildListLines()
	m.ensureCursorVisible()
}

// rebuildListLines flattens headers and chat rows into the line layout the
// virtualized view slices.
func (m *Model) rebuildListLines() {
	displayed := m.displayedChats()
	favoriteCount := m.displayedFavoriteCount()
	listWidth := m.listWidth()

	m.listLines = m.listLines[:0]
	m.chatIndexToLine = make([]int, len(displayed))

	appendHeader := func(label string) {
		rendered := styles.MenuHeaderStyle.Width(listWidth).Render(label)
		for _, line := range strings.Split(rendered, "\n") {
			m.listLines = append(m.listLines, listLine{text: line, chatIndex: -1})
		}
	}
	appendChatRows := func(start, end int) {
		for i := start; i < end; i++ {
			m.chatIndexToLine[i] = len(m.listLines)
			m.listLines = append(m.listLines, listLine{chatIndex: i})
		}
	}

	if favoriteCount > 0 {
		appendHeader("⭐ Favorites")
		appendChatRows(0, favoriteCount)
		if favoriteCount < len(displayed) {
			m.listLines = append(m.listLines, listLine{chatIndex: -1})
		}
	}
	if favoriteCount < len(displayed) {
		appendHeader("📋 Chats")
		appendChatRows(favoriteCount, len(displayed))
	}
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
	if line < m.listYOffset {
		m.listYOffset = line
	} else if line >= m.listYOffset+m.listHeight {
		m.listYOffset = line - m.listHeight + 1
	}
	m.clampListOffset()
}

func (m *Model) clampListOffset() {
	maxOffset := len(m.listLines) - m.listHeight
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.listYOffset > maxOffset {
		m.listYOffset = maxOffset
	}
	if m.listYOffset < 0 {
		m.listYOffset = 0
	}
}

func (m *Model) listWidth() int {
	return m.width / 2
}

func (m *Model) detailWidth() int {
	// Never negative: a zero-size terminal (headless, pre-first-resize)
	// must not crash renderers doing strings.Repeat(width).
	return max(0, m.width-m.listWidth()-1)
}

func (m *Model) recalculateLayout() {
	if m.width == 0 || m.height == 0 {
		return
	}

	inputHeight := 3
	totalViewportHeight := m.height - 4
	listViewportHeight := totalViewportHeight - inputHeight
	if listViewportHeight < 1 {
		listViewportHeight = 1
	}
	if totalViewportHeight < 1 {
		totalViewportHeight = 1
	}

	listWidth := m.listWidth()
	detailWidth := m.detailWidth()

	m.listHeight = listViewportHeight

	if !m.ready {
		m.detailViewport = viewport.New(
			viewport.WithWidth(detailWidth),
			viewport.WithHeight(totalViewportHeight),
		)
		m.ready = true
	} else {
		m.detailViewport.SetWidth(detailWidth)
		m.detailViewport.SetHeight(totalViewportHeight)
	}

	rendererWidth := detailWidth - 4
	if rendererWidth < 10 {
		rendererWidth = 10
	}
	m.renderer.SetWidth(rendererWidth)
	// Width changes invalidate every cached preview and row (wrapping changed).
	m.detailCache = map[string]string{}
	m.rowCache = map[string]string{}
	// messagesCache survives (fetches are width-independent), so previewOrder
	// stays in sync with it rather than being cleared here.
	m.rebuildListLines()
	m.ensureCursorVisible()

	m.filterInput.SetWidth(listWidth - 6)
}

var _ screen.Screen = (*Model)(nil)
