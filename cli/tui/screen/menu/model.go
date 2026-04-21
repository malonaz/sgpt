package menu

import (
	"context"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/malonaz/sgpt/cli/tui/screen"
	"github.com/malonaz/sgpt/cli/tui/styles"
	sgptservicepb "github.com/malonaz/sgpt/genproto/sgpt/sgpt_service/v1"
	chatpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/markdown"
)

const (
	searchDebounceInterval = 300 * time.Millisecond
)

type FocusTarget int

const (
	FocusFilter FocusTarget = iota
	FocusSearch
	FocusChatList
)

type chatsLoadedMsg struct {
	Chats         []*chatpb.Chat
	NextPageToken string
	Err           error
	PageToken     string
	SearchQuery   string
}

type chatDeletedMsg struct {
	Name string
	Err  error
}

type searchDebounceTickMsg struct {
	Query string
}

type Model struct {
	ctx        context.Context
	chatClient sgptservicepb.SgptServiceClient
	wrap       screen.WrapFunc

	chats            []*chatpb.Chat
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
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
		defer cancel()

		if searchQuery != "" {
			searchChatsRequest := &sgptservicepb.SearchChatsRequest{
				Query:     searchQuery,
				PageSize:  int32(m.visibleRowCapacity()),
				PageToken: pageToken,
			}
			searchChatsResponse, err := m.chatClient.SearchChats(ctx, searchChatsRequest)
			if err != nil {
				return wrap(chatsLoadedMsg{Err: err, SearchQuery: searchQuery})
			}
			return wrap(chatsLoadedMsg{
				Chats:         searchChatsResponse.Chats,
				NextPageToken: searchChatsResponse.NextPageToken,
				PageToken:     pageToken,
				SearchQuery:   searchQuery,
			})
		}

		listChatsRequest := &sgptservicepb.ListChatsRequest{
			PageSize:  int32(m.visibleRowCapacity()),
			OrderBy:   "create_time desc",
			PageToken: pageToken,
		}
		listChatsResponse, err := m.chatClient.ListChats(ctx, listChatsRequest)
		if err != nil {
			return wrap(chatsLoadedMsg{Err: err})
		}
		return wrap(chatsLoadedMsg{
			Chats:         listChatsResponse.Chats,
			NextPageToken: listChatsResponse.NextPageToken,
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

func (m *Model) resetPagination() {
	m.pageTokenStack = nil
	m.currentPageToken = ""
	m.nextPageToken = ""
}

func (m *Model) displayedChats() []*chatpb.Chat {
	return m.filteredChats()
}

func (m *Model) selectedChat() *chatpb.Chat {
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

func (m *Model) filteredChats() []*chatpb.Chat {
	if m.filterText == "" {
		return m.chats
	}
	var result []*chatpb.Chat
	for _, chat := range m.chats {
		title := chat.GetMetadata().GetTitle()
		if containsIgnoreCase(title, m.filterText) || containsIgnoreCase(chat.Name, m.filterText) {
			result = append(result, chat)
		}
	}
	return result
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
