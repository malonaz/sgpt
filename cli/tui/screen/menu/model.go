package menu

import (
	"context"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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

	focusTarget      FocusTarget
	selectedChatName string

	// detailCache memoizes the (expensive) markdown render of each chat
	// preview, keyed by chat name; invalidated on resize/refresh.
	detailCache map[string]string

	renderer       *markdown.Renderer
	listViewport   viewport.Model
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
		ctx:         ctx,
		store:       chatStore,
		wrap:        wrap,
		filterInput: filterInput,
		renderer:    renderer,
		detailCache: map[string]string{},
		focusTarget: FocusFilter,
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
	m.updateSelection()
	m.listViewport.SetContent(m.renderList())
	m.ensureCursorVisible()
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

// displayedChats returns favorites then others, with client-side filter applied.
func (m *Model) displayedChats() []*aipb.Chat {
	favorites := m.applyFilter(m.favorites)
	others := m.applyFilter(m.others)
	return append(favorites, others...)
}

func (m *Model) displayedFavoriteCount() int {
	return len(m.applyFilter(m.favorites))
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
		if m.selectedChatName != "" {
			m.detailCache[m.selectedChatName] = content
		}
	}
	m.detailViewport.SetContent(content)
	m.detailViewport.GotoTop()
}

// cursorLine computes the absolute line of the cursor row inside the rendered
// list, accounting for section headers and the inter-section gap.
func (m *Model) cursorLine() int {
	headerHeight := lipgloss.Height(styles.MenuHeaderStyle.Render("H"))
	favCount := m.displayedFavoriteCount()
	if favCount == 0 {
		return headerHeight + m.chatCursor
	}
	if m.chatCursor < favCount {
		return headerHeight + m.chatCursor
	}
	// favorites header + favorite rows + blank separator + others header.
	return headerHeight + favCount + 1 + headerHeight + (m.chatCursor - favCount)
}

// ensureCursorVisible scrolls the list viewport so the cursor never runs off
// the page.
func (m *Model) ensureCursorVisible() {
	if !m.ready || m.focusTarget != FocusChatList {
		return
	}
	line := m.cursorLine()
	top := m.listViewport.YOffset()
	height := m.listViewport.Height()
	if line < top {
		m.listViewport.SetYOffset(line)
	} else if line >= top+height {
		m.listViewport.SetYOffset(line - height + 1)
	}
}

func (m *Model) listWidth() int {
	return m.width / 2
}

func (m *Model) detailWidth() int {
	return m.width - m.listWidth() - 1
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

	if !m.ready {
		m.listViewport = viewport.New(
			viewport.WithWidth(listWidth),
			viewport.WithHeight(listViewportHeight),
		)
		m.detailViewport = viewport.New(
			viewport.WithWidth(detailWidth),
			viewport.WithHeight(totalViewportHeight),
		)
		m.ready = true
	} else {
		m.listViewport.SetWidth(listWidth)
		m.listViewport.SetHeight(listViewportHeight)
		m.detailViewport.SetWidth(detailWidth)
		m.detailViewport.SetHeight(totalViewportHeight)
	}

	rendererWidth := detailWidth - 4
	if rendererWidth < 10 {
		rendererWidth = 10
	}
	m.renderer.SetWidth(rendererWidth)
	// Width changes invalidate every cached preview (wrapping changed).
	m.detailCache = map[string]string{}

	m.filterInput.SetWidth(listWidth - 6)
}

var _ screen.Screen = (*Model)(nil)
