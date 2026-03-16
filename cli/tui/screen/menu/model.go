package menu

import (
	"context"
	"fmt"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/malonaz/sgpt/cli/tui/screen"
	"github.com/malonaz/sgpt/cli/tui/styles"
	chatservicepb "github.com/malonaz/sgpt/genproto/chat/chat_service/v1"
	chatpb "github.com/malonaz/sgpt/genproto/chat/v1"
	"github.com/malonaz/sgpt/internal/markdown"
)

const pageSize = 50
const searchDebounceInterval = 300 * time.Millisecond

type ViewMode int

const (
	ViewModeList ViewMode = iota
	ViewModeSearch
)

type chatsLoadedMsg struct {
	Chats         []*chatpb.Chat
	NextPageToken string
	Err           error
	PageToken     string
}

type chatDeletedMsg struct {
	Name string
	Err  error
}

type searchResultsMsg struct {
	Chats []*chatpb.Chat
	Err   error
	Query string
}

type searchDebounceTickMsg struct {
	Query string
}

type Model struct {
	ctx        context.Context
	chatClient chatservicepb.ChatServiceClient
	wrap       screen.WrapFunc

	viewMode ViewMode

	chats             []*chatpb.Chat
	cursor            int
	loading           bool
	err               error
	filterInput       textarea.Model
	filtering         bool
	filterText        string
	nextPageToken     string
	previousPageToken string
	pageTokenStack    []string
	currentPageToken  string

	searchInput     textarea.Model
	searchResults   []*chatpb.Chat
	searchCursor    int
	searchLoading   bool
	searchErr       error
	lastSearchQuery string

	selectedChatName string

	renderer       *markdown.Renderer
	listViewport   viewport.Model
	detailViewport viewport.Model
	width          int
	height         int
	ready          bool
	focused        bool
}

func New(ctx context.Context, chatClient chatservicepb.ChatServiceClient, wrap screen.WrapFunc) *Model {
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
	}
}

func (m *Model) Init() tea.Cmd {
	return m.loadChats("")
}

func (m *Model) Title() string {
	if m.viewMode == ViewModeSearch {
		return "Search"
	}
	return fmt.Sprintf("Menu (%d chats)", len(m.chats))
}

func (m *Model) ShortTitle() string {
	if m.viewMode == ViewModeSearch {
		return "Search"
	}
	return "Menu"
}

func (m *Model) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.recalculateLayout()
}

func (m *Model) OnFocus() tea.Cmd {
	m.focused = true
	if m.viewMode == ViewModeSearch {
		m.searchInput.Focus()
		return textarea.Blink
	}
	if m.filtering {
		m.filterInput.Focus()
		return textarea.Blink
	}
	return nil
}

func (m *Model) OnBlur() {
	m.focused = false
	m.filterInput.Blur()
	m.searchInput.Blur()
}

func (m *Model) ActivateSearch() tea.Cmd {
	m.viewMode = ViewModeSearch
	m.searchInput.Focus()
	m.filterInput.Blur()
	m.filtering = false
	m.recalculateLayout()
	return textarea.Blink
}

func (m *Model) loadChats(pageToken string) tea.Cmd {
	m.loading = true
	wrap := m.wrap
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
		defer cancel()

		listChatsRequest := &chatservicepb.ListChatsRequest{
			PageSize:  pageSize,
			OrderBy:   "create_time desc",
			PageToken: pageToken,
			Filter:    "metadata.messages:*",
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

func (m *Model) executeSearch(query string) tea.Cmd {
	m.searchLoading = true
	wrap := m.wrap
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
		defer cancel()

		searchChatsRequest := &chatservicepb.SearchChatsRequest{
			Query:    query,
			PageSize: 50,
		}
		searchChatsResponse, err := m.chatClient.SearchChats(ctx, searchChatsRequest)
		if err != nil {
			return wrap(searchResultsMsg{Err: err, Query: query})
		}
		return wrap(searchResultsMsg{Chats: searchChatsResponse.Chats, Query: query})
	}
}

func (m *Model) deleteChat(name string) tea.Cmd {
	wrap := m.wrap
	return func() tea.Msg {
		deleteChatRequest := &chatservicepb.DeleteChatRequest{Name: name}
		_, err := m.chatClient.DeleteChat(m.ctx, deleteChatRequest)
		return wrap(chatDeletedMsg{Name: name, Err: err})
	}
}

func (m *Model) selectedChat() *chatpb.Chat {
	if m.viewMode == ViewModeSearch {
		if m.searchCursor >= 0 && m.searchCursor < len(m.searchResults) {
			return m.searchResults[m.searchCursor]
		}
		return nil
	}
	for _, chat := range m.chats {
		if chat.Name == m.selectedChatName {
			return chat
		}
	}
	return nil
}

func (m *Model) updateSelection() {
	if m.viewMode == ViewModeSearch {
		m.detailViewport.SetContent(m.renderDetail())
		m.detailViewport.GotoTop()
		return
	}
	filtered := m.filteredChats()
	if m.cursor < len(filtered) {
		m.selectedChatName = filtered[m.cursor].Name
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

	viewportHeight := m.height - 3
	if m.viewMode == ViewModeSearch || m.filtering {
		viewportHeight -= 2
	}
	if viewportHeight < 1 {
		viewportHeight = 1
	}

	listWidth := m.listWidth()
	detailWidth := m.detailWidth()

	if !m.ready {
		m.listViewport = viewport.New(
			viewport.WithWidth(listWidth),
			viewport.WithHeight(viewportHeight),
		)
		m.detailViewport = viewport.New(
			viewport.WithWidth(detailWidth),
			viewport.WithHeight(viewportHeight),
		)
		m.ready = true
	} else {
		m.listViewport.SetWidth(listWidth)
		m.listViewport.SetHeight(viewportHeight)
		m.detailViewport.SetWidth(detailWidth)
		m.detailViewport.SetHeight(viewportHeight)
	}

	rendererWidth := detailWidth - 4
	if rendererWidth < 10 {
		rendererWidth = 10
	}
	m.renderer.SetWidth(rendererWidth)

	m.filterInput.SetWidth(listWidth - 4)
	m.searchInput.SetWidth(listWidth - 4)
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
	return m.loadChats(m.nextPageToken)
}

func (m *Model) previousPage() tea.Cmd {
	if !m.hasPreviousPage() {
		return nil
	}
	previousToken := m.pageTokenStack[len(m.pageTokenStack)-1]
	m.pageTokenStack = m.pageTokenStack[:len(m.pageTokenStack)-1]
	return m.loadChats(previousToken)
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
