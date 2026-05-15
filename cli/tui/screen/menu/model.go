package menu

import (
	"context"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/malonaz/sgpt/cli/tui/screen"
	"github.com/malonaz/sgpt/cli/tui/styles"
	sgptservicepb "github.com/malonaz/sgpt/genproto/sgpt/sgpt_service/v1"
	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/markdown"
)

const (
	searchDebounceInterval = 300 * time.Millisecond
	favoriteTag            = "favorite"
	favoriteFilter         = `tags:"favorite"`
)

type FocusTarget int

const (
	FocusFilter FocusTarget = iota
	FocusSearch
	FocusChatList
)

type chatsLoadedMsg struct {
	Favorites     []*sgptpb.Chat
	Others        []*sgptpb.Chat
	NextPageToken string
	Err           error
	PageToken     string
	SearchQuery   string
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

type searchDebounceTickMsg struct {
	Query string
}

type Model struct {
	ctx        context.Context
	chatClient sgptservicepb.SgptServiceClient
	wrap       screen.WrapFunc

	favorites []*sgptpb.Chat
	others    []*sgptpb.Chat

	chatCursor       int
	loading          bool
	err              error
	nextPageToken    string
	pageTokenStack   []string
	currentPageToken string

	filterInput textarea.Model
	filterText  string

	searchInput     textarea.Model
	searchQuery     string
	lastSearchQuery string

	focusTarget      FocusTarget
	selectedChatName string

	renderer       *markdown.Renderer
	listViewport   viewport.Model
	detailViewport viewport.Model
	width          int
	height         int
	ready          bool
	focused        bool
}

func New(ctx context.Context, chatClient sgptservicepb.SgptServiceClient, wrap screen.WrapFunc) *Model {
	filterInput := textarea.New()
	filterInput.Placeholder = "Filter chats..."
	filterInput.CharLimit = 256
	filterInput.SetHeight(1)
	filterInput.ShowLineNumbers = false
	filterInput.Prompt = "/ "

	searchInput := textarea.New()
	searchInput.Placeholder = "Search chats..."
	searchInput.CharLimit = 256
	searchInput.SetHeight(1)
	searchInput.ShowLineNumbers = false
	searchInput.Prompt = "🔍 "

	renderer, _ := markdown.NewRenderer(styles.DefaultTextareaWidth)

	return &Model{
		ctx:         ctx,
		chatClient:  chatClient,
		wrap:        wrap,
		filterInput: filterInput,
		searchInput: searchInput,
		renderer:    renderer,
		focusTarget: FocusFilter,
	}
}

func (m *Model) Init() tea.Cmd {
	return m.fetchChats("")
}

func (m *Model) visibleRowCapacity() int {
	inputHeight := 4
	headerHeight := 1
	helpBarHeight := 1
	available := m.height - 4 - inputHeight - headerHeight - helpBarHeight
	if available < 1 {
		return 1
	}
	return available
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
	m.searchInput.Blur()
}

func (m *Model) ActivateSearch() tea.Cmd {
	m.focusTarget = FocusSearch
	return m.applyFocus()
}

func (m *Model) applyFocus() tea.Cmd {
	m.filterInput.Blur()
	m.searchInput.Blur()
	switch m.focusTarget {
	case FocusFilter:
		m.filterInput.Focus()
		return textarea.Blink
	case FocusSearch:
		m.searchInput.Focus()
		return textarea.Blink
	}
	return nil
}

func (m *Model) fetchChats(pageToken string) tea.Cmd {
	m.loading = true
	wrap := m.wrap
	searchQuery := m.searchQuery
	pageSize := int32(m.visibleRowCapacity())
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
		defer cancel()

		if searchQuery != "" {
			searchChatsRequest := &sgptservicepb.SearchChatsRequest{
				Query:     searchQuery,
				PageSize:  pageSize,
				PageToken: pageToken,
			}
			searchChatsResponse, err := m.chatClient.SearchChats(ctx, searchChatsRequest)
			if err != nil {
				return wrap(chatsLoadedMsg{Err: err, SearchQuery: searchQuery})
			}
			favorites, others := partitionByTag(searchChatsResponse.Chats, favoriteTag)
			return wrap(chatsLoadedMsg{
				Favorites:     favorites,
				Others:        others,
				NextPageToken: searchChatsResponse.NextPageToken,
				PageToken:     pageToken,
				SearchQuery:   searchQuery,
			})
		}

		// Fetch favorites.
		listFavoritesRequest := &sgptservicepb.ListChatsRequest{
			PageSize: pageSize,
			OrderBy:  "create_time desc",
			Filter:   favoriteFilter,
		}
		listFavoritesResponse, err := m.chatClient.ListChats(ctx, listFavoritesRequest)
		if err != nil {
			return wrap(chatsLoadedMsg{Err: err})
		}

		// Fetch others (paginated).
		listOthersRequest := &sgptservicepb.ListChatsRequest{
			PageSize:  pageSize,
			OrderBy:   "create_time desc",
			PageToken: pageToken,
		}
		listOthersResponse, err := m.chatClient.ListChats(ctx, listOthersRequest)
		if err != nil {
			return wrap(chatsLoadedMsg{Err: err})
		}

		return wrap(chatsLoadedMsg{
			Favorites:     listFavoritesResponse.Chats,
			Others:        listOthersResponse.Chats,
			NextPageToken: listOthersResponse.NextPageToken,
			PageToken:     pageToken,
		})
	}
}

func (m *Model) deleteChat(name string) tea.Cmd {
	wrap := m.wrap
	return func() tea.Msg {
		deleteChatRequest := &sgptservicepb.DeleteChatRequest{Name: name}
		_, err := m.chatClient.DeleteChat(m.ctx, deleteChatRequest)
		return wrap(chatDeletedMsg{Name: name, Err: err})
	}
}

func (m *Model) toggleFavorite(chat *sgptpb.Chat) tea.Cmd {
	wrap := m.wrap
	wasFavorite := chatHasTag(chat, favoriteTag)

	if wasFavorite {
		filtered := make([]string, 0, len(chat.Tags)-1)
		for _, tag := range chat.Tags {
			if tag != favoriteTag {
				filtered = append(filtered, tag)
			}
		}
		chat.Tags = filtered
	} else {
		chat.Tags = append(chat.Tags, favoriteTag)
	}

	return func() tea.Msg {
		updateChatRequest := &sgptservicepb.UpdateChatRequest{
			Chat:       chat,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"tags"}},
		}
		_, err := m.chatClient.UpdateChat(m.ctx, updateChatRequest)
		return wrap(chatFavoriteToggledMsg{
			Name:      chat.GetName(),
			Favorited: !wasFavorite,
			Err:       err,
		})
	}
}

func (m *Model) resetPagination() {
	m.pageTokenStack = nil
	m.currentPageToken = ""
	m.nextPageToken = ""
}

// displayedChats returns favorites then others, with client-side filter applied.
func (m *Model) displayedChats() []*sgptpb.Chat {
	favorites := m.applyFilter(m.favorites)
	others := m.applyFilter(m.others)
	return append(favorites, others...)
}

func (m *Model) displayedFavoriteCount() int {
	return len(m.applyFilter(m.favorites))
}

func (m *Model) applyFilter(chats []*sgptpb.Chat) []*sgptpb.Chat {
	if m.filterText == "" {
		return chats
	}
	var result []*sgptpb.Chat
	for _, chat := range chats {
		title := chat.GetMetadata().GetTitle()
		if containsIgnoreCase(title, m.filterText) || containsIgnoreCase(chat.Name, m.filterText) {
			result = append(result, chat)
		}
	}
	return result
}

func (m *Model) selectedChat() *sgptpb.Chat {
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
	m.detailViewport.SetContent(m.renderDetail())
	m.detailViewport.GotoTop()
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

	inputHeight := 4
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

	m.filterInput.SetWidth(listWidth - 6)
	m.searchInput.SetWidth(listWidth - 6)
}

func (m *Model) hasNextPage() bool {
	return m.nextPageToken != ""
}

func (m *Model) hasPreviousPage() bool {
	return len(m.pageTokenStack) > 0
}

func (m *Model) nextPage() tea.Cmd {
	if !m.hasNextPage() {
		return nil
	}
	m.pageTokenStack = append(m.pageTokenStack, m.currentPageToken)
	return m.fetchChats(m.nextPageToken)
}

func (m *Model) previousPage() tea.Cmd {
	if !m.hasPreviousPage() {
		return nil
	}
	previousToken := m.pageTokenStack[len(m.pageTokenStack)-1]
	m.pageTokenStack = m.pageTokenStack[:len(m.pageTokenStack)-1]
	return m.fetchChats(previousToken)
}

func (m *Model) currentPage() int {
	return len(m.pageTokenStack) + 1
}

func chatHasTag(chat *sgptpb.Chat, tag string) bool {
	for _, t := range chat.GetTags() {
		if t == tag {
			return true
		}
	}
	return false
}

func partitionByTag(chats []*sgptpb.Chat, tag string) (withTag []*sgptpb.Chat, withoutTag []*sgptpb.Chat) {
	for _, chat := range chats {
		if chatHasTag(chat, tag) {
			withTag = append(withTag, chat)
		} else {
			withoutTag = append(withoutTag, chat)
		}
	}
	return withTag, withoutTag
}

func containsIgnoreCase(s, substr string) bool {
	if substr == "" {
		return true
	}
	ls := len(s)
	lsub := len(substr)
	if lsub > ls {
		return false
	}
	for i := 0; i <= ls-lsub; i++ {
		match := true
		for j := 0; j < lsub; j++ {
			a := s[i+j]
			b := substr[j]
			if a >= 'A' && a <= 'Z' {
				a += 32
			}
			if b >= 'A' && b <= 'Z' {
				b += 32
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

var _ screen.Screen = (*Model)(nil)
